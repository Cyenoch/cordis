package access

import (
	"cmp"
	"context"
	"fmt"
	"slices"

	"github.com/Cyenoch/cordis/contract"
)

// Validator is supplied by the host for a slot. It must reject executable view
// definitions and object handles unless the host's data schema explicitly allows them.
type Validator func(context.Context, contract.Identity, contract.Contribution) error

type OwnedContribution struct {
	Owner        contract.Identity     `json:"owner"`
	Contribution contract.Contribution `json:"contribution"`
}

type ContributionStore struct {
	policy     *Policy
	validators map[string]Validator
	published  map[contract.Identity][]contract.Contribution
	staged     map[*Batch]struct{}
	activeGate func(contract.Identity) bool
}

// NewContributionStore creates a store. It panics if policy is nil.
func NewContributionStore(policy *Policy) *ContributionStore {
	if policy == nil {
		panic("access: contribution store requires a policy")
	}
	store := &ContributionStore{
		policy: policy, validators: make(map[string]Validator),
		published: make(map[contract.Identity][]contract.Contribution), staged: make(map[*Batch]struct{}),
	}
	policy.mu.Lock()
	if policy.contributions == nil {
		policy.contributions = make(map[*ContributionStore]struct{})
	}
	policy.contributions[store] = struct{}{}
	policy.mu.Unlock()
	return store
}

// SetActiveGate binds visibility to the core activation commit. The callback is
// always invoked without the policy lock, so it may safely query core state.
func (s *ContributionStore) SetActiveGate(gate func(contract.Identity) bool) error {
	if gate == nil {
		return contract.Fail(contract.Invalid, "invalid activation gate")
	}
	s.policy.mu.Lock()
	s.activeGate = gate
	s.policy.mu.Unlock()
	return nil
}

func (s *ContributionStore) Register(slot string, validator Validator) error {
	if slot == "" || validator == nil {
		return contract.Fail(contract.Invalid, "incomplete contribution validator")
	}
	s.policy.mu.Lock()
	defer s.policy.mu.Unlock()
	if _, exists := s.validators[slot]; exists {
		return contract.Fail(contract.Conflict, "slot validator already registered")
	}
	s.validators[slot] = validator
	return nil
}

// Batch holds a complete replacement snapshot for its owner. Nothing is visible
// until Commit. A batch can be committed or aborted exactly once.
type Batch struct {
	store  *ContributionStore
	owner  contract.Identity
	items  []contract.Contribution
	closed bool
}

type ContributionError struct {
	Index int
	Cause error
}

func (e *ContributionError) Error() string { return fmt.Sprintf("contribution %d rejected", e.Index) }
func (e *ContributionError) Unwrap() error { return e.Cause }

func (s *ContributionStore) Stage(
	ctx context.Context, owner contract.Identity, items []contract.Contribution,
) (*Batch, error) {
	p := s.policy
	p.mu.Lock()
	if _, err := p.activeLocked(owner); err != nil {
		p.mu.Unlock()
		return nil, err
	}
	validators := make([]Validator, len(items))
	for i, item := range items {
		validators[i] = s.validators[item.Slot]
	}
	p.mu.Unlock()
	copyItems := make([]contract.Contribution, len(items))
	seen := make(map[struct{ slot, id string }]struct{}, len(items))
	for i, item := range items {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("stage contributions: %w", err)
		}
		key := struct{ slot, id string }{slot: item.Slot, id: item.ID}
		_, duplicate := seen[key]
		if item.ID == "" || validators[i] == nil || duplicate {
			return nil, &ContributionError{Index: i, Cause: contract.Fail(contract.Invalid, "invalid contribution key or slot")}
		}
		seen[key] = struct{}{}
		if err := contract.CheckJSON(item.Data, 32, 10000); err != nil {
			return nil, &ContributionError{Index: i, Cause: err}
		}
		copyItems[i] = cloneContribution(item)
		if err := validators[i](ctx, owner, cloneContribution(item)); err != nil {
			return nil, &ContributionError{Index: i, Cause: err}
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, err := p.activeLocked(owner); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("stage contributions: %w", err)
	}
	batch := &Batch{store: s, owner: owner, items: copyItems}
	s.staged[batch] = struct{}{}
	return batch, nil
}

// Commit must be invoked from the host activation's publication hook. It does no
// user callbacks; publication and revocation use the same policy critical section.
func (b *Batch) Commit() error {
	if b == nil || b.store == nil {
		return contract.Fail(contract.Invalid, "invalid contribution batch")
	}
	s := b.store
	s.policy.mu.Lock()
	defer s.policy.mu.Unlock()
	if b.closed {
		return contract.Fail(contract.Conflict, "contribution batch is closed")
	}
	if _, err := s.policy.activeLocked(b.owner); err != nil {
		return err
	}
	s.published[b.owner] = b.items
	b.items = nil
	b.closed = true
	delete(s.staged, b)
	return nil
}

func (b *Batch) Abort() error {
	if b == nil || b.store == nil {
		return contract.Fail(contract.Invalid, "invalid contribution batch")
	}
	b.store.policy.mu.Lock()
	defer b.store.policy.mu.Unlock()
	if b.closed {
		return contract.Fail(contract.Conflict, "contribution batch is closed")
	}
	b.closed = true
	b.items = nil
	delete(b.store.staged, b)
	return nil
}

// Revoke removes staged and visible data only for this owner. It is also called
// automatically by Policy.Revoke and by activation replacement.
func (s *ContributionStore) Revoke(owner contract.Identity) error {
	s.policy.mu.Lock()
	defer s.policy.mu.Unlock()
	s.revokeLocked(owner)
	return nil
}

func (s *ContributionStore) revokeLocked(owner contract.Identity) {
	delete(s.published, owner)
	for batch := range s.staged {
		if batch.owner != owner {
			continue
		}
		batch.closed = true
		batch.items = nil
		delete(s.staged, batch)
	}
}

func (s *ContributionStore) Snapshot() []OwnedContribution {
	s.policy.mu.Lock()
	gate := s.activeGate
	result := make([]OwnedContribution, 0)
	for owner, items := range s.published {
		for _, item := range items {
			result = append(result, OwnedContribution{Owner: owner, Contribution: cloneContribution(item)})
		}
	}
	s.policy.mu.Unlock()
	if gate != nil {
		visible := result[:0]
		owners := make(map[contract.Identity]bool)
		for _, item := range result {
			active, checked := owners[item.Owner]
			if !checked {
				active = gate(item.Owner)
				owners[item.Owner] = active
			}
			if active {
				visible = append(visible, item)
			}
		}
		result = visible
	}
	slices.SortFunc(result, func(a, b OwnedContribution) int {
		return cmp.Or(
			cmp.Compare(a.Owner.Activation, b.Owner.Activation),
			cmp.Compare(a.Contribution.Slot, b.Contribution.Slot),
			cmp.Compare(a.Contribution.ID, b.Contribution.ID),
		)
	})
	return result
}

func cloneContribution(item contract.Contribution) contract.Contribution {
	item.Data = append([]byte(nil), item.Data...)
	return item
}
