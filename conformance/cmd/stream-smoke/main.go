// Command stream-smoke runs the feed example as a real native plugin and
// checks streaming methods and a middleware chain shared across processes.
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

	"github.com/Cyenoch/cordis/contract"
	"github.com/Cyenoch/cordis/examples/pluginmeta"
	"github.com/Cyenoch/cordis/hosting"
	"github.com/Cyenoch/cordis/packagehost"
)

func main() {
	guest := flag.String("guest", "", "independently built feed executable")
	flag.Parse()
	if err := run(*guest); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

type tailRequest struct {
	Count int `json:"count"`
}

type tailItem struct {
	Line string `json:"line"`
}

func run(guest string) (result error) {
	if guest == "" {
		return errors.New("-guest is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	root, err := os.MkdirTemp("", "cordis-stream-")
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, os.RemoveAll(root)) }()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	manifest := pluginmeta.Feed()
	manifest.Runtime = contract.RuntimeSpec{Kind: "native", Artifacts: []contract.Artifact{{OS: runtime.GOOS, Arch: runtime.GOARCH, Path: "plugin"}}}
	source := filepath.Join(root, "source")
	if err := os.Mkdir(source, 0700); err != nil {
		return err
	}
	binary, err := os.ReadFile(guest)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(source, "plugin"), binary, 0600); err != nil {
		return err
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(source, "plugin.json"), data, 0600); err != nil {
		return err
	}
	archive := filepath.Join(root, "feed.zip")
	if _, err := packagehost.Pack(ctx, source, archive); err != nil {
		return err
	}
	if _, err := packagehost.Sign(ctx, archive, "author", private); err != nil {
		return err
	}
	host, err := hosting.Open(hosting.Options{Directory: filepath.Join(root, "store"), Trust: packagehost.TrustPolicy{
		Keys:       map[string]packagehost.TrustedKey{"author": {PublicKey: public, Publisher: manifest.Publisher}},
		Publishers: map[string][]string{manifest.Publisher: {manifest.ID}},
	}})
	if err != nil {
		return err
	}
	defer func() {
		closeCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		result = errors.Join(result, host.Close(closeCtx))
	}()
	pkg, err := host.Install(ctx, archive)
	if err != nil {
		return err
	}
	if err := host.Select(ctx, pkg.Ref); err != nil {
		return err
	}
	instance, err := host.Enable(ctx, contract.Entry{ID: "feed", Package: pkg.Ref.ID})
	if err != nil {
		return err
	}
	if err := instance.Wait(ctx); err != nil {
		return err
	}
	feed, err := contract.EndpointKey("example.feed", 1).Get(host.Scope())
	if err != nil {
		return err
	}

	// Every item arrives, in order.
	seen := 0
	for item, err := range contract.InvokeStream[tailRequest, tailItem](ctx, feed, "tail", tailRequest{Count: 200}) {
		if err != nil {
			return fmt.Errorf("stream: %w", err)
		}
		if want := fmt.Sprintf("line %d", seen); item.Line != want {
			return fmt.Errorf("stream item %d = %q", seen, item.Line)
		}
		seen++
	}
	if seen != 200 {
		return fmt.Errorf("stream delivered %d of 200 items", seen)
	}
	// Stopping early leaves the plugin and the session usable.
	for range contract.InvokeStream[tailRequest, tailItem](ctx, feed, "tail", tailRequest{Count: 1 << 30}) {
		if seen++; seen == 205 {
			break
		}
	}
	for _, err := range contract.InvokeStream[tailRequest, tailItem](ctx, feed, "tail", tailRequest{Count: 1}) {
		if err != nil {
			return fmt.Errorf("stream after an early stop: %w", err)
		}
	}

	// The plugin's middleware wraps a chain that the host runs.
	render := contract.NewChainKey[string, string]("example.render")
	if _, err := render.Use(ctx, host.Scope(), func(ctx context.Context, text string, next func(context.Context, string) (string, error)) (string, error) {
		out, err := next(ctx, text+"!")
		return "<" + out + ">", err
	}); err != nil {
		return err
	}
	out, err := render.Do(ctx, host.Scope(), "hi", func(_ context.Context, text string) (string, error) { return text, nil })
	if err != nil || out != "[<hi!>]" {
		return fmt.Errorf("chain = %q, %v", out, err)
	}
	if err := host.Disable(ctx, "feed"); err != nil {
		return err
	}
	out, err = render.Do(ctx, host.Scope(), "hi", func(_ context.Context, text string) (string, error) { return text, nil })
	if err != nil || out != "<hi!>" {
		return fmt.Errorf("chain after the plugin stopped = %q, %v", out, err)
	}
	fmt.Println("Native streams and chains: ordered delivery, early stop and cross-process middleware passed.")
	return nil
}
