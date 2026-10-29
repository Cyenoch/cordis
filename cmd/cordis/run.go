package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	cordis "github.com/Cyenoch/cordis"
	"github.com/Cyenoch/cordis/config"
	"github.com/Cyenoch/cordis/contract"
	"github.com/Cyenoch/cordis/packagehost"
)

type dataInput struct {
	inline string
	file   string
	format string
}

func (input *dataInput) register(flags *flag.FlagSet, name string) {
	flags.StringVar(&input.inline, name, "{}", "inline JSON "+name+"; conflicts with -"+name+"-file")
	flags.StringVar(&input.file, name+"-file", "", name+" file; - selects the only stdin input")
	flags.StringVar(&input.format, name+"-format", "", name+" file format: toml, yaml or json (required for stdin)")
}

func (input dataInput) check(name string, seen map[string]bool) error {
	if err := checkFormat(input.format); err != nil {
		return err
	}
	if seen[name] && (seen[name+"-file"] || seen[name+"-format"]) {
		return &usageError{message: "-" + name + " conflicts with -" + name + "-file/-" + name + "-format"}
	}
	if seen[name+"-file"] && input.file == "" {
		return &usageError{message: "-" + name + "-file requires a path or -"}
	}
	if seen[name+"-format"] && input.file == "" {
		return &usageError{message: "-" + name + "-format requires -" + name + "-file"}
	}
	if input.file == "-" && input.format == "" {
		return &usageError{message: "stdin requires explicit -" + name + "-format"}
	}
	return nil
}

func (input dataInput) load(name string, in io.Reader) (json.RawMessage, error) {
	var doc *config.Document
	var err error
	switch input.file {
	case "":
		doc, err = config.Parse("-"+name, []byte(input.inline), config.JSON)
	case "-":
		var data []byte
		data, err = io.ReadAll(io.LimitReader(in, (1<<20)+1))
		if err == nil {
			doc, err = config.Parse("stdin (-"+name+"-file)", data, config.Format(input.format))
		}
	default:
		doc, err = config.Load(input.file, config.Format(input.format))
	}
	if err != nil {
		return nil, fmt.Errorf("load %s: %w", name, err)
	}
	return doc.JSON, nil
}

func runCommand(ctx context.Context, flags *flag.FlagSet, parse func() (bool, error), in io.Reader, out, errOut io.Writer) (result error) {
	dev := flags.Bool("dev-unsigned", false, "explicitly trust only this archive digest for this run")
	service := flags.String("service", "", "service name to call")
	definitionID := flags.String("definition", "", "package definition (default: manifest default)")
	major := flags.Uint("major", 0, "service major (default: unique declared major)")
	minMinor := flags.Uint("min-minor", 0, "minimum compatible minor")
	maxMinor := flags.Uint("max-minor", uint(^uint32(0)), "maximum compatible minor")
	startupTimeout := flags.Duration("startup-timeout", 30*time.Second, "maximum startup wait, independent of business calls")
	var input, configuration dataInput
	input.register(flags, "input")
	configuration.register(flags, "config")
	if done, err := parse(); done {
		return err
	}
	if flags.NArg() != 1 || !*dev {
		return &usageError{message: "run requires -dev-unsigned and one local archive; no persisted/global trust is granted"}
	}
	if *startupTimeout <= 0 {
		return &usageError{message: "-startup-timeout must be positive"}
	}
	seen := map[string]bool{}
	flags.Visit(func(f *flag.Flag) { seen[f.Name] = true })
	if err := configuration.check("config", seen); err != nil {
		return err
	}
	if err := input.check("input", seen); err != nil {
		return err
	}
	if configuration.file == "-" && input.file == "-" {
		return &usageError{message: "only one of -config-file and -input-file may use stdin"}
	}
	configData, err := configuration.load("config", in)
	if err != nil {
		return err
	}
	inputData, err := input.load("input", in)
	if err != nil {
		return err
	}
	startupCtx, cancel := context.WithTimeout(ctx, *startupTimeout)
	defer cancel()
	pkg, err := packagehost.Inspect(startupCtx, flags.Arg(0))
	if err != nil {
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
	root, err := os.MkdirTemp("", "cordis-run-")
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, os.RemoveAll(root)) }()
	host, err := cordis.Open(cordis.Options{
		FrameBytes: contract.MaxFrameBytes, Directory: filepath.Join(root, "store"),
		Trust: packagehost.TrustPolicy{
			DevDigests: map[string]bool{pkg.Ref.Digest: true},
			Publishers: map[string][]string{pkg.Manifest.Publisher: {pkg.Ref.ID}},
		},
	})
	if err != nil {
		return err
	}
	defer func() {
		closeCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		result = errors.Join(result, host.Close(closeCtx))
	}()
	pkg, err = host.Install(startupCtx, flags.Arg(0))
	if err != nil {
		return err
	}
	if err := host.Select(startupCtx, pkg.Ref); err != nil {
		return err
	}
	instance, err := host.Enable(startupCtx, contract.Entry{
		ID: "cli.instance", Package: pkg.Ref.ID, Definition: selected, Config: configData,
	})
	if err != nil {
		return err
	}
	if err := waitForStartup(startupCtx, instance, selected, errOut); err != nil {
		return fmt.Errorf("start definition %q (timeout %s): %w", selected, *startupTimeout, err)
	}
	cancel()
	if *service == "" {
		return writeJSON(out, instance.Status())
	}
	value, err := host.Call(ctx, required, inputData)
	if err != nil {
		return err
	}
	return writeJSON(out, value)
}

func waitForStartup(ctx context.Context, instance contract.Instance, definition string, errOut io.Writer) error {
	waitCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- instance.Wait(waitCtx) }()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	last := ""
	lines := 0
	report := func() error {
		status := instance.Status()
		if lines >= 16 || (status.State != contract.Waiting && status.State != contract.Starting) {
			return nil
		}
		reasons := append([]contract.WaitReason{}, status.Waiting[:min(4, len(status.Waiting))]...)
		for i := range reasons {
			reasons[i].Kind = bounded(reasons[i].Kind, 64)
			reasons[i].Reason = bounded(reasons[i].Reason, 256)
			reasons[i].Parent = bounded(reasons[i].Parent, 128)
			if reasons[i].Requirement != nil {
				required := *reasons[i].Requirement
				required.Name = bounded(required.Name, 128)
				reasons[i].Requirement = &required
			}
			reasons[i].Visible = append([]contract.Service{}, reasons[i].Visible[:min(8, len(reasons[i].Visible))]...)
			for j := range reasons[i].Visible {
				reasons[i].Visible[j].Name = bounded(reasons[i].Visible[j].Name, 128)
			}
		}
		data, err := json.Marshal(struct {
			Definition string                `json:"definition"`
			State      contract.State        `json:"state"`
			Waiting    []contract.WaitReason `json:"waiting"`
		}{Definition: bounded(definition, 128), State: status.State, Waiting: reasons})
		if err != nil {
			return err
		}
		text := string(data)
		if text == last {
			return nil
		}
		last = text
		lines++
		_, err = fmt.Fprintln(errOut, "startup "+text)
		return err
	}
	for {
		if err := report(); err != nil {
			cancel()
			return errors.Join(err, <-done)
		}
		select {
		case err := <-done:
			return err
		case <-ticker.C:
		}
	}
}

func bounded(text string, limit int) string {
	if len(text) > limit {
		return text[:limit] + "..."
	}
	return text
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
