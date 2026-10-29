package packagehost

import (
	"context"
	"errors"
	"fmt"
	"os"
)

// Source identifies one archive or one explicit development package directory.
type Source struct {
	Path      string
	Directory bool
}

// Candidate is an inspection result, not an installation or trust grant.
// Err diagnoses this source, including conflicts with other candidates.
type Candidate struct {
	Package Package
	Source  Source
	Err     error
}

// Discover checks every source in input order. Source failures stay on the
// candidate; cancellation returns the partial results and the context error.
func Discover(ctx context.Context, sources []Source) ([]Candidate, error) {
	return discover(ctx, sources, DefaultLimits())
}

// Discover uses the Store's limits, without changing it or
// authorizing any candidate. Trust must still pass at Install and Snapshot.
func (s *Store) Discover(ctx context.Context, sources []Source) ([]Candidate, error) {
	s.mu.Lock()
	if err := s.check(); err != nil {
		s.mu.Unlock()
		return nil, err
	}
	limits := s.limits
	s.mu.Unlock()
	return discover(ctx, sources, limits)
}

func discover(ctx context.Context, sources []Source, limits Limits) ([]Candidate, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	results := make([]Candidate, 0, len(sources))
	groups := map[[2]string][]int{}
	for _, source := range sources {
		if err := ctx.Err(); err != nil {
			return results, err
		}
		p, err := inspectSource(ctx, source, limits)
		results = append(results, Candidate{Package: p, Source: source, Err: err})
		if canceled := ctx.Err(); canceled != nil {
			return results, canceled
		}
		if err == nil {
			key := [2]string{p.Ref.ID, p.Ref.Version}
			groups[key] = append(groups[key], len(results)-1)
		}
	}
	for _, indices := range groups {
		first := indices[0]
		other := -1
		for _, i := range indices[1:] {
			if results[i].Package.Ref.Digest != results[first].Package.Ref.Digest {
				other = i
				break
			}
		}
		if other < 0 {
			continue
		}
		for _, i := range indices {
			peer := first
			if results[i].Package.Ref.Digest == results[first].Package.Ref.Digest {
				peer = other
			}
			results[i].Err = fmt.Errorf("same-version content conflict: %s (%s) versus %s (%s)",
				results[i].Source.Path, results[i].Package.Ref.Digest,
				results[peer].Source.Path, results[peer].Package.Ref.Digest)
		}
	}
	return results, nil
}

func inspectSource(ctx context.Context, source Source, limits Limits) (p Package, err error) {
	dir, err := os.MkdirTemp("", "cordis-discover-")
	if err != nil {
		return p, err
	}
	defer func() { err = errors.Join(err, os.RemoveAll(dir)) }()
	if source.Directory {
		err = stageDirectory(ctx, source.Path, dir, limits)
	} else {
		err = extractZIP(ctx, source.Path, dir, limits)
	}
	if err != nil {
		return p, err
	}
	p, _, _, err = inspect(ctx, dir, source.Path, source.Directory, limits)
	p.Directory = ""
	return p, err
}
