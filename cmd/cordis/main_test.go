package main

import (
	"testing"

	"github.com/Cyenoch/cordis/contract"
)

func TestServiceSelectionUsesDeclaredVersion(t *testing.T) {
	m := contract.Manifest{Definitions: []contract.DefinitionSpec{{ID: "main", Provides: []contract.Service{{Name: "echo", Major: 1, Minor: 1}, {Name: "echo", Major: 2, Minor: 4}}}}}
	if _, err := selectService(m, "main", "echo", 0, 0, uint(^uint32(0))); err == nil {
		t.Fatal("ambiguous major accepted")
	}
	req, err := selectService(m, "main", "echo", 2, 2, 4)
	if err != nil {
		t.Fatal(err)
	}
	if !req.Accepts(m.Definitions[0].Provides[1]) || req.Accepts(m.Definitions[0].Provides[0]) {
		t.Fatal("wrong version selected")
	}
	m.Definitions[0].Provides = m.Definitions[0].Provides[:1]
	req, err = selectService(m, "main", "echo", 0, 0, uint(^uint32(0)))
	if err != nil || !req.Accepts(m.Definitions[0].Provides[0]) {
		t.Fatal(req, err)
	}
}
