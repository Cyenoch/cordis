//go:build integration

package packagehost

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Cyenoch/cordis/contract"
)

func runOwner(activation string) contract.Identity {
	return contract.Identity{Package: "demo", Entry: "entry", Activation: activation, Digest: strings.Repeat("a", 64)}
}

func TestRunsRetainRunningAndBoundTerminalHistory(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := t.TempDir()
	s := openFixture(t, root, TrustPolicy{})
	live := runOwner("live")
	if err := s.BeginRun(ctx, live); err != nil {
		t.Fatal(err)
	}
	for i := range 260 {
		owner := runOwner(fmt.Sprintf("activation-%03d", i))
		if err := s.BeginRun(ctx, owner); err != nil {
			t.Fatal(err)
		}
		largeError := contract.Fail("business_failure", strings.Repeat("large-error", 1<<14))
		code := 17
		outcome := contract.Outcome{Reason: contract.ExitUnexpected, ExitCode: &code,
			FailureCode: contract.ErrorCode(largeError), CleanupCode: contract.Internal, StartupDuration: time.Second}
		if err := s.EndRun(ctx, owner, "failed", outcome); err != nil {
			t.Fatal(err)
		}
	}
	runs, err := s.Runs()
	if err != nil || len(runs) != 257 || runs[0].Owner != live || runs[0].State != "running" || runs[1].Owner.Activation != "activation-004" {
		t.Fatalf("retention lost live owner or kept old terminals: %+v, %v", runs, err)
	}
	runs[0].State = "failed"
	wrong := live
	wrong.Digest = strings.Repeat("b", 64)
	if err := s.EndRun(ctx, wrong, "stopped", contract.Outcome{}); err == nil {
		t.Fatal("mismatched owner ended run")
	}
	if err := s.BeginRun(ctx, live); err != nil {
		t.Fatal("identical running owner was not idempotent", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s = openFixture(t, root, TrustPolicy{})
	runs, err = s.Runs()
	if err != nil || len(runs) != 256 || runs[len(runs)-1].Owner != live || runs[len(runs)-1].State != "interrupted" || runs[len(runs)-1].EndedAt.IsZero() {
		t.Fatalf("recovery failed to record interruption: %+v, %v", runs, err)
	}
	if runs[0].Outcome.FailureCode != contract.PluginFailed || runs[0].Outcome.CleanupCode != contract.Internal || runs[0].Outcome.ExitCode == nil || *runs[0].Outcome.ExitCode != 17 {
		t.Fatalf("bounded history lost structured outcome: %+v", runs[0])
	}
	*runs[0].Outcome.ExitCode = 99
	snapshot, err := s.Runs()
	if err != nil || *snapshot[0].Outcome.ExitCode != 17 {
		t.Fatal("run snapshot exposed mutable outcome", err)
	}
	if err := s.BeginRun(ctx, live); err == nil {
		t.Fatal("reused retained activation")
	}
	if err := s.EndRun(ctx, live, "stopped", contract.Outcome{}); err == nil {
		t.Fatal("terminal state changed")
	}
	if err := s.EndRun(ctx, live, "interrupted", contract.Outcome{}); err != nil {
		t.Fatal("terminal repeat was not idempotent", err)
	}
	ended := runs[len(runs)-1].EndedAt
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s = openFixture(t, root, TrustPolicy{})
	runs, err = s.Runs()
	if err != nil || !runs[len(runs)-1].EndedAt.Equal(ended) {
		t.Fatal("interruption was not persisted", err)
	}
}

func TestRunCancellationRespectsAtomicBoundary(t *testing.T) {
	t.Parallel()
	for _, operation := range []string{"begin", "end"} {
		for _, stage := range []commitStage{commitRunsSynced, commitRunsSwapped} {
			t.Run(operation+"/"+string(stage), func(t *testing.T) {
				t.Parallel()
				s := openFixture(t, t.TempDir(), TrustPolicy{})
				owner := runOwner("activation")
				if operation == "end" {
					if err := s.BeginRun(context.Background(), owner); err != nil {
						t.Fatal(err)
					}
				}
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				s.commitObserver = func(observed commitStage) {
					if observed == stage {
						cancel()
					}
				}
				var err error
				if operation == "begin" {
					err = s.BeginRun(ctx, owner)
				} else {
					err = s.EndRun(ctx, owner, "stopped", contract.Outcome{})
				}
				committed := stage == commitRunsSwapped
				if committed && err != nil || !committed && !errors.Is(err, context.Canceled) {
					t.Fatalf("commit status %v: %v", committed, err)
				}
				runs, err := s.Runs()
				if err != nil {
					t.Fatal(err)
				}
				if operation == "begin" && !committed {
					if len(runs) != 0 {
						t.Fatal("canceled begin recorded run")
					}
				} else {
					state := "running"
					if operation == "end" && committed {
						state = "stopped"
					}
					if len(runs) != 1 || runs[0].State != state {
						t.Fatalf("unexpected run state: %+v", runs)
					}
				}
			})
		}
	}
}
