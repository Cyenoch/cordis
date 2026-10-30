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
	"slices"
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
	project         string
	extra           []string
	startupTimeout  time.Duration
	shutdownTimeout time.Duration
}

type devUpdate struct {
	Event      string                  `json:"event"`
	Generation uint64                  `json:"generation"`
	Error      string                  `json:"error,omitempty"`
	Instances  []contract.InstanceInfo `json:"instances,omitempty"`
}

func devCommand(ctx context.Context, flags *flag.FlagSet, parse func() (bool, error), out, errOut io.Writer) (result error) {
	unsigned := flags.Bool("dev-unsigned", false, "explicitly authorize each exact local development digest")
	var options devOptions
	var extra stringFlags
	flags.StringVar(&options.goPackage, "go-package", "", "explicit Go package to build")
	flags.StringVar(&options.definition, "definition", "", "single entry definition (default: package default)")
	flags.StringVar(&options.project, "project", "", "explicit JSON/TOML/YAML multi-package development project")
	flags.Var(&extra, "watch", "additional external input directory; repeat for multiple directories")
	flags.DurationVar(&options.startupTimeout, "startup-timeout", 30*time.Second, "maximum wait for each candidate's startup")
	flags.DurationVar(&options.shutdownTimeout, "shutdown-timeout", 5*time.Second, "maximum wait for complete old-host cleanup")
	options.configuration.register(flags, "config")
	if done, err := parse(); done {
		return err
	}
	if !*unsigned || options.startupTimeout <= 0 || options.shutdownTimeout <= 0 {
		return &usageError{message: commandUsage["dev"]}
	}
	seen := map[string]bool{}
	flags.Visit(func(f *flag.Flag) { seen[f.Name] = true })
	options.extra = extra
	if seen["project"] {
		if options.project == "" || flags.NArg() != 0 {
			return &usageError{message: commandUsage["dev"]}
		}
		for _, name := range []string{"go-package", "definition", "config", "config-file", "config-format"} {
			if seen[name] {
				return &usageError{message: "-project conflicts with -" + name}
			}
		}
		var err error
		options.project, err = filepath.Abs(options.project)
		if err != nil {
			return err
		}
	} else {
		if flags.NArg() != 1 || options.goPackage == "" {
			return &usageError{message: commandUsage["dev"]}
		}
		source, err := filepath.Abs(flags.Arg(0))
		if err != nil {
			return err
		}
		options.source, err = filepath.EvalSymlinks(source)
		if err != nil {
			return err
		}
	}
	if err := options.configuration.check("config", seen); err != nil {
		return err
	}
	if options.configuration.file == "-" {
		return &usageError{message: "dev requires inline configuration or a watched config file; stdin cannot be reloaded"}
	}
	watcher, err := newDevWatcher(options.plan)
	if err != nil {
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
			candidate, err = prepareDevCandidate(ctx, options, input, logger)
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

func prepareDevCandidate(ctx context.Context, options devOptions, input devInput, logger *slog.Logger) (candidate *devCandidate, result error) {
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
	trust := packagehost.TrustPolicy{DevDigests: map[string]bool{}, Publishers: map[string][]string{}}
	packages := map[string]packagehost.Package{}
	archives := []string{}
	for n, source := range input.plan.packages {
		if _, err := packagehost.Build(ctx, source.directory, packagehost.BuildOptions{
			GoPackage: source.goPackage, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
		}); err != nil {
			return nil, fmt.Errorf("build package %s: %w", source.id, err)
		}
		archive := filepath.Join(dir, fmt.Sprintf("package-%d.zip", n))
		pkg, err := packagehost.Pack(ctx, source.directory, archive)
		if err != nil {
			return nil, fmt.Errorf("pack package %s: %w", source.id, err)
		}
		if pkg.Ref.ID != source.id {
			return nil, fmt.Errorf("package identity changed during build: %s became %s", source.id, pkg.Ref.ID)
		}
		archives = append(archives, archive)
		packages[pkg.Ref.ID] = pkg
		trust.DevDigests[pkg.Ref.Digest] = true
		trust.Publishers[pkg.Manifest.Publisher] = append(trust.Publishers[pkg.Manifest.Publisher], pkg.Ref.ID)
	}
	h, err := cordis.Open(cordis.Options{
		Directory: filepath.Join(dir, "store"), Logger: logger, Trust: trust,
	})
	if err != nil {
		return nil, err
	}
	candidate = &devCandidate{host: h, directory: dir, generation: input.generation}
	for _, archive := range archives {
		if _, err := h.Install(ctx, archive); err != nil {
			return candidate, err
		}
	}
	if err := selectDevPackages(ctx, h, packages); err != nil {
		return candidate, err
	}
	if err := saveDevEntries(ctx, h.Store(), input.plan.entries); err != nil {
		return candidate, err
	}
	return candidate, nil
}

func selectDevPackages(ctx context.Context, h *cordis.Host, packages map[string]packagehost.Package) error {
	states := map[string]uint8{}
	var selectPackage func(string) error
	selectPackage = func(id string) error {
		if states[id] == 2 {
			return nil
		}
		if states[id] == 1 {
			return fmt.Errorf("dev package dependency cycle at %s", id)
		}
		pkg, exists := packages[id]
		if !exists {
			return fmt.Errorf("required package %s is absent from dev project", id)
		}
		states[id] = 1
		for _, dependency := range pkg.Manifest.Dependencies {
			if err := selectPackage(dependency.ID); err != nil {
				return err
			}
		}
		if err := h.Select(ctx, pkg.Ref); err != nil {
			return err
		}
		states[id] = 2
		return nil
	}
	var ids []string
	for id := range packages {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		if err := selectPackage(id); err != nil {
			return err
		}
	}
	return nil
}

func saveDevEntries(ctx context.Context, store *packagehost.Store, entries []contract.Entry) error {
	byID := map[string]contract.Entry{}
	for _, entry := range entries {
		byID[entry.ID] = entry
	}
	saved := map[string]bool{}
	var save func(string) error
	save = func(id string) error {
		if saved[id] {
			return nil
		}
		entry := byID[id]
		if entry.Parent != "" {
			if err := save(entry.Parent); err != nil {
				return err
			}
		}
		if _, err := store.EnableEntry(ctx, entry); err != nil {
			return err
		}
		saved[id] = true
		return nil
	}
	for _, entry := range entries {
		if err := save(entry.ID); err != nil {
			return err
		}
	}
	return nil
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
