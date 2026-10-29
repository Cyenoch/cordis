package packagehost

import (
	"context"
	"errors"
	"os"
)

// Inspect validates an archive's format, manifest and complete integrity table
// without granting trust, installing content or executing code. Its digest is
// suitable for an explicit exact-digest development authorization.
func Inspect(ctx context.Context, archive string) (p Package, err error) {
	dir, err := os.MkdirTemp("", "cordis-inspect-")
	if err != nil {
		return p, err
	}
	defer func() { err = errors.Join(err, os.RemoveAll(dir)) }()
	limits := DefaultLimits()
	if err = extractZIP(ctx, archive, dir, limits); err != nil {
		return p, err
	}
	p, _, _, err = inspect(ctx, dir, archive, false, limits)
	p.Directory = ""
	return p, err
}
