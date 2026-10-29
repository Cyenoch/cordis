package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	cordis "github.com/Cyenoch/cordis"
	"github.com/Cyenoch/cordis/contract"
	"github.com/Cyenoch/cordis/examples/pluginmeta"
	"github.com/Cyenoch/cordis/packagehost"
)

func main() {
	guest := flag.String("guest", "", "independently built chain executable")
	badGuest := flag.String("bad-guest", "", "chain executable built to reject initialization")
	flag.Parse()
	if err := run(*guest, *badGuest); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(guest, badGuest string) (result error) {
	if guest == "" || badGuest == "" {
		return errors.New("-guest and -bad-guest are required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	root, err := os.MkdirTemp("", "cordis-lifecycle-")
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, os.RemoveAll(root)) }()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	good, err := pack(ctx, root, guest, "1.0.0", private)
	if err != nil {
		return err
	}
	bad, err := pack(ctx, root, badGuest, "2.0.0", private)
	if err != nil {
		return err
	}
	manifest := pluginmeta.Chain()
	options := cordis.Options{Directory: filepath.Join(root, "store"), Trust: packagehost.TrustPolicy{
		Keys:       map[string]packagehost.TrustedKey{"author": {PublicKey: public, Publisher: manifest.Publisher}},
		Publishers: map[string][]string{manifest.Publisher: {manifest.ID}},
	}}
	host, err := cordis.Open(options)
	if err != nil {
		return err
	}
	defer func() {
		closeCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		result = errors.Join(result, host.Close(closeCtx))
	}()
	pkg, err := host.Install(ctx, good)
	if err != nil {
		return err
	}
	if err := host.Select(ctx, pkg.Ref); err != nil {
		return err
	}
	instances := map[string]contract.Instance{}
	for _, definition := range []string{"consumer", "middle", "provider"} {
		instance, err := host.Enable(ctx, contract.Entry{ID: definition, Package: pkg.Ref.ID, Definition: definition, Config: json.RawMessage(`{"label":"initial"}`)})
		if err != nil {
			return err
		}
		instances[definition] = instance
	}
	for name, instance := range instances {
		if err := instance.Wait(ctx); err != nil {
			return fmt.Errorf("start %s: %w", name, err)
		}
	}
	if err := expect(ctx, host, "initial"); err != nil {
		return err
	}
	before := instances["consumer"].Status().Identity.Activation
	if _, err := instances["provider"].Update(ctx, json.RawMessage(`{"label":"updated"}`)); err != nil {
		return err
	}
	if err := instances["consumer"].Wait(ctx); err != nil {
		return err
	}
	if instances["consumer"].Status().Identity.Activation == before {
		return errors.New("provider update did not rebind consumer")
	}
	if err := expect(ctx, host, "updated"); err != nil {
		return err
	}
	callCtx, stop := context.WithTimeout(ctx, 100*time.Millisecond)
	_, err = invoke(callCtx, host, "chain.c", "wait")
	stop()
	if !errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("expected cancellation deadline, got %v", err)
	}
	if err := expect(ctx, host, "updated"); err != nil {
		return err
	}
	// Reopening restores persistent intent; Close does not rewrite it as disabled.
	if err := host.Close(ctx); err != nil {
		return err
	}
	reopened, err := cordis.Open(options)
	if err != nil {
		return err
	}
	host = reopened
	if _, err := host.Restore(ctx); err != nil {
		return err
	}
	if err := expect(ctx, host, "updated"); err != nil {
		return err
	}
	for _, id := range []string{"consumer", "middle", "provider"} {
		if err := host.Disable(ctx, id); err != nil {
			return err
		}
	}
	instance, err := host.Enable(ctx, contract.Entry{ID: "recovery", Package: pkg.Ref.ID, Definition: "provider", Config: json.RawMessage(`{"label":"recovered"}`)})
	if err != nil {
		return err
	}
	if err := instance.Wait(ctx); err != nil {
		return err
	}
	broken, err := host.Install(ctx, bad)
	if err != nil {
		return err
	}
	if err := host.Replace(ctx, broken.Ref); err == nil || !strings.Contains(err.Error(), "intentional version-specific initialization failure") {
		return fmt.Errorf("broken package initialization: %v", err)
	}
	selected, err := host.Store().Selected(pkg.Ref.ID)
	if err != nil {
		return err
	}
	if selected.Ref != broken.Ref {
		return errors.New("failed startup silently rolled package selection back")
	}
	// Replacement reports cleanup errors from the failed activation even when the new one starts.
	if err := host.Replace(ctx, pkg.Ref); err != nil && !strings.Contains(err.Error(), "intentional version-specific initialization failure") {
		return err
	}
	current, err := host.Instance("recovery")
	if err != nil {
		return err
	}
	if err := current.Wait(ctx); err != nil {
		return err
	}
	if _, err := invoke(ctx, host, "chain.a", "stats"); err != nil {
		return err
	}
	if err := host.Disable(ctx, "recovery"); err != nil {
		return err
	}
	if err := optionalChild(ctx, host, pkg.Ref.ID); err != nil {
		return err
	}
	fmt.Println("Native lifecycle: dependency cleanup, update, cancellation, restore, failed-version recovery and owned children passed.")
	return nil
}

func optionalChild(ctx context.Context, host *cordis.Host, packageID string) error {
	parent, err := host.Enable(ctx, contract.Entry{ID: "parent", Package: packageID, Definition: "optional-parent"})
	if err != nil {
		return err
	}
	if err := parent.Wait(ctx); err != nil {
		return fmt.Errorf("parent must publish while its child waits for a provider: %w", err)
	}
	provider, err := host.Enable(ctx, contract.Entry{ID: "provider", Package: packageID, Definition: "provider"})
	if err != nil {
		return err
	}
	if err := provider.Wait(ctx); err != nil {
		return err
	}
	results, err := host.Emit(ctx, "chain.optional-ready", json.RawMessage(`null`), contract.First)
	if err != nil {
		return err
	}
	if len(results) != 1 || !results[0].Handled || string(results[0].Value) != "true" {
		return fmt.Errorf("optional child did not activate: %+v", results)
	}
	value, err := invoke(ctx, host, "chain.b", "stats")
	if err != nil {
		return err
	}
	if value.Live != 1 {
		return fmt.Errorf("optional child did not acquire its resource: %+v", value)
	}
	results, err = host.Emit(ctx, "chain.handled", json.RawMessage(`null`), contract.First)
	if err != nil {
		return err
	}
	if len(results) != 1 || !results[0].Handled || string(results[0].Value) != "0" {
		return fmt.Errorf("first handled event lost a zero result: %+v", results)
	}
	if err := host.Disable(ctx, "parent"); err != nil {
		return err
	}
	value, err = invoke(ctx, host, "chain.a", "stats")
	if err != nil {
		return err
	}
	if value.Live != 0 || value.Acquired != 1 || value.Released != 1 {
		return fmt.Errorf("parent stopped before its child released the resource: %+v", value)
	}
	return host.Disable(ctx, "provider")
}

func pack(ctx context.Context, root, artifact, version string, key ed25519.PrivateKey) (string, error) {
	source := filepath.Join(root, version)
	if err := os.Mkdir(source, 0700); err != nil {
		return "", err
	}
	binary, err := os.ReadFile(artifact)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(source, "plugin"), binary, 0600); err != nil {
		return "", err
	}
	manifest := pluginmeta.Chain()
	manifest.Version = version
	manifest.Runtime = contract.RuntimeSpec{Kind: "native", Artifacts: []contract.Artifact{{OS: runtime.GOOS, Arch: runtime.GOARCH, Path: "plugin"}}}
	data, err := json.Marshal(manifest)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(source, "plugin.json"), data, 0600); err != nil {
		return "", err
	}
	archive := source + ".zip"
	if _, err := packagehost.Pack(ctx, source, archive); err != nil {
		return "", err
	}
	if _, err := packagehost.Sign(ctx, archive, "author", key); err != nil {
		return "", err
	}
	return archive, nil
}

type counts struct {
	Acquired int    `json:"acquired"`
	Released int    `json:"released"`
	Live     int    `json:"live"`
	Label    string `json:"label"`
}

func invoke(ctx context.Context, host *cordis.Host, service, operation string) (counts, error) {
	input := json.RawMessage(fmt.Sprintf(`{"method":"execute","input":{"operation":%q}}`, operation))
	data, err := host.Call(ctx, contract.Requirement{Name: service, Major: 1, MinMinor: 1, MaxMinor: 3}, input)
	var value counts
	if err != nil {
		return value, fmt.Errorf("%s %s: %w", service, operation, err)
	}
	err = contract.Decode(data, &value)
	return value, err
}

func expect(ctx context.Context, host *cordis.Host, label string) error {
	value, err := invoke(ctx, host, "chain.c", "stats")
	if err != nil {
		return err
	}
	if value.Label != label || value.Live != 2 || value.Acquired != 2 || value.Released != 0 {
		return fmt.Errorf("dependency resources: %+v, expected label=%s and two live resources", value, label)
	}
	return nil
}
