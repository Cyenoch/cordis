package sdk_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/Cyenoch/cordis/contract"
	"github.com/Cyenoch/cordis/sdk"
	"github.com/Cyenoch/cordis/wire"
)

func TestFailedStartReleasesBuiltinOnlyUnderHostShutdownParent(t *testing.T) {
	for _, outcome := range []string{"failed", "canceled"} {
		t.Run(outcome, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				type parentKey struct{}
				left, right := net.Pipe()
				builtin := contract.Requirement{Name: "builtin.resource", Major: 1}
				var acquired, released atomic.Int32
				started, returned := make(chan struct{}), make(chan struct{})
				cleanupResult := make(chan error, 1)
				definition := contract.Definition{
					ID: "failed-start", Requires: []contract.Requirement{builtin},
					Start: func(ctx context.Context, scope contract.Scope) error {
						defer close(returned)
						output, err := scope.Call(ctx, builtin, json.RawMessage(`{"operation":"acquire"}`))
						if err != nil {
							return err
						}
						if string(output) != `{"resource":"owned"}` {
							return fmt.Errorf("acquire returned %s", output)
						}
						if err := scope.Defer(func(cleanupCtx context.Context) error {
							output, err := scope.Call(cleanupCtx, builtin, json.RawMessage(`{"operation":"release","resource":"owned"}`))
							if err == nil && string(output) != `{"released":true}` {
								err = fmt.Errorf("release returned %s", output)
							}
							cleanupResult <- err
							return err
						}); err != nil {
							return err
						}
						close(started)
						if outcome == "canceled" {
							<-ctx.Done()
							return ctx.Err()
						}
						return contract.Fail(contract.PluginFailed, "start rejected after acquisition")
					},
				}
				serveDone := make(chan error, 1)
				go func() { serveDone <- sdk.ServeTransport(t.Context(), definition, right) }()
				host, err := wire.New(wire.Options{
					Transport: left, Identity: contract.Identity{Activation: "failed-start"},
					Limits: wire.Limits{Grace: time.Minute},
					Handler: func(ctx context.Context, method string, input json.RawMessage) (json.RawMessage, error) {
						if method != "call" {
							return nil, fmt.Errorf("unexpected guest method %q", method)
						}
						var call contract.ServiceCall
						if err := contract.Decode(input, &call); err != nil {
							return nil, err
						}
						if call.Required != builtin || wire.RequestID(ctx) == "" {
							return nil, errors.New("builtin call lost requirement or request context")
						}
						var operation struct {
							Operation string `json:"operation"`
							Resource  string `json:"resource,omitempty"`
						}
						if err := contract.Decode(call.Input, &operation); err != nil {
							return nil, err
						}
						switch operation.Operation {
						case "acquire":
							if ctx.Value(parentKey{}) != "initialize" || ctx.Err() != nil {
								return nil, errors.New("acquire was not bound to initialize")
							}
							acquired.Add(1)
							return json.RawMessage(`{"resource":"owned"}`), nil
						case "release":
							// Only the actual shutdown parent carries this host-local
							// value. Reusing initialize or a detached context cannot pass.
							if ctx.Value(parentKey{}) != "shutdown" || ctx.Err() != nil {
								return nil, errors.New("release did not inherit live shutdown parent")
							}
							if operation.Resource != "owned" || acquired.Load() != 1 || released.Add(1) != 1 {
								return nil, errors.New("resource was missing or released more than once")
							}
							return json.RawMessage(`{"released":true}`), nil
						default:
							return nil, fmt.Errorf("unknown operation %q", operation.Operation)
						}
					},
				})
				if err != nil {
					t.Fatal(err)
				}
				defer func() {
					_ = host.Close()
					host.Wait()
					if err := <-serveDone; err != nil && !errors.Is(err, net.ErrClosed) && !errors.Is(err, io.EOF) {
						t.Errorf("SDK exit: %v", err)
					}
				}()
				input, err := json.Marshal(contract.Initialize{
					Identity: contract.Identity{Activation: "failed-start"}, Protocol: contract.Protocol{Major: 1},
					Definition: definition.ID, Config: json.RawMessage(`{}`),
				})
				if err != nil {
					t.Fatal(err)
				}
				initCtx, cancelInit := context.WithCancel(context.WithValue(t.Context(), parentKey{}, "initialize"))
				defer cancelInit()
				initialized := make(chan error, 1)
				go func() {
					_, err := host.Call(initCtx, "initialize", input)
					initialized <- err
				}()
				<-started
				if outcome == "canceled" {
					cancelInit()
				}
				initErr := <-initialized
				if outcome == "canceled" {
					if !errors.Is(initErr, context.Canceled) {
						t.Fatalf("initialize cancellation = %v", initErr)
					}
				} else {
					var coded *contract.Error
					if !errors.As(initErr, &coded) || coded.Code != contract.PluginFailed {
						t.Fatalf("failed initialize = %v", initErr)
					}
				}
				<-returned
				synctest.Wait()
				if acquired.Load() != 1 || released.Load() != 0 {
					t.Fatalf("before host shutdown: acquired=%d released=%d", acquired.Load(), released.Load())
				}
				select {
				case err := <-cleanupResult:
					t.Fatalf("failed Start ran cleanup before shutdown: %v", err)
				default:
				}
				shutdownCtx, cancelShutdown := context.WithTimeout(context.WithValue(t.Context(), parentKey{}, "shutdown"), time.Second)
				defer cancelShutdown()
				if _, err := host.Call(shutdownCtx, "shutdown", json.RawMessage(`{}`)); err != nil {
					t.Fatalf("host shutdown could not release acquired builtin: %v", err)
				}
				if err := <-cleanupResult; err != nil {
					t.Fatalf("deferred builtin release: %v", err)
				}
				if released.Load() != 1 {
					t.Fatalf("shutdown released builtin %d times", released.Load())
				}
				if _, err := host.Call(shutdownCtx, "shutdown", json.RawMessage(`{}`)); err == nil {
					t.Fatal("completed shutdown kept accepting requests")
				}
				if released.Load() != 1 {
					t.Fatal("repeated shutdown released builtin twice")
				}
			})
		})
	}
}
