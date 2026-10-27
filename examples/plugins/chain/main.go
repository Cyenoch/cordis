package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/Cyenoch/cordis/contract"
	"github.com/Cyenoch/cordis/sdk"
)

type request struct {
	Operation string `json:"operation"`
	Owner     string `json:"owner,omitempty"`
}

type response struct {
	Acquired int    `json:"acquired"`
	Released int    `json:"released"`
	Live     int    `json:"live"`
	Label    string `json:"label"`
}

type config struct {
	Label string `json:"label"`
}

// Set with -ldflags '-X main.rejectInitialization=true' when building the
// deliberately broken signed version used by lifecycle recovery conformance.
var rejectInitialization string

var (
	serviceA  = contract.Service{Name: "chain.a", Major: 1, Minor: 2}
	serviceB  = contract.Service{Name: "chain.b", Major: 1, Minor: 2}
	serviceC  = contract.Service{Name: "chain.c", Major: 1, Minor: 2}
	requiresA = contract.Requirement{Name: "chain.a", Major: 1, MinMinor: 1, MaxMinor: 3}
	requiresB = contract.Requirement{Name: "chain.b", Major: 1, MinMinor: 1, MaxMinor: 3}
)

func main() {
	if err := sdk.ServeDefinitions(context.Background(), provider(), middle(), consumer()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func call(ctx context.Context, scope contract.Scope, required contract.Requirement, input request) (response, error) {
	data, err := json.Marshal(input)
	if err != nil {
		return response{}, err
	}
	data, err = scope.Call(ctx, required, data)
	if err != nil {
		return response{}, err
	}
	var output response
	if err := contract.Decode(data, &output); err != nil {
		return response{}, err
	}
	return output, nil
}

func provider() contract.Definition {
	return contract.Definition{ID: "provider", Provides: []contract.Service{serviceA},
		Start: func(_ context.Context, scope contract.Scope) error {
			var settings config
			if err := contract.Decode(scope.Config(), &settings); err != nil {
				return err
			}
			if rejectInitialization == "true" {
				return contract.Fail(contract.PluginFailed, "intentional version-specific initialization failure")
			}
			if settings.Label == "fail-initialize" {
				return contract.Fail(contract.PluginFailed, "intentional failed initialization")
			}
			if err := scope.On("chain.handled", func(context.Context, json.RawMessage) (contract.EventResult, error) {
				return contract.EventResult{Handled: true, Value: json.RawMessage(`0`)}, nil
			}); err != nil {
				return err
			}
			if err := scope.On("chain.handled", func(context.Context, json.RawMessage) (contract.EventResult, error) {
				return contract.EventResult{}, contract.Fail(contract.PluginFailed, "second listener must not run in First")
			}); err != nil {
				return err
			}
			leases := map[string]bool{}
			var acquired, released int
			if err := scope.Defer(func(context.Context) error {
				if len(leases) != 0 || acquired != released {
					return fmt.Errorf("provider leaked resources: acquired=%d released=%d live=%d", acquired, released, len(leases))
				}
				return nil
			}); err != nil {
				return err
			}
			return sdk.Provide(scope, serviceA, func(ctx context.Context, input request) (response, error) {
				switch input.Operation {
				case "acquire":
					if input.Owner == "" || leases[input.Owner] {
						return response{}, errors.New("invalid or duplicate resource owner")
					}
					leases[input.Owner] = true
					acquired++
				case "release":
					if !leases[input.Owner] {
						return response{}, errors.New("resource not acquired")
					}
					delete(leases, input.Owner)
					released++
				case "stats":
				case "wait":
					client, ok := scope.(*sdk.Client)
					if !ok {
						return response{}, errors.New("external SDK client required")
					}
					task, err := client.BeginTask(scope.Context(), "blocked")
					if err != nil {
						return response{}, err
					}
					<-ctx.Done()
					// The canceled invocation's parent may already be retired on the
					// wire; task completion is an independent activation control call.
					finishCtx, cancel := context.WithTimeout(scope.Context(), 3*time.Second)
					defer cancel()
					if err := task.Finish(finishCtx, "completed", json.RawMessage(`{"canceled":true}`), ""); err != nil {
						return response{}, fmt.Errorf("acknowledge cancellation: %w", err)
					}
					return response{}, ctx.Err()
				case "crash":
					os.Exit(7)
				case "exit":
					os.Exit(0)
				default:
					return response{}, contract.Fail(contract.Invalid, "unknown operation")
				}
				return response{Acquired: acquired, Released: released, Live: len(leases), Label: settings.Label}, nil
			})
		}}
}

func middle() contract.Definition {
	return contract.Definition{ID: "middle", Requires: []contract.Requirement{requiresA}, Provides: []contract.Service{serviceB},
		Start: func(ctx context.Context, scope contract.Scope) error {
			owner := scope.Identity().Activation
			if _, err := call(ctx, scope, requiresA, request{Operation: "acquire", Owner: owner}); err != nil {
				return fmt.Errorf("middle acquire: %w", err)
			}
			leases := map[string]bool{}
			if err := scope.Defer(func(ctx context.Context) error {
				if len(leases) != 0 {
					return fmt.Errorf("middle still owns %d consumer resources", len(leases))
				}
				output, err := call(ctx, scope, requiresA, request{Operation: "release", Owner: owner})
				if err != nil {
					return fmt.Errorf("middle cleanup release: %w", err)
				}
				if output.Live != 0 || output.Acquired != output.Released {
					return fmt.Errorf("middle cleanup left provider resources: %+v", output)
				}
				return nil
			}); err != nil {
				return err
			}
			return sdk.Provide(scope, serviceB, func(ctx context.Context, input request) (response, error) {
				if input.Operation == "acquire" && (input.Owner == "" || leases[input.Owner]) {
					return response{}, errors.New("invalid or duplicate consumer resource")
				}
				if input.Operation == "release" && !leases[input.Owner] {
					return response{}, errors.New("consumer resource not acquired")
				}
				// Forward the inherited context unchanged. During C cleanup this must
				// retain the host's trusted C -> B -> A cleanup lineage.
				output, err := call(ctx, scope, requiresA, input)
				if err != nil {
					return response{}, fmt.Errorf("middle forward: %w", err)
				}
				switch input.Operation {
				case "acquire":
					leases[input.Owner] = true
				case "release":
					delete(leases, input.Owner)
				}
				return output, nil
			})
		}}
}

func consumer() contract.Definition {
	return contract.Definition{ID: "consumer", Requires: []contract.Requirement{requiresB}, Provides: []contract.Service{serviceC},
		Start: func(ctx context.Context, scope contract.Scope) error {
			owner := scope.Identity().Activation
			if _, err := call(ctx, scope, requiresB, request{Operation: "acquire", Owner: owner}); err != nil {
				return fmt.Errorf("consumer acquire: %w", err)
			}
			if err := scope.Defer(func(ctx context.Context) error {
				output, err := call(ctx, scope, requiresB, request{Operation: "release", Owner: owner})
				if err != nil {
					return fmt.Errorf("consumer transitive cleanup: %w", err)
				}
				if output.Live != 1 || output.Released == 0 {
					return fmt.Errorf("consumer cleanup did not release through B to A: %+v", output)
				}
				return nil
			}); err != nil {
				return err
			}
			return sdk.Provide(scope, serviceC, func(ctx context.Context, input request) (response, error) {
				return call(ctx, scope, requiresB, input)
			})
		}}
}
