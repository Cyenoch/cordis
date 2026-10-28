package access

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"

	"github.com/Cyenoch/cordis/contract"
)

const maxNamespaceRecordBytes = 64 << 20

// RecoveryError means neither rollback nor durable completion could be proven.
// This store refuses subsequent work; reopening validates the on-disk records.
type RecoveryError = contract.NamespaceRecoveryError

type namespaceRecord struct {
	Sequence  uint64            `json:"sequence"`
	Operation string            `json:"operation"`
	Namespace string            `json:"namespace"`
	Package   string            `json:"package,omitempty"`
	Config    NamespaceConfig   `json:"config"`
	Key       string            `json:"key,omitempty"`
	Value     []byte            `json:"value,omitempty"`
	Values    map[string][]byte `json:"values,omitempty"`
}

type namespaceEnvelope struct {
	Record json.RawMessage `json:"record"`
	SHA256 string          `json:"sha256"`
}

func (s *NamespaceStore) appendLog(record namespaceRecord) error {
	if s.path == "" {
		return nil
	}
	if s.failure != nil {
		return &RecoveryError{Cause: s.failure}
	}
	record.Sequence = s.sequence + 1
	raw, err := json.Marshal(record)
	if err != nil {
		return err
	}
	hash := sha256.Sum256(raw)
	data, err := json.Marshal(namespaceEnvelope{Record: raw, SHA256: hex.EncodeToString(hash[:])})
	if err != nil {
		return err
	}
	if len(data)+1 > maxNamespaceRecordBytes {
		return ErrQuota
	}
	file, err := os.OpenFile(s.path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	start, err := file.Seek(0, io.SeekEnd)
	if err != nil {
		return errors.Join(err, file.Close())
	}
	data = append(data, '\n')
	if s.commitObserver != nil {
		s.commitObserver()
	}
	n, err := file.Write(data)
	if err == nil && n != len(data) {
		err = io.ErrShortWrite
	}
	if err == nil {
		err = file.Sync()
	}
	if err != nil {
		rollback := file.Truncate(start)
		if rollback == nil {
			rollback = file.Sync()
		}
		if rollback != nil {
			s.failure = errors.Join(err, rollback)
			return &RecoveryError{Cause: errors.Join(s.failure, file.Close())}
		}
		return errors.Join(err, file.Close())
	}
	s.sequence++
	if err := errors.Join(file.Close(), syncNamespaceDirectory(filepath.Dir(s.path))); err != nil {
		return &CommittedError{Cause: err}
	}
	return nil
}

func (s *NamespaceStore) recoverLog() error {
	file, err := os.Open(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer file.Close()
	reader := bufio.NewReader(file)
	for {
		raw, err := readNamespaceRecord(reader)
		if len(raw) == 0 && errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("incomplete namespace log record %d: %w", s.sequence+1, err)
		}
		var envelope namespaceEnvelope
		if err := contract.Decode(raw, &envelope); err != nil {
			return fmt.Errorf("namespace log record %d: %w", s.sequence+1, err)
		}
		hash := sha256.Sum256(envelope.Record)
		if hex.EncodeToString(hash[:]) != envelope.SHA256 {
			return fmt.Errorf("namespace log checksum mismatch at %d", s.sequence+1)
		}
		var record namespaceRecord
		if err := contract.Decode(envelope.Record, &record); err != nil {
			return err
		}
		if record.Sequence != s.sequence+1 {
			return fmt.Errorf("namespace log sequence mismatch")
		}
		if err := s.replay(record); err != nil {
			return fmt.Errorf("namespace log record %d: %w", record.Sequence, err)
		}
		s.sequence = record.Sequence
	}
}

func readNamespaceRecord(reader *bufio.Reader) ([]byte, error) {
	var raw []byte
	for {
		part, err := reader.ReadSlice('\n')
		if len(raw)+len(part) > maxNamespaceRecordBytes {
			return nil, ErrQuota
		}
		raw = append(raw, part...)
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		return raw, err
	}
}

// Compact atomically replaces history with current bindings and values. It is
// explicit maintenance, never an unpredictable full-store write on the hot path.
func (s *NamespaceStore) Compact(ctx context.Context) (result error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.path == "" {
		return nil
	}
	if s.failure != nil {
		return &RecoveryError{Cause: s.failure}
	}
	file, err := os.CreateTemp(filepath.Dir(s.path), ".namespace-compact-*")
	if err != nil {
		return err
	}
	closed, replaced := false, false
	defer func() {
		if !closed {
			result = errors.Join(result, file.Close())
		}
		if !replaced {
			result = errors.Join(result, os.Remove(file.Name()))
		}
	}()
	var sequence uint64
	write := func(record namespaceRecord) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		sequence++
		record.Sequence = sequence
		raw, err := json.Marshal(record)
		if err != nil {
			return err
		}
		hash := sha256.Sum256(raw)
		data, err := json.Marshal(namespaceEnvelope{Record: raw, SHA256: hex.EncodeToString(hash[:])})
		if err != nil {
			return err
		}
		if len(data)+1 > maxNamespaceRecordBytes {
			return ErrQuota
		}
		_, err = file.Write(append(data, '\n'))
		return err
	}
	for _, name := range slices.Sorted(maps.Keys(s.spaces)) {
		space := s.spaces[name]
		if err = write(namespaceRecord{Operation: "configure", Namespace: name, Package: space.Package, Config: space.Config}); err != nil {
			return err
		}
		for _, key := range slices.Sorted(maps.Keys(space.Values)) {
			if err = write(namespaceRecord{Operation: "write", Namespace: name, Key: key, Value: space.Values[key]}); err != nil {
				return err
			}
		}
	}
	if err = file.Sync(); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	// Close before rename is required for Windows replacement.
	err = file.Close()
	closed = true
	if err != nil {
		return err
	}
	if err = replaceNamespaceFile(file.Name(), s.path); err != nil {
		return err
	}
	replaced = true
	s.sequence = sequence
	if err = syncNamespaceDirectory(filepath.Dir(s.path)); err != nil {
		return &CommittedError{Cause: err}
	}
	return nil
}

func (s *NamespaceStore) replay(record namespaceRecord) error {
	space := s.spaces[record.Namespace]
	switch record.Operation {
	case "configure":
		if record.Namespace == "" || record.Package == "" || record.Config.Name != record.Namespace || record.Config.QuotaBytes < 0 {
			return fmt.Errorf("invalid namespace binding")
		}
		if space != nil {
			if space.Package != record.Package {
				return fmt.Errorf("namespace already bound to another package")
			}
			if record.Config.QuotaBytes < space.Used {
				return ErrQuota
			}
			space.Config.QuotaBytes = record.Config.QuotaBytes
			return nil
		}
		for _, other := range s.spaces {
			if other.Package == record.Package {
				return fmt.Errorf("duplicate package binding")
			}
		}
		s.spaces[record.Namespace] = &namespaceData{Package: record.Package, Config: record.Config, Values: map[string][]byte{}}
		return nil
	case "remove":
		if space == nil {
			return ErrNotFound
		}
		delete(s.spaces, record.Namespace)
		return nil
	}
	if space == nil {
		return ErrNotFound
	}
	switch record.Operation {
	case "write":
		if record.Key == "" {
			return fmt.Errorf("empty namespace key")
		}
		used := space.Used
		if previous, exists := space.Values[record.Key]; exists {
			used -= int64(len(record.Key)) + int64(len(previous))
		}
		cost := int64(len(record.Key)) + int64(len(record.Value))
		if cost > space.Config.QuotaBytes-used {
			return ErrQuota
		}
		space.Values[record.Key] = bytes.Clone(record.Value)
		space.Used = used + cost
	case "delete":
		previous, exists := space.Values[record.Key]
		if !exists {
			return ErrNotFound
		}
		delete(space.Values, record.Key)
		space.Used -= int64(len(record.Key)) + int64(len(previous))
	case "migrate":
		if record.Config.Name != record.Namespace || record.Config.QuotaBytes != space.Config.QuotaBytes {
			return fmt.Errorf("invalid migration config")
		}
		var used int64
		for key, value := range record.Values {
			cost := int64(len(key)) + int64(len(value))
			if key == "" || cost > space.Config.QuotaBytes-used {
				return ErrQuota
			}
			used += cost
		}
		space.Values = cloneValues(record.Values)
		space.Config = record.Config
		space.Used = used
	default:
		return fmt.Errorf("unknown namespace operation")
	}
	return nil
}
