package contract_test

import (
	"encoding/json"
	"testing"

	"github.com/Cyenoch/cordis/contract"
)

// What the strict decoder accepts is valid JSON that the standard decoder
// reads the same way; it never panics on what it rejects.
func FuzzDecode(f *testing.F) {
	for _, seed := range []string{
		`{"schema":"x","id":"a","version":"1.0.0"}`, `{"a":1,"a":2}`, `{"A":1}`, `[[[[[[1]]]]]]`, `1e400`, `"\ud800"`,
		`{"definitions":[{"id":"main","configSchema":{}}]}`, ``, `null`, `{} {}`, "\xff",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		if len(input) > 4096 {
			t.Skip()
		}
		var strict, plain contract.Manifest
		if err := contract.Decode([]byte(input), &strict); err != nil {
			return
		}
		if err := json.Unmarshal([]byte(input), &plain); err != nil {
			t.Fatalf("accepted what encoding/json rejects: %v\n%q", err, input)
		}
		a, _ := json.Marshal(strict)
		b, _ := json.Marshal(plain)
		if string(a) != string(b) {
			t.Fatalf("decoded differently from encoding/json:\n%s\n%s", a, b)
		}
	})
}
