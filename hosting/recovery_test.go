package hosting

import (
	"context"
	"errors"
	"maps"
	"strings"
	"testing"
	"time"

	"github.com/Cyenoch/cordis"
	"github.com/Cyenoch/cordis/contract"
	"github.com/Cyenoch/cordis/sdk"
)

// failing is a definition whose first failures starts fail.
func (f *fixture) failing(name string, failures int) sdk.Definition {
	return sdk.Define(cordis.Plugin[struct{}]{Name: "main", Start: func(context.Context, *cordis.Scope, struct{}) error {
		f.log.add(name)
		if f.log.count(name) <= failures {
			return errors.New(name + " is not ready")
		}
		return nil
	}})
}

// recovery is the recovery status of id, if it has a policy.
func (f *fixture) recovery(id string) (RecoveryInfo, bool) {
	for _, info := range f.host.RecoveryStatus() {
		if info.Entry == id {
			return info, true
		}
	}
	return RecoveryInfo{}, false
}

// quiet checks that name makes no further attempts for a while in which a
// policy with a short delay would have retried it several times.
func (f *fixture) quiet(name string) {
	f.t.Helper()
	before := f.log.count(name)
	time.Sleep(150 * time.Millisecond)
	if after := f.log.count(name); after != before {
		f.t.Fatalf("%s was retried: %d attempts, then %d", name, before, after)
	}
}

func TestRecoveryCanBeSetWhileTheHostRuns(t *testing.T) {
	f := newFixture(t)
	f.pack(plainManifest("test.flaky"), f.failing("flaky", 2))
	f.pack(plainManifest("test.broken"), f.failing("broken", 1000))
	f.open()
	policy := RecoveryPolicy{MaxRetries: 3, InitialDelay: 10 * time.Millisecond, MaxDelay: 20 * time.Millisecond}

	// An entry enabled while the host runs gets a policy, and recovers by it.
	if err := f.enable(contract.Entry{ID: "flaky", Package: "test.flaky"}).Wait(f.ctx()); err == nil {
		t.Fatal("flaky started")
	}
	must(t, f.host.SetRecovery(f.ctx(), "flaky", policy))
	eventually(t, "flaky to recover", func() bool { return f.state("flaky") == contract.Active })
	if info, _ := f.recovery("flaky"); info.Used != 2 || info.MaxRetries != 3 || !strings.Contains(info.Error, "flaky is not ready") {
		t.Fatalf("recovery of flaky = %+v", info)
	}

	// Setting the same policy again keeps the cycle and what it has used.
	must(t, f.host.SetRecovery(f.ctx(), "flaky", policy))
	if info, _ := f.recovery("flaky"); info.Used != 2 {
		t.Fatalf("recovery after the same policy = %+v", info)
	}

	// Changing the policy starts a new cycle with the new budget.
	policy.MaxRetries = 5
	must(t, f.host.SetRecovery(f.ctx(), "flaky", policy))
	if info, _ := f.recovery("flaky"); info.Used != 0 || info.MaxRetries != 5 || info.State != RecoveryIdle || info.Error != "" {
		t.Fatalf("recovery after a change = %+v", info)
	}

	// The checks are those of Options.Recovery.
	for name, bad := range map[string]func() error{
		"no entry": func() error { return f.host.SetRecovery(f.ctx(), "nowhere", policy) },
		"no retries": func() error {
			return f.host.SetRecovery(f.ctx(), "flaky", RecoveryPolicy{InitialDelay: time.Second, MaxDelay: time.Second})
		},
		"shorter limit": func() error {
			return f.host.SetRecovery(f.ctx(), "flaky", RecoveryPolicy{MaxRetries: 1, InitialDelay: time.Second, MaxDelay: time.Millisecond})
		},
	} {
		if err := bad(); contract.ErrorCode(err) != contract.Invalid {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if info, _ := f.recovery("flaky"); info.MaxRetries != 5 {
		t.Fatalf("a rejected policy changed the recovery: %+v", info)
	}

	// A cleared policy retries nothing more.
	if err := f.enable(contract.Entry{ID: "broken", Package: "test.broken"}).Wait(f.ctx()); err == nil {
		t.Fatal("broken started")
	}
	must(t, f.host.SetRecovery(f.ctx(), "broken", RecoveryPolicy{MaxRetries: 1000, InitialDelay: 10 * time.Millisecond, MaxDelay: 10 * time.Millisecond}))
	eventually(t, "broken to be retried", func() bool { return f.log.count("broken") > 2 })
	must(t, f.host.ClearRecovery(f.ctx(), "broken"))
	must(t, f.host.ClearRecovery(f.ctx(), "broken"))
	if _, exists := f.recovery("broken"); exists {
		t.Fatal("a cleared policy is still reported")
	}
	f.quiet("broken")

	// Removing an entry clears its policy: an entry enabled again with the
	// same ID is a new one and is not retried.
	must(t, f.host.SetRecovery(f.ctx(), "broken", policy))
	must(t, f.host.Remove(f.ctx(), "broken"))
	if _, exists := f.recovery("broken"); exists {
		t.Fatal("the policy of a removed entry is still reported")
	}
	if err := f.enable(contract.Entry{ID: "broken", Package: "test.broken"}).Wait(f.ctx()); err == nil {
		t.Fatal("broken started")
	}
	f.quiet("broken")
	if _, exists := f.recovery("broken"); exists {
		t.Fatal("the entry enabled again took the policy of the removed one")
	}
}

func TestRecoveryRemovesAPolicyGivenAtOpen(t *testing.T) {
	f := newFixture(t)
	f.pack(plainManifest("test.broken"), f.failing("broken", 1000))
	f.open()
	f.enable(contract.Entry{ID: "broken", Package: "test.broken"}).Wait(f.ctx())
	f.host.Close(f.ctx())
	f.open(func(o *Options) {
		o.Recovery = map[string]RecoveryPolicy{"broken": {MaxRetries: 1000, InitialDelay: 10 * time.Millisecond, MaxDelay: 10 * time.Millisecond}}
	})
	must(t, f.host.Remove(f.ctx(), "broken"))
	if status := f.host.RecoveryStatus(); len(status) != 0 {
		t.Fatalf("recovery after remove = %+v", status)
	}
	if err := f.enable(contract.Entry{ID: "broken", Package: "test.broken"}).Wait(f.ctx()); err == nil {
		t.Fatal("broken started")
	}
	f.quiet("broken")
}

func TestRecoveryDoesNotRetryAPackageTheTrustPolicyRefuses(t *testing.T) {
	f := newFixture(t)
	ref := f.pack(plainManifest("test.plain"), f.failing("plain", 0))
	f.open()
	must(t, f.enable(contract.Entry{ID: "plain", Package: "test.plain"}).Wait(f.ctx()))
	f.host.Close(f.ctx())

	// The host no longer approves the package's digest when it opens again.
	refused := f.trust
	refused.Digests = map[string]bool{}
	f.open(func(o *Options) {
		o.Trust = refused
		o.Recovery = map[string]RecoveryPolicy{"plain": {MaxRetries: 3, InitialDelay: 10 * time.Millisecond, MaxDelay: 10 * time.Millisecond}}
	})
	f.host.Restore(f.ctx())
	eventually(t, "recovery to give up on the refused package", func() bool {
		info, _ := f.recovery("plain")
		return info.State == RecoveryDenied
	})
	f.quiet("plain")
	info, _ := f.recovery("plain")
	if info.State != RecoveryDenied || info.Used != 0 || info.NextRetryAt != nil ||
		!strings.Contains(info.Error, "requires an authorized signature or an approved exact digest") {
		t.Fatalf("recovery of a refused package = %+v", info)
	}
	if status, _ := f.host.Status("plain"); status.State != contract.Failed || status.Outcome.FailureCode != contract.Denied {
		t.Fatalf("status of a refused package = %+v", status)
	}

	// Once the host approves it, a retry starts a new cycle and runs it.
	approved := refused
	approved.Digests = maps.Clone(f.trust.Digests)
	approved.Digests[ref.Digest] = true
	must(t, f.host.UpdateTrust(approved))
	must(t, f.host.Retry(f.ctx(), "plain"))
	if info, _ := f.recovery("plain"); info.State != RecoveryIdle || info.Error != "" {
		t.Fatalf("recovery after an approved retry = %+v", info)
	}
}

// A plugin that fails with denied itself is retried: only the host's own
// refusal to run a package is final.
func TestRecoveryRetriesAPluginThatReportsDenied(t *testing.T) {
	f := newFixture(t)
	f.pack(plainManifest("test.denying"), sdk.Define(cordis.Plugin[struct{}]{Name: "main", Start: func(context.Context, *cordis.Scope, struct{}) error {
		f.log.add("denying")
		if f.log.count("denying") <= 1 {
			return contract.Fail(contract.Denied, "credentials rejected")
		}
		return nil
	}}))
	f.open()
	if err := f.enable(contract.Entry{ID: "denying", Package: "test.denying"}).Wait(f.ctx()); contract.ErrorCode(err) != contract.Denied {
		t.Fatalf("first start = %v", err)
	}
	must(t, f.host.SetRecovery(f.ctx(), "denying", RecoveryPolicy{MaxRetries: 2, InitialDelay: 10 * time.Millisecond, MaxDelay: 10 * time.Millisecond}))
	eventually(t, "the plugin to be retried", func() bool { return f.state("denying") == contract.Active })
}
