package hosting

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Cyenoch/cordis"
	"github.com/Cyenoch/cordis/contract"
	"github.com/Cyenoch/cordis/sdk"
)

var render = contract.NewChainKey[string, string]("test.render")

type next = func(context.Context, string) (string, error)

// middleware is a native definition that registers one middleware.
func middleware(fn func(ctx context.Context, in string, next next) (string, error)) sdk.Definition {
	return sdk.Define(cordis.Plugin[struct{}]{Name: "main", Start: func(ctx context.Context, s *cordis.Scope, _ struct{}) error {
		_, err := render.Use(ctx, s, fn)
		return err
	}})
}

func TestChainAcrossProcesses(t *testing.T) {
	f := newFixture(t)
	results := make(chan string, 1)
	failures := make(chan error, 1)
	// B, a native middleware, wraps what comes back from downstream.
	f.pack(plainManifest("test.b"), middleware(func(ctx context.Context, in string, next next) (string, error) {
		out, err := next(ctx, in)
		return "b(" + out + ")", err
	}))
	// C, another native plugin, starts an execution; the final step is its own.
	f.pack(plainManifest("test.c"), sdk.Define(cordis.Plugin[struct{}]{Name: "main", Start: func(_ context.Context, s *cordis.Scope, _ struct{}) error {
		s.Go(func(ctx context.Context) error {
			out, err := render.Do(ctx, s, "x", func(_ context.Context, in string) (string, error) {
				// Longer than the deadline of a plain request would allow
				// in production; here only that it is the final step.
				return "final:" + in, nil
			})
			if err != nil {
				failures <- err
				return nil
			}
			results <- out
			return nil
		})
		return nil
	}}))
	f.open()
	root := f.host.Scope()
	// A, in the host process, changes the input and wraps the result.
	if _, err := render.Use(f.ctx(), root, func(ctx context.Context, in string, next next) (string, error) {
		out, err := next(ctx, in+"+a")
		return "a(" + out + ")", err
	}); err != nil {
		t.Fatal(err)
	}
	must(t, f.enable(contract.Entry{ID: "b", Package: "test.b"}).Wait(f.ctx()))
	must(t, f.enable(contract.Entry{ID: "c", Package: "test.c"}).Wait(f.ctx()))
	select {
	case out := <-results:
		if out != "a(b(final:x+a))" {
			t.Fatalf("result = %q", out)
		}
	case err := <-failures:
		t.Fatal(err)
	case <-time.After(10 * time.Second):
		t.Fatal("the execution did not finish")
	}

	// From the host process the same chain runs through B as well.
	out, err := render.Do(f.ctx(), root, "y", func(_ context.Context, in string) (string, error) { return in, nil })
	if err != nil || out != "a(b(y+a))" {
		t.Fatalf("do = %q, %v", out, err)
	}
	// A stopped instance takes its middleware along.
	must(t, f.host.Disable(f.ctx(), "b"))
	out, err = render.Do(f.ctx(), root, "z", func(_ context.Context, in string) (string, error) { return in, nil })
	if err != nil || out != "a(z+a)" {
		t.Fatalf("do after the middleware's instance stopped = %q, %v", out, err)
	}
}

func TestChainMiddlewareMisbehaving(t *testing.T) {
	f := newFixture(t)
	late, finished := make(chan error, 1), make(chan struct{})
	f.pack(plainManifest("test.guard"), middleware(func(ctx context.Context, in string, next next) (string, error) {
		switch in {
		case "short":
			return "short-circuited", nil
		case "twice":
			out, err := next(ctx, in)
			if _, again := next(ctx, in); again == nil {
				return "", errors.New("a second next succeeded")
			}
			return out, err
		case "late":
			go func() {
				<-finished // the execution has ended for its starter
				_, err := next(context.Background(), in)
				late <- err
			}()
			return "returned early", nil
		case "wait":
			<-ctx.Done()
			return "", ctx.Err()
		}
		return next(ctx, in)
	}))
	f.open()
	root := f.host.Scope()
	must(t, f.enable(contract.Entry{ID: "guard", Package: "test.guard"}).Wait(f.ctx()))
	finals := 0
	final := func(_ context.Context, in string) (string, error) {
		finals++
		return "final:" + in, nil
	}
	if out, err := render.Do(f.ctx(), root, "short", final); err != nil || out != "short-circuited" || finals != 0 {
		t.Fatalf("short circuit = %q, %v, finals %d", out, err, finals)
	}
	if out, err := render.Do(f.ctx(), root, "twice", final); err != nil || out != "final:twice" || finals != 1 {
		t.Fatalf("next twice = %q, %v, finals %d", out, err, finals)
	}
	if out, err := render.Do(f.ctx(), root, "late", final); err != nil || out != "returned early" {
		t.Fatalf("late next = %q, %v", out, err)
	}
	close(finished)
	if err := <-late; err == nil || finals != 1 {
		t.Fatalf("a next after the middleware returned = %v, finals %d", err, finals)
	}
	// The starter's cancellation reaches the middleware.
	ctx, cancel := context.WithCancel(f.ctx())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	if _, err := render.Do(ctx, root, "wait", final); !errors.Is(err, context.Canceled) {
		t.Fatalf("a canceled execution = %v", err)
	}
	// The session is still good.
	if out, err := render.Do(f.ctx(), root, "ok", final); err != nil || out != "final:ok" || f.state("guard") != contract.Active {
		t.Fatalf("after the misbehavior: %q, %v, %s", out, err, f.state("guard"))
	}
}

func TestChainMiddlewareProcessGoesAway(t *testing.T) {
	f := newFixture(t)
	crash := make(chan struct{})
	f.pack(plainManifest("test.doomed"), sdk.Define(cordis.Plugin[struct{}]{Name: "main", Start: func(ctx context.Context, s *cordis.Scope, _ struct{}) error {
		s.Go(func(ctx context.Context) error {
			select {
			case <-crash:
				return errors.New("middleware process crashed")
			case <-ctx.Done():
				return nil
			}
		})
		_, err := render.Use(ctx, s, func(ctx context.Context, in string, next next) (string, error) {
			out, err := next(ctx, in)
			close(crash)
			<-ctx.Done() // never returns on its own
			return out, err
		})
		return err
	}}))
	f.open()
	must(t, f.enable(contract.Entry{ID: "doomed", Package: "test.doomed"}).Wait(f.ctx()))
	finals := 0
	_, err := render.Do(f.ctx(), f.host.Scope(), "x", func(_ context.Context, in string) (string, error) {
		finals++
		return in, nil
	})
	var located *cordis.HandlerError
	if !errors.As(err, &located) || located.Instance.Name != "doomed" || finals != 1 {
		t.Fatalf("an execution through a process that went away = %v, finals %d", err, finals)
	}
}

func TestChainsNeedAHostThatCarriesThem(t *testing.T) {
	f := newFixture(t)
	outcome := make(chan []string, 1)
	f.pack(echoManifest("test.plugin", true), sdk.Define(cordis.Plugin[struct{}]{Name: "main", Start: func(ctx context.Context, s *cordis.Scope, _ struct{}) error {
		var seen []string
		_, err := render.Use(ctx, s, func(ctx context.Context, in string, next next) (string, error) { return next(ctx, in) })
		seen = append(seen, "use: "+errorText(err))
		_, err = render.Do(ctx, s, "x", func(_ context.Context, in string) (string, error) { return in, nil })
		seen = append(seen, "do: "+errorText(err))
		outcome <- seen
		return f.echoPlugin("inner", "").Start(ctx, s, struct{}{})
	}}))
	f.open()
	previous := hostFeatures
	hostFeatures = slices.DeleteFunc(slices.Clone(previous), func(feature string) bool { return feature == "chains" })
	defer func() { hostFeatures = previous }()
	instance := f.enable(contract.Entry{ID: "plugin", Package: "test.plugin"})
	must(t, instance.Wait(f.ctx()))
	for _, line := range <-outcome {
		if !strings.Contains(line, "chains") || !strings.Contains(line, "incompatible") {
			t.Fatalf("without the feature: %q", line)
		}
	}
	// The rest of the plugin works.
	provider, err := echoKey.Get(f.host.Scope())
	must(t, err)
	if _, err := contract.Invoke[echoRequest, echoResponse](f.ctx(), provider, "echo", echoRequest{Text: "x"}); err != nil {
		t.Fatal(err)
	}
}

func errorText(err error) string {
	if err == nil {
		return "nil"
	}
	return err.Error()
}
