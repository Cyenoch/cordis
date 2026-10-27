// Package process owns exactly one activation's operating-system process and pipes.
package process

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/Cyenoch/cordis/contract"
	"github.com/Cyenoch/cordis/wire"
)

// Options never inherit the parent's environment. Environment contains only
// values explicitly selected by the host. Lifetime is independent of Start ctx.
type Options struct {
	Environment    map[string]string
	Lifetime       context.Context
	Limits         wire.Limits
	Grace          time.Duration
	StderrBytes    int64
	CopyExecutable bool
}

type Session struct {
	endpoint     contract.Session
	stop         chan struct{}
	done         chan struct{}
	once         sync.Once
	mu           sync.Mutex
	err          error
	expectedStop atomic.Bool
}

func (s *Session) Call(ctx context.Context, method string, input json.RawMessage) (json.RawMessage, error) {
	if method == "shutdown" || method == "plugin.shutdown" {
		s.expectedStop.Store(true)
	}
	return s.endpoint.Call(ctx, method, input)
}
func (s *Session) Done() <-chan struct{} { return s.done }
func (s *Session) Err() error            { s.mu.Lock(); defer s.mu.Unlock(); return s.err }
func (s *Session) Close() error {
	s.once.Do(func() { close(s.stop) })
	<-s.done
	return s.Err()
}

// Artifact resolves a regular package file without allowing a path escape.
func Artifact(directory, artifact string) (string, error) {
	if directory == "" || artifact == "" {
		return "", errors.New("package directory and artifact are required")
	}
	root, err := filepath.Abs(directory)
	if err != nil {
		return "", fmt.Errorf("resolve package directory: %w", err)
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("resolve package directory: %w", err)
	}
	path := artifact
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("resolve artifact: %w", err)
	}
	rel, err := filepath.Rel(root, path)
	if err != nil || !filepath.IsLocal(rel) {
		return "", errors.New("artifact escapes package directory")
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("stat artifact: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("artifact must be a regular file")
	}
	return path, nil
}

// Executable accepts an exact absolute path, never PATH lookup or shell syntax.
func Executable(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", errors.New("executable path must be absolute")
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("stat executable: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("executable must be a regular file")
	}
	return filepath.Clean(path), nil
}

func Start(ctx context.Context, executable string, args []string, launch contract.Launch, options Options) (contract.Session, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if launch.Identity.Activation == "" || launch.Handle == nil {
		return nil, errors.New("activation identity and host handler are required")
	}
	path, err := Executable(executable)
	if err != nil {
		return nil, err
	}
	grace := options.Grace
	if launch.Grace != 0 {
		grace = launch.Grace
	}
	if grace == 0 {
		grace = time.Second
	}
	if grace < 0 || grace > 30*time.Second {
		return nil, errors.New("shutdown grace must be between zero and 30 seconds")
	}
	stderrLimit := options.StderrBytes
	if stderrLimit == 0 {
		stderrLimit = 1 << 20
	}
	if stderrLimit < 0 {
		return nil, errors.New("stderr byte limit must be positive")
	}
	environment := make([]string, 0, len(options.Environment))
	for key, value := range options.Environment {
		if key == "" || strings.ContainsAny(key, "=\x00") || strings.ContainsRune(value, '\x00') {
			return nil, errors.New("invalid explicit environment entry")
		}
		environment = append(environment, key+"="+value)
	}
	sort.Strings(environment)
	cwd, err := os.MkdirTemp("", "cordis-activation-")
	if err != nil {
		return nil, fmt.Errorf("create private working directory: %w", err)
	}
	cleanupDirectory := func() error { return removeActivationDirectory(cwd) }
	if options.CopyExecutable {
		source, openErr := os.Open(path)
		if openErr != nil {
			return nil, errors.Join(openErr, cleanupDirectory())
		}
		targetPath := filepath.Join(cwd, "plugin")
		if runtime.GOOS == "windows" {
			targetPath += ".exe"
		}
		target, createErr := os.OpenFile(targetPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0700)
		if createErr != nil {
			return nil, errors.Join(createErr, source.Close(), cleanupDirectory())
		}
		_, copyErr := io.Copy(target, source)
		copyErr = errors.Join(copyErr, source.Close(), target.Close())
		if copyErr != nil {
			return nil, errors.Join(copyErr, cleanupDirectory())
		}
		if chmodErr := os.Chmod(targetPath, 0700); chmodErr != nil {
			return nil, errors.Join(chmodErr, cleanupDirectory())
		}
		path = targetPath
	}
	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		return nil, errors.Join(err, cleanupDirectory())
	}
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		return nil, errors.Join(err, stdinR.Close(), stdinW.Close(), cleanupDirectory())
	}
	stderrR, stderrW, err := os.Pipe()
	if err != nil {
		return nil, errors.Join(err, stdinR.Close(), stdinW.Close(), stdoutR.Close(), stdoutW.Close(), cleanupDirectory())
	}
	allPipes := []*os.File{stdinR, stdinW, stdoutR, stdoutW, stderrR, stderrW}
	cleanupPipes := func() error {
		var result error
		for _, pipe := range allPipes {
			if e := pipe.Close(); e != nil && !errors.Is(e, os.ErrClosed) {
				result = errors.Join(result, e)
			}
		}
		return result
	}
	cmd := exec.Command(path, args...)
	cmd.Dir, cmd.Env = cwd, environment
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdinR, stdoutW, stderrW
	tree, err := startTree(cmd)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("start activation: %w", err), cleanupPipes(), cleanupDirectory())
	}
	// The child owns duplicated handles; parent copies must not hold pipes open.
	parentCloseErr := errors.Join(stdinR.Close(), stdoutW.Close(), stderrW.Close())
	transport := &wire.Transport{Reader: stdoutR, Writer: stdinW, Closers: []io.Closer{stdoutR, stdinW}}
	var endpoint contract.Session
	if launch.OpenSession != nil {
		endpoint, err = launch.OpenSession(transport, launch.Identity, launch.Handle)
	} else {
		endpoint, err = wire.New(wire.Options{Transport: transport, Identity: launch.Identity, Handler: wire.Handler(launch.Handle), Limits: options.Limits})
	}
	if err == nil && endpoint == nil {
		err = errors.New("session factory returned a nil session")
	}
	if err != nil {
		killErr := tree.kill()
		waitErr := cmd.Wait()
		return nil, errors.Join(err, killErr, waitErr, tree.close(), cleanupPipes(), cleanupDirectory())
	}
	s := &Session{endpoint: endpoint, stop: make(chan struct{}), done: make(chan struct{})}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	stderrDone := make(chan error, 1)
	stderrExceeded := make(chan struct{})
	var exceeded atomic.Bool
	go func() {
		var total int64
		var buffer [4096]byte
		for {
			n, readErr := stderrR.Read(buffer[:])
			total += int64(n)
			if total > stderrLimit && exceeded.CompareAndSwap(false, true) {
				close(stderrExceeded)
			}
			if readErr != nil {
				if errors.Is(readErr, io.EOF) || errors.Is(readErr, os.ErrClosed) {
					readErr = nil
				}
				stderrDone <- readErr
				return
			}
		}
	}()
	go func() {
		var reason, waitErr error
		var lifetimeDone <-chan struct{}
		if options.Lifetime != nil {
			lifetimeDone = options.Lifetime.Done()
		}
		requested := false
		reaped := false
		select {
		case <-s.stop:
			requested = true
		case <-endpoint.Done():
			reason = endpoint.Err()
		case waitErr = <-exited:
			reaped = true
		case <-stderrExceeded:
			reason = errors.New("activation stderr byte limit exceeded")
		case <-lifetimeDone:
			reason = options.Lifetime.Err()
		}
		if reaped && s.expectedStop.Load() {
			timer := time.NewTimer(grace)
			select {
			case <-endpoint.Done():
				reason = endpoint.Err()
			case <-timer.C:
			case <-s.stop:
				requested = true
			}
			timer.Stop()
		}
		// Windows synchronous pipe reads can hold Close until the child exits.
		// Start closure before termination, but never wait on it before killing.
		type closeResult struct{ transport, endpoint error }
		closed := make(chan closeResult, 1)
		go func() { closed <- closeResult{transport: transport.Close(), endpoint: endpoint.Close()} }()
		// Closing transport unblocks protocol I/O; signal the whole managed tree.
		terminateErr := tree.terminate()
		if !reaped {
			timer := time.NewTimer(grace)
			select {
			case waitErr = <-exited:
			case <-timer.C:
				forcedErr := tree.kill()
				terminateErr = errors.Join(terminateErr, forcedErr)
				waitErr = <-exited // Always reap: never leave an operating-system zombie.
			}
			timer.Stop()
		}
		// Also remove descendants after a leader exits while holding inherited fds.
		killErr := tree.kill()
		// A just-exited Darwin leader can race SIGTERM with reaping. The final
		// kill is authoritative: retain permission failures unless it succeeds.
		if errors.Is(terminateErr, syscall.EPERM) && killErr == nil {
			terminateErr = nil
		}
		treeErr := tree.close()
		closeErrors := <-closed
		pipeErr := cleanupPipes()
		stderrErr := <-stderrDone
		// Endpoint handlers are host-owned and may be non-cooperative. Done means
		// process+pipe reaping, not a false promise to join arbitrary host code.
		if errors.Is(reason, io.EOF) {
			reason = nil
		}
		if !requested && !s.expectedStop.Load() && reason == nil && waitErr == nil {
			reason = contract.Fail(contract.PluginFailed, "activation exited without shutdown")
		}
		if requested || s.expectedStop.Load() {
			var exited *exec.ExitError
			if errors.As(waitErr, &exited) && tree.expectedExit(exited) {
				waitErr = nil
			}
		}
		s.mu.Lock()
		s.err = errors.Join(reason, waitErr, parentCloseErr, closeErrors.transport, closeErrors.endpoint, terminateErr, killErr, treeErr, pipeErr, stderrErr, cleanupDirectory())
		s.mu.Unlock()
		close(s.done)
	}()
	// Cancellation before ownership transfer must not strand a launched process.
	if err := ctx.Err(); err != nil {
		return nil, errors.Join(err, s.Close())
	}
	return s, nil
}
