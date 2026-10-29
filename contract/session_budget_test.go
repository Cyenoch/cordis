package contract_test

import (
	"github.com/Cyenoch/cordis/contract"
	"testing"
)

func TestSessionBudgetValidation(t *testing.T) {
	for _, test := range []struct {
		name   string
		budget int
		valid  bool
	}{
		{"missing", 0, false}, {"minimum", contract.MinFrameBytes, true},
		{"maximum", contract.MaxFrameBytes, true}, {"too large", contract.MaxFrameBytes + 1, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := contract.ValidateFrameBytes(test.budget); (err == nil) != test.valid {
				t.Fatal(err)
			}
		})
	}
}
