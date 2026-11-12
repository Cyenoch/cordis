package contract_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
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

// Wrap classifies an error without hiding it, and does not classify one
// twice: its text names the code once.
func TestWrapClassifiesAndKeepsTheCause(t *testing.T) {
	cause := errors.New("disk full")
	err := contract.Wrap(contract.Internal, fmt.Errorf("write catalog: %w", cause))
	if contract.ErrorCode(err) != contract.Internal || !errors.Is(err, cause) || err.Error() != "internal: write catalog: disk full" {
		t.Fatalf("wrapped = %v", err)
	}
	if again := contract.Wrap(contract.Internal, err); again != err {
		t.Fatalf("same code wrapped twice: %v", again)
	}
	// Context around a same-code error is kept, since peers see the text.
	inner := fmt.Errorf("entry x: %w", contract.Fail(contract.NotReady, "guest not ready"))
	if wrapped := contract.Wrap(contract.NotReady, inner); !strings.Contains(wrapped.Error(), "entry x: ") {
		t.Fatalf("context dropped: %v", wrapped)
	}
	if contract.Wrap(contract.Internal, nil) != nil {
		t.Fatal("nil was classified")
	}
}
