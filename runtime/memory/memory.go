// Package memory runs plugin definitions inside the host process over an
// in-memory session. It executes the real SDK runtime, protocol and codecs
// without building or starting a process, which is what tests want.
// Production hosts use runtime/native.
package memory

import (
	"context"
	"errors"
	"io"
	"sync"

	"github.com/Cyenoch/cordis/contract"
	"github.com/Cyenoch/cordis/sdk"
	"github.com/Cyenoch/cordis/wire"
)

// Runtime starts the definitions registered for the package of a launch.
type Runtime struct {
	mu       sync.Mutex
	packages map[string][]sdk.Definition
}

// New returns a runtime without packages.
func New() *Runtime { return &Runtime{packages: map[string][]sdk.Definition{}} }

// Register makes definitions what the package with this ID runs. It
// replaces an earlier registration; activations already running keep theirs.
func (r *Runtime) Register(packageID string, definitions ...sdk.Definition) {
	r.mu.Lock()
	r.packages[packageID] = definitions
	r.mu.Unlock()
}

func (r *Runtime) Start(ctx context.Context, launch contract.Launch) (contract.Session, error) {
	r.mu.Lock()
	definitions := r.packages[launch.Identity.Package]
	r.mu.Unlock()
	if len(definitions) == 0 {
		return nil, contract.Fail(contract.Invalid, "no in-memory definitions for package "+launch.Identity.Package)
	}
	guestIn, hostOut := io.Pipe()
	hostIn, guestOut := io.Pipe()
	guest := &wire.Transport{Reader: guestIn, Writer: guestOut, Closers: []io.Closer{guestIn, guestOut}}
	host := &wire.Transport{Reader: hostIn, Writer: hostOut, Closers: []io.Closer{hostIn, hostOut}}
	endpoint, err := wire.New(wire.Options{FrameBytes: launch.FrameBytes, Transport: host, Identity: launch.Identity, Handler: wire.Handler(launch.Handle), Stream: wire.StreamHandler(launch.Stream)})
	if err != nil {
		return nil, errors.Join(err, guest.Close(), host.Close())
	}
	s := &session{Endpoint: endpoint, served: make(chan struct{})}
	go func() {
		defer close(s.served)
		// The guest outlives the context of its start, like a process.
		s.guestErr = errors.Join(sdk.RunTransport(context.WithoutCancel(ctx), guest, definitions...), guest.Close())
	}()
	return s, nil
}

type session struct {
	*wire.Endpoint
	served   chan struct{}
	guestErr error
}

// Close ends the session and waits for the guest's cleanups, as closing a
// process session waits for the process.
func (s *session) Close() error {
	err := s.Endpoint.Close()
	<-s.served
	s.Endpoint.Wait()
	return err
}

// GuestErr is what the guest's Run returned. It is valid after Close.
func (s *session) GuestErr() error { return s.guestErr }

var _ contract.Runtime = (*Runtime)(nil)
