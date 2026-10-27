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
	"time"

	cordis "github.com/Cyenoch/cordis"
	"github.com/Cyenoch/cordis/contract"
	"github.com/Cyenoch/cordis/examples/pluginmeta"
	"github.com/Cyenoch/cordis/packagehost"
	native "github.com/Cyenoch/cordis/runtime/native"
	wasm "github.com/Cyenoch/cordis/runtime/wasm"
)

func main() {
	worker := flag.String("worker", "", "WASM worker executable")
	nativePath := flag.String("native", "", "independently compiled native plugin")
	wasmPath := flag.String("wasm", "", "independently compiled WASI plugin")
	flag.Parse()
	if err := run(*worker, *nativePath, *wasmPath); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(worker, nativePath, wasmPath string) (result error) {
	if worker == "" || nativePath == "" || wasmPath == "" {
		return fmt.Errorf("-worker, -native, -wasm are required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	root, err := os.MkdirTemp("", "cordis-host-smoke-")
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, os.RemoveAll(root)) }()
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	trust := packagehost.TrustPolicy{Keys: map[string]packagehost.TrustedKey{"example-key": {PublicKey: pub, Publisher: "example.author"}},
		Publishers: map[string][]string{"example.author": {"example.native", "example.wasm"}}}
	host, err := cordis.Open(cordis.Options{FrameBytes: contract.MaxFrameBytes, Directory: filepath.Join(root, "store"), Trust: trust,
		Runtimes: map[string]contract.Runtime{"native": native.New(), "wasm": wasm.New(worker)},
		Authorize: func(context.Context, contract.Identity, contract.Capability) (json.RawMessage, error) {
			return json.RawMessage(`{}`), nil
		}})
	if err != nil {
		return err
	}
	defer func() {
		closeCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		result = errors.Join(result, host.Close(closeCtx))
	}()
	if err := host.Broker().Register("example.greeting", func(context.Context, contract.Identity, json.RawMessage, json.RawMessage, json.RawMessage) error {
		return nil
	},
		func(context.Context, contract.Identity, json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(`{"message":"hello from host"}`), nil
		}); err != nil {
		return err
	}
	if err := host.Contributions().Register("example.status", func(context.Context, contract.Identity, contract.Contribution) error { return nil }); err != nil {
		return err
	}
	counter := pluginmeta.Counter().Definitions[0].Provides[0]
	for _, kind := range []string{"native", "wasm"} {
		artifact := nativePath
		if kind == "wasm" {
			artifact = wasmPath
		}
		ref, err := buildPackage(ctx, root, kind, pluginmeta.Counter().Version, artifact, key)
		if err != nil {
			return err
		}
		pkg, err := host.Install(ctx, ref)
		if err != nil {
			return err
		}
		if err := host.Select(ctx, pkg.Ref); err != nil {
			return err
		}
		if err := host.ConfigureRealm(kind+"-a", "", []string{counter.Key()}); err != nil {
			return err
		}
		if err := host.ConfigureRealm(kind+"-b", "", []string{counter.Key()}); err != nil {
			return err
		}
		a, err := host.Enable(ctx, contract.Entry{ID: kind + ".a", Package: pkg.Ref.ID, Realm: kind + "-a", Config: json.RawMessage(`{"initial":10}`)})
		if err != nil {
			return err
		}
		if err := a.Wait(ctx); err != nil {
			return err
		}
		b, err := host.Enable(ctx, contract.Entry{ID: kind + ".b", Package: pkg.Ref.ID, Realm: kind + "-b", Config: json.RawMessage(`{"initial":100}`)})
		if err != nil {
			return err
		}
		if err := b.Wait(ctx); err != nil {
			return err
		}
		if err := expect(ctx, host, kind+"-a", `{"operation":"add","delta":2}`, 12, 0, ""); err != nil {
			return err
		}
		if err := expect(ctx, host, kind+"-b", `{"operation":"get","delta":0}`, 100, 0, ""); err != nil {
			return err
		}
		if err := expect(ctx, host, kind+"-a", `{"operation":"host","delta":0}`, 12, 0, "hello from host"); err != nil {
			return err
		}
		if _, err := host.Emit(ctx, kind+"-a", "counter.changed", json.RawMessage(`{}`), contract.Sequential); err != nil {
			return err
		}
		if err := expect(ctx, host, kind+"-a", `{"operation":"get","delta":0}`, 12, 1, ""); err != nil {
			return err
		}
		if _, err := a.Update(ctx, json.RawMessage(`{"initial":20}`)); err != nil {
			return err
		}
		if err := expect(ctx, host, kind+"-a", `{"operation":"get","delta":0}`, 20, 0, ""); err != nil {
			return err
		}
		if err := host.Disable(ctx, kind+".a"); err != nil {
			return err
		}
		if err := expect(ctx, host, kind+"-b", `{"operation":"get","delta":0}`, 100, 0, ""); err != nil {
			return err
		}
		if err := host.Disable(ctx, kind+".b"); err != nil {
			return err
		}
		newArchive, err := buildPackage(ctx, root, kind, "1.1.0", artifact, key)
		if err != nil {
			return err
		}
		newPkg, err := host.Install(ctx, newArchive)
		if err != nil {
			return err
		}
		if err := host.Select(ctx, newPkg.Ref); err != nil {
			return err
		}
		c, err := host.Enable(ctx, contract.Entry{ID: kind + ".c", Package: newPkg.Ref.ID, Realm: kind + "-a", Config: json.RawMessage(`{"initial":30}`)})
		if err != nil {
			return err
		}
		if err := c.Wait(ctx); err != nil {
			return err
		}
		if err := expect(ctx, host, kind+"-a", `{"operation":"get","delta":0}`, 30, 0, ""); err != nil {
			return err
		}
		if err := host.Disable(ctx, kind+".c"); err != nil {
			return err
		}
		removal, err := host.Uninstall(ctx, pkg.Ref, false)
		if err != nil {
			return err
		}
		if !removal.Removed {
			return fmt.Errorf("old package not removed: %+v", removal)
		}
		removal, err = host.Uninstall(ctx, newPkg.Ref, false)
		if err != nil {
			return err
		}
		if !removal.Removed {
			return fmt.Errorf("new package not removed: %+v", removal)
		}
		fmt.Printf("%s: signed external package, dual instance, host callback, event, config restart, version switch, unload verified\n", kind)
	}
	return nil
}

func expect(ctx context.Context, host *cordis.Host, realm, input string, value, events int, greeting string) error {
	service := pluginmeta.Counter().Definitions[0].Provides[0]
	request, err := json.Marshal(struct {
		Method string          `json:"method"`
		Input  json.RawMessage `json:"input"`
	}{Method: "execute", Input: json.RawMessage(input)})
	if err != nil {
		return err
	}
	data, err := host.Realm(realm).Call(ctx, contract.Requirement{
		Name: service.Name, Major: service.Major, MinMinor: service.Minor, MaxMinor: service.Minor,
	}, request)
	if err != nil {
		return err
	}
	var output struct {
		Value  int    `json:"value"`
		Events int    `json:"events"`
		Host   string `json:"host,omitempty"`
	}
	if err := contract.Decode(data, &output); err != nil {
		return err
	}
	if output.Value != value || output.Events != events || output.Host != greeting {
		return fmt.Errorf("unexpected counter response %s", data)
	}
	return nil
}

func buildPackage(ctx context.Context, root, kind, version, artifact string, key ed25519.PrivateKey) (string, error) {
	dir := filepath.Join(root, kind+"-"+version)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	data, err := os.ReadFile(artifact)
	if err != nil {
		return "", err
	}
	name := "plugin"
	spec := contract.RuntimeSpec{Kind: kind, Artifacts: []contract.Artifact{}}
	if kind == "wasm" {
		name = "plugin.wasm"
		spec.Entry = name
	} else {
		spec.Artifacts = []contract.Artifact{{OS: runtime.GOOS, Arch: runtime.GOARCH, Path: name}}
	}
	if err := os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
		return "", err
	}
	manifest := pluginmeta.Counter()
	// This host exercises two runtime-specific identities and a version switch.
	manifest.ID = "example." + kind
	manifest.Version = version
	manifest.Runtime = spec
	raw, err := json.Marshal(manifest)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, "plugin.json"), raw, 0600); err != nil {
		return "", err
	}
	archive := dir + ".zip"
	if _, err := packagehost.Pack(ctx, dir, archive); err != nil {
		return "", err
	}
	if _, err := packagehost.Sign(ctx, archive, "example-key", key); err != nil {
		return "", err
	}
	return archive, nil
}
