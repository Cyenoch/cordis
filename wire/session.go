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
	// FrameBytes is required for hosts. Guests bind it from initialize.
	FrameBytes int
}

type reply struct {
	payload json.RawMessage
	err     error
}

type outbound struct {
	data      []byte
	shutdown  bool
	committed <-chan struct{}
}
type pending struct {
	ctx    context.Context
	method string
	result chan reply
}
type incoming struct {
	cancel      context.CancelFunc
	method      string
	recoverable bool
	canceled    bool
}
type cancellation struct {
	ctx      context.Context
	method   string
	awaiting bool
	timer    *time.Timer
}

type Endpoint struct {
	mu               sync.Mutex
	issue            chan struct{}
	transport        io.ReadWriteCloser
	handler          Handler
	limits           Limits
	guest            bool
	identity         contract.Identity
	ready            bool
	frameBytes       int
	initializeIssued bool
	sequence         uint64
	pending          map[string]*pending
	incoming         map[string]incoming
	activeIncoming   int
	remoteSequence   uint64
	canceled         map[string]*cancellation
	cancelOrder      []string
	out              chan outbound
	done             chan struct{}
	err              error
	closeOnce        sync.Once
	ctx              context.Context
	cancel           context.CancelFunc
	wg               sync.WaitGroup
}

func New(options Options) (*Endpoint, error) {
	if options.Transport == nil || options.Handler == nil {
		return nil, contract.Fail(contract.Invalid, "session transport and handler are required")
	}
	limits := options.Limits
	defaults := DefaultLimits()
	if limits.Inflight == 0 {
		limits.Inflight = defaults.Inflight
	}
	if limits.Deadline == 0 {
		limits.Deadline = defaults.Deadline
	}
	if limits.Grace == 0 {
		limits.Grace = defaults.Grace
	}
	if limits.Inflight < 1 || limits.Deadline <= 0 || limits.Deadline > 120*time.Second || limits.Grace < 0 {
		return nil, contract.Fail(contract.Invalid, "invalid session limits")
	}
	if options.Guest {
		if options.FrameBytes != 0 {
			return nil, contract.Fail(contract.Invalid, "guest frameBytes is bound by initialize")
		}
	} else {
		if err := contract.ValidateFrameBytes(options.FrameBytes); err != nil {
			return nil, err
		}
		if options.Identity.Activation == "" || len(options.Identity.Activation) > 128 {
			return nil, contract.Fail(contract.Invalid, "invalid host activation")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	e := &Endpoint{transport: options.Transport, handler: options.Handler, limits: limits, guest: options.Guest,
		identity: options.Identity, frameBytes: options.FrameBytes, pending: map[string]*pending{}, incoming: map[string]incoming{},
		canceled: map[string]*cancellation{}, cancelOrder: []string{}, issue: make(chan struct{}, 1),
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
		for _, canceled := range e.canceled {
			if canceled.timer != nil {
				canceled.timer.Stop()
			}
		}
		e.cancel()
		e.mu.Unlock()
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
	select {
	case e.issue <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-e.done:
		return nil, e.Err()
	}
	if err := ctx.Err(); err != nil {
		<-e.issue
		return nil, err
	}
	e.mu.Lock()
	if e.guest && e.frameBytes == 0 {
		e.mu.Unlock()
		<-e.issue
		return nil, contract.Fail(contract.NotReady, "session budget not initialized")
	}
	if method == "initialize" {
		initialization, err := decodeInitialize(input)
		if err == nil && (e.guest || e.initializeIssued || initialization.FrameBytes != e.frameBytes || initialization.Identity.Activation != e.identity.Activation) {
			err = contract.Fail(contract.Invalid, "invalid or repeated initialization")
		}
		if err != nil {
			e.mu.Unlock()
			<-e.issue
			return nil, err
		}
		e.initializeIssued = true
	}
	ref, _ := ctx.Value(requestKey{}).(requestReference)
	initializing := false
	if ref.endpoint == e {
		parent, exists := e.incoming[ref.id]
		initializing = exists && (parent.method == "initialize" || parent.method == "shutdown")
	}
	if !e.ready && !initializing && method != "initialize" && method != "health" && method != "shutdown" {
		e.mu.Unlock()
		<-e.issue
		return nil, contract.Fail(contract.NotReady, "session not initialized")
	}
	if len(e.pending) >= e.limits.Inflight {
		e.mu.Unlock()
		<-e.issue
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
		<-e.issue
		e.removePending(id, false)
		return nil, err
	}
	<-e.issue
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
		e.mu.Lock()
		if record := e.canceled[id]; record != nil && record.awaiting && e.ctx.Err() == nil {
			record.timer = time.AfterFunc(e.limits.Grace, func() {
				e.mu.Lock()
				unanswered := record.awaiting && e.ctx.Err() == nil
				e.mu.Unlock()
				if unanswered {
					e.shutdown(contract.Fail(contract.Timeout, "peer ignored cancellation"))
				}
			})
		}
		e.mu.Unlock()
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
	if _, exists := e.pending[id]; !exists {
		e.mu.Unlock()
		return false
	}
	p := e.pending[id]
	delete(e.pending, id)
	if canceled {
		// Only acknowledged history may be evicted. Unanswered cancellation is
		// still a live obligation, even after it leaves the request table.
		if len(e.cancelOrder) >= e.limits.Inflight*4 {
			for n, old := range e.cancelOrder {
				if !e.canceled[old].awaiting {
					delete(e.canceled, old)
					e.cancelOrder = append(e.cancelOrder[:n], e.cancelOrder[n+1:]...)
					break
				}
			}
		}
		if len(e.cancelOrder) >= e.limits.Inflight*4 {
			e.mu.Unlock()
			e.shutdown(contract.Fail(contract.Timeout, "unanswered cancellation limit exceeded"))
			return false
		}
		e.canceled[id] = &cancellation{ctx: p.ctx, method: p.method, awaiting: true}
		e.cancelOrder = append(e.cancelOrder, id)
	}
	e.mu.Unlock()
	return true
}

func (e *Endpoint) send(ctx context.Context, frame Frame) error {
	e.mu.Lock()
	limit := e.frameBytes
	e.mu.Unlock()
	data, err := encodeFrame(frame, limit)
	if err != nil {
		return err
	}
	return e.enqueue(ctx, outbound{data: data, shutdown: e.guest && frame.Kind == "response" && frame.Method == "shutdown" && frame.Error == nil})
}

func (e *Endpoint) enqueue(ctx context.Context, message outbound) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case e.out <- message:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-e.done:
		return e.Err()
	}
}

func controlMethod(method string) bool {
	switch method {
	case "initialize", "health", "shutdown", "activation.publish", "activation.retire":
		return true
	default:
		return false
	}
}

func canceledResponse(ctx context.Context, response Frame) Frame {
	if ctx.Err() == nil {
		return response
	}
	response.Payload = nil
	response.Error = &contract.Error{Code: contract.Canceled, Message: "request canceled"}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		response.Error = &contract.Error{Code: contract.Timeout, Message: "request deadline exceeded"}
	}
	return response
}

func (e *Endpoint) sendResponse(ctx, deliveryCtx context.Context, response Frame) error {
	e.mu.Lock()
	recoverable := e.incoming[response.ID].recoverable
	limit := e.frameBytes
	e.mu.Unlock()
	initializes := response.Method == "initialize" && response.Error == nil
	data, err := encodeFrame(response, limit)
	if _, oversized := errors.AsType[*frameSizeError](err); oversized && recoverable {
		response.Payload = nil
		response.Error = &contract.Error{Code: contract.PluginFailed, Message: "response exceeds frame byte limit"}
		data, err = encodeFrame(response, limit)
	}
	if err != nil {
		return err
	}
	if initializes {
		committed := make(chan struct{})
		if err := e.enqueue(deliveryCtx, outbound{data: data, committed: committed}); err != nil {
			return err
		}
		e.mu.Lock()
		if ctx.Err() != nil || e.ctx.Err() != nil {
			e.mu.Unlock()
			// Closing the endpoint releases the writer without exposing success.
			return contract.Fail(contract.Canceled, "initialization canceled")
		}
		e.ready = true
		close(committed)
		e.mu.Unlock()
		return nil
	}
	requestDone := ctx.Done()
	for {
		if err := deliveryCtx.Err(); err != nil {
			return err
		}
		if ctx.Err() != nil && requestDone != nil {
			response = canceledResponse(ctx, response)
			data, err = encodeFrame(response, limit)
			if err != nil {
				return err
			}
			requestDone = nil
		}
		select {
		case e.out <- outbound{data: data, shutdown: e.guest && response.Method == "shutdown" && response.Error == nil}:
			return nil
		case <-requestDone:
		case <-deliveryCtx.Done():
			return deliveryCtx.Err()
		case <-e.done:
			return e.Err()
		}
	}
}

func (e *Endpoint) writeLoop() {
	for {
		select {
		case <-e.done:
			return
		case message := <-e.out:
			if message.committed != nil {
				select {
				case <-message.committed:
				case <-e.done:
					return
				}
			}
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
	reader := bufio.NewReaderSize(e.transport, 4096)
	for {
		e.mu.Lock()
		limit := e.frameBytes
		e.mu.Unlock()
		if limit == 0 {
			limit = contract.MaxFrameBytes
		}
		frame, err := readFrame(reader, limit)
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
	if e.guest && e.frameBytes == 0 {
		if frame.Kind != "request" || frame.Method != "initialize" || frame.Parent != "" {
			e.mu.Unlock()
			return contract.Fail(contract.Invalid, "first frame must initialize the session")
		}
		init, err := decodeInitialize(frame.Payload)
		if err != nil {
			e.mu.Unlock()
			return err
		}
		if frame.wireBytes > init.FrameBytes || init.Identity.Activation != frame.Activation {
			e.mu.Unlock()
			return contract.Fail(contract.Invalid, "initialize exceeds budget or identity mismatch")
		}
		e.frameBytes = init.FrameBytes
		e.identity = init.Identity
	} else if frame.Kind == "request" && frame.Method == "initialize" {
		e.mu.Unlock()
		return contract.Fail(contract.Invalid, "session already initialized")
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
			if late != nil && late.awaiting && late.method == frame.Method {
				late.awaiting = false
				if late.timer != nil {
					late.timer.Stop()
				}
				e.mu.Unlock()
				return nil
			}
			e.mu.Unlock()
			return contract.Fail(contract.Invalid, "unknown or duplicate response")
		}
		if p.method != frame.Method {
			e.mu.Unlock()
			return contract.Fail(contract.Invalid, "response method mismatch")
		}
		delete(e.pending, frame.ID)
		if frame.Method == "initialize" && frame.Error == nil && p.ctx.Err() == nil {
			if err := decodeRegistration(frame.Payload, e.frameBytes); err != nil {
				e.mu.Unlock()
				return err
			}
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
		if ok && !in.canceled {
			in.canceled = true
			e.incoming[frame.ID] = in
			e.activeIncoming--
			if len(e.incoming)-e.activeIncoming > e.limits.Inflight*4 {
				e.mu.Unlock()
				return contract.Fail(contract.Invalid, "canceled incoming request limit exceeded")
			}
		}
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
	sequence, ok := strings.CutPrefix(frame.ID, prefix)
	if !ok {
		e.mu.Unlock()
		return contract.Fail(contract.Invalid, "invalid request direction")
	}
	n, err := strconv.ParseUint(sequence, 10, 64)
	if err != nil || n <= e.remoteSequence {
		e.mu.Unlock()
		return contract.Fail(contract.Invalid, "invalid request sequence")
	}
	e.remoteSequence = n
	initializing := false
	if parent, exists := e.pending[frame.Parent]; exists {
		initializing = parent.method == "initialize" || parent.method == "shutdown"
	} else if parent := e.canceled[frame.Parent]; parent != nil {
		initializing = parent.method == "initialize" || parent.method == "shutdown"
	}
	if !e.ready && !initializing && frame.Method != "initialize" && frame.Method != "health" && frame.Method != "shutdown" {
		e.mu.Unlock()
		return contract.Fail(contract.NotReady, "request before initialization")
	}
	if e.activeIncoming >= e.limits.Inflight {
		e.mu.Unlock()
		return contract.Fail(contract.Invalid, "peer exceeded inflight limit")
	}
	parent := e.ctx
	if frame.Parent != "" {
		p, ok := e.pending[frame.Parent]
		if !ok {
			previous := e.canceled[frame.Parent]
			if previous == nil {
				e.mu.Unlock()
				return contract.Fail(contract.Invalid, "invalid parent request")
			}
			parent = previous.ctx
		} else {
			parent = p.ctx
		}
	}
	ctx, cancel := context.WithTimeout(parent, time.Duration(frame.DeadlineMS)*time.Millisecond)
	ctx = context.WithValue(ctx, requestKey{}, requestReference{endpoint: e, id: frame.ID})
	e.incoming[frame.ID] = incoming{cancel: cancel, method: frame.Method, recoverable: e.ready && !controlMethod(frame.Method)}
	e.activeIncoming++
	e.mu.Unlock()
	e.wg.Go(func() { e.handle(ctx, cancel, frame) })
	return nil
}

func (e *Endpoint) handle(ctx context.Context, cancel context.CancelFunc, frame Frame) {
	defer cancel()
	defer func() {
		e.mu.Lock()
		if !e.incoming[frame.ID].canceled {
			e.activeIncoming--
		}
		delete(e.incoming, frame.ID)
		e.mu.Unlock()
	}()
	var payload json.RawMessage
	err := ctx.Err()
	if err == nil {
		payload, err = e.handler(ctx, frame.Method, frame.Payload)
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		err = contract.Fail(contract.Timeout, "request deadline exceeded")
	} else if ctx.Err() != nil {
		err = contract.Fail(contract.Canceled, "request canceled")
	}
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
	if err := e.sendResponse(ctx, deliveryCtx, response); err != nil && e.ctx.Err() == nil {
		e.shutdown(err)
	}
}
