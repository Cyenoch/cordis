// Package access implements activation-owned host access, contributions and tasks.
// Host handlers own resource semantics and external side effects.
package access

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/Cyenoch/cordis/contract"
)

type activation struct {
	commit   sync.Mutex
	owner    contract.Identity
	declared map[string]json.RawMessage
	grants   map[string]*Grant
	calls    map[*Lease]struct{}
}

type instanceKey struct{ packageID, entry string }

// Policy is host-owned. All admission and revocation share its mutex.
// A Policy must not be copied after first use. Its zero value is ready for use.
type Policy struct {
	mu            sync.Mutex
	current       map[instanceKey]*activation
	seen          map[string]struct{}
	contributions map[*ContributionStore]struct{}
	tasks         map[*TaskManager]struct{}
	calls         map[*Lease]struct{}
}

func NewPolicy() *Policy { return &Policy{} }

// Activate establishes an identity from trusted host metadata, not guest input.
// Replacing an entry revokes its previous activation atomically. IDs cannot recur.
func (p *Policy) Activate(owner contract.Identity, capabilities []contract.Capability) error {
	if owner.Package == "" || owner.Entry == "" || owner.Activation == "" {
		return contract.Fail(contract.Invalid, "incomplete activation identity")
	}
	declared := make(map[string]json.RawMessage, len(capabilities))
	for _, capability := range capabilities {
		if capability.ID == "" {
			return contract.Fail(contract.Invalid, "empty capability")
		}
		if _, exists := declared[capability.ID]; exists {
			return contract.Fail(contract.Invalid, "duplicate capability")
		}
		if len(capability.Scope) > 0 {
			if err := contract.CheckJSON(capability.Scope, 32, 10000); err != nil {
				return contract.Fail(contract.Invalid, "invalid capability scope")
			}
		}
		declared[capability.ID] = append(json.RawMessage(nil), capability.Scope...)
	}
	previous := p.lockCurrent(instanceKey{packageID: owner.Package, entry: owner.Entry})
	defer p.unlockCurrent(previous)
	if p.seen == nil {
		p.seen = make(map[string]struct{})
	}
	if _, exists := p.seen[owner.Activation]; exists {
		return contract.Fail(contract.Conflict, "activation identity already used")
	}
	if p.current == nil {
		p.current = make(map[instanceKey]*activation)
	}
	key := instanceKey{packageID: owner.Package, entry: owner.Entry}
	if previous != nil {
		p.revokeLocked(previous)
	}
	p.seen[owner.Activation] = struct{}{}
	p.current[key] = &activation{
		owner: owner, declared: declared,
		grants: make(map[string]*Grant), calls: make(map[*Lease]struct{}),
	}
	return nil
}

func (p *Policy) activeLocked(owner contract.Identity) (*activation, error) {
	a := p.current[instanceKey{packageID: owner.Package, entry: owner.Entry}]
	if a == nil || a.owner != owner {
		return nil, contract.Fail(contract.Denied, "activation is not current")
	}
	return a, nil
}

func (p *Policy) Check(owner contract.Identity) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	_, err := p.activeLocked(owner)
	return err
}

func (p *Policy) Revoke(owner contract.Identity) error {
	previous := p.lockCurrent(instanceKey{packageID: owner.Package, entry: owner.Entry})
	defer p.unlockCurrent(previous)
	a, err := p.activeLocked(owner)
	if err != nil {
		return err
	}
	p.revokeLocked(a)
	return nil
}

// Only the affected activation waits for its durable commit. Never wait for
// disk while holding the policy registry lock needed by unrelated activations.
func (p *Policy) lockCurrent(key instanceKey) *activation {
	for {
		p.mu.Lock()
		a := p.current[key]
		if a == nil {
			return nil
		}
		p.mu.Unlock()
		a.commit.Lock()
		p.mu.Lock()
		if p.current[key] == a {
			return a
		}
		p.mu.Unlock()
		a.commit.Unlock()
	}
}

func (p *Policy) unlockCurrent(a *activation) {
	p.mu.Unlock()
	if a != nil {
		a.commit.Unlock()
	}
}

func (p *Policy) revokeLocked(a *activation) {
	delete(p.current, instanceKey{packageID: a.owner.Package, entry: a.owner.Entry})
	for _, grant := range a.grants {
		grant.revoked = true
	}
	for lease := range a.calls {
		lease.cancel()
	}
	for store := range p.contributions {
		store.revokeLocked(a.owner)
	}
	for manager := range p.tasks {
		manager.revokeLocked(a.owner)
	}
}

// Grant is an unforgeable host-side handle. Granting again revokes the old grant.
type Grant struct {
	policy     *Policy
	owner      contract.Identity
	capability string
	scope      json.RawMessage
	revoked    bool
}

func (p *Policy) Grant(owner contract.Identity, capability string, scope json.RawMessage) (*Grant, error) {
	if len(scope) > 0 {
		if err := contract.CheckJSON(scope, 32, 10000); err != nil {
			return nil, contract.Fail(contract.Invalid, "invalid grant scope")
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	a, err := p.activeLocked(owner)
	if err != nil {
		return nil, err
	}
	if _, declared := a.declared[capability]; !declared {
		return nil, contract.Fail(contract.Denied, "capability not declared")
	}
	if previous := a.grants[capability]; previous != nil {
		p.revokeGrantLocked(a, previous)
	}
	grant := &Grant{policy: p, owner: owner, capability: capability, scope: append(json.RawMessage(nil), scope...)}
	a.grants[capability] = grant
	return grant, nil
}

// RevokeCapability revokes the current grant using a trusted activation identity.
// A missing grant returns NotReady; revoking an already revoked grant is idempotent.
func (p *Policy) RevokeCapability(owner contract.Identity, capability string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	a, err := p.activeLocked(owner)
	if err != nil {
		return err
	}
	grant := a.grants[capability]
	if grant == nil {
		return contract.Fail(contract.NotReady, "capability grant not found")
	}
	if !grant.revoked {
		p.revokeGrantLocked(a, grant)
	}
	return nil
}

func (g *Grant) Revoke() error {
	if g == nil || g.policy == nil {
		return contract.Fail(contract.Invalid, "invalid grant")
	}
	p := g.policy
	p.mu.Lock()
	defer p.mu.Unlock()
	if g.revoked {
		return nil
	}
	a, err := p.activeLocked(g.owner)
	if err != nil {
		return err
	}
	p.revokeGrantLocked(a, g)
	return nil
}

func (p *Policy) revokeGrantLocked(a *activation, grant *Grant) {
	grant.revoked = true
	// Retain the current handle so host lookup can distinguish revocation from absence.
	for lease := range a.calls {
		if lease.grant == grant {
			lease.cancel()
		}
	}
}

// Lease represents one admitted call. The broker releases it when the handler exits.
// Its identity and cancellation cannot be supplied by a guest.
type Lease struct {
	policy     *Policy
	activation *activation
	grant      *Grant
	ctx        context.Context
	cancel     context.CancelFunc
}

func (l *Lease) Context() context.Context { return l.ctx }

func (l *Lease) release() {
	l.policy.mu.Lock()
	delete(l.activation.calls, l)
	delete(l.policy.calls, l)
	l.cancel()
	l.policy.mu.Unlock()
}

// Calls reports admitted operations, including canceled operations that have not
// returned yet. Cancellation does not imply rollback or completed system calls.
type CallStatus struct {
	Owner      contract.Identity `json:"owner"`
	Capability string            `json:"capability"`
	Canceled   bool              `json:"canceled"`
}

func (p *Policy) Calls(owner contract.Identity) []CallStatus {
	p.mu.Lock()
	defer p.mu.Unlock()
	result := make([]CallStatus, 0)
	for lease := range p.calls {
		if lease.activation.owner != owner {
			continue
		}
		result = append(result, CallStatus{
			Owner: owner, Capability: lease.grant.capability, Canceled: lease.ctx.Err() != nil,
		})
	}
	return result
}
