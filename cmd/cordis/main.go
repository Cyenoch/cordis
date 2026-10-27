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
	"runtime"
	"runtime/debug"
	"syscall"

	"github.com/Cyenoch/cordis/contract"
	"github.com/Cyenoch/cordis/packagehost"
)

const sdkModule = "github.com/Cyenoch/cordis"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := exitCode(execute(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr), os.Stderr)
	stop()
	os.Exit(code)
}

type usageError struct{ message string }

func (e *usageError) Error() string { return e.message }

func exitCode(err error, errOut io.Writer) int {
	if err == nil {
		return 0
	}
	fmt.Fprintln(errOut, err)
	var usage *usageError
	if errors.As(err, &usage) {
		return 2
	}
	return 1
}

var commandUsage = map[string]string{
	"help":     "help [COMMAND]",
	"init":     "init -module MODULE [-sdk-version VERSION] [-id ID] [-publisher NAME] [-runtime native|wasm] DIRECTORY",
	"build":    "build -go-package PACKAGE -goos OS -goarch ARCH [FLAGS] DIRECTORY",
	"pack":     "pack DIRECTORY ARCHIVE.zip",
	"sign":     "sign ARCHIVE.zip KEY_ID PRIVATE_KEY_FILE",
	"keygen":   "keygen PRIVATE_KEY_FILE",
	"inspect":  "inspect ARCHIVE.zip",
	"validate": "validate ARCHIVE.zip",
	"generate": "generate -output FILE|- [-package NAME] [-format FORMAT] CONTRACT...",
	"run":      "run -dev-unsigned [FLAGS] ARCHIVE.zip",
	"version":  "version",
}

// Subcommand-local stdlib flag sets avoid hidden environment/config layering.
func execute(ctx context.Context, args []string, in io.Reader, out, errOut io.Writer) error {
	if len(args) == 0 {
		return printHelp(out)
	}
	switch args[0] {
	case "-h", "-help", "--help":
		if len(args) != 1 {
			return &usageError{message: "help [COMMAND]"}
		}
		return printHelp(out)
	case "help":
		if len(args) == 1 {
			return printHelp(out)
		}
		if len(args) == 2 {
			switch args[1] {
			case "-h", "--help", "-help", "help":
				return printHelp(out)
			case "-version", "--version":
				return printVersion(out)
			}
		}
		if len(args) != 2 || commandUsage[args[1]] == "" {
			return &usageError{message: "help [COMMAND]"}
		}
		return execute(ctx, []string{args[1], "-help"}, in, out, errOut)
	case "-version", "--version":
		args = append([]string{"version"}, args[1:]...)
	}
	usage, ok := commandUsage[args[0]]
	if !ok {
		return &usageError{message: "unknown command: " + args[0]}
	}
	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.Usage = func() {}
	version := flags.Bool("version", false, "print build and module version")
	parse := func() (bool, error) {
		err := flags.Parse(args[1:])
		if errors.Is(err, flag.ErrHelp) {
			if _, err := fmt.Fprintln(out, "Usage: cordis "+usage); err != nil {
				return true, err
			}
			flags.SetOutput(out)
			flags.PrintDefaults()
			return true, nil
		}
		if err != nil {
			return true, &usageError{message: args[0] + ": " + err.Error()}
		}
		if *version {
			return true, printVersion(out)
		}
		return false, nil
	}
	switch args[0] {
	case "init":
		return initCommand(flags, parse, out)
	case "build":
		return buildCommand(ctx, flags, parse, out)
	case "generate":
		return generateCommand(flags, parse, out)
	case "run":
		return runCommand(ctx, flags, parse, in, out, errOut)
	}
	if done, err := parse(); done {
		return err
	}
	count := map[string]int{"pack": 2, "inspect": 1, "validate": 1, "keygen": 1, "sign": 3, "version": 0}
	if flags.NArg() != count[args[0]] {
		return &usageError{message: usage}
	}
	switch args[0] {
	case "version":
		return printVersion(out)
	case "pack":
		pkg, err := packagehost.Pack(ctx, flags.Arg(0), flags.Arg(1))
		if err != nil {
			return err
		}
		return writeJSON(out, pkg.Ref)
	case "inspect", "validate":
		pkg, err := packagehost.Inspect(ctx, flags.Arg(0))
		if err != nil {
			return err
		}
		return writeJSON(out, struct {
			Ref      contract.PackageRef `json:"ref"`
			Manifest contract.Manifest   `json:"manifest"`
			Trust    string              `json:"trust"`
		}{Ref: pkg.Ref, Manifest: pkg.Manifest, Trust: "not evaluated; inspection never authorizes execution"})
	case "keygen":
		pub, key, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return err
		}
		file, err := os.OpenFile(flags.Arg(0), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		_, writeErr := io.WriteString(file, hex.EncodeToString(key))
		if err := errors.Join(writeErr, file.Close()); err != nil {
			return err
		}
		return writeJSON(out, map[string]string{"publicKey": hex.EncodeToString(pub)})
	case "sign":
		raw, err := os.ReadFile(flags.Arg(2))
		if err != nil {
			return err
		}
		key, err := hex.DecodeString(string(raw))
		if err != nil || len(key) != ed25519.PrivateKeySize {
			return fmt.Errorf("invalid Ed25519 private key file %q", flags.Arg(2))
		}
		pkg, err := packagehost.Sign(ctx, flags.Arg(0), flags.Arg(1), ed25519.PrivateKey(key))
		if err != nil {
			return err
		}
		return writeJSON(out, pkg.Ref)
	}
	return nil
}

func printHelp(out io.Writer) error {
	_, err := fmt.Fprintln(out, "cordis: init | build | pack | sign | keygen | inspect | validate | generate | run | version\nUsage: cordis help [COMMAND]")
	return err
}

func writeJSON(out io.Writer, value any) error {
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(value)
}

func cordisModule(info *debug.BuildInfo) (debug.Module, bool) {
	if info.Main.Path == sdkModule {
		return info.Main, true
	}
	for _, dep := range info.Deps {
		if dep.Path == sdkModule {
			return *dep, true
		}
	}
	return debug.Module{}, false
}

func printVersion(out io.Writer) error {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return fmt.Errorf("Cordis build metadata unavailable")
	}
	module, ok := cordisModule(info)
	if !ok {
		return fmt.Errorf("Cordis module metadata unavailable")
	}
	if _, err := fmt.Fprintf(out, "cordis %s %s %s/%s\nmodule %s\n", module.Version, info.GoVersion, runtime.GOOS, runtime.GOARCH, module.Path); err != nil {
		return err
	}
	if module.Replace != nil {
		if _, err := fmt.Fprintf(out, "replace %s %s\n", module.Replace.Path, module.Replace.Version); err != nil {
			return err
		}
	}
	for _, setting := range info.Settings {
		if setting.Key == "vcs.revision" || setting.Key == "vcs.modified" || setting.Key == "vcs.time" {
			if _, err := fmt.Fprintf(out, "%s %s\n", setting.Key, setting.Value); err != nil {
				return err
			}
		}
	}
	return nil
}
