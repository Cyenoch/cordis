package cordis_test

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/Cyenoch/cordis"
)

// kernel runs body inside a synctest bubble with a fresh kernel and closes it
// afterwards. The bubble fails the test if a goroutine outlives the kernel.
func kernel(t *testing.T, body func(ctx context.Context, app *cordis.App)) {
	t.Helper()
	synctest.Test(t, func(t *testing.T) {
		app := cordis.New(cordis.Options{})
		body(t.Context(), app)
		select {
		case <-app.Done():
		default:
			app.Close(context.Background()) // failed instances are asserted by the tests themselves
		}
	})
}

// journal records events from several goroutines in order.
type journal struct {
	mu      sync.Mutex
	entries []string
}

func (j *journal) add(entry string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.entries = append(j.entries, entry)
}

func (j *journal) list() []string {
	j.mu.Lock()
	defer j.mu.Unlock()
	return slices.Clone(j.entries)
}

func (j *journal) expect(t *testing.T, want ...string) {
	t.Helper()
	if got := j.list(); !slices.Equal(got, want) {
		t.Fatalf("journal = %v, want %v", got, want)
	}
}

// plugin builds a plugin without configuration.
func plugin(name string, requires []cordis.Requirement, start func(ctx context.Context, s *cordis.Scope) error) cordis.Plugin[struct{}] {
	return cordis.Plugin[struct{}]{Name: name, Requires: requires, Start: func(ctx context.Context, s *cordis.Scope, _ struct{}) error {
		return start(ctx, s)
	}}
}

func load(s *cordis.Scope, p cordis.Plugin[struct{}]) *cordis.Instance[struct{}] {
	return p.Load(s, struct{}{})
}

func mustWait[C any](t *testing.T, ctx context.Context, i *cordis.Instance[C], want cordis.State) {
	t.Helper()
	err := i.Wait(ctx)
	if got := i.Status().State; got != want {
		t.Fatalf("state = %v (wait error %v), want %v", got, err, want)
	}
}

func note(j *journal, entry string) func(context.Context) error {
	return func(context.Context) error { j.add(entry); return nil }
}

var errBoom = errors.New("boom")
