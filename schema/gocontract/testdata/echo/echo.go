// Package echo exercises every shape a contract is derived from.
package echo

import (
	"context"
	"fmt"
	"iter"

	"github.com/Cyenoch/cordis"
	"github.com/Cyenoch/cordis/schema/gocontract/testdata/clock"
)

// Echo repeats things.
//
//cordis:service example.echo 1
type Echo interface {
	Say(ctx context.Context, request SayRequest) (SayResponse, error)
	Tail(ctx context.Context, request TailRequest) iter.Seq2[Line, error]
	Ticks(ctx context.Context) iter.Seq2[int, error]
	Reset(ctx context.Context) error
	Put(ctx context.Context, line Line) error
	Count(ctx context.Context) (int64, error)
}

type SayRequest struct {
	Text  string   `json:"text" cordis:"minLength=1,maxLength=16"`
	Times *int     `json:"times,omitempty" cordis:"minimum=1,maximum=3"`
	Tags  []string `json:"tags,omitempty" cordis:"maxItems=2"`
	Mode  string   `json:"mode,omitempty" cordis:"enum=loud|quiet"`
	Big   int64    `json:"big,string,omitempty"`
	Code  string   `json:"code,omitempty" cordis:"pattern=^[a-z]{1,3}$"`
	// Labels is a map: any key, every value held to the element type.
	Labels map[string]string `json:"labels,omitempty"`
}

type SayResponse struct {
	Text string `json:"text"`
	Meta
}

// Meta is embedded: its fields are fields of the response.
type Meta struct {
	Length int `json:"length"`
}

type TailRequest struct {
	Count int `json:"count" cordis:"minimum=0"`
}

type Line struct {
	Line string `json:"line"`
}

type Config struct {
	Prefix string `json:"prefix,omitempty" cordis:"maxLength=8"`
}

// Service is the implementation the plugin provides.
type Service struct {
	Prefix string
	Total  int64
}

//cordis:plugin echo
//cordis:provides Echo
//cordis:requires clock.Clock 0-2
func start(_ context.Context, s *cordis.Scope, config Config) error {
	if _, err := clock.GetClock(s); err != nil {
		return err
	}
	return ProvideEcho(s, &Service{Prefix: config.Prefix})
}

func (e *Service) Say(_ context.Context, r SayRequest) (SayResponse, error) {
	text := e.Prefix + r.Text
	return SayResponse{Text: text, Meta: Meta{Length: len(text)}}, nil
}

func (e *Service) Tail(_ context.Context, r TailRequest) iter.Seq2[Line, error] {
	return func(yield func(Line, error) bool) {
		for n := range r.Count {
			if !yield(Line{fmt.Sprint(n)}, nil) {
				return
			}
		}
	}
}

func (e *Service) Ticks(context.Context) iter.Seq2[int, error] {
	return func(yield func(int, error) bool) { yield(1, nil) }
}

func (e *Service) Reset(context.Context) error { e.Total = 0; return nil }

func (e *Service) Put(_ context.Context, line Line) error {
	e.Total += int64(len(line.Line))
	return nil
}

func (e *Service) Count(context.Context) (int64, error) { return e.Total, nil }
