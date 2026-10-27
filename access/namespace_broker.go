package access

import (
	"context"
	"errors"

	"github.com/Cyenoch/cordis/contract"
)

// RegisterNamespace uses the broker's existing declaration, grant and scope
// admission. The host must configure the package binding separately.
func RegisterNamespace(broker *Broker, store *NamespaceStore, capability string, authorize ScopeHandler) error {
	if broker == nil || store == nil || store.policy == nil || store.policy != broker.policy {
		return contract.Fail(contract.Invalid, "namespace store must share broker policy")
	}
	return Register(broker, capability, authorize,
		func(ctx context.Context, owner contract.Identity, request contract.NamespaceRequest) (contract.NamespaceResult, error) {
			if err := request.Validate(); err != nil {
				return namespaceResult(nil, err), nil
			}
			name, err := store.namespaceFor(owner)
			if err != nil {
				return namespaceResult(nil, err), nil
			}
			var value []byte
			switch request.Operation {
			case contract.NamespaceRead:
				value, err = store.Read(ctx, owner, name, request.Key)
			case contract.NamespaceWrite:
				err = store.Write(ctx, owner, name, request.Key, request.Value)
			case contract.NamespaceDelete:
				err = store.Delete(ctx, owner, name, request.Key)
			}
			return namespaceResult(value, err), nil
		})
}

func (s *NamespaceStore) namespaceFor(owner contract.Identity) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for name, space := range s.spaces {
		if space.Package == owner.Package {
			return name, nil
		}
	}
	return "", contract.Fail(contract.Denied, "namespace not configured")
}

func namespaceResult(value []byte, err error) contract.NamespaceResult {
	result := contract.NamespaceResult{Outcome: contract.NamespaceOK, Value: value}
	var committed *CommittedError
	var recovery *RecoveryError
	var denied *contract.Error
	switch {
	case err == nil:
	case errors.As(err, &committed):
		result.Outcome = contract.NamespaceCommitted
	case errors.As(err, &recovery):
		result.Outcome = contract.NamespaceRecoveryRequired
	case errors.Is(err, ErrNotFound):
		result.Outcome = contract.NamespaceMissing
	case errors.Is(err, ErrQuota):
		result.Outcome = contract.NamespaceQuota
	case errors.As(err, &denied) && denied.Code == contract.Denied:
		result.Outcome = contract.NamespaceDenied
	default:
		result.Outcome = contract.NamespaceNotCommitted
	}
	return result
}
