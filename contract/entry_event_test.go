package contract_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/Cyenoch/cordis/contract"
)

func TestEntryGraphRejectsSelfAndDeepCycles(t *testing.T) {
	deep := make([]contract.Entry, 128)
	for i := range deep {
		deep[i].ID = fmt.Sprintf("entry-%d", i)
		if i+1 < len(deep) {
			deep[i].Parent = fmt.Sprintf("entry-%d", i+1)
		}
	}
	acyclic := append([]contract.Entry(nil), deep...)
	deep[len(deep)-1].Parent = "entry-64"
	for _, test := range []struct {
		name    string
		entries []contract.Entry
		code    contract.Code
	}{
		{name: "self cycle", entries: []contract.Entry{{ID: "self", Parent: "self"}}, code: contract.Reentrant},
		{name: "deep cycle behind an acyclic prefix", entries: deep, code: contract.Reentrant},
		{name: "missing parent", entries: []contract.Entry{{ID: "child", Parent: "missing"}}, code: contract.Invalid},
		{name: "deep valid chain", entries: acyclic},
		{name: "root and siblings", entries: []contract.Entry{{ID: "root"}, {ID: "left", Parent: "root"}, {ID: "right", Parent: "root"}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := contract.CheckEntryGraph(test.entries)
			if test.code == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			var coded *contract.Error
			if !errors.As(err, &coded) || coded.Code != test.code {
				t.Fatalf("graph error = %v, want %s", err, test.code)
			}
		})
	}
}

func TestDecodeRejectsInvalidUTF8RatherThanReplacingIdentity(t *testing.T) {
	for _, test := range []struct {
		name string
		data []byte
	}{
		{name: "invalid entry identity", data: []byte("{\"id\":\"entry-\xff\"}")},
		{name: "invalid config value", data: []byte("{\"id\":\"valid\",\"config\":{\"value\":\"\xff\"}}")},
	} {
		t.Run(test.name, func(t *testing.T) {
			var entry contract.Entry
			err := contract.Decode(test.data, &entry)
			var coded *contract.Error
			if !errors.As(err, &coded) || coded.Code != contract.Invalid {
				t.Fatalf("invalid UTF-8 decode = %#v, %v", entry, err)
			}
			if entry.ID != "" || entry.Config != nil {
				t.Fatalf("invalid input partially populated entry: %+v", entry)
			}
		})
	}
	var valid contract.Entry
	if err := contract.Decode([]byte(`{"id":"有效条目","config":{"value":"有效"}}`), &valid); err != nil || valid.ID != "有效条目" {
		t.Fatalf("valid UTF-8 decode = %+v, %v", valid, err)
	}
}
