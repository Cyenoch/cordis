// Package shadow declares what generated code could trip over: types named
// like its parameters, methods named like its helpers, and fields whose JSON
// form is easy to get wrong.
package shadow

import (
	"context"
	"iter"

	"github.com/Cyenoch/cordis"
)

type (
	request struct {
		Mode *string `json:"mode" cordis:"enum=a|b"`
		base
		// Name hides the promoted field of base.
		Name string `json:"name" cordis:"minLength=1"`
	}
	base struct {
		Name string `json:"name"`
		Kind string `json:"kind,omitempty"`
	}
	ctx    struct{}
	input  struct{}
	method struct{}
	c      struct{}
	e      struct{}
)

//cordis:service example.shadow 1
type Shadow interface {
	Client(ctx context.Context, r request) error
	Endpoint(ctx context.Context) (request, error)
	Config(ctx context.Context, r ctx) (input, error)
	Service(ctx context.Context, r method) iter.Seq2[c, error]
	Operation(ctx context.Context) iter.Seq2[e, error]
}

// Keeper is the implementation the plugin provides.
type Keeper struct{ last request }

func (k *Keeper) Client(_ context.Context, r request) error  { k.last = r; return nil }
func (k *Keeper) Endpoint(context.Context) (request, error)  { return k.last, nil }
func (k *Keeper) Config(context.Context, ctx) (input, error) { return input{}, nil }
func (k *Keeper) Service(context.Context, method) iter.Seq2[c, error] {
	return func(yield func(c, error) bool) { yield(c{}, nil) }
}
func (k *Keeper) Operation(context.Context) iter.Seq2[e, error] {
	return func(yield func(e, error) bool) { yield(e{}, nil) }
}

// RoundTrip sends a request through a client and reads it back.
func RoundTrip(ctx context.Context, mode *string, name string) (gotMode *string, gotName string, err error) {
	client, err := NewShadowClient(NewShadowEndpoint(&Keeper{}))
	if err != nil {
		return nil, "", err
	}
	if err := client.Client(ctx, request{Mode: mode, Name: name, base: base{Name: "hidden"}}); err != nil {
		return nil, "", err
	}
	back, err := client.Endpoint(ctx)
	return back.Mode, back.Name, err
}

//cordis:plugin shadow
//cordis:provides Shadow
func start(_ context.Context, s *cordis.Scope, _ struct{}) error {
	return ProvideShadow(s, &Keeper{})
}
