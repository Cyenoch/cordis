package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Cyenoch/cordis/packagehost"
)

type devInput struct {
	generation uint64
	digest     string
	err        error
}

type devWatcher struct {
	mu         sync.Mutex
	source     string
	roots      []string
	excluded   map[string]bool
	artifacts  map[string]bool
	configFile string
	input      devInput
	changed    chan struct{}
	done       chan struct{}
}

func newDevWatcher(source, configFile string, extra []string) (*devWatcher, error) {
	module := source
	for dir := source; ; dir = filepath.Dir(dir) {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			module = dir
			break
		} else if !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
		if parent := filepath.Dir(dir); parent == dir {
			break
		}
	}
	roots := append([]string{module}, extra...)
	if configFile != "" {
		roots = append(roots, configFile)
	}
	for n, root := range roots {
		absolute, err := filepath.Abs(root)
		if err != nil {
			return nil, err
		}
		roots[n] = absolute
	}
	if configFile != "" {
		configFile = roots[len(roots)-1]
	}
	return &devWatcher{source: source, roots: roots, configFile: configFile,
		excluded: map[string]bool{
			filepath.Join(source, "cordis_gen.go"):                 true,
			filepath.Join(source, packagehost.BuildDescriptorFile): true,
		}, changed: make(chan struct{}, 1), done: make(chan struct{})}, nil
}

func (w *devWatcher) refresh() devInput {
	// Serialize scans so an earlier slow scan cannot overwrite newer evidence.
	w.mu.Lock()
	defer w.mu.Unlock()
	if author, err := packagehost.LoadAuthor(w.source); err == nil {
		w.artifacts = make(map[string]bool, len(author.Manifest.Runtime.Artifacts))
		for _, artifact := range author.Manifest.Runtime.Artifacts {
			w.artifacts[filepath.Join(w.source, filepath.FromSlash(artifact.Path))] = true
		}
	}
	digest, err := w.fingerprint()
	if w.input.generation == 0 || digest != w.input.digest || errorText(err) != errorText(w.input.err) {
		w.input = devInput{generation: w.input.generation + 1, digest: digest, err: err}
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
	seen := map[string]bool{}
	for _, root := range w.roots {
		configFile := root == w.configFile
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
			if !configFile && (name == ".git" || w.excluded[path] || w.artifacts[path] || strings.HasPrefix(name, ".cordis-") ||
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
			fmt.Fprintf(sum, "%d:%s:%d:", len(path), path, info.Size())
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
	temporary, err := filepath.EvalSymlinks(os.TempDir())
	if err != nil {
		return err
	}
	for _, root := range w.roots {
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
