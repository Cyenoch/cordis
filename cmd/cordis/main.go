package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	cordis "github.com/Cyenoch/cordis"
	"github.com/Cyenoch/cordis/contract"
	"github.com/Cyenoch/cordis/packagehost"
	native "github.com/Cyenoch/cordis/runtime/native"
	wasm "github.com/Cyenoch/cordis/runtime/wasm"
	"github.com/Cyenoch/cordis/schema"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := execute(ctx, os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		var usage *usageError
		if errors.As(err, &usage) {
			os.Exit(2)
		}
		os.Exit(1)
	}
}

type usageError struct{ message string }

func (e *usageError) Error() string { return e.message }

// Subcommand-local stdlib flag sets deliberately avoid hidden env/config layering.
func execute(ctx context.Context, args []string, out, errOut io.Writer) (result error) {
	if len(args) == 0 {
		return &usageError{message: "commands: pack, sign, keygen, inspect, validate, generate, run"}
	}
	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	flags.SetOutput(errOut)
	writeJSON := func(value any) error { enc := json.NewEncoder(out); enc.SetIndent("", "  "); return enc.Encode(value) }
	switch args[0] {
	case "pack":
		if len(args) != 3 {
			return &usageError{message: "pack DIRECTORY ARCHIVE.zip"}
		}
		pkg, err := packagehost.Pack(ctx, args[1], args[2])
		if err != nil {
			return err
		}
		return writeJSON(pkg.Ref)
	case "inspect", "validate":
		if len(args) != 2 {
			return &usageError{message: args[0] + " ARCHIVE.zip"}
		}
		pkg, err := packagehost.Inspect(ctx, args[1])
		if err != nil {
			return err
		}
		return writeJSON(struct {
			Ref      contract.PackageRef `json:"ref"`
			Manifest contract.Manifest   `json:"manifest"`
			Trust    string              `json:"trust"`
		}{Ref: pkg.Ref, Manifest: pkg.Manifest, Trust: "not evaluated; inspection never authorizes execution"})
	case "keygen":
		if len(args) != 2 {
			return &usageError{message: "keygen PRIVATE_KEY_FILE"}
		}
		pub, key, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return err
		}
		file, err := os.OpenFile(args[1], os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		_, writeErr := io.WriteString(file, hex.EncodeToString(key))
		if err := errors.Join(writeErr, file.Close()); err != nil {
			return err
		}
		return writeJSON(map[string]string{"publicKey": hex.EncodeToString(pub)})
	case "sign":
		if len(args) != 4 {
			return &usageError{message: "sign ARCHIVE.zip KEY_ID PRIVATE_KEY_FILE"}
		}
		raw, err := os.ReadFile(args[3])
		if err != nil {
			return err
		}
		key, err := hex.DecodeString(string(raw))
		if err != nil || len(key) != ed25519.PrivateKeySize {
			return fmt.Errorf("invalid Ed25519 private key file")
		}
		pkg, err := packagehost.Sign(ctx, args[1], args[2], ed25519.PrivateKey(key))
		if err != nil {
			return err
		}
		return writeJSON(pkg.Ref)
	case "generate":
		name := flags.String("package", "pluginapi", "generated Go package")
		if err := flags.Parse(args[1:]); err != nil {
			return &usageError{message: err.Error()}
		}
		if flags.NArg() != 1 {
			return &usageError{message: "generate [-package NAME] CONTRACT.json"}
		}
		data, err := os.ReadFile(flags.Arg(0))
		if err != nil {
			return err
		}
		definition, err := schema.Parse(data)
		if err != nil {
			return err
		}
		return schema.Generate(out, *name, definition)
	case "run":
		worker := flags.String("worker", "", "worker binary for WASM")
		dev := flags.Bool("dev-unsigned", false, "explicitly trust only this archive digest for this run")
		service := flags.String("service", "", "service name to call")
		definitionID := flags.String("definition", "", "package definition (default: manifest default)")
		major := flags.Uint("major", 0, "service major (default: unique declared major)")
		minMinor := flags.Uint("min-minor", 0, "minimum compatible minor")
		maxMinor := flags.Uint("max-minor", uint(^uint32(0)), "maximum compatible minor")
		input := flags.String("input", "{}", "JSON call input")
		config := flags.String("config", "{}", "JSON plugin configuration")
		if err := flags.Parse(args[1:]); err != nil {
			return &usageError{message: err.Error()}
		}
		if flags.NArg() != 1 || !*dev {
			return &usageError{message: "run requires -dev-unsigned and one local archive; no persisted/global trust is granted"}
		}
		pkg, err := packagehost.Inspect(ctx, flags.Arg(0))
		if err != nil {
			return err
		}
		root, err := os.MkdirTemp("", "cordis-run-")
		if err != nil {
			return err
		}
		defer func() { result = errors.Join(result, os.RemoveAll(root)) }()
		host, err := cordis.Open(cordis.Options{Directory: filepath.Join(root, "store"), Trust: packagehost.TrustPolicy{DevDigests: map[string]bool{pkg.Ref.Digest: true}, Publishers: map[string][]string{pkg.Manifest.Publisher: {pkg.Ref.ID}}},
			Runtimes: map[string]contract.Runtime{"native": native.New(), "wasm": wasm.New(*worker)}})
		if err != nil {
			return err
		}
		defer func() {
			closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			result = errors.Join(result, host.Close(closeCtx))
		}()
		pkg, err = host.Install(ctx, flags.Arg(0))
		if err != nil {
			return err
		}
		if err := host.Select(ctx, pkg.Ref); err != nil {
			return err
		}
		selected := *definitionID
		if selected == "" {
			selected = pkg.Manifest.DefaultDefinition
		}
		required, err := selectService(pkg.Manifest, selected, *service, *major, *minMinor, *maxMinor)
		if err != nil {
			return err
		}
		instance, err := host.Enable(ctx, contract.Entry{ID: "cli.instance", Package: pkg.Ref.ID, Definition: selected, Config: json.RawMessage(*config)})
		if err != nil {
			return err
		}
		if err := instance.Wait(ctx); err != nil {
			return err
		}
		if *service == "" {
			return writeJSON(instance.Status())
		}
		result, err := host.Call(ctx, "", required, json.RawMessage(*input))
		if err != nil {
			return err
		}
		return writeJSON(result)
	case "help", "-h", "--help":
		_, err := fmt.Fprintln(out, "cordis: pack | sign | keygen | inspect | validate | generate | run")
		return err
	default:
		return &usageError{message: "unknown command: " + args[0]}
	}
}

func selectService(manifest contract.Manifest, definition, name string, major, minMinor, maxMinor uint) (contract.Requirement, error) {
	if major > uint(^uint32(0)) || maxMinor > uint(^uint32(0)) || minMinor > maxMinor {
		return contract.Requirement{}, &usageError{message: "service versions require uint32 values and min-minor <= max-minor"}
	}
	for _, candidate := range manifest.Definitions {
		if candidate.ID != definition {
			continue
		}
		if name == "" {
			return contract.Requirement{}, nil
		}
		matches := []contract.Service{}
		for _, service := range candidate.Provides {
			if service.Name == name && (major == 0 || uint(service.Major) == major) && uint(service.Minor) >= minMinor && uint(service.Minor) <= maxMinor {
				matches = append(matches, service)
			}
		}
		if len(matches) != 1 {
			return contract.Requirement{}, &usageError{message: "service version is absent or ambiguous; specify -definition, -major and a compatible minor range"}
		}
		return contract.Requirement{Name: name, Major: matches[0].Major, MinMinor: uint32(minMinor), MaxMinor: uint32(maxMinor)}, nil
	}
	return contract.Requirement{}, &usageError{message: "unknown definition: " + definition}
}
