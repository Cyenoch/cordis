//go:build integration

package packagehost

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/Cyenoch/cordis/contract"
)

func TestEntriesReadCommittedSnapshotDuringPendingWrite(t *testing.T) {
	key, trust := signingFixture(t)
	manifest := manifestFixture("demo", "1.0.0")
	manifest.Definitions[0].ConfigSchema = json.RawMessage(`{"type":"object","properties":{"value":{"type":"integer"}}}`)
	archive, pkg := archiveFixture(t, manifest, key)
	s := openFixture(t, t.TempDir(), trust)
	if _, err := s.Install(t.Context(), archive); err != nil {
		t.Fatal(err)
	}
	if err := s.Select(t.Context(), pkg.Ref, ChangeOptions{}); err != nil {
		t.Fatal(err)
	}
	entry := contract.Entry{ID: "entry", Package: "demo", Definition: "main", Config: json.RawMessage(`{"value":1}`)}
	if err := s.SaveEntry(t.Context(), entry); err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	s.commitObserver = func(stage commitStage) {
		if stage == commitCatalogSynced {
			close(entered)
			<-release
		}
	}
	done := make(chan error, 1)
	go func() {
		entry.Config = json.RawMessage(`{"value":2}`)
		done <- s.SaveEntry(context.Background(), entry)
	}()
	<-entered
	read := make(chan []contract.Entry, 1)
	go func() { entries, _ := s.Entries(); read <- entries }()
	select {
	case entries := <-read:
		if len(entries) != 1 || string(entries[0].Config) != `{"value":1}` {
			t.Fatalf("reader observed an uncommitted intent: %+v", entries)
		}
		entries[0].Config[0] = 'x'
	case <-time.After(time.Second):
		t.Fatal("catalog query waited for pending filesystem commit")
	}
	unblock()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	s.commitObserver = nil
	entries, err := s.Entries()
	if err != nil || string(entries[0].Config) != `{"value":2}` {
		t.Fatalf("reader did not advance at commit: %+v %v", entries, err)
	}
	s.Close()
	if _, err := s.Entries(); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("closed Store remained readable: %v", err)
	}
}

type pausedCopyContext struct {
	context.Context
	once             sync.Once
	entered, release chan struct{}
}

func (ctx *pausedCopyContext) Err() error {
	ctx.once.Do(func() { close(ctx.entered); <-ctx.release })
	return ctx.Context.Err()
}

func TestExecutionSnapshotRechecksTrustAfterSlowCopy(t *testing.T) {
	key, trust := signingFixture(t)
	archive, pkg := archiveFixture(t, manifestFixture("demo", "1.0.0"), key)
	s := openFixture(t, t.TempDir(), trust)
	if _, err := s.Install(t.Context(), archive); err != nil {
		t.Fatal(err)
	}
	if err := s.Select(t.Context(), pkg.Ref, ChangeOptions{}); err != nil {
		t.Fatal(err)
	}
	ctx := &pausedCopyContext{Context: t.Context(), entered: make(chan struct{}), release: make(chan struct{})}
	var once sync.Once
	unblock := func() { once.Do(func() { close(ctx.release) }) }
	defer unblock()
	done := make(chan error, 1)
	go func() {
		snapshot, err := s.Snapshot(ctx, pkg.Ref)
		if snapshot != nil {
			snapshot.Close()
		}
		done <- err
	}()
	<-ctx.entered
	updated := make(chan error, 1)
	go func() { updated <- s.UpdateTrust(TrustPolicy{}) }()
	select {
	case err := <-updated:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("copy held the Store mutation lock")
	}
	unblock()
	if err := <-done; err == nil {
		t.Fatal("execution snapshot ignored revocation during copy")
	}
}
