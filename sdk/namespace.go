package sdk

import (
	"context"
	"fmt"

	"github.com/Cyenoch/cordis/contract"
)

// NamespaceClient carries no guest-selectable storage identity. Its invoker is
// bound by the host to the current activation; the capability is host-defined.
type NamespaceClient struct {
	invoker    contract.HostInvoker
	capability string
}

func NewNamespaceClient(invoker contract.HostInvoker, capability string) *NamespaceClient {
	return &NamespaceClient{invoker: invoker, capability: capability}
}

// Do returns a tagged result only when it is known. A zero result together with
// SubmissionUnknownError must never be interpreted as not-committed or retried.
func (c *NamespaceClient) Do(ctx context.Context, request contract.NamespaceRequest) (contract.NamespaceResult, error) {
	notCommitted := func(cause error) (contract.NamespaceResult, error) {
		return contract.NamespaceResult{Outcome: contract.NamespaceNotCommitted}, &contract.NotCommittedError{Cause: cause}
	}
	if ctx == nil || c == nil || c.invoker == nil || c.capability == "" {
		return notCommitted(contract.Fail(contract.Invalid, "namespace client not configured"))
	}
	if err := ctx.Err(); err != nil {
		return notCommitted(err)
	}
	if err := request.Validate(); err != nil {
		return notCommitted(err)
	}
	data, err := contract.Encode(request)
	if err != nil {
		return notCommitted(err)
	}
	if err := ctx.Err(); err != nil {
		return notCommitted(err)
	}
	data, err = c.invoker.InvokeHost(ctx, c.capability, data)
	if err != nil {
		return contract.NamespaceResult{}, &contract.SubmissionUnknownError{Cause: err}
	}
	if err := ctx.Err(); err != nil {
		return contract.NamespaceResult{}, &contract.SubmissionUnknownError{Cause: err}
	}
	var result contract.NamespaceResult
	if err := contract.DecodeData(data, &result); err != nil {
		return contract.NamespaceResult{}, &contract.SubmissionUnknownError{Cause: fmt.Errorf("decode namespace result: %w", err)}
	}
	if err := result.Validate(request.Operation); err != nil {
		return contract.NamespaceResult{}, &contract.SubmissionUnknownError{Cause: err}
	}
	if err := ctx.Err(); err != nil {
		return contract.NamespaceResult{}, &contract.SubmissionUnknownError{Cause: err}
	}
	return result, result.Err()
}

func (c *NamespaceClient) Read(ctx context.Context, key string) ([]byte, error) {
	result, err := c.Do(ctx, contract.NamespaceRequest{Operation: contract.NamespaceRead, Key: key})
	return result.Value, err
}

func (c *NamespaceClient) Write(ctx context.Context, key string, value []byte) error {
	_, err := c.Do(ctx, contract.NamespaceRequest{Operation: contract.NamespaceWrite, Key: key, Value: value})
	return err
}

func (c *NamespaceClient) Delete(ctx context.Context, key string) error {
	_, err := c.Do(ctx, contract.NamespaceRequest{Operation: contract.NamespaceDelete, Key: key})
	return err
}
