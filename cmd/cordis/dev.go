package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"time"

	cordis "github.com/Cyenoch/cordis"
	"github.com/Cyenoch/cordis/contract"
	"github.com/Cyenoch/cordis/packagehost"
)

type devCandidate struct {
	host       *cordis.Host
	directory  string
	generation uint64
}

func (c *devCandidate) close(timeout time.Duration) (bool, error) {
	err := closeHost(c.host, timeout)
	select {
	case <-c.host.Done():
		return true, errors.Join(err, os.RemoveAll(c.directory))
	default:
		return false, fmt.Errorf("host cleanup incomplete; retained %s: %w", c.directory, err)
	}
}

type devOptions struct {
	source          string
	workspace       string
	goPackage       string
	definition      string
	configuration   dataInput
	startupTimeout  time.Duration
	shutdownTimeout time.Duration
}

type devUpdate struct {
	Event      string                  `json:"event"`
	Generation uint64                  `json:"generation"`
	Error      string                  `json:"error,omitempty"`
	Instances  []contract.InstanceInfo `json:"instances,omitempty"`
}

func devCommand(ctx context.Context, flags *flag.FlagSet, parse func() (bool, error), in io.Reader, out, errOut io.Writer) (result error) {
	unsigned := flags.Bool("dev-unsigned", false, "explicitly authorize each exact local development digest")
	var options devOptions
	var extra stringFlags
	flags.StringVar(&options.goPackage, "go-package", "", "explicit Go package to build")
	flags.StringVar(&options.definition, "definition", "", "single entry definition (default: package default)")
	flags.Var(&extra, "watch", "additional external input directory; repeat for multiple directories")
	flags.DurationVar(&options.startupTimeout, "startup-timeout", 30*time.Second, "maximum wait for each candidate's startup")
	flags.DurationVar(&options.shutdownTimeout, "shutdown-timeout", 5*time.Second, "maximum wait for complete old-host cleanup")
	options.configuration.register(flags, "config")
	if done, err := parse(); done {
		return err
	}
	if flags.NArg() != 1 || !*unsigned || options.goPackage == "" || options.startupTimeout <= 0 || options.shutdownTimeout <= 0 {
		return &usageError{message: commandUsage["dev"]}
	}
	seen := map[string]bool{}
	flags.Visit(func(f *flag.Flag) { seen[f.Name] = true })
	if err := options.configuration.check("config", seen); err != nil {
		return err
	}
	if options.configuration.file == "-" {
		return &usageError{message: "dev requires inline configuration or a watched config file; stdin cannot be reloaded"}
	}
	source, err := filepath.Abs(flags.Arg(0))
	if err != nil {
		return err
	}
	options.source, err = filepath.EvalSymlinks(source)
	if err != nil {
		return err
	}
	if err := checkDevTarget(options.source); err != nil {
		return err
	}
	watcher, err := newDevWatcher(options.source, options.configuration.file, extra)
	if err != nil {
		return err
	}
	if err := watcher.checkTemporaryRoot(); err != nil {
		return err
	}
	options.workspace, err = os.MkdirTemp("", "cordis-dev-")
	if err != nil {
		return err
	}
	removeWorkspace := true
	defer func() {
		if removeWorkspace {
			result = errors.Join(result, os.RemoveAll(options.workspace))
		}
	}()
	workspace, err := filepath.EvalSymlinks(options.workspace)
	if err != nil {
		return err
	}
	options.workspace = workspace
	if relative, err := filepath.Rel(options.source, options.workspace); err != nil || filepath.IsLocal(relative) {
		return fmt.Errorf("dev temporary directory must be outside author source; choose an external temporary directory")
	}
	watcher.excluded[options.workspace] = true
	watchCtx, stop := context.WithCancel(ctx)
	go watcher.watch(watchCtx)
	defer func() { stop(); <-watcher.done }()
	logger := slog.New(slog.NewJSONHandler(errOut, nil))
	var current *devCandidate
	defer func() {
		if current != nil {
			complete, err := current.close(options.shutdownTimeout)
			removeWorkspace = removeWorkspace && complete
			result = errors.Join(result, err)
		}
	}()
	again := true
	var attempted uint64
	for {
		if !again {
			select {
			case <-ctx.Done():
				return nil
			case <-watcher.changed:
			}
		}
		again = false
		if ctx.Err() != nil {
			return nil
		}
		input := watcher.refresh()
		if input.generation == attempted {
			continue
		}
		attempted = input.generation
		if err := writeJSON(out, devUpdate{Event: "build", Generation: input.generation}); err != nil {
			return err
		}
		var candidate *devCandidate
		err = input.err
		if err == nil {
			candidate, err = prepareDevCandidate(ctx, options, input.generation, in, logger)
		}
		if err != nil {
			if candidate != nil {
				removeWorkspace = false
				return err
			}
			if ctx.Err() != nil {
				return nil
			}
			if err := writeJSON(out, devUpdate{Event: "build_failed", Generation: input.generation, Error: err.Error()}); err != nil {
				return err
			}
			continue
		}
		discard := func() error {
			complete, err := candidate.close(options.shutdownTimeout)
			removeWorkspace = removeWorkspace && complete
			return err
		}
		if ctx.Err() != nil {
			return discard()
		}
		if watcher.refresh().generation != input.generation {
			if err := discard(); err != nil {
				return err
			}
			again = true
			continue
		}
		if current != nil {
			complete, err := current.close(options.shutdownTimeout)
			if err != nil {
				logger.Error("previous development host close", "error", err)
			}
			if !complete {
				return errors.Join(err, discard())
			}
			current = nil
		}
		if ctx.Err() != nil {
			return discard()
		}
		if watcher.refresh().generation != input.generation {
			if err := discard(); err != nil {
				return err
			}
			again = true
			continue
		}
		current = candidate
		again, err = runDevCandidate(ctx, options, current, watcher, out)
		if err != nil {
			return err
		}
	}
}

func checkDevTarget(source string) error {
	author, err := packagehost.LoadAuthor(source)
	if err != nil {
		return err
	}
	targets := author.Manifest.Runtime.Artifacts
	if len(targets) != 1 || targets[0].OS != runtime.GOOS || targets[0].Arch != runtime.GOARCH {
		return fmt.Errorf("dev requires exactly one native target matching %s/%s; use a single-target development declaration", runtime.GOOS, runtime.GOARCH)
	}
	return nil
}

func prepareDevCandidate(ctx context.Context, options devOptions, generation uint64, in io.Reader, logger *slog.Logger) (candidate *devCandidate, result error) {
	if err := checkDevTarget(options.source); err != nil {
		return nil, err
	}
	configuration, err := options.configuration.load("config", in)
	if err != nil {
		return nil, err
	}
	if _, err := packagehost.Build(ctx, options.source, packagehost.BuildOptions{
		GoPackage: options.goPackage, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
	}); err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp(options.workspace, "candidate-")
	if err != nil {
		return nil, err
	}
	defer func() {
		if result != nil {
			if candidate != nil {
				complete, closeErr := candidate.close(options.shutdownTimeout)
				result = errors.Join(result, closeErr)
				if complete {
					candidate = nil
				}
			} else {
				result = errors.Join(result, os.RemoveAll(dir))
			}
		}
	}()
	archive := filepath.Join(dir, "candidate.zip")
	if _, err := packagehost.Pack(ctx, options.source, archive); err != nil {
		return nil, err
	}
	pkg, err := packagehost.Inspect(ctx, archive)
	if err != nil {
		return nil, err
	}
	h, err := cordis.Open(cordis.Options{
		Directory: filepath.Join(dir, "store"), Logger: logger,
		Trust: packagehost.TrustPolicy{DevDigests: map[string]bool{pkg.Ref.Digest: true},
			Publishers: map[string][]string{pkg.Manifest.Publisher: {pkg.Ref.ID}}},
	})
	if err != nil {
		return nil, err
	}
	candidate = &devCandidate{host: h, directory: dir, generation: generation}
	if _, err := h.Install(ctx, archive); err != nil {
		return candidate, err
	}
	if err := h.Select(ctx, pkg.Ref); err != nil {
		return candidate, err
	}
	if _, err := h.Store().EnableEntry(ctx, contract.Entry{
		ID: "dev", Package: pkg.Ref.ID, Definition: options.definition, Config: configuration,
	}); err != nil {
		return candidate, err
	}
	return candidate, nil
}

func runDevCandidate(ctx context.Context, options devOptions, candidate *devCandidate, watcher *devWatcher, out io.Writer) (bool, error) {
	startup, cancel := context.WithTimeout(ctx, options.startupTimeout)
	defer cancel()
	finished := make(chan error, 1)
	go func() { _, err := candidate.host.Restore(startup); finished <- err }()
	changed := false
	var startupErr error
	for finished != nil {
		select {
		case startupErr = <-finished:
			finished = nil
		case <-watcher.changed:
			if watcher.refresh().generation != candidate.generation {
				changed = true
				cancel()
				startupErr = <-finished
				finished = nil
			}
		case <-ctx.Done():
			cancel()
			startupErr = <-finished
			finished = nil
		}
	}
	return changed, writeJSON(out, devUpdate{Event: "runtime", Generation: candidate.generation,
		Error: errorText(startupErr), Instances: candidate.host.Instances()})
}
