package sdk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/Cyenoch/cordis/contract"
)

var _ contract.ManagedScope = (*Client)(nil)

// Go registers explicitly concurrent work during Start. Work starts only after
// the host's final publication and must return when its activation is canceled.
func (c *Client) Go(work func(context.Context) error) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.starting || c.initialized || c.retiring || c.closed {
		return contract.Fail(contract.NotReady, "managed work registers during Start only")
	}
	if work == nil {
		return contract.Fail(contract.Invalid, "nil managed work")
	}
	c.work = append(c.work, work)
	return nil
}

func (c *Client) publish(input json.RawMessage) (json.RawMessage, error) {
	var request struct{}
	if err := contract.Decode(input, &request); err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.initialized || c.retiring || c.closed || c.ctx.Err() != nil {
		return nil, contract.Fail(contract.NotReady, "plugin cannot publish")
	}
	if !c.published {
		c.published = true
		for _, work := range c.work {
			c.workers.Add(1)
			go c.runWork(work)
		}
		c.work = nil
	}
	return json.RawMessage(`{}`), nil
}

func (c *Client) retire() {
	c.mu.Lock()
	c.retiring = true
	c.work = nil
	c.cancel()
	c.mu.Unlock()
}

func (c *Client) runWork(work func(context.Context) error) {
	defer c.workers.Done()
	err := invokeWork(c.ctx, work)
	if err == nil {
		return
	}
	c.mu.Lock()
	c.workErr = errors.Join(c.workErr, fmt.Errorf("managed work: %w", err))
	c.mu.Unlock()
	c.retire()
	// Closing the session makes failure observable to the host; Serve retains
	// the local cause instead of replacing it with the endpoint's EOF.
	_ = c.endpoint.Close()
}

func invokeWork(ctx context.Context, work func(context.Context) error) (err error) {
	defer func() {
		if value := recover(); value != nil {
			if cause, ok := value.(error); ok {
				err = fmt.Errorf("panic: %w", cause)
			} else {
				err = fmt.Errorf("panic: %v", value)
			}
		}
	}()
	err = work(ctx)
	if ctx.Err() != nil && errors.Is(err, ctx.Err()) {
		return nil
	}
	return err
}
