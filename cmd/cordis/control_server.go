package main

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	cordis "github.com/Cyenoch/cordis"
	"github.com/Cyenoch/cordis/contract"
	"github.com/Cyenoch/cordis/packagehost"
)

type controlServer struct {
	host       *cordis.Host
	connection controlConnection
	path       string
	fileInfo   os.FileInfo
	server     *http.Server
	cancel     context.CancelFunc
	done       chan struct{}
	err        error
	mu         sync.Mutex
	closing    bool
	requests   sync.WaitGroup
}

func startControl(ctx context.Context, host *cordis.Host, path string) (*controlServer, error) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("listen for local control: %w", err)
	}
	token, session := make([]byte, 32), make([]byte, 16)
	rand.Read(token)
	rand.Read(session)
	ctx, cancel := context.WithCancel(ctx)
	s := &controlServer{host: host, path: path, cancel: cancel, done: make(chan struct{}),
		connection: controlConnection{Version: 1, Address: listener.Addr().String(),
			Session: base64.RawURLEncoding.EncodeToString(session), Token: base64.RawURLEncoding.EncodeToString(token)}}
	if err := s.writeConnection(); err != nil {
		cancel()
		return nil, errors.Join(err, listener.Close())
	}
	s.server = &http.Server{
		Handler: s, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 5 * time.Second,
		WriteTimeout: maxControlTimeout + 5*time.Second, IdleTimeout: 15 * time.Second,
		MaxHeaderBytes: 8192, BaseContext: func(net.Listener) context.Context { return ctx },
	}
	go func() {
		if err := s.server.Serve(listener); !errors.Is(err, http.ErrServerClosed) {
			s.err = err
		}
		close(s.done)
	}()
	return s, nil
}

func (s *controlServer) writeConnection() error {
	file, err := createControlFile(s.path)
	if err != nil {
		return fmt.Errorf("create control connection: %w", err)
	}
	s.fileInfo, err = file.Stat()
	if err == nil {
		err = checkControlFile(file)
	}
	if err == nil {
		err = json.NewEncoder(file).Encode(s.connection)
	}
	err = errors.Join(err, file.Close())
	if err != nil {
		// A failed creation owns only this inode, never a replacement at its path.
		if info, statErr := os.Lstat(s.path); statErr == nil && s.fileInfo != nil && os.SameFile(s.fileInfo, info) {
			err = errors.Join(err, os.Remove(s.path))
		}
	}
	return err
}

func (s *controlServer) close() error {
	s.mu.Lock()
	s.closing = true
	s.cancel()
	s.mu.Unlock()
	err := s.server.Close()
	<-s.done
	s.requests.Wait()
	info, statErr := os.Lstat(s.path)
	if errors.Is(statErr, os.ErrNotExist) {
		return errors.Join(err, s.err)
	}
	if statErr != nil {
		return errors.Join(err, s.err, statErr)
	}
	if os.SameFile(info, s.fileInfo) {
		current, readErr := readControlConnection(s.path)
		if readErr == nil && current == s.connection {
			err = errors.Join(err, os.Remove(s.path))
		}
	}
	return errors.Join(err, s.err)
}

func (s *controlServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	if s.closing || r.Context().Err() != nil {
		s.mu.Unlock()
		s.respond(w, "rejected", nil, contract.Fail(contract.NotReady, "control session is closing"))
		return
	}
	s.requests.Add(1)
	s.mu.Unlock()
	defer s.requests.Done()
	if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+s.connection.Token)) != 1 {
		s.respond(w, "rejected", nil, contract.Fail(contract.Denied, "control authentication rejected"))
		return
	}
	if r.Host != s.connection.Address || r.Header.Get("Origin") != "" ||
		r.Method != http.MethodPost || r.URL.Path != controlPath || r.URL.RawQuery != "" {
		s.respond(w, "rejected", nil, contract.Fail(contract.Invalid, "invalid control request target"))
		return
	}
	data, err := readControlBody(r.Body)
	var request controlRequest
	if err == nil {
		err = contract.Decode(data, &request)
	}
	if err != nil {
		s.respond(w, "rejected", nil, contract.Fail(contract.Invalid, "invalid control request body"))
		return
	}
	if request.Version != 1 || request.Session != s.connection.Session || !controlAction(request.Action) ||
		request.TimeoutMillis < 1 || request.TimeoutMillis > maxControlTimeout.Milliseconds() ||
		request.Action != "status" && request.Entry == "" {
		s.respond(w, "rejected", nil, contract.Fail(contract.Invalid, "invalid control request fields"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(request.TimeoutMillis)*time.Millisecond)
	defer cancel()
	if request.Action == "status" {
		status, err := s.snapshot(ctx, request.Entry)
		s.respond(w, "completed", status, err)
		return
	}
	entries, err := s.host.Store().Entries()
	if err != nil {
		s.respond(w, "rejected", nil, err)
		return
	}
	var entry *contract.Entry
	for _, saved := range entries {
		if saved.ID == request.Entry {
			entry = &saved
			break
		}
	}
	if entry == nil {
		s.respond(w, "rejected", nil, contract.Fail(contract.NotReady, "control mutations require an existing persistent entry"))
		return
	}
	switch request.Action {
	case "enable":
		var instance contract.Instance
		instance, err = s.host.Enable(ctx, *entry)
		if err == nil {
			err = instance.Wait(ctx)
		}
	case "disable":
		err = s.host.Disable(ctx, entry.ID)
	case "stop":
		var instance contract.Instance
		instance, err = s.host.Instance(entry.ID)
		if err != nil {
			s.respond(w, "rejected", nil, err)
			return
		}
		err = instance.Stop(ctx)
	case "resume":
		err = s.host.Resume(ctx, entry.ID)
	case "retry":
		err = s.host.Retry(ctx, entry.ID)
	}
	if err != nil {
		phase := "unknown"
		var committed *packagehost.CommitError
		if errors.As(err, &committed) {
			phase = "committed_with_error"
		}
		s.respond(w, phase, nil, err)
		return
	}
	status, err := s.snapshot(ctx, request.Entry)
	phase := "completed"
	if err != nil {
		phase = "committed_with_error"
	}
	s.respond(w, phase, status, err)
}

func (s *controlServer) snapshot(ctx context.Context, id string) (*controlSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	entries, err := s.host.Store().Entries()
	if err != nil {
		return nil, err
	}
	status := &controlSnapshot{Online: true, Entries: []contract.Entry{}, Instances: []contract.InstanceInfo{}, Recovery: []cordis.RecoveryInfo{}}
	for _, entry := range entries {
		if id == "" || entry.ID == id {
			status.Entries = append(status.Entries, entry)
		}
	}
	for _, instance := range s.host.Instances() {
		if id == "" || instance.Entry.ID == id {
			status.Instances = append(status.Instances, instance)
		}
	}
	for _, recovery := range s.host.RecoveryStatus() {
		if id == "" || recovery.Entry == id {
			status.Recovery = append(status.Recovery, recovery)
		}
	}
	if id != "" && len(status.Entries) == 0 && len(status.Instances) == 0 && len(status.Recovery) == 0 {
		return nil, contract.Fail(contract.NotReady, "entry not found")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return status, nil
}

func (s *controlServer) respond(w http.ResponseWriter, phase string, status *controlSnapshot, err error) {
	response := controlResponse{Version: 1, Session: s.connection.Session, Phase: phase, Status: status}
	if err != nil {
		response.Code, response.Error = contract.ErrorCode(err), err.Error()
	}
	data, encodeErr := json.Marshal(response)
	if encodeErr != nil || len(data) > contract.MaxFrameBytes {
		response.Status = nil
		response.Code, response.Error = contract.Invalid, "control response exceeds message budget; query a single entry"
		// Status is read-only; mutation results keep their original phase.
		data, _ = json.Marshal(response)
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(data)
}
