package pluginmeta

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Cyenoch/cordis/contract"
	"github.com/Cyenoch/cordis/packagehost"
	"github.com/Cyenoch/cordis/schema/gocontract"
)

func TestGeneratedDeclarationsMatchAuthor(t *testing.T) {
	for _, tc := range []struct {
		name     string
		manifest contract.Manifest
	}{{"counter", Counter()}, {"chain", Chain()}, {"resource", Resource()}, {"provider", Provider()}, {"consumer", Consumer()}, {"feed", Feed()}} {
		t.Run(tc.name, func(t *testing.T) {
			dir := filepath.Join("..", "plugins", tc.name)
			author, err := packagehost.LoadAuthor(dir)
			if err != nil {
				t.Fatal(err)
			}
			want, err := json.Marshal(author.Manifest)
			if err != nil {
				t.Fatal(err)
			}
			got, err := json.Marshal(tc.manifest)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, want) {
				t.Fatal("host metadata is stale; run go generate ./examples/pluginmeta")
			}
			derived, err := gocontract.Load(t.Context(), dir)
			if err != nil {
				t.Fatal(err)
			}
			source, err := derived.Source()
			if err != nil {
				t.Fatal(err)
			}
			declared, err := derived.Contract()
			if err != nil {
				t.Fatal(err)
			}
			for name, want := range map[string][]byte{gocontract.GoFile: source, gocontract.ContractFile: declared} {
				if got, err := os.ReadFile(filepath.Join(dir, name)); err != nil || !bytes.Equal(got, want) {
					t.Fatalf("%s is stale (%v); run go generate ./examples/pluginmeta", name, err)
				}
			}
		})
	}
}
