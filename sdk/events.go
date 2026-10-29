package sdk

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"

	"github.com/Cyenoch/cordis/contract"
	"github.com/Cyenoch/cordis/wire"
)

type subscription struct {
	client   *Client
	spec     contract.SubscriptionSpec
	handler  contract.EventHandler
	inactive bool
}

func (c *Client) On(ctx context.Context, name string, options contract.SubscriptionOptions, handler contract.EventHandler) (contract.Subscription, error) {
	if name == "" || handler == nil {
		return nil, contract.Fail(contract.Invalid, "event name and handler are required")
	}
	select {
	case c.register <- struct{}{}:
		defer func() { <-c.register }()
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.ctx.Done():
		return nil, contract.Fail(contract.NotReady, "plugin is closing")
	}
	c.mu.Lock()
	if c.retiring || c.closed || !c.starting && !c.initialized {
		c.mu.Unlock()
		return nil, contract.Fail(contract.NotReady, "plugin cannot register subscriptions")
	}
	if err := ctx.Err(); err != nil {
		c.mu.Unlock()
		return nil, err
	}
	c.sequence++
	spec := contract.SubscriptionSpec{ID: c.identity.Activation + "/" + strconv.FormatUint(c.sequence, 10),
		Name: name, Options: options}
	sub := &subscription{client: c, spec: spec, handler: handler}
	c.events[spec.ID] = sub
	c.mu.Unlock()
	var acknowledged struct{}
	if err := c.subscriptionControl(ctx, "event.subscribe", spec, &acknowledged); err != nil {
		c.mu.Lock()
		c.removeSubscriptionLocked(sub)
		c.mu.Unlock()
		return nil, err
	}
	return sub, nil
}

func (c *Client) removeSubscriptionLocked(sub *subscription) {
	if c.events[sub.spec.ID] == sub {
		delete(c.events, sub.spec.ID)
	}
	sub.handler = nil
}

func (s *subscription) Cancel(ctx context.Context) error {
	c := s.client
	c.mu.Lock()
	inactive := s.inactive || c.retiring || c.closed
	c.mu.Unlock()
	if inactive {
		return nil
	}
	var state contract.SubscriptionState
	err := c.subscriptionControl(ctx, "event.unsubscribe", contract.SubscriptionCancel{ID: s.spec.ID}, &state)
	c.mu.Lock()
	defer c.mu.Unlock()
	if s.inactive {
		return nil
	}
	if err != nil {
		return err
	}
	if !state.Inactive {
		// A malformed acknowledgement cannot establish remote cancellation.
		c.mu.Unlock()
		c.endpoint.Close()
		c.mu.Lock()
		return contract.Fail(contract.Invalid, "subscription cancellation not acknowledged")
	}
	s.inactive = true
	c.removeSubscriptionLocked(s)
	return nil
}

func (c *Client) subscriptionControl(ctx context.Context, method string, input, output any) error {
	data, err := json.Marshal(input)
	if err != nil {
		return err
	}
	sent := false
	data, err = c.endpoint.CallWithReceipt(ctx, method, data, func(queued bool) { sent = queued })
	if err != nil {
		definite := false
		if coded, ok := errors.AsType[*contract.Error](err); ok {
			switch coded.Code {
			case contract.Invalid, contract.NotReady, contract.Denied, contract.Conflict:
				definite = true
			}
		}
		if sent && !definite {
			c.endpoint.Close()
		}
		return err
	}
	if err := contract.Decode(data, output); err != nil {
		c.endpoint.Close()
		return err
	}
	return nil
}

func (c *Client) prepare(_ context.Context, method string, input json.RawMessage) (wire.Handler, error) {
	if method != "event" {
		return nil, nil
	}
	var delivery contract.EventDelivery
	if err := contract.Decode(input, &delivery); err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, err := contract.SubscriptionNumber(c.identity.Activation, delivery.ID); err != nil {
		return nil, err
	}
	if len(delivery.Input) == 0 {
		return nil, contract.Fail(contract.Invalid, "event input is required")
	}
	if !c.published || c.retiring || c.closed {
		return nil, contract.Fail(contract.NotReady, "plugin is not active")
	}
	sub := c.events[delivery.ID]
	if sub == nil {
		return nil, contract.Fail(contract.Invalid, "unknown event subscription")
	}
	handler := sub.handler
	if sub.spec.Options.Once {
		sub.inactive = true
		c.removeSubscriptionLocked(sub)
	}
	// This closure owns the admitted callback even if cancellation removes the
	// registration before the asynchronous handler acquires the business gate.
	return func(ctx context.Context, _ string, _ json.RawMessage) (json.RawMessage, error) {
		return c.business(ctx, false, func(ctx context.Context) (json.RawMessage, error) {
			result, err := handler(ctx, delivery.Input)
			if err != nil {
				return nil, err
			}
			return json.Marshal(result)
		})
	}, nil
}
