package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	cordis "github.com/Cyenoch/cordis"
	"github.com/Cyenoch/cordis/access"
	"github.com/Cyenoch/cordis/contract"
	"github.com/Cyenoch/cordis/examples/pluginmeta"
	"github.com/Cyenoch/cordis/packagehost"
	"github.com/Cyenoch/cordis/runtime/native"
	"github.com/Cyenoch/cordis/runtime/wasm"
	"github.com/Cyenoch/cordis/wire"
)

type counts struct {
	Acquired int    `json:"acquired"`
	Released int    `json:"released"`
	Live     int    `json:"live"`
	Label    string `json:"label"`
}

type observation struct {
	Owner   contract.Identity `json:"owner"`
	Kind    string            `json:"kind"`
	Service string            `json:"service,omitempty"`
	Counts  counts            `json:"counts"`
}

type evidence struct {
	mu     sync.Mutex
	events []observation
}

// These are real protocol sessions; the wrapper only observes completed calls.
// A denied release or failed shutdown is never converted to success.
func (e *evidence) factory(contract.Manifest) *contract.SessionFactory {
	return &contract.SessionFactory{Protocol: contract.GenericSession, Open: func(transport io.ReadWriteCloser, owner contract.Identity, handler contract.SessionHandler, frameBytes int) (contract.Session, error) {
		endpoint, err := wire.New(wire.Options{Transport: transport, Identity: owner, FrameBytes: frameBytes,
			Handler: func(ctx context.Context, method string, data json.RawMessage) (json.RawMessage, error) {
				output, err := handler(ctx, method, data)
				if err != nil {
					return nil, err
				}
				if method == "call" {
					var call contract.ServiceCall
					if err := contract.Decode(data, &call); err != nil {
						return nil, err
					}
					var request struct {
						Method string `json:"method"`
						Input  struct {
							Operation string `json:"operation"`
							Owner     string `json:"owner,omitempty"`
						} `json:"input"`
					}
					if err := contract.Decode(call.Input, &request); err != nil {
						return nil, err
					}
					if request.Method == "execute" && request.Input.Operation == "release" {
						var value counts
						if err := contract.Decode(output, &value); err != nil {
							return nil, err
						}
						e.add(observation{Owner: owner, Kind: "release", Service: call.Required.Name, Counts: value})
					}
				}
				return output, nil
			}})
		if err != nil {
			return nil, err
		}
		return &observedSession{Session: endpoint, owner: owner, evidence: e}, nil
	}}
}

func (e *evidence) add(value observation) {
	e.mu.Lock()
	e.events = append(e.events, value)
	e.mu.Unlock()
}

func (e *evidence) snapshot() []observation {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]observation{}, e.events...)
}

type observedSession struct {
	contract.Session
	owner    contract.Identity
	evidence *evidence
}

func (s *observedSession) Call(ctx context.Context, method string, data json.RawMessage) (json.RawMessage, error) {
	output, err := s.Session.Call(ctx, method, data)
	if err == nil && method == "shutdown" {
		s.evidence.add(observation{Owner: s.owner, Kind: "shutdown-ack"})
	}
	if err == nil && method == "initialize" {
		s.evidence.add(observation{Owner: s.owner, Kind: "initialize-ack"})
	}
	return output, err
}

type report struct {
	Runtime      string        `json:"runtime"`
	Assertions   []string      `json:"assertions"`
	Observations []observation `json:"observations"`
	FaultClose   string        `json:"faultClose"`
}

func main() {
	kind := flag.String("runtime", "native", "native or wasm")
	guest := flag.String("guest", "", "prebuilt independent chain executable or WASM")
	worker := flag.String("worker", "", "absolute trusted WASI worker path")
	badGuest := flag.String("bad-guest", "", "separate chain artifact built with rejectInitialization=true")
	flag.Parse()
	result, err := run(*kind, *guest, *worker, *badGuest)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(kind, guest, worker, badGuest string) (result report, resultErr error) {
	if guest == "" || badGuest == "" || (kind != "native" && kind != "wasm") || (kind == "wasm" && worker == "") {
		return result, errors.New("-guest, -bad-guest and -runtime native|wasm required; wasm also requires -worker")
	}
	guest, err := filepath.Abs(guest)
	if err != nil {
		return result, err
	}
	root, err := os.MkdirTemp("", "cordis-lifecycle-")
	if err != nil {
		return result, err
	}
	defer func() { resultErr = errors.Join(resultErr, os.RemoveAll(root)) }()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return result, err
	}
	archives := []string{}
	for _, version := range []string{"1.0.0", "1.1.0"} {
		archive, err := makeArchive(ctx, root, kind, guest, version, private)
		if err != nil {
			return result, err
		}
		archives = append(archives, archive)
	}
	observed := &evidence{events: []observation{}}
	var adapter contract.Runtime = native.New(native.Options{Grace: 3 * time.Second})
	if kind == "wasm" {
		worker, err = filepath.Abs(worker)
		if err != nil {
			return result, err
		}
		adapter = wasm.New(worker, wasm.Options{Grace: 3 * time.Second})
	}
	options := cordis.Options{FrameBytes: contract.MaxFrameBytes, Directory: filepath.Join(root, "catalog"), Grace: 5 * time.Second,
		Trust: packagehost.TrustPolicy{Keys: map[string]packagehost.TrustedKey{
			"acceptance": {PublicKey: public, Publisher: "independent-author"}},
			Publishers: map[string][]string{"independent-author": {"example.chain"}}},
		Runtimes: map[string]contract.Runtime{kind: adapter}, SessionFactory: observed.factory}
	host, err := cordis.Open(options)
	if err != nil {
		return result, err
	}
	defer func() {
		if host != nil {
			closeCtx, closeCancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer closeCancel()
			resultErr = errors.Join(resultErr, host.Close(closeCtx))
		}
	}()
	refs := []contract.PackageRef{}
	for _, archive := range archives {
		pkg, err := host.Install(ctx, archive)
		if err != nil {
			return result, err
		}
		refs = append(refs, pkg.Ref)
	}
	if err := host.Select(ctx, refs[0]); err != nil {
		return result, err
	}
	consumer, err := host.Enable(ctx, entry("a-consumer", "consumer", "", ""))
	if err != nil {
		return result, err
	}
	middle, err := host.Enable(ctx, entry("b-middle", "middle", "", ""))
	if err != nil {
		return result, err
	}
	if consumer.Status().State != contract.Waiting || middle.Status().State != contract.Waiting {
		return result, errors.New("dependents did not wait for late provider")
	}
	provider, err := host.Enable(ctx, entry("z-provider", "provider", "", ""))
	if err != nil {
		return result, err
	}
	for _, instance := range []contract.Instance{provider, middle, consumer} {
		if err := instance.Wait(ctx); err != nil {
			return result, err
		}
	}
	if err := expectCounts(ctx, host, "", "initial", 2); err != nil {
		return result, err
	}
	result.Runtime = kind
	result.Assertions = []string{"signed-install", "consumer-first-late-provider", "minor-2-in-range-1-to-3"}
	if !slices.Contains(provider.Status().Diagnostics, "example.chain: optional dependency example.optional ^1.0.0 is not selected") {
		return result, errors.New("optional package diagnostic lost at facade status")
	}
	result.Assertions = append(result.Assertions, "optional-dependency-status-diagnostic")
	if _, err := host.Enable(ctx, entry("invalid-parent", "provider", "", "invalid-parent")); err == nil {
		return result, errors.New("self-parent entry admitted")
	}
	for _, saved := range mustEntries(host) {
		if saved.ID == "invalid-parent" {
			return result, errors.New("failed enable poisoned persistent parent graph")
		}
	}
	results, err := host.Emit(ctx, "", "chain.handled", json.RawMessage(`{}`), contract.First)
	if err != nil || len(results) != 1 || !results[0].Handled || string(results[0].Value) != "0" {
		return result, fmt.Errorf("external first handled zero lost: %+v %v", results, err)
	}
	if _, err := host.Emit(ctx, "", "chain.handled", json.RawMessage(`{}`), contract.Sequential); err == nil {
		return result, errors.New("sequential dispatch failed to reach second listener")
	}
	result.Assertions = append(result.Assertions, "external-first-handled-zero", "rejected-parent-cycle-is-not-persistent")
	if err := cancellation(ctx, host, provider.Status().Identity); err != nil {
		return result, fmt.Errorf("mid-call cancellation/control: %w", err)
	}
	result.Assertions = append(result.Assertions, "mid-call-cancel-and-task-finish-control")
	updated, err := provider.Update(ctx, json.RawMessage(`{"label":"persisted"}`))
	if err != nil || !updated.Applied {
		return result, fmt.Errorf("update result %+v: %w", updated, err)
	}
	if err := waitActive(ctx, host, "a-consumer"); err != nil {
		return result, err
	}
	if err := expectCounts(ctx, host, "", "persisted", 2); err != nil {
		return result, err
	}
	// A second independent realm makes parent-before-child startup and reverse
	// shutdown observable without introducing duplicate providers in one realm.
	if err := configureFamily(host); err != nil {
		return result, err
	}
	for _, spec := range []contract.Entry{
		entry("family-z-provider", "provider", "family", ""),
		entry("family-b-middle", "middle", "family", "family-z-provider"),
		entry("family-a-consumer", "consumer", "family", "family-b-middle"),
	} {
		instance, err := host.Enable(ctx, spec)
		if err != nil {
			return result, err
		}
		if err := instance.Wait(ctx); err != nil {
			return result, err
		}
	}
	if err := expectCounts(ctx, host, "family", "initial", 2); err != nil {
		return result, err
	}
	builtinEntry := contract.Entry{ID: "family-builtin-child", Parent: "family-z-provider", Realm: "family", Config: json.RawMessage(`{}`)}
	builtinDef := contract.Definition{ID: "owned-builtin", Start: func(context.Context, contract.Scope) error { return nil }}
	builtin, err := host.AddBuiltin(ctx, builtinEntry, builtinDef)
	if err != nil {
		return result, err
	}
	if err := builtin.Wait(ctx); err != nil {
		return result, err
	}
	builtinBefore := builtin.Status().Identity.Activation
	before, err := host.Status("a-consumer")
	if err != nil {
		return result, err
	}
	if err := host.Replace(ctx, refs[1], packagehost.ChangeOptions{}); err != nil {
		return result, fmt.Errorf("replace consumer-first package: %w", err)
	}
	after, err := host.Status("a-consumer")
	if err != nil {
		return result, err
	}
	if before.Identity.Activation == after.Identity.Activation || after.Identity.Digest != refs[1].Digest {
		return result, errors.New("replacement did not create a fresh consumer from selected digest")
	}
	if err := host.Access().Check(before.Identity); err == nil {
		return result, errors.New("old consumer retained activation authority")
	}
	if err := expectCounts(ctx, host, "", "persisted", 2); err != nil {
		return result, err
	}
	if err := expectCounts(ctx, host, "family", "initial", 2); err != nil {
		return result, err
	}
	builtinAfter, err := host.Status(builtinEntry.ID)
	if err != nil || builtinAfter.State != contract.Active || builtinAfter.Identity.Activation == builtinBefore {
		return result, fmt.Errorf("builtin child not recreated with external parent: %+v %v", builtinAfter, err)
	}
	if err := checkCleanup(observed.snapshot()); err != nil {
		return result, err
	}
	result.Assertions = append(result.Assertions, "transitive-cleanup-release-and-shutdown-ack", "consumer-first-replacement", "parent-child-ordering")
	if err := host.Disable(ctx, "family-z-provider"); err != nil {
		return result, fmt.Errorf("disable owned subtree: %w", err)
	}
	if _, err := host.Status(builtinEntry.ID); err == nil {
		return result, errors.New("builtin child remains registered after parent disable")
	}
	result.Assertions = append(result.Assertions, "builtin-child-replaced-and-retired-with-parent")
	for _, saved := range mustEntries(host) {
		if strings.HasPrefix(saved.ID, "family-") && saved.Enabled {
			return result, errors.New("parent disable left durable enabled child")
		}
	}
	for _, spec := range []contract.Entry{entry("family-z-provider", "provider", "family", ""), entry("family-b-middle", "middle", "family", "family-z-provider"), entry("family-a-consumer", "consumer", "family", "family-b-middle")} {
		instance, err := host.Enable(ctx, spec)
		if err != nil {
			return result, err
		}
		if err := instance.Wait(ctx); err != nil {
			return result, err
		}
	}
	result.Assertions = append(result.Assertions, "parent-disable-persists-subtree")
	if err := stoppedUpdate(ctx, host); err != nil {
		return result, err
	}
	result.Assertions = append(result.Assertions, "stopped-update-does-not-persist")
	if err := host.Close(ctx); err != nil {
		return result, err
	}
	host = nil
	host, err = cordis.Open(options)
	if err != nil {
		return result, err
	}
	if err := configureFamily(host); err != nil {
		return result, err
	}
	if _, err := host.Restore(ctx); err != nil {
		return result, fmt.Errorf("reopen restore: %w", err)
	}
	if err := expectCounts(ctx, host, "", "persisted", 2); err != nil {
		return result, err
	}
	if err := expectCounts(ctx, host, "family", "initial", 2); err != nil {
		return result, err
	}
	selected, err := host.Store().Selected("example.chain")
	if err != nil || selected.Ref != refs[1] {
		return result, fmt.Errorf("reopen selection %+v: %w", selected.Ref, err)
	}
	result.Assertions = append(result.Assertions, "updated-config-survives-replace-and-reopen-restore")
	if err := host.Close(ctx); err != nil {
		return result, err
	}
	host = nil
	result.Observations = observed.snapshot()
	options.Directory = filepath.Join(root, "fault-catalog")
	result.FaultClose, err = faults(ctx, options, archives[1])
	if err != nil {
		return result, err
	}
	result.Assertions = append(result.Assertions, "finite-one-restart-budget", "unexpected-clean-exit-invalidates", "failed-initialize-live-recovery")
	badArchive, err := makeArchive(ctx, root, kind, badGuest, "1.2.0", private)
	if err != nil {
		return result, err
	}
	options.Directory = filepath.Join(root, "version-recovery-catalog")
	if err := failedVersionRecovery(ctx, options, archives[0], badArchive); err != nil {
		return result, err
	}
	result.Assertions = append(result.Assertions, "failed-new-code-restores-old-version-live")
	options.Directory = filepath.Join(root, "author-child-catalog")
	if err := authorChildren(ctx, options, archives[0]); err != nil {
		return result, err
	}
	result.Assertions = append(result.Assertions, "author-created-optional-child-wait-cleanup")
	return result, nil
}

func authorChildren(ctx context.Context, options cordis.Options, archive string) (result error) {
	host, err := cordis.Open(options)
	if err != nil {
		return err
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		result = errors.Join(result, host.Close(closeCtx))
	}()
	pkg, err := host.Install(ctx, archive)
	if err != nil {
		return err
	}
	if err := host.Select(ctx, pkg.Ref); err != nil {
		return err
	}
	parent, err := host.Enable(ctx, entry("author-parent", "optional-parent", "", ""))
	if err != nil {
		return err
	}
	if err := parent.Wait(ctx); err != nil {
		return fmt.Errorf("author parent: %w", err)
	}
	// The child has a declared required service, but the parent's optional
	// feature does not prevent the parent from publishing its subscription.
	if parent.Status().State != contract.Active {
		return errors.New("optional child blocked its parent")
	}
	provider, err := host.Enable(ctx, entry("author-provider", "provider", "", ""))
	if err != nil {
		return err
	}
	if err := provider.Wait(ctx); err != nil {
		return err
	}
	results, err := host.Emit(ctx, "", "chain.optional-ready", json.RawMessage(`{}`), contract.First)
	if err != nil {
		return fmt.Errorf("wait author child: %w", err)
	}
	if len(results) != 1 || !results[0].Handled {
		return errors.New("author child did not acknowledge readiness")
	}
	stats, err := invoke(ctx, host, "", "chain.a", "stats")
	if err != nil || stats.Live != 1 {
		return fmt.Errorf("author child acquisition: %+v: %w", stats, err)
	}
	if err := host.Disable(ctx, "author-parent"); err != nil {
		return fmt.Errorf("author child retirement: %w", err)
	}
	stats, err = invoke(ctx, host, "", "chain.a", "stats")
	if err != nil || stats.Live != 0 || stats.Acquired != stats.Released {
		return fmt.Errorf("author child cleanup: %+v: %w", stats, err)
	}
	runs, err := host.Store().Runs()
	if err != nil {
		return err
	}
	for _, run := range runs {
		if run.Owner.Entry == "author-parent.optional" && run.State == "stopped" {
			return nil
		}
	}
	return errors.New("author child terminal run not recorded")
}

func entry(id, definition, realm, parent string) contract.Entry {
	return contract.Entry{ID: id, Package: "example.chain", Definition: definition,
		Realm: realm, Parent: parent, Config: json.RawMessage(`{"label":"initial"}`)}
}

func configureFamily(host *cordis.Host) error {
	return host.ConfigureRealm("family", "", []string{"chain.a@1", "chain.b@1", "chain.c@1"})
}

func waitFor(ctx context.Context, ready func() (bool, error)) error {
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		ok, err := ready()
		if err != nil || ok {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func waitActive(ctx context.Context, host *cordis.Host, id string) error {
	return waitFor(ctx, func() (bool, error) {
		status, err := host.Status(id)
		if err != nil {
			return false, err
		}
		if status.State == contract.Failed {
			return false, fmt.Errorf("%s failed: %s", id, status.Error)
		}
		return status.State == contract.Active, nil
	})
}

func invoke(ctx context.Context, host *cordis.Host, realm, service, operation string) (counts, error) {
	input, err := json.Marshal(struct {
		Method string `json:"method"`
		Input  struct {
			Operation string `json:"operation"`
		} `json:"input"`
	}{Method: "execute", Input: struct {
		Operation string `json:"operation"`
	}{Operation: operation}})
	if err != nil {
		return counts{}, err
	}
	data, err := host.Call(ctx, realm,
		contract.Requirement{Name: service, Major: 1, MinMinor: 1, MaxMinor: 3}, input)
	if err != nil {
		return counts{}, err
	}
	var value counts
	if err := contract.Decode(data, &value); err != nil {
		return counts{}, err
	}
	return value, nil
}

func expectCounts(ctx context.Context, host *cordis.Host, realm, label string, live int) error {
	value, err := invoke(ctx, host, realm, "chain.c", "stats")
	if err != nil {
		return err
	}
	if value.Label != label || value.Live != live || value.Acquired-value.Released != live {
		return fmt.Errorf("realm %q: got %+v, want label=%q live=%d", realm, value, label, live)
	}
	return nil
}

func cancellation(ctx context.Context, host *cordis.Host, owner contract.Identity) error {
	callCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := invoke(callCtx, host, "", "chain.c", "wait"); done <- err }()
	if err := waitFor(ctx, func() (bool, error) {
		for _, task := range host.Tasks().Snapshot() {
			if task.Owner == owner && task.ID == "blocked" && task.State == access.TaskRunning {
				return true, nil
			}
		}
		return false, nil
	}); err != nil {
		cancel()
		return errors.Join(err, <-done)
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		return fmt.Errorf("waiting call did not return cancellation: %w", err)
	}
	return waitFor(ctx, func() (bool, error) {
		for _, task := range host.Tasks().Snapshot() {
			if task.Owner == owner && task.ID == "blocked" && task.State == access.TaskCompleted {
				if string(task.Result) != `{"canceled":true}` {
					return false, fmt.Errorf("unexpected cancellation acknowledgment %s", task.Result)
				}
				return true, nil
			}
			if task.Owner == owner && task.ID == "blocked" && task.State != access.TaskRunning {
				return false, fmt.Errorf("cancellation acknowledgment task ended as %s", task.State)
			}
		}
		return false, nil
	})
}

func checkCleanup(events []observation) error {
	for _, prefix := range []string{"", "family-"} {
		consumerRelease, middleRelease := -1, -1
		consumerShutdown, middleShutdown, providerShutdown := -1, -1, -1
		for index, event := range events {
			switch event.Owner.Entry {
			case prefix + "a-consumer":
				if consumerRelease < 0 && event.Kind == "release" && event.Service == "chain.b" && event.Counts.Live == 1 {
					consumerRelease = index
				}
				if consumerShutdown < 0 && event.Kind == "shutdown-ack" {
					consumerShutdown = index
				}
			case prefix + "b-middle":
				if middleRelease < 0 && event.Kind == "release" && event.Service == "chain.a" && event.Counts.Live == 0 {
					middleRelease = index
				}
				if middleShutdown < 0 && event.Kind == "shutdown-ack" {
					middleShutdown = index
				}
			case prefix + "z-provider":
				if providerShutdown < 0 && event.Kind == "shutdown-ack" {
					providerShutdown = index
				}
			}
		}
		validConsumer := consumerRelease >= 0 && consumerRelease < consumerShutdown
		validMiddle := consumerShutdown < middleRelease && middleRelease < middleShutdown
		validProvider := middleShutdown < providerShutdown
		if !validConsumer || !validMiddle || !validProvider {
			return fmt.Errorf("missing ordered cleanup evidence for %q: C=%d/%d B=%d/%d A=%d",
				prefix, consumerRelease, consumerShutdown, middleRelease, middleShutdown, providerShutdown)
		}
	}
	providerStart, middleStart, consumerStart := -1, -1, -1
	for index, event := range events {
		if event.Kind != "initialize-ack" {
			continue
		}
		switch event.Owner.Entry {
		case "family-z-provider":
			if providerStart < 0 {
				providerStart = index
			}
		case "family-b-middle":
			if middleStart < 0 {
				middleStart = index
			}
		case "family-a-consumer":
			if consumerStart < 0 {
				consumerStart = index
			}
		}
	}
	if providerStart < 0 || providerStart >= middleStart || middleStart >= consumerStart {
		return fmt.Errorf("parent-child initialization order: provider=%d middle=%d consumer=%d",
			providerStart, middleStart, consumerStart)
	}
	return nil
}

func stoppedUpdate(ctx context.Context, host *cordis.Host) error {
	if err := host.ConfigureRealm("stopped", "", []string{"chain.a@1"}); err != nil {
		return err
	}
	instance, err := host.Enable(ctx, entry("stopped", "provider", "stopped", ""))
	if err != nil {
		return err
	}
	if err := instance.Wait(ctx); err != nil {
		return err
	}
	if err := instance.Stop(ctx); err != nil {
		return err
	}
	if _, err := instance.Update(ctx, json.RawMessage(`{"label":"must-not-persist"}`)); err == nil {
		return errors.New("stopped facade accepted Update")
	}
	entries, err := host.Store().Entries()
	if err != nil {
		return err
	}
	for _, saved := range entries {
		if saved.ID == "stopped" && string(saved.Config) != `{"label":"initial"}` {
			return fmt.Errorf("stopped Update changed durable config: %s", saved.Config)
		}
	}
	return host.Disable(ctx, "stopped")
}

func faults(ctx context.Context, options cordis.Options, archive string) (diagnostic string, resultErr error) {
	host, err := cordis.Open(options)
	if err != nil {
		return "", err
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		err := host.Close(closeCtx)
		if err == nil {
			resultErr = errors.Join(resultErr, errors.New("fault host Close omitted recorded process failure"))
			return
		}
		if errors.Is(err, context.DeadlineExceeded) {
			resultErr = errors.Join(resultErr, err)
		}
		diagnostic = err.Error()
	}()
	pkg, err := host.Install(ctx, archive)
	if err != nil {
		return "", err
	}
	if err := host.Select(ctx, pkg.Ref); err != nil {
		return "", err
	}
	instance, err := host.Enable(ctx, entry("crash", "provider", "", ""))
	if err != nil {
		return "", err
	}
	if err := instance.Wait(ctx); err != nil {
		return "", err
	}
	first := instance.Status().Identity
	superviseCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- host.Supervise(superviseCtx, "crash", 1, 10*time.Millisecond) }()
	defer func() {
		cancel()
		if done != nil {
			resultErr = errors.Join(resultErr, ignoreCanceled(<-done))
		}
	}()
	if _, err := invoke(ctx, host, "", "chain.a", "crash"); err == nil {
		return "", errors.New("crashed call unexpectedly succeeded")
	}
	if err := waitFor(ctx, func() (bool, error) {
		status := instance.Status()
		return status.State == contract.Active && status.Identity.Activation != first.Activation, nil
	}); err != nil {
		return "", err
	}
	if err := host.Access().Check(first); err == nil {
		return "", errors.New("crashed activation retained authority")
	}
	if _, err := invoke(ctx, host, "", "chain.a", "crash"); err == nil {
		return "", errors.New("second crashed call unexpectedly succeeded")
	}
	select {
	case err := <-done:
		done = nil
		if err == nil || !strings.Contains(err.Error(), "restart budget exhausted") {
			return "", fmt.Errorf("supervision did not exhaust finite budget: %w", err)
		}
	case <-ctx.Done():
		return "", ctx.Err()
	}
	if instance.Status().State != contract.Failed {
		return "", errors.New("exhausted entry did not remain failed")
	}
	if err := host.ConfigureRealm("exit", "", []string{"chain.a@1"}); err != nil {
		return "", err
	}
	exiting, err := host.Enable(ctx, entry("exit", "provider", "exit", ""))
	if err != nil {
		return "", err
	}
	if err := exiting.Wait(ctx); err != nil {
		return "", err
	}
	identity := exiting.Status().Identity
	if _, err := invoke(ctx, host, "exit", "chain.a", "exit"); err == nil {
		return "", errors.New("unexpected clean exit returned successful service output")
	}
	if err := waitFor(ctx, func() (bool, error) { return exiting.Status().State == contract.Failed, nil }); err != nil {
		return "", err
	}
	if err := host.Access().Check(identity); err == nil {
		return "", errors.New("unexpected clean exit retained activation authority")
	}
	if _, err := invoke(ctx, host, "exit", "chain.a", "stats"); err == nil {
		return "", errors.New("cleanly exited provider remained callable")
	}
	if err := host.ConfigureRealm("recovery", "", []string{"chain.a@1"}); err != nil {
		return "", err
	}
	failedSpec := entry("startup-failed", "provider", "recovery", "")
	failedSpec.Config = json.RawMessage(`{"label":"fail-initialize"}`)
	failed, err := host.Enable(ctx, failedSpec)
	if err != nil {
		return "", err
	}
	if err := failed.Wait(ctx); err == nil {
		return "", errors.New("failed initialization reported success")
	}
	if err := host.Disable(ctx, failedSpec.ID); err == nil {
		return "", errors.New("disable lost historical startup error")
	}
	failedSpec.Config = json.RawMessage(`{"label":"recovered"}`)
	recovered, err := host.Enable(ctx, failedSpec)
	if err != nil {
		return "", fmt.Errorf("failed instance prevented live recovery: %w", err)
	}
	if err := recovered.Wait(ctx); err != nil {
		return "", err
	}
	if _, err := invoke(ctx, host, "recovery", "chain.a", "stats"); err != nil {
		return "", err
	}
	if err := host.Disable(ctx, failedSpec.ID); err != nil {
		return "", err
	}
	return "", nil
}

func ignoreCanceled(err error) error {
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

func makeArchive(ctx context.Context, root, kind, guest, version string, key ed25519.PrivateKey) (string, error) {
	source := filepath.Join(root, "source-"+version)
	if err := os.MkdirAll(source, 0700); err != nil {
		return "", err
	}
	artifact := "chain"
	if kind == "wasm" {
		artifact += ".wasm"
	}
	input, err := os.Open(guest)
	if err != nil {
		return "", err
	}
	output, err := os.OpenFile(filepath.Join(source, artifact), os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0700)
	if err != nil {
		return "", errors.Join(err, input.Close())
	}
	_, copyErr := io.Copy(output, input)
	if err := errors.Join(copyErr, output.Close(), input.Close()); err != nil {
		return "", err
	}
	manifest := pluginmeta.Chain()
	// Recovery conformance deliberately packages this declaration as several releases.
	manifest.Version = version
	manifest.Runtime = contract.RuntimeSpec{Kind: kind}
	if kind == "wasm" {
		manifest.Runtime.Entry = artifact
	} else {
		manifest.Runtime.Artifacts = []contract.Artifact{{OS: runtime.GOOS, Arch: runtime.GOARCH, Path: artifact}}
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(source, packagehost.ManifestFile), data, 0600); err != nil {
		return "", err
	}
	archive := filepath.Join(root, "chain-"+version+".zip")
	if _, err := packagehost.Pack(ctx, source, archive); err != nil {
		return "", err
	}
	if _, err := packagehost.Sign(ctx, archive, "acceptance", key); err != nil {
		return "", err
	}
	return archive, nil
}

func mustEntries(host *cordis.Host) []contract.Entry {
	entries, err := host.Store().Entries()
	if err != nil {
		panic(err)
	}
	return entries
}
