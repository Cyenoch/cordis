package wire

import (
	"context"
	"encoding/json"
	"errors"
	"iter"
	"strconv"
	"sync"
	"time"

	"github.com/Cyenoch/cordis/contract"
)

// unbounded reports the methods whose requests have no deadline of their
// own. A chain execution may include work of any length; it ends with the
// context of whoever started it, a cancellation, or the session.
func unbounded(method string) bool {
	switch method {
	case "chain.invoke", "chain.next", "chain.do", "chain.final":
		return true
	}
	return false
}

// inflow is the caller's side of a stream.
type inflow struct {
	items    chan json.RawMessage // holds at most a window of items
	granted  int                  // items the callee may send in total
	received int                  // items it has sent
}

// outflow is the callee's side of a stream.
type outflow struct {
	credit  int           // items it may still send
	granted chan struct{} // signals that credit arrived

	// sending orders the items and the response of the stream: the handler
	// may still be running when a canceled stream is answered.
	sending  sync.Mutex
	answered bool
}

// Stream calls a method whose results arrive one by one. The request is
// sent when iteration starts. Iteration ends after the last item, or with a
// final non-nil error. Stopping early cancels the callee.
//
// The callee may send only as many items as the caller has granted; the
// caller grants more as it consumes them, so a slow consumer slows the
// callee down instead of filling memory. The deadline of the session
// applies to the time the caller waits for an item the callee is free to
// send, not to the stream as a whole and not to the consumer's own pace.
func (e *Endpoint) Stream(ctx context.Context, method string, input json.RawMessage) iter.Seq2[json.RawMessage, error] {
	return func(yield func(json.RawMessage, error) bool) {
		if len(input) == 0 {
			input = json.RawMessage(`{}`)
		}
		id, p, activation, err := e.issueStream(ctx, method, input)
		if err != nil {
			yield(nil, err)
			return
		}
		s := p.stream
		abandon := func() { e.abandon(id, activation) }
		silence := time.NewTimer(e.limits.Deadline)
		defer silence.Stop()
		consumed := 0
		for {
			// Count silence only while the callee has credit to send with.
			e.mu.Lock()
			free := s.granted > s.received
			e.mu.Unlock()
			silence.Stop()
			select {
			case <-silence.C:
			default:
			}
			if free {
				silence.Reset(e.limits.Deadline)
			}
			select {
			case item := <-s.items:
				if !yield(item, nil) {
					abandon()
					return
				}
				if consumed++; consumed >= (DefaultWindow+1)/2 {
					if err := e.grant(ctx, id, activation, s, consumed); err != nil {
						abandon()
						yield(nil, err)
						return
					}
					consumed = 0
				}
			case result := <-p.result:
				// The items of a stream precede its response on the wire.
				for {
					select {
					case item := <-s.items:
						if !yield(item, nil) {
							return
						}
						continue
					default:
					}
					break
				}
				if result.err != nil {
					yield(nil, result.err)
				}
				return
			case <-silence.C:
				abandon()
				yield(nil, contract.Fail(contract.Timeout, "stream produced nothing within the request deadline"))
				return
			case <-ctx.Done():
				abandon()
				yield(nil, ctx.Err())
				return
			case <-e.done:
				// What had arrived before the session ended still counts.
				for {
					select {
					case item := <-s.items:
						if !yield(item, nil) {
							return
						}
						continue
					default:
					}
					break
				}
				select {
				case result := <-p.result:
					if result.err != nil {
						yield(nil, result.err)
					}
				default:
					e.removePending(id, false)
					yield(nil, e.Err())
				}
				return
			}
		}
	}
}

// issueStream registers and sends a stream request.
func (e *Endpoint) issueStream(ctx context.Context, method string, input json.RawMessage) (string, *pending, string, error) {
	if err := ctx.Err(); err != nil {
		return "", nil, "", err
	}
	select {
	case e.issue <- struct{}{}:
	case <-ctx.Done():
		return "", nil, "", ctx.Err()
	case <-e.done:
		return "", nil, "", e.Err()
	}
	defer func() { <-e.issue }()
	e.mu.Lock()
	if !e.ready {
		e.mu.Unlock()
		return "", nil, "", contract.Fail(contract.NotReady, "session not initialized")
	}
	if len(e.pending) >= e.limits.Inflight {
		e.mu.Unlock()
		return "", nil, "", contract.Fail(contract.NotReady, "inflight request limit reached")
	}
	e.sequence++
	prefix := "h:"
	if e.guest {
		prefix = "g:"
	}
	id := prefix + strconv.FormatUint(e.sequence, 10)
	p := &pending{ctx: ctx, method: method, result: make(chan reply, 1),
		stream: &inflow{items: make(chan json.RawMessage, DefaultWindow), granted: DefaultWindow}}
	e.pending[id] = p
	activation := e.identity.Activation
	e.mu.Unlock()
	parentRef, _ := ctx.Value(requestKey{}).(requestReference)
	parentID := ""
	if parentRef.endpoint == e {
		parentID = parentRef.id
	}
	frame := Frame{Schema: contract.SessionSchema, Kind: "request", Activation: activation, ID: id, Method: method, Payload: input,
		DeadlineMS: min(120000, max(1, e.limits.Deadline.Milliseconds())), Parent: parentID, Stream: true, Window: DefaultWindow}
	if err := e.send(ctx, frame); err != nil {
		e.removePending(id, false)
		return "", nil, "", err
	}
	return id, p, activation, nil
}

// grant lets the callee send n more items.
func (e *Endpoint) grant(ctx context.Context, id, activation string, s *inflow, n int) error {
	e.mu.Lock()
	s.granted += n
	e.mu.Unlock()
	return e.send(ctx, Frame{Schema: contract.SessionSchema, Kind: "credit", Activation: activation, ID: id, Credit: n})
}

// abandon cancels a request the caller no longer waits for. What the callee
// had already sent is discarded until its response arrives.
func (e *Endpoint) abandon(id, activation string) {
	// Requests are issued one at a time. Taking that turn until the cancel
	// is queued keeps a new request from reaching the peer before it: the
	// peer still counts the canceled one, and would see its limit exceeded.
	select {
	case e.issue <- struct{}{}:
		defer func() { <-e.issue }()
	case <-e.done:
		return
	}
	if !e.removePending(id, true) {
		return
	}
	cancelCtx, cancel := context.WithTimeout(e.ctx, e.limits.Grace)
	defer cancel()
	if err := e.send(cancelCtx, Frame{Schema: contract.SessionSchema, Kind: "cancel", Activation: activation, ID: id}); err != nil && e.ctx.Err() == nil {
		e.shutdown(err)
		return
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
}

// receiveItem queues one item of a stream. It runs on the read loop with
// e.mu held and releases it.
func (e *Endpoint) receiveItem(frame Frame) error {
	defer e.mu.Unlock()
	p, ok := e.pending[frame.ID]
	if !ok {
		// Items of a stream the caller gave up on arrive until its response.
		if late := e.canceled[frame.ID]; late != nil && late.awaiting {
			return nil
		}
		return contract.Fail(contract.Invalid, "item of an unknown request")
	}
	if p.stream == nil {
		return contract.Fail(contract.Invalid, "item of a request that is not a stream")
	}
	if p.stream.received >= p.stream.granted {
		return contract.Fail(contract.Invalid, "item beyond the granted credit")
	}
	p.stream.received++
	p.stream.items <- frame.Payload // never blocks: the credit bounds what is buffered
	return nil
}

// receiveCredit adds credit to a stream this side serves. It runs on the
// read loop with e.mu held and releases it.
func (e *Endpoint) receiveCredit(frame Frame) error {
	defer e.mu.Unlock()
	in, ok := e.incoming[frame.ID]
	if !ok {
		return nil // the stream ended while the credit was on its way
	}
	if in.stream == nil {
		return contract.Fail(contract.Invalid, "credit for a request that is not a stream")
	}
	in.stream.credit += frame.Credit
	select {
	case in.stream.granted <- struct{}{}:
	default:
	}
	return nil
}

// serveStream runs the handler of a stream request and sends its items as
// credit allows.
func (e *Endpoint) serveStream(ctx context.Context, cancel context.CancelFunc, frame Frame, out *outflow) {
	defer cancel()
	defer func() {
		e.mu.Lock()
		if !e.incoming[frame.ID].canceled {
			e.activeIncoming--
		}
		delete(e.incoming, frame.ID)
		e.mu.Unlock()
	}()
	var err error
	if e.stream == nil {
		err = contract.Fail(contract.Incompatible, "this side does not serve streams")
	} else {
		// A canceled stream is answered at once. A handler that does not
		// watch its context goes on until its next item, which it can no
		// longer send; the caller is not made to wait for that.
		finished := make(chan error, 1)
		go func() { finished <- e.sendItems(ctx, frame, out) }()
		select {
		case err = <-finished:
		case <-ctx.Done():
		}
	}
	out.sending.Lock()
	out.answered = true
	out.sending.Unlock()
	if ctx.Err() != nil {
		err = contract.Fail(contract.Canceled, "request canceled")
	}
	response := Frame{Schema: contract.SessionSchema, Kind: "response", Activation: frame.Activation, ID: frame.ID, Method: frame.Method, Payload: json.RawMessage(`{}`)}
	if err != nil {
		response.Payload = nil
		if coded, ok := errors.AsType[*contract.Error](err); ok {
			response.Error = coded
		} else {
			response.Error = &contract.Error{Code: contract.PluginFailed, Message: "handler failed"}
		}
	}
	deliveryCtx, deliveryCancel := context.WithTimeout(e.ctx, e.limits.Grace)
	defer deliveryCancel()
	if err := e.sendResponse(ctx, deliveryCtx, response); err != nil && e.ctx.Err() == nil {
		e.shutdown(err)
	}
}

func (e *Endpoint) sendItems(ctx context.Context, frame Frame, out *outflow) (err error) {
	e.mu.Lock()
	limit := e.frameBytes
	e.mu.Unlock()
	for item, itemErr := range e.stream(ctx, frame.Method, frame.Payload) {
		if itemErr != nil {
			return itemErr
		}
		// Wait until the caller has room for one more item.
		for {
			e.mu.Lock()
			free := out.credit > 0
			if free {
				out.credit--
			}
			e.mu.Unlock()
			if free {
				break
			}
			select {
			case <-out.granted:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		if len(item) == 0 {
			item = json.RawMessage(`null`)
		}
		data, err := encodeFrame(Frame{Schema: contract.SessionSchema, Kind: "item", Activation: frame.Activation, ID: frame.ID, Payload: item}, limit)
		if err != nil {
			if _, oversized := errors.AsType[*frameSizeError](err); oversized {
				// One item that is too large fails its stream, not the session.
				return contract.Fail(contract.PluginFailed, "stream item exceeds frame byte limit")
			}
			return err
		}
		out.sending.Lock()
		if out.answered {
			out.sending.Unlock()
			return nil
		}
		err = e.enqueue(ctx, outbound{data: data})
		out.sending.Unlock()
		if err != nil {
			return err
		}
	}
	return nil
}
