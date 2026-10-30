package packagehost

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/Cyenoch/cordis/contract"
)

// releaseFiles describes content covered by integrity, excluding integrity and
// signature themselves. Multiple platforms may share one declared artifact.
func releaseFiles(m contract.Manifest) ([]string, error) {
	files := []string{ManifestFile}
	for _, artifact := range m.Runtime.Artifacts {
		if err := validateBuildPath(artifact.Path); err != nil {
			return nil, fmt.Errorf("artifact %q: %w", artifact.Path, err)
		}
		if !slices.Contains(files, artifact.Path) {
			files = append(files, artifact.Path)
		}
	}
	for _, resource := range m.Resources {
		if err := validateBuildPath(resource); err != nil {
			return nil, fmt.Errorf("resource %q: %w", resource, err)
		}
		files = append(files, resource)
	}
	if len(files) > DefaultLimits().Entries {
		return nil, fmt.Errorf("release file count quota exceeded")
	}
	reserved := []string{IntegrityFile, SignatureFile, BuildDescriptorFile}
	for _, name := range authorCandidates {
		if name != ManifestFile {
			reserved = append(reserved, name)
		}
	}
	if err := checkDirectories(slices.Concat(files, reserved), nil); err != nil {
		return nil, err
	}
	slices.Sort(files)
	return files, nil
}

func snapshotAuthor(ctx context.Context, source, dest string, limits Limits) error {
	a, err := LoadAuthor(source)
	if err != nil {
		return err
	}
	files := slices.Clone(a.sources)
	for _, artifact := range a.Manifest.Runtime.Artifacts {
		files = append(files, artifact.Path)
	}
	files = append(files, a.Manifest.Resources...)
	if len(a.Contracts) != 0 {
		descriptor, err := readBuildDescriptor(source)
		if err != nil {
			return fmt.Errorf("%s: author package requires an explicit %s: %w", a.Source, BuildDescriptorFile, err)
		}
		files = append(files, BuildDescriptorFile, descriptor.GeneratedOutput)
	}
	slices.Sort(files)
	return copySelectedFiles(ctx, source, dest, slices.Compact(files), limits)
}

func copySelectedFiles(ctx context.Context, source, dest string, files []string, limits Limits) (result error) {
	if len(files) > limits.Entries {
		return fmt.Errorf("snapshot entry count quota exceeded")
	}
	if err := checkDirectories(files, nil); err != nil {
		return err
	}
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("source root must be a real directory")
	}
	root, err := os.OpenRoot(source)
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, root.Close()) }()
	budget := byteBudget{remaining: limits.ExpandedBytes, file: limits.FileBytes}
	for _, name := range files {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := checkSourceParents(root, name); err != nil {
			return err
		}
		f, err := openRegular(root, name)
		if err != nil {
			return fmt.Errorf("snapshot %q: %w", name, err)
		}
		info, err := f.Stat()
		if err == nil {
			err = checkLinks(f, info)
		}
		if err == nil {
			err = copyFile(ctx, filepath.Join(dest, filepath.FromSlash(name)), f, &budget)
		}
		if err = errors.Join(err, f.Close()); err != nil {
			return fmt.Errorf("snapshot %q: %w", name, err)
		}
	}
	return nil
}
