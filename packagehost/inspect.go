package packagehost

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"
)

// Inspection adds the verified archive's complete file listing to its package
// metadata. The listing does not grant trust or persist in the installed catalog.
type Inspection struct {
	Package
	Files []IntegrityEntry `json:"files"`
}

// Inspect validates an archive's format, manifest and complete integrity table
// without granting trust, installing content or executing code. Its digest is
// suitable for an explicit exact-digest development authorization.
func Inspect(ctx context.Context, archive string) (p Inspection, err error) {
	dir, err := os.MkdirTemp("", "cordis-inspect-")
	if err != nil {
		return p, err
	}
	defer func() { err = errors.Join(err, os.RemoveAll(dir)) }()
	limits := DefaultLimits()
	if err = extractZIP(ctx, archive, dir, limits); err != nil {
		return p, err
	}
	p.Package, _, _, err = inspect(ctx, dir, archive, false, limits)
	p.Directory = ""
	if err != nil {
		return p, err
	}
	p.Files, err = hashDirectory(ctx, dir, limits)
	if err != nil {
		return p, err
	}
	for _, name := range []string{IntegrityFile, SignatureFile} {
		raw, readErr := readMetadata(dir, name)
		if name == SignatureFile && errors.Is(readErr, os.ErrNotExist) {
			continue
		}
		if readErr != nil {
			return p, readErr
		}
		p.Files = append(p.Files, IntegrityEntry{Path: name, Size: uint64(len(raw)), SHA256: hashBytes(raw)})
	}
	slices.SortFunc(p.Files, func(a, b IntegrityEntry) int { return strings.Compare(a.Path, b.Path) })
	return p, err
}
