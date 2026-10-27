package packagehost

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/Cyenoch/cordis/contract"
)

func manifestFixture(id, version string) contract.Manifest {
	return contract.Manifest{Schema: contract.PackageSchema, ID: id, Version: version, Publisher: "example", Protocol: contract.Protocol{Major: 1, MaxMinor: 0}, Definitions: []contract.DefinitionSpec{{ID: "main"}}, DefaultDefinition: "main", Runtime: contract.RuntimeSpec{Kind: "wasm", Entry: "plugin.wasm"}, DataVersion: 1}
}
func TestValidateManifest(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		change func(*contract.Manifest)
	}{
		{"unknown schema", func(m *contract.Manifest) { m.Schema = "future" }},
		{"ambiguous runtime", func(m *contract.Manifest) {
			m.Runtime.Artifacts = []contract.Artifact{{OS: "linux", Arch: "amd64", Path: "native"}}
		}},
		{"invalid semver", func(m *contract.Manifest) { m.Version = "1.0" }},
		{"unknown definition", func(m *contract.Manifest) { m.DefaultDefinition = "missing" }},
		{"duplicate definition", func(m *contract.Manifest) { m.Definitions = append(m.Definitions, m.Definitions[0]) }},
		{"invalid schema", func(m *contract.Manifest) { m.ConfigSchema = []byte(`{"type":"not-a-json-type"}`) }},
		{"invalid range", func(m *contract.Manifest) {
			m.Dependencies = []contract.Dependency{{ID: "dep", Min: "2.0.0", Max: "1.0.0"}}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := manifestFixture("demo", "1.0.0")
			tc.change(&m)
			if err := ValidateManifest(m); err == nil {
				t.Fatal("accepted invalid manifest")
			}
		})
	}
	if err := ValidateManifest(manifestFixture("demo", "1.0.0")); err != nil {
		t.Fatal(err)
	}
}
func TestSignatureInput(t *testing.T) {
	t.Parallel()
	hash := sha256.Sum256([]byte("abc"))
	entry := IntegrityEntry{"a", 3, hex.EncodeToString(hash[:])}
	input, err := SignatureInput([]IntegrityEntry{entry})
	if err != nil {
		t.Fatal(err)
	}
	prefix := []byte("cordis-go-package/v1\x00")
	expected := append([]byte(nil), prefix...)
	expected = binary.BigEndian.AppendUint32(expected, 1)
	expected = binary.BigEndian.AppendUint32(expected, 1)
	expected = append(expected, 'a')
	expected = binary.BigEndian.AppendUint64(expected, 3)
	expected = append(expected, hash[:]...)
	if !equalBytes(input, expected) {
		t.Fatalf("incorrect binary signature input %x", input)
	}
	for _, name := range []string{"../outside", "/absolute", "C:/drive", "a\\b", "CON.txt", "com¹.txt", "a.", "a ", "e\u0301", "a\x00b", "a:b", "x//y"} {
		t.Run(name, func(t *testing.T) {
			if err := validatePath(name); err == nil {
				t.Fatalf("accepted %q", name)
			}
		})
	}
	for _, names := range [][]string{{"A", "a"}, {"Dir/a", "dir/b"}, {"a", "a/b"}, {"Straße/a", "STRASSE/b"}} {
		if err := checkFileNames(names); err == nil {
			t.Fatalf("accepted conflicts %q", names)
		}
	}
	if err := checkDirectories([]string{"folder/a"}, []string{"folder", "folder/nested"}); err != nil {
		t.Fatal(err)
	}
}
func FuzzPackagePath(f *testing.F) {
	for _, seed := range []string{"plugin.wasm", "../escape", "CON", "e\u0301", "a/b"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, name string) {
		if validatePath(name) == nil {
			if strings.Contains(name, "\\") || strings.HasPrefix(name, "/") {
				t.Fatalf("unsafe accepted path %q", name)
			}
			if err := checkFileNames([]string{name, name}); err == nil {
				t.Fatal("duplicate path accepted")
			}
		}
	})
}
func FuzzManifest(f *testing.F) {
	f.Add([]byte(`{"schema":"cordis-go.package/v1"}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 1<<20 {
			t.Skip()
		}
		m, err := decodeManifest(data)
		if err == nil {
			if err = ValidateManifest(m); err != nil {
				t.Fatal(err)
			}
		}
	})
}
