// Package wire implements the bounded, bidirectional Cordis session protocol.
package wire

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Cyenoch/cordis/contract"
)

type Frame struct {
	wireBytes  int
	Schema     string          `json:"schema"`
	Kind       string          `json:"kind"`
	Activation string          `json:"activation"`
	ID         string          `json:"id"`
	Method     string          `json:"method,omitempty"`
	Payload    json.RawMessage `json:"payload,omitempty"`
	Error      *contract.Error `json:"error,omitempty"`
	DeadlineMS int64           `json:"deadlineMs,omitempty"`
	Parent     string          `json:"parent,omitempty"`
}

type receivedField[T any] struct {
	value   T
	present bool
}

func (f *receivedField[T]) UnmarshalJSON(data []byte) error {
	f.present = true
	return contract.Decode(data, &f.value)
}

type receivedFrame struct {
	Schema     string                        `json:"schema"`
	Kind       string                        `json:"kind"`
	Activation string                        `json:"activation"`
	ID         string                        `json:"id"`
	Method     receivedField[string]         `json:"method"`
	Payload    json.RawMessage               `json:"payload"`
	Error      receivedField[contract.Error] `json:"error"`
	DeadlineMS receivedField[int64]          `json:"deadlineMs"`
	Parent     receivedField[string]         `json:"parent"`
}

type Limits struct {
	Inflight int
	Deadline time.Duration
	Grace    time.Duration
}

func DefaultLimits() Limits {
	return Limits{Inflight: 64, Deadline: 30 * time.Second, Grace: time.Second}
}

func readFrame(reader *bufio.Reader, limit int) (Frame, error) {
	data := make([]byte, 0, min(limit, 4096))
	for {
		var part []byte
		var err error
		if limit-len(data) < reader.Size() {
			// Do not wait for another full buffer after the hard limit is reached.
			var b byte
			b, err = reader.ReadByte()
			if err == nil {
				part = []byte{b}
				if b != '\n' {
					err = bufio.ErrBufferFull
				}
			}
		} else {
			part, err = reader.ReadSlice('\n')
		}
		if len(data)+len(part) > limit {
			return Frame{}, contract.Fail(contract.Invalid, "frame exceeds byte limit")
		}
		data = append(data, part...)
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if err != nil {
			if errors.Is(err, io.EOF) && len(data) != 0 {
				return Frame{}, contract.Fail(contract.Invalid, "unterminated frame")
			}
			return Frame{}, fmt.Errorf("read frame: %w", err)
		}
		break
	}
	var received receivedFrame
	if err := contract.Decode(data, &received); err != nil {
		return Frame{}, err
	}
	frame := Frame{wireBytes: len(data), Schema: received.Schema, Kind: received.Kind, Activation: received.Activation, ID: received.ID, Method: received.Method.value, Payload: received.Payload, DeadlineMS: received.DeadlineMS.value, Parent: received.Parent.value}
	if received.Error.present {
		frame.Error = &received.Error.value
	}
	if err := validateIdentifiers(frame); err != nil {
		return Frame{}, err
	}
	switch frame.Kind {
	case "request":
		if frame.Method == "" || frame.Error != nil || len(frame.Payload) == 0 || frame.DeadlineMS < 1 || frame.DeadlineMS > 120000 {
			return Frame{}, contract.Fail(contract.Invalid, "invalid request fields")
		}
	case "response":
		if frame.Method == "" || (frame.Error == nil) == (len(frame.Payload) == 0) || received.Parent.present || received.DeadlineMS.present {
			return Frame{}, contract.Fail(contract.Invalid, "invalid response fields")
		}
	case "cancel":
		if received.Method.present || received.Error.present || len(frame.Payload) != 0 || received.DeadlineMS.present || received.Parent.present {
			return Frame{}, contract.Fail(contract.Invalid, "invalid cancel fields")
		}
	default:
		return Frame{}, contract.Fail(contract.Invalid, "unknown frame kind")
	}
	return frame, nil
}

func encodeFrame(frame Frame, limit int) ([]byte, error) {
	if err := validateIdentifiers(frame); err != nil {
		return nil, err
	}
	data, err := json.Marshal(frame)
	if err != nil {
		return nil, fmt.Errorf("encode frame: %w", err)
	}
	if len(data)+1 > limit {
		return nil, &frameSizeError{}
	}
	return append(data, '\n'), nil
}

func validateIdentifiers(frame Frame) error {
	if frame.Schema != contract.SessionSchema || frame.Activation == "" || len(frame.Activation) > 128 || !utf8.ValidString(frame.Activation) {
		return contract.Fail(contract.Invalid, "invalid frame activation")
	}
	if !validRequestID(frame.ID) || frame.Parent != "" && !validRequestID(frame.Parent) || len(frame.Method) > 256 || !utf8.ValidString(frame.Method) || frame.Kind != "cancel" && frame.Method == "" {
		return contract.Fail(contract.Invalid, "invalid frame identifiers")
	}
	return nil
}

func validRequestID(id string) bool {
	if len(id) < 3 || len(id) > 128 || id[1] != ':' || id[0] != 'h' && id[0] != 'g' || id[2] == '0' {
		return false
	}
	if strings.IndexFunc(id[2:], func(r rune) bool { return r < '0' || r > '9' }) >= 0 {
		return false
	}
	n, err := strconv.ParseUint(id[2:], 10, 64)
	return err == nil && n > 0
}

type frameSizeError struct{}

func (*frameSizeError) Error() string { return "invalid_message: frame exceeds byte limit" }
func (*frameSizeError) Unwrap() error {
	return contract.Fail(contract.Invalid, "frame exceeds byte limit")
}

type requestKey struct{}
type requestReference struct {
	endpoint *Endpoint
	id       string
}

func RequestID(ctx interface{ Value(any) any }) string {
	ref, _ := ctx.Value(requestKey{}).(requestReference)
	return ref.id
}
