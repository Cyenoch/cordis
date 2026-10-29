//go:build integration

package cordis_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	cordis "github.com/Cyenoch/cordis"
	"github.com/Cyenoch/cordis/contract"
	"github.com/Cyenoch/cordis/packagehost"
)

func TestNativeDynamicEvents(t *testing.T) {
	source := t.TempDir()
	artifact := "plugin"
	if runtime.GOOS == "windows" {
		artifact += ".exe"
	}
	build := exec.CommandContext(t.Context(), "go", "build", "-o", filepath.Join(source, artifact), "./runtime/testdata/subscriptionplugin")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build native event fixture: %v\n%s", err, output)
	}
	service := contract.Service{Name: "test.events", Major: 1}
	requirement := contract.Requirement{Name: service.Name, Major: 1}
	manifest := contract.Manifest{Schema: contract.PackageSchema, ID: "test.events", Version: "1.0.0", Publisher: "tests",
		Protocol: contract.Protocol{Major: 1, RequiredFeatures: []string{"subscriptions"}}, DefaultDefinition: "events",
		Definitions: []contract.DefinitionSpec{{ID: "events", Provides: []contract.Service{service}, ConfigSchema: json.RawMessage(`{"type":"object","properties":{"fail":{"type":"boolean"}}}`)}},
		Runtime:     contract.RuntimeSpec{Kind: "native", Artifacts: []contract.Artifact{{OS: runtime.GOOS, Arch: runtime.GOARCH, Path: artifact}}}}
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "plugin.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(t.TempDir(), "events.zip")
	if _, err := packagehost.Pack(t.Context(), source, archive); err != nil {
		t.Fatal(err)
	}
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pkg, err := packagehost.Sign(t.Context(), archive, "events", private)
	if err != nil {
		t.Fatal(err)
	}
	h, err := cordis.Open(cordis.Options{Directory: t.TempDir(), FrameBytes: contract.MinFrameBytes, Trust: packagehost.TrustPolicy{
		Keys: map[string]packagehost.TrustedKey{"events": {PublicKey: public, Publisher: manifest.Publisher}}, Publishers: map[string][]string{manifest.Publisher: {manifest.ID}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	var expectedFailure error
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := h.Close(ctx); err != nil && !errors.Is(err, expectedFailure) {
			t.Error(err)
		}
		select {
		case <-h.Done():
		default:
			t.Error("native event host did not drain")
		}
	})
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	if _, err := h.Install(ctx, archive); err != nil {
		t.Fatal(err)
	}
	if err := h.Select(ctx, pkg.Ref); err != nil {
		t.Fatal(err)
	}
	var builtinScope contract.Scope
	builtin, err := h.AddBuiltin(ctx, contract.Entry{ID: "builtin"}, contract.Definition{Start: func(ctx context.Context, scope contract.Scope) error {
		builtinScope = scope
		for range 2 {
			if _, err := scope.On(ctx, "mixed", contract.SubscriptionOptions{}, func(context.Context, json.RawMessage) (contract.EventResult, error) {
				return contract.EventResult{Value: json.RawMessage(`0`)}, nil
			}); err != nil {
				return err
			}
		}
		_, err := scope.On(ctx, "loop", contract.SubscriptionOptions{}, func(ctx context.Context, _ json.RawMessage) (contract.EventResult, error) {
			_, err := h.Call(ctx, requirement, json.RawMessage(`{"operation":"counts"}`))
			return contract.EventResult{}, err
		})
		return err
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := builtin.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	instance, err := h.Enable(ctx, contract.Entry{ID: "native", Package: manifest.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err := instance.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	control := func(input string) (map[string]int, error) {
		output, err := h.Call(ctx, requirement, json.RawMessage(input))
		if err != nil {
			return nil, err
		}
		var counts map[string]int
		err = json.Unmarshal(output, &counts)
		return counts, err
	}
	mustControl := func(input string) map[string]int {
		t.Helper()
		counts, err := control(input)
		if err != nil {
			t.Fatal(err)
		}
		return counts
	}
	for _, mode := range []contract.Dispatch{contract.Sequential, contract.Parallel, contract.First} {
		results, err := h.Emit(ctx, "mixed", json.RawMessage(`null`), mode)
		if err != nil || len(results) != 4 || !results[3].Handled || string(results[3].Value) != "false" {
			t.Fatalf("mixed %s dispatch: %+v %v", mode, results, err)
		}
	}
	mustControl(`{"operation":"listen","id":"front","name":"mixed","handled":true,"options":{"prepend":true}}`)
	if results, err := h.Emit(ctx, "mixed", json.RawMessage(`null`), contract.First); err != nil || len(results) != 1 || !results[0].Handled {
		t.Fatalf("native prepend: %+v %v", results, err)
	}
	mustControl(`{"operation":"cancel","id":"front"}`)
	mustControl(`{"operation":"cancel","id":"front"}`)
	mustControl(`{"operation":"listen","id":"self","name":"self","selfCancel":true}`)
	for range 2 {
		if _, err := h.Emit(ctx, "self", json.RawMessage(`null`), contract.Sequential); err != nil {
			t.Fatal(err)
		}
	}
	if count := mustControl(`{"operation":"counts"}`)["self"]; count != 1 {
		t.Fatalf("native self-cancel: %d", count)
	}
	mustControl(`{"operation":"listen","id":"once","name":"once","selfCancel":true,"options":{"once":true}}`)
	var group sync.WaitGroup
	for range 8 {
		group.Go(func() {
			if _, err := h.Emit(ctx, "once", json.RawMessage(`null`), contract.Parallel); err != nil {
				t.Error(err)
			}
		})
	}
	group.Wait()
	mustControl(`{"operation":"cancel","id":"once"}`)
	if count := mustControl(`{"operation":"counts"}`)["once"]; count != 1 {
		t.Fatalf("native concurrent once: %d", count)
	}
	mustControl(`{"operation":"listen","id":"bad","name":"errors","fail":true,"options":{"once":true}}`)
	var laterCalls int
	if _, err := builtinScope.On(ctx, "errors", contract.SubscriptionOptions{}, func(context.Context, json.RawMessage) (contract.EventResult, error) {
		laterCalls++
		return contract.EventResult{}, nil
	}); err != nil {
		t.Fatal(err)
	}
	if results, err := h.Emit(ctx, "errors", json.RawMessage(`null`), contract.Parallel); contract.ErrorCode(err) != contract.Invalid || len(results) != 2 || laterCalls != 1 || !strings.Contains(err.Error(), "native/") {
		t.Fatalf("mixed error attribution: %+v %v later=%d", results, err, laterCalls)
	}
	if _, err := control(`{"operation":"emit","name":"loop"}`); contract.ErrorCode(err) != contract.Reentrant {
		t.Fatalf("cross native/builtin event cycle: %v", err)
	}
	mustControl(`{"operation":"listen","id":"unsent","name":"unsent","options":{"once":true}}`)
	large, _ := json.Marshal(strings.Repeat("x", contract.MinFrameBytes))
	if _, err := h.Emit(ctx, "unsent", large, contract.Sequential); contract.ErrorCode(err) != contract.Invalid {
		t.Fatalf("oversized once delivery: %v", err)
	}
	mustControl(`{"operation":"cancel","id":"unsent"}`)
	if counts := mustControl(`{"operation":"counts"}`); counts["unsent"] != 0 {
		t.Fatal("unsent once ran or damaged the session")
	}
	// Two initial listeners remain active. A failed or canceled registration
	// must not consume capacity, including once deliveries rejected pre-send.
	for n := range contract.MaxSubscriptions - 2 {
		mustControl(fmt.Sprintf(`{"operation":"listen","id":"quota-%d","name":"quota","options":{"once":true}}`, n))
	}
	if _, err := control(`{"operation":"listen","id":"overflow","name":"quota"}`); contract.ErrorCode(err) != contract.Denied {
		t.Fatalf("native subscription limit: %v", err)
	}
	mustControl(`{"operation":"cancel","id":"quota-0"}`)
	mustControl(`{"operation":"listen","id":"replacement","name":"quota","options":{"once":true}}`)
	if results, err := h.Emit(ctx, "quota", json.RawMessage(`null`), contract.Sequential); err != nil || len(results) != contract.MaxSubscriptions-2 {
		t.Fatalf("native capacity damaged: %d %v", len(results), err)
	}
	if err := instance.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if results, err := h.Emit(ctx, "mixed", json.RawMessage(`null`), contract.Sequential); err != nil || len(results) != 2 {
		t.Fatalf("retired native listeners retained: %d %v", len(results), err)
	}
	failed, err := h.Enable(ctx, contract.Entry{ID: "failed", Package: manifest.ID, Config: json.RawMessage(`{"fail":true}`)})
	if err != nil {
		t.Fatal(err)
	}
	expectedFailure = failed.Wait(ctx)
	if expectedFailure == nil {
		t.Fatal("native initialization unexpectedly succeeded")
	}
	if results, err := h.Emit(ctx, "mixed", json.RawMessage(`null`), contract.Sequential); err != nil || len(results) != 2 {
		t.Fatalf("failed native initialization published listeners: %d %v", len(results), err)
	}
}
