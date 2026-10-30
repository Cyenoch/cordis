package cordis

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"math"
	"testing"
	"time"

	"github.com/Cyenoch/cordis/contract"
)

type recoveryLaunch struct {
	identity contract.Identity
	at       time.Time
}

type recoveryRuntime struct {
	started  chan recoveryLaunch
	shutdown func(context.Context) error
}

func (r recoveryRuntime) Start(_ context.Context, launch contract.Launch) (contract.Session, error) {
	r.started <- recoveryLaunch{launch.Identity, time.Now()}
	return &recoverySession{fixtureSession: fixtureSession{done: make(chan struct{})}, shutdown: r.shutdown}, nil
}

type recoverySession struct {
	fixtureSession
	shutdown func(context.Context) error
}

func (s *recoverySession) Call(ctx context.Context, method string, input json.RawMessage) (json.RawMessage, error) {
	if method == "shutdown" && s.shutdown != nil {
		return json.RawMessage(`{}`), s.shutdown(ctx)
	}
	return s.fixtureSession.Call(ctx, method, input)
}

func recoveryFixture(t *testing.T, policies map[string]RecoveryPolicy, adapter contract.Runtime) (*Host, Options) {
	t.Helper()
	old, ref, _ := lifecycleHost(t)
	for _, id := range []string{"target", "other"} {
		if err := old.store.SaveEntry(t.Context(), contract.Entry{ID: id, Package: ref.ID, Definition: "main", Enabled: true, Config: json.RawMessage(`{}`)}); err != nil {
			t.Fatal(err)
		}
	}
	options := old.options
	options.Recovery, options.Runtime = policies, adapter
	if err := old.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	h, err := Open(options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = h.Close(ctx)
		select {
		case <-h.Done():
		default:
			t.Error("recovery Host did not finish closing")
		}
	})
	return h, options
}

func awaitRecovery(t *testing.T, h *Host, state RecoveryState) RecoveryInfo {
	t.Helper()
	deadline := time.After(5 * time.Second)
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		info := h.RecoveryStatus()[0]
		if info.State == state {
			return info
		}
		select {
		case <-deadline:
			t.Fatalf("recovery did not reach %s: %+v", state, info)
		case <-ticker.C:
		}
	}
}

func TestRecoveryBudgetBackoffAndManualRetry(t *testing.T) {
	policy := RecoveryPolicy{MaxRetries: 3, InitialDelay: 50 * time.Millisecond, MaxDelay: 80 * time.Millisecond}
	starts := make(chan recoveryLaunch, 16)
	input := map[string]RecoveryPolicy{"target": policy}
	h, _ := recoveryFixture(t, input, recoveryRuntime{started: starts})
	input["target"] = RecoveryPolicy{MaxRetries: 99, InitialDelay: time.Hour, MaxDelay: time.Hour}
	if _, err := h.Restore(t.Context()); err != nil {
		t.Fatal(err)
	}
	<-starts
	<-starts
	target, _ := h.Instance("target")
	other, _ := h.Instance("other")
	peer := other.Status().Identity
	h.engine.FailActivation(peer, errors.New("unconfigured peer failure"))
	_ = other.Wait(t.Context())
	for attempt := 0; attempt < 4; attempt++ {
		before := target.Status().Identity
		failedAt := time.Now()
		h.engine.FailActivation(before, errors.New("crashed"))
		_ = target.Wait(t.Context())
		if attempt == 3 {
			info := awaitRecovery(t, h, RecoveryExhausted)
			if info.Used != 3 || info.MaxRetries != 3 {
				t.Fatalf("budget refreshed after brief Active: %+v", info)
			}
			break
		}
		info := awaitRecovery(t, h, RecoveryScheduled)
		minimum := policy.InitialDelay
		if attempt > 0 {
			minimum = policy.MaxDelay
		}
		next := *info.NextRetryAt
		if next.Before(failedAt.Add(minimum)) || info.Used != attempt {
			t.Fatalf("incorrect backoff: attempt=%d %+v", attempt, info)
		}
		*info.NextRetryAt = time.Time{}
		if fresh := h.RecoveryStatus()[0]; fresh.NextRetryAt != nil && fresh.NextRetryAt.IsZero() {
			t.Fatal("snapshot mutation changed scheduler state")
		}
		launch := <-starts
		if launch.identity.Entry != "target" || launch.identity == before || launch.at.Before(next) {
			t.Fatalf("wrong or early retry: %+v scheduled=%v", launch, next)
		}
		if err := target.Wait(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case extra := <-starts:
		t.Fatalf("unexpected retry: %+v", extra)
	default:
	}
	if err := h.Retry(t.Context(), "target"); err != nil {
		t.Fatal(err)
	}
	<-starts
	if info := h.RecoveryStatus()[0]; info.Used != 0 {
		t.Fatalf("manual retry did not reset target budget: %+v", info)
	}
	if after := other.Status(); after.State != contract.Failed || after.Identity != peer {
		t.Fatalf("policy affected another entry: %+v", after)
	}
}

type recoveryLogGate struct {
	entered, release chan struct{}
}

func (g recoveryLogGate) Enabled(context.Context, slog.Level) bool { return true }
func (g recoveryLogGate) Handle(_ context.Context, record slog.Record) error {
	record.Attrs(func(attr slog.Attr) bool {
		if record.Message == "plugin recovery" && attr.Key == "stage" && attr.Value.String() == "scheduled" {
			close(g.entered)
			<-g.release
		}
		return true
	})
	return nil
}
func (g recoveryLogGate) WithAttrs([]slog.Attr) slog.Handler { return g }
func (g recoveryLogGate) WithGroup(string) slog.Handler      { return g }

func TestCloseSubmitsRetirementEvenWhenRecoveryJoinTimesOut(t *testing.T) {
	starts := make(chan recoveryLaunch, 8)
	initial, options := recoveryFixture(t, map[string]RecoveryPolicy{
		"target": {MaxRetries: 1, InitialDelay: time.Hour, MaxDelay: time.Hour},
	}, recoveryRuntime{started: starts})
	if err := initial.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	gate := recoveryLogGate{entered: make(chan struct{}), release: make(chan struct{})}
	options.Logger = slog.New(gate)
	h, err := Open(options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close(context.Background()) })
	t.Cleanup(func() { close(gate.release) })
	if _, err := h.Restore(t.Context()); err != nil {
		t.Fatal(err)
	}
	target, _ := h.Instance("target")
	healthy, _ := h.Instance("other")
	h.engine.FailActivation(target.Status().Identity, errors.New("failure"))
	<-gate.entered
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if err := h.Close(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected recovery join timeout: %v", err)
	}
	if state := healthy.Status().State; state != contract.Stopped && state != contract.Quiescing {
		t.Fatalf("accepted Close did not submit retirement: %s", state)
	}
	select {
	case <-h.Done():
		t.Fatal("Host.Done closed before recovery worker joined")
	default:
	}
}

func TestRecoveryRejectedChangesKeepPlanAndStopCancelsIt(t *testing.T) {
	policy := RecoveryPolicy{MaxRetries: 2, InitialDelay: 300 * time.Millisecond, MaxDelay: time.Second}
	starts := make(chan recoveryLaunch, 16)
	h, options := recoveryFixture(t, map[string]RecoveryPolicy{"target": policy}, recoveryRuntime{started: starts})
	if _, err := h.Restore(t.Context()); err != nil {
		t.Fatal(err)
	}
	<-starts
	<-starts
	target, _ := h.Instance("target")
	h.engine.FailActivation(target.Status().Identity, errors.New("failure"))
	_ = target.Wait(t.Context())
	before := awaitRecovery(t, h, RecoveryScheduled)
	if _, err := target.Update(t.Context(), json.RawMessage(`{"unknown":1}`)); err == nil {
		t.Fatal("invalid config accepted")
	}
	if err := h.Replace(t.Context(), contract.PackageRef{ID: "test.package", Version: "9.9.9", Digest: "absent"}); err == nil {
		t.Fatal("invalid package replacement accepted")
	}
	after := h.RecoveryStatus()[0]
	if after.Used != before.Used || after.NextRetryAt == nil || !after.NextRetryAt.Equal(*before.NextRetryAt) {
		t.Fatalf("rejected change modified the plan: before=%+v after=%+v", before, after)
	}
	_ = target.Stop(t.Context()) // Stop may report the historical failure.
	time.Sleep(time.Until(*before.NextRetryAt) + 50*time.Millisecond)
	if info := h.RecoveryStatus()[0]; info.State != RecoveryCanceled || target.Status().State != contract.Stopped {
		t.Fatalf("stale plan revived a stopped entry: %+v %+v", info, target.Status())
	}
	select {
	case start := <-starts:
		t.Fatalf("stopped plan launched: %+v", start)
	default:
	}
	if err := h.Resume(t.Context(), "target"); err != nil {
		t.Fatal(err)
	}
	<-starts
	if info := h.RecoveryStatus()[0]; info.Used != 0 {
		t.Fatal("resume retained an old budget")
	}
	if err := h.Remove(t.Context(), "target"); err != nil {
		t.Fatal(err)
	}
	awaitRecovery(t, h, RecoveryMissing)
	ref, _ := h.store.Selected("test.package")
	replacement, err := h.Enable(t.Context(), contract.Entry{ID: "target", Package: ref.Ref.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err := replacement.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	<-starts
	if _, err := target.Update(t.Context(), json.RawMessage(`{}`)); err == nil {
		t.Fatal("old wrapper controlled the new recovery cycle")
	}
	if info := h.RecoveryStatus()[0]; info.Used != 0 || info.Identity == before.Identity {
		t.Fatalf("new registration inherited stale plan: %+v", info)
	}
	_ = h.Close(t.Context())
	select {
	case <-h.recoveryDone:
	default:
		t.Fatal("Host closed before recovery loop")
	}
	reopened, err := Open(options)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close(context.Background())
	if reopened.RecoveryStatus()[0].Used != 0 {
		t.Fatal("reopening retained an old budget")
	}
}

func TestRecoveryWaitsForCleanupAndIsolatesCleanupFailure(t *testing.T) {
	entered, release := make(chan struct{}, 1), make(chan struct{})
	policy := RecoveryPolicy{MaxRetries: 1, InitialDelay: time.Millisecond, MaxDelay: time.Millisecond}
	starts := make(chan recoveryLaunch, 16)
	h, _ := recoveryFixture(t, map[string]RecoveryPolicy{"target": policy}, recoveryRuntime{
		started: starts, shutdown: func(context.Context) error {
			entered <- struct{}{}
			<-release
			return errors.New("cleanup failure")
		},
	})
	t.Cleanup(func() { close(release) })
	i, err := h.Enable(t.Context(), contract.Entry{ID: "target", Package: "test.package"})
	if err != nil {
		t.Fatal(err)
	}
	if err := i.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	<-starts
	h.engine.FailActivation(i.Status().Identity, errors.New("crash"))
	<-entered
	time.Sleep(75 * time.Millisecond)
	if status := i.Status(); status.State != contract.Quiescing || h.RecoveryStatus()[0].Used != 0 {
		t.Fatalf("recovery overlapped cleanup: %+v", status)
	}
	release <- struct{}{}
	info := awaitRecovery(t, h, RecoveryCleanupFailed)
	if info.Used != 0 || info.NextRetryAt != nil {
		t.Fatalf("cleanup failure was scheduled: %+v", info)
	}
	if err := h.Retry(t.Context(), "target"); err != nil {
		t.Fatal(err)
	}
	<-starts
}

func TestRecoveryPolicyValidationAndDelaySaturation(t *testing.T) {
	h, options := recoveryFixture(t, nil, fixtureRuntime{})
	if len(h.RecoveryStatus()) != 0 || h.recoveryDone != nil {
		t.Fatal("default Host started recovery")
	}
	if err := h.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, policies := range []map[string]RecoveryPolicy{
		{"missing": {MaxRetries: 1, InitialDelay: 1, MaxDelay: 1}},
		{"target": {MaxRetries: 0, InitialDelay: 1, MaxDelay: 1}},
		{"target": {MaxRetries: 1, InitialDelay: 0, MaxDelay: 1}},
		{"target": {MaxRetries: 1, InitialDelay: 2, MaxDelay: 1}},
	} {
		options.Recovery = policies
		if invalid, err := Open(options); err == nil {
			invalid.Close(t.Context())
			t.Fatalf("invalid recovery policy accepted: %+v", policies)
		}
	}
	maximum := time.Duration(math.MaxInt64)
	if delay := recoveryDelay(RecoveryPolicy{InitialDelay: maximum / 2, MaxDelay: maximum}, math.MaxInt); delay != maximum {
		t.Fatalf("backoff overflow: %v", delay)
	}
}
