package contract_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/Cyenoch/cordis/contract"
)

func TestErrorClassificationSurvivesWrapping(t *testing.T) {
	err := fmt.Errorf("admit call: %w", contract.Fail(contract.Denied, "local policy detail"))
	if !errors.Is(err, contract.Fail(contract.Denied, "different message")) {
		t.Fatal("same-code errors did not match")
	}
	if errors.Is(err, contract.Fail(contract.Invalid, "local policy detail")) {
		t.Fatal("different-code errors matched")
	}
	for _, tc := range []struct {
		code contract.Code
		want error
	}{{contract.Canceled, context.Canceled}, {contract.Timeout, context.DeadlineExceeded}} {
		if !errors.Is(fmt.Errorf("call: %w", contract.Fail(tc.code, "detail")), tc.want) {
			t.Errorf("%s lost standard context classification", tc.code)
		}
	}
}

func TestUnknownFieldClassificationSurvivesWrapping(t *testing.T) {
	var value struct {
		Name string `json:"name"`
	}
	err := fmt.Errorf("load settings: %w", contract.Decode([]byte(`{"name":"demo","secret":true}`), &value))
	var unknown *contract.UnknownFieldError
	if !errors.As(err, &unknown) || unknown.Field != "secret" {
		t.Fatalf("unknown field identity lost: %v", err)
	}
}
