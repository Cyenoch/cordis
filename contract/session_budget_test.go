package contract_test

import (
	"io"
	"testing"

	"github.com/Cyenoch/cordis/contract"
)

func TestSessionFactoryBudgetValidation(t *testing.T) {
	open := func(io.ReadWriteCloser, contract.Identity, contract.SessionHandler, int) (contract.Session, error) {
		return nil, nil
	}
	for _, test := range []struct {
		name    string
		factory *contract.SessionFactory
		budget  int
		valid   bool
	}{
		{"generic library missing budget", nil, 0, false},
		{"generic library minimum", nil, contract.MinFrameBytes, true},
		{"generic wrapper missing budget", &contract.SessionFactory{Protocol: contract.GenericSession, Open: open}, 0, false},
		{"generic wrapper maximum", &contract.SessionFactory{Protocol: contract.GenericSession, Open: open}, contract.MaxFrameBytes, true},
		{"custom owns its policy", &contract.SessionFactory{Protocol: contract.CustomSession, Open: open}, 0, true},
		{"missing protocol", &contract.SessionFactory{Open: open}, contract.MaxFrameBytes, false},
		{"unknown protocol", &contract.SessionFactory{Protocol: "guess", Open: open}, contract.MaxFrameBytes, false},
		{"missing opener", &contract.SessionFactory{Protocol: contract.CustomSession}, 0, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := test.factory.Validate(test.budget); (err == nil) != test.valid {
				t.Fatalf("Validate(%d) = %v", test.budget, err)
			}
		})
	}
}
