package core_test

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Cyenoch/cordis/contract"
	"github.com/Cyenoch/cordis/internal/core"
)

func TestConfigurationCommitCannotRacePastStopOrClose(t *testing.T) {
	for _, transition := range []string{"stop", "remove", "close"} {
		t.Run(transition, func(t *testing.T) {
			e := core.New()
			defer closeEngine(t, e)
			i := add(t, e, "configured", contract.Definition{Start: func(context.Context, contract.Scope) error { return nil }})
			if err := i.Wait(t.Context()); err != nil {
				t.Fatal(err)
			}
			entered, release := make(chan struct{}), make(chan struct{})
			submitted := make(chan error, 1)
			var commits atomic.Int32
			go func() {
				_, err := e.Configure(i, json.RawMessage(`{"revision":2}`), func() error { close(entered); <-release; commits.Add(1); return nil })
				submitted <- err
			}()
			<-entered
			stopped := make(chan error, 1)
			go func() {
				switch transition {
				case "stop":
					stopped <- i.Stop(t.Context())
				case "remove":
					stopped <- e.Remove(t.Context(), "configured")
				case "close":
					stopped <- e.Close(t.Context())
				}
			}()
			select {
			case err := <-stopped:
				t.Fatalf("%s completed while accepted commit was unresolved: %v", transition, err)
			case <-time.After(20 * time.Millisecond):
			}
			close(release)
			if err := <-submitted; err != nil {
				t.Fatalf("accepted durable config was rejected after commit: %v", err)
			}
			if err := <-stopped; err != nil {
				t.Fatal(err)
			}
			_, err := e.Configure(i, json.RawMessage(`{"revision":3}`), func() error { commits.Add(1); return nil })
			if err == nil || commits.Load() != 1 {
				t.Fatalf("%s admitted later durable update: err=%v commits=%d", transition, err, commits.Load())
			}
		})
	}
}
