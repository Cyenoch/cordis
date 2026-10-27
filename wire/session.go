package wire

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Cyenoch/cordis/contract"
)

type Handler func(context.Context, string, json.RawMessage) (json.RawMessage, error)

type Options struct {
	Transport io.ReadWriteCloser
	Identity  contract.Identity
	Guest     bool
	Handler   Handler
	Limits    Limits
}

type reply struct {
	payload json.RawMessage
	err     error
}

type outbound struct {
	data     []byte
	shutdown bool
}
type pending struct {
	ctx    context.Context
	method string
	result chan reply
}
type incoming struct {
	cancel context.CancelFunc
	method string
}

type Endpoint struct {
	mu             sync.Mutex
	issueMu        sync.Mutex
	transport      io.ReadWriteCloser
	handler        Handler
	limits         Limits
	guest          bool
	identity       contract.Identity
	ready          bool
	sequence       uint64
	pending        map[string]*pending
	incoming       map[string]incoming
	remoteSequence uint64
	canceled       map[string]bool
	cancelOrder    []string
	out            chan outbound
	done           chan struct{}
	err            error
	closeOnce      sync.Once
	ctx            context.Context
	cancel         context.CancelFunc
	wg             sync.WaitGroup
}

func New(options Options) (*Endpoint, error) {
	if options.Transport == nil || options.Handler == nil {
		return nil, contract.Fail(contract.Invalid, "session transport and handler are required")
	}
	limits := options.Limits
	defaults := DefaultLimits()
	if limits.FrameBytes == 0 {
		limits.FrameBytes = defaults.FrameBytes
	}
	if limits.Inflight == 0 {
		limits.Inflight = defaults.Inflight
	}
	if limits.Deadline == 0 {
		limits.Deadline = defaults.Deadline
	}
	if limits.Grace == 0 {
		limits.Grace = defaults.Grace
	}
	if limits.FrameBytes < 128 || limits.Inflight < 1 || limits.Deadline <= 0 || limits.Deadline > 120*time.Second || limits.Grace < 0 {
		return nil, contract.Fail(contract.Invalid, "invalid session limits")
	}
	ctx, cancel := context.WithCancel(context.Background())
	e := &Endpoint{transport: options.Transport, handler: options.Handler, limits: limits, guest: options.Guest,
		identity: options.Identity, pending: map[string]*pending{}, incoming: map[string]incoming{},
		canceled: map[string]bool{}, cancelOrder: []string{},
		out: make(chan outbound, limits.Inflight+4), done: make(chan struct{}), ctx: ctx, cancel: cancel}
	e.wg.Go(e.readLoop)
	e.wg.Go(e.writeLoop)
	return e, nil
}

func (e *Endpoint) Identity() contract.Identity { e.mu.Lock(); defer e.mu.Unlock(); return e.identity }
func (e *Endpoint) Done() <-chan struct{}       { return e.done }
func (e *Endpoint) Err() error                  { e.mu.Lock(); defer e.mu.Unlock(); return e.err }
func (e *Endpoint) Close() error                { e.shutdown(io.EOF); return nil }
func (e *Endpoint) Wait()                       { e.wg.Wait() }

func (e *Endpoint) shutdown(err error) {
	e.closeOnce.Do(func() {
		e.mu.Lock()
		e.err = err
		for _, in := range e.incoming {
			in.cancel()
		}
		e.mu.Unlock()
		e.cancel()
		close(e.done)
		if closeErr := e.transport.Close(); closeErr != nil {
			e.mu.Lock()
			e.err = errors.Join(e.err, closeErr)
			e.mu.Unlock()
		}
	})
}

func (e *Endpoint) Call(ctx context.Context, method string, input json.RawMessage) (json.RawMessage, error) {
	if len(input) == 0 {
		input = json.RawMessage(`{}`)
	}
	deadline, has := ctx.Deadline()
	if !has {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, e.limits.Deadline)
		defer cancel()
		deadline, _ = ctx.Deadline()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	e.issueMu.Lock()
	e.mu.Lock()
	ref, _ := ctx.Value(requestKey{}).(requestReference)
	initializing := false
	if ref.endpoint == e {
		parent, exists := e.incoming[ref.id]
		initializing = exists && (parent.method == "initialize" || parent.method == "shutdown")
	}
	if !e.ready && !initializing && method != "initialize" && method != "health" && method != "shutdown" {
		e.mu.Unlock()
		e.issueMu.Unlock()
		return nil, contract.Fail(contract.NotReady, "session not initialized")
	}
	if len(e.pending) >= e.limits.Inflight {
		e.mu.Unlock()
		e.issueMu.Unlock()
		return nil, contract.Fail(contract.NotReady, "inflight request limit reached")
	}
	e.sequence++
	prefix := "h:"
	if e.guest {
		prefix = "g:"
	}
	id := prefix + strconv.FormatUint(e.sequence, 10)
	p := &pending{ctx: ctx, method: method, result: make(chan reply, 1)}
	e.pending[id] = p
	activation := e.identity.Activation
	e.mu.Unlock()
	parentRef, _ := ctx.Value(requestKey{}).(requestReference)
	parentID := ""
	if parentRef.endpoint == e {
		parentID = parentRef.id
	}
	frame := Frame{Schema: contract.SessionSchema, Kind: "request", Activation: activation, ID: id, Method: method,
		Payload: input, DeadlineMS: min(120000, max(1, time.Until(deadline).Milliseconds())), Parent: parentID}
	if err := e.send(ctx, frame); err != nil {
		e.issueMu.Unlock()
		e.removePending(id, false)
		return nil, err
	}
	e.issueMu.Unlock()
	select {
	case result := <-p.result:
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return result.payload, result.err
	case <-ctx.Done():
		if !e.removePending(id, true) {
			return nil, ctx.Err()
		}
		cancelCtx, cancel := context.WithTimeout(e.ctx, e.limits.Grace)
		defer cancel()
		if err := e.send(cancelCtx, Frame{Schema: contract.SessionSchema, Kind: "cancel", Activation: activation, ID: id}); err != nil && e.ctx.Err() == nil {
			e.shutdown(err)
		}
		e.wg.Go(func() {
			timer := time.NewTimer(e.limits.Grace)
			defer timer.Stop()
			select {
			case <-e.done:
				return
			case <-timer.C:
			}
			e.mu.Lock()
			unanswered := e.canceled[id]
			e.mu.Unlock()
			if unanswered {
				e.shutdown(contract.Fail(contract.Timeout, "peer ignored cancellation"))
			}
		})
		return nil, ctx.Err()
	case <-e.done:
		select {
		case result := <-p.result:
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			return result.payload, result.err
		default:
		}
		e.removePending(id, false)
		return nil, e.Err()
	}
}

func (e *Endpoint) removePending(id string, canceled bool) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, exists := e.pending[id]; !exists {
		return false
	}
	delete(e.pending, id)
	if canceled {
		e.canceled[id] = true
		e.cancelOrder = append(e.cancelOrder, id)
		if len(e.cancelOrder) > e.limits.Inflight*4 {
			delete(e.canceled, e.cancelOrder[0])
			e.cancelOrder = e.cancelOrder[1:]
		}
	}
	return true
}

func (e *Endpoint) send(ctx context.Context, frame Frame) error {
	data, err := encodeFrame(frame, e.limits.FrameBytes)
	if err != nil {
		return err
	}
	select {
	case e.out <- outbound{data: data, shutdown: e.guest && frame.Kind == "response" && frame.Method == "shutdown" && frame.Error == nil}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-e.done:
		return e.Err()
	}
}

func (e *Endpoint) writeLoop() {
	for {
		select {
		case <-e.done:
			return
		case message := <-e.out:
			data := message.data
			for len(data) > 0 {
				n, err := e.transport.Write(data)
				if err != nil {
					e.shutdown(fmt.Errorf("write session: %w", err))
					return
				}
				if n == 0 {
					e.shutdown(io.ErrShortWrite)
					return
				}
				data = data[n:]
			}
			if message.shutdown {
				e.shutdown(io.EOF)
				return
			}
		}
	}
}

func (e *Endpoint) readLoop() {
	reader := bufio.NewReaderSize(e.transport, min(e.limits.FrameBytes, 4096))
	for {
		frame, err := readFrame(reader, e.limits.FrameBytes)
		if err != nil {
			e.shutdown(err)
			return
		}
		if err := e.receive(frame); err != nil {
			e.shutdown(err)
			return
		}
	}
}

func (e *Endpoint) receive(frame Frame) error {
	e.mu.Lock()
	if e.identity.Activation == "" && e.guest && frame.Kind == "request" && frame.Method == "initialize" {
		e.identity.Activation = frame.Activation
	}
	if frame.Activation != e.identity.Activation {
		e.mu.Unlock()
		return contract.Fail(contract.Invalid, "activation mismatch")
	}
	switch frame.Kind {
	case "response":
		p, ok := e.pending[frame.ID]
		if !ok {
			late := e.canceled[frame.ID]
			if late {
				delete(e.canceled, frame.ID)
			}
			e.mu.Unlock()
			if late {
				return nil
			}
			return contract.Fail(contract.Invalid, "unknown or duplicate response")
		}
		if p.method != frame.Method {
			e.mu.Unlock()
			return contract.Fail(contract.Invalid, "response method mismatch")
		}
		delete(e.pending, frame.ID)
		if frame.Method == "initialize" && frame.Error == nil && p.ctx.Err() == nil {
			e.ready = true
		}
		var err error
		if frame.Error != nil {
			err = frame.Error
		}
		p.result <- reply{payload: frame.Payload, err: err}
		e.mu.Unlock()
		return nil
	case "cancel":
		in, ok := e.incoming[frame.ID]
		e.mu.Unlock()
		if ok {
			in.cancel()
		}
		return nil
	}
	prefix := "g:"
	if e.guest {
		prefix = "h:"
	}
	if !strings.HasPrefix(frame.ID, prefix) {
		e.mu.Unlock()
		return contract.Fail(contract.Invalid, "invalid request direction")
	}
	n, err := strconv.ParseUint(strings.TrimPrefix(frame.ID, prefix), 10, 64)
	if err != nil || n <= e.remoteSequence {
		e.mu.Unlock()
		return contract.Fail(contract.Invalid, "invalid request sequence")
	}
	e.remoteSequence = n
	initializing := false
	if parent, exists := e.pending[frame.Parent]; exists {
		initializing = parent.method == "initialize" || parent.method == "shutdown"
	}
	if !e.ready && !initializing && frame.Method != "initialize" && frame.Method != "health" && frame.Method != "shutdown" {
		e.mu.Unlock()
		return contract.Fail(contract.NotReady, "request before initialization")
	}
	if len(e.incoming) >= e.limits.Inflight {
		e.mu.Unlock()
		return contract.Fail(contract.Invalid, "peer exceeded inflight limit")
	}
	parent := e.ctx
	if frame.Parent != "" {
		p, ok := e.pending[frame.Parent]
		if !ok {
			e.mu.Unlock()
			return contract.Fail(contract.Invalid, "invalid parent request")
		}
		parent = p.ctx
	}
	ctx, cancel := context.WithTimeout(parent, time.Duration(frame.DeadlineMS)*time.Millisecond)
	ctx = context.WithValue(ctx, requestKey{}, requestReference{endpoint: e, id: frame.ID})
	e.incoming[frame.ID] = incoming{cancel: cancel, method: frame.Method}
	e.mu.Unlock()
	e.wg.Go(func() { e.handle(ctx, cancel, frame) })
	return nil
}

func (e *Endpoint) handle(ctx context.Context, cancel context.CancelFunc, frame Frame) {
	defer cancel()
	defer func() { e.mu.Lock(); delete(e.incoming, frame.ID); e.mu.Unlock() }()
	payload, err := e.handler(ctx, frame.Method, frame.Payload)
	e.mu.Lock()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		err = contract.Fail(contract.Timeout, "request deadline exceeded")
	} else if ctx.Err() != nil {
		err = contract.Fail(contract.Canceled, "request canceled")
	}
	if err == nil && frame.Method == "initialize" {
		e.ready = true
	}
	e.mu.Unlock()
	response := Frame{Schema: contract.SessionSchema, Kind: "response", Activation: frame.Activation, ID: frame.ID, Method: frame.Method, Payload: payload}
	if err != nil {
		response.Payload = nil
		if coded, ok := errors.AsType[*contract.Error](err); ok {
			response.Error = coded
		} else {
			response.Error = &contract.Error{Code: contract.PluginFailed, Message: "handler failed"}
		}
	} else if len(payload) == 0 {
		response.Payload = json.RawMessage(`{}`)
	}
	deliveryCtx, deliveryCancel := context.WithTimeout(e.ctx, e.limits.Grace)
	defer deliveryCancel()
	if err := e.send(deliveryCtx, response); err != nil && e.ctx.Err() == nil {
		e.shutdown(err)
	}
}
