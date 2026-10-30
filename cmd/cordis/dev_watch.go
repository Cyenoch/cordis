package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

type devInput struct {
	generation uint64
	digest     string
	err        error
	plan       *devPlan
}

type devWatcher struct {
	mu       sync.Mutex
	load     func() (*devPlan, error)
	plan     *devPlan
	excluded map[string]bool
	input    devInput
	changed  chan struct{}
	done     chan struct{}
}

func newDevWatcher(load func() (*devPlan, error)) (*devWatcher, error) {
	plan, err := load()
	if plan == nil {
		return nil, err
	}
	w := &devWatcher{load: load, plan: plan, excluded: map[string]bool{},
		changed: make(chan struct{}, 1), done: make(chan struct{})}
	return w, w.checkTemporaryRoot()
}

func (w *devWatcher) refresh() devInput {
	// Serialize scans so an earlier slow scan cannot overwrite newer evidence.
	w.mu.Lock()
	defer w.mu.Unlock()
	plan, planErr := w.load()
	if plan != nil {
		if err := checkDevTemporaryRoots(plan.roots); err != nil {
			planErr = errors.Join(planErr, err)
		} else {
			w.plan = plan
		}
	}
	digest, scanErr := w.fingerprint()
	err := errors.Join(planErr, scanErr)
	if w.input.generation == 0 || digest != w.input.digest || errorText(err) != errorText(w.input.err) {
		w.input = devInput{generation: w.input.generation + 1, digest: digest, err: err, plan: plan}
		select {
		case w.changed <- struct{}{}:
		default:
		}
	}
	return w.input
}

func errorText(err error) string {
	if err != nil {
		return err.Error()
	}
	return ""
}

func (w *devWatcher) watch(ctx context.Context) {
	defer close(w.done)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.refresh()
		}
	}
}

func (w *devWatcher) fingerprint() (string, error) {
	sum := sha256.New()
	sum.Write([]byte(w.plan.digest))
	seen := map[string]bool{}
	for _, root := range slices.Concat(w.plan.roots, slices.Sorted(maps.Keys(w.plan.explicit))) {
		explicit := w.plan.explicit[root]
		root, err := filepath.EvalSymlinks(root)
		if err != nil {
			return "", fmt.Errorf("watch input: %w", err)
		}
		err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				if errors.Is(err, fs.ErrNotExist) && path != root {
					return nil
				}
				return err
			}
			name := entry.Name()
			if w.excluded[path] || w.plan.outputs[path] || !explicit && (name == ".git" || strings.HasPrefix(name, ".cordis-") ||
				strings.HasPrefix(name, ".#") || strings.HasSuffix(name, "~") || strings.HasSuffix(name, ".swp")) {
				if entry.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || seen[path] {
				return nil
			}
			seen[path] = true
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() {
				return fmt.Errorf("watch input is not a regular file: %s", path)
			}
			file, err := os.Open(path)
			if err != nil {
				return err
			}
			fmt.Fprintf(sum, "%d:%s:%d:%d:", len(path), path, info.Size(), info.ModTime().UnixNano())
			_, readErr := io.Copy(sum, file)
			return errors.Join(readErr, file.Close())
		})
		if err != nil {
			return "", fmt.Errorf("watch %s: %w", root, err)
		}
	}
	return hex.EncodeToString(sum.Sum(nil)), nil
}

func (w *devWatcher) checkTemporaryRoot() error {
	return checkDevTemporaryRoots(w.plan.roots)
}

func checkDevTemporaryRoots(roots []string) error {
	temporary, err := filepath.EvalSymlinks(os.TempDir())
	if err != nil {
		return err
	}
	for _, root := range roots {
		info, err := os.Stat(root)
		if err != nil || !info.IsDir() {
			continue
		}
		root, err = filepath.EvalSymlinks(root)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, temporary)
		if err == nil && (relative == "." || filepath.IsLocal(relative)) {
			return fmt.Errorf("dev temporary root %s is inside watched directory %s; choose an external TMPDIR/TMP directory", temporary, root)
		}
	}
	return nil
}
