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
)

func main() {
	artifact := flag.String("native", "", "independently built counter executable")
	flag.Parse()
	if err := run(*artifact); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(artifact string) (result error) {
	if artifact == "" {
		return errors.New("-native is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	root, err := os.MkdirTemp("", "cordis-example-")
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, os.RemoveAll(root)) }()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	manifest := pluginmeta.Counter()
	manifest.Runtime = contract.RuntimeSpec{Kind: "native", Artifacts: []contract.Artifact{{OS: runtime.GOOS, Arch: runtime.GOARCH, Path: "plugin"}}}
	source := filepath.Join(root, "source")
	if err := os.Mkdir(source, 0700); err != nil {
		return err
	}
	data, err := os.ReadFile(artifact)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(source, "plugin"), data, 0600); err != nil {
		return err
	}
	data, err = json.Marshal(manifest)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(source, "plugin.json"), data, 0600); err != nil {
		return err
	}
	archive := filepath.Join(root, "counter.zip")
	if _, err := packagehost.Pack(ctx, source, archive); err != nil {
		return err
	}
	if _, err := packagehost.Sign(ctx, archive, "example", private); err != nil {
		return err
	}
	trust := packagehost.TrustPolicy{
		Keys:       map[string]packagehost.TrustedKey{"example": {PublicKey: public, Publisher: manifest.Publisher}},
		Publishers: map[string][]string{manifest.Publisher: {manifest.ID}},
	}
	// Each Host owns its service registry. Separate hosts can provide the same key.
	hosts := make([]*cordis.Host, 0, 2)
	defer func() {
		for _, host := range hosts {
			closeCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
			result = errors.Join(result, host.Close(closeCtx))
			stop()
		}
	}()
	instances := make([]contract.Instance, 0, 2)
	for index, initial := range []int{10, 100} {
		host, err := cordis.Open(cordis.Options{Directory: filepath.Join(root, fmt.Sprint(index)), Trust: trust})
		if err != nil {
			return err
		}
		hosts = append(hosts, host)
		pkg, err := host.Install(ctx, archive)
		if err != nil {
			return err
		}
		if err := host.Select(ctx, pkg.Ref); err != nil {
			return err
		}
		instance, err := host.Enable(ctx, contract.Entry{ID: "counter", Package: manifest.ID, Config: json.RawMessage(fmt.Sprintf(`{"initial":%d}`, initial))})
		if err != nil {
			return err
		}
		instances = append(instances, instance)
		if err := instance.Wait(ctx); err != nil {
			return err
		}
	}
	if err := expect(ctx, hosts[0], "add", 2, 12); err != nil {
		return err
	}
	if err := expect(ctx, hosts[1], "get", 0, 100); err != nil {
		return err
	}
	if _, err := instances[0].Update(ctx, json.RawMessage(`{"initial":20}`)); err != nil {
		return err
	}
	if err := expect(ctx, hosts[0], "get", 0, 20); err != nil {
		return err
	}
	if err := hosts[0].Disable(ctx, "counter"); err != nil {
		return err
	}
	if err := expect(ctx, hosts[1], "get", 0, 100); err != nil {
		return err
	}
	fmt.Println("Native counter: independent instances, update and cleanup passed.")
	return nil
}

func expect(ctx context.Context, host *cordis.Host, operation string, delta, want int) error {
	input := json.RawMessage(fmt.Sprintf(`{"method":"execute","input":{"operation":%q,"delta":%d}}`, operation, delta))
	data, err := host.Call(ctx, contract.Requirement{Name: "example.counter", Major: 1}, input)
	if err != nil {
		return err
	}
	var value struct {
		Value  int `json:"value"`
		Events int `json:"events"`
	}
	if err := contract.Decode(data, &value); err != nil {
		return err
	}
	if value.Value != want {
		return fmt.Errorf("counter = %d, want %d", value.Value, want)
	}
	return nil
}
