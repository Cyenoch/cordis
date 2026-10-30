package packagehost

import (
	"archive/zip"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/Cyenoch/cordis/contract"
)

func TestPackDeclaredResourcesAndInspection(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	manifest := manifestFixture("demo", "1.0.0")
	manifest.Resources = []string{"assets/template.txt"}
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	writeAuthorFile(t, dir, ManifestFile, string(raw))
	writeAuthorFile(t, dir, "plugin", "artifact")
	writeAuthorFile(t, dir, "assets/template.txt", "template data")
	writeAuthorFile(t, dir, "secret.private", "not published")
	large, err := os.Create(filepath.Join(dir, "irrelevant.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if err := large.Truncate(DefaultLimits().FileBytes + 1); err != nil {
		t.Fatal(err)
	}
	if err := large.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(filepath.Join(dir, "secret.private"), filepath.Join(dir, "unrelated-link")); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(t.TempDir(), "release.zip")
	if _, err := Pack(t.Context(), dir, archive); err != nil {
		t.Fatal(err)
	}
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Sign(t.Context(), archive, "test", key); err != nil {
		t.Fatal(err)
	}
	inspection, err := Inspect(t.Context(), archive)
	if err != nil {
		t.Fatal(err)
	}
	z, err := zip.OpenReader(archive)
	if err != nil {
		t.Fatal(err)
	}
	defer z.Close()
	actual := map[string][]byte{}
	for _, file := range z.File {
		stream, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		value, err := io.ReadAll(stream)
		closeErr := stream.Close()
		if err != nil || closeErr != nil {
			t.Fatalf("read %s: %v %v", file.Name, err, closeErr)
		}
		actual[file.Name] = value
	}
	var names []string
	for _, file := range inspection.Files {
		names = append(names, file.Path)
		if hashBytes(actual[file.Path]) != file.SHA256 || uint64(len(actual[file.Path])) != file.Size {
			t.Fatalf("incorrect inspection for %s", file.Path)
		}
	}
	expected := []string{"assets/template.txt", IntegrityFile, "plugin", ManifestFile, SignatureFile}
	if !slices.Equal(names, expected) || len(actual) != len(expected) {
		t.Fatalf("unexpected release files: %v", names)
	}
	if string(actual["assets/template.txt"]) != "template data" {
		t.Fatal("resource content missing")
	}
}

func TestPackRejectsInvalidResourcesWithoutReplacingOutput(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"missing", "directory", "hardlink", "duplicate", "artifact", "metadata", "metadata case", "metadata directory", "traversal", "glob", "case", "prefix"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			manifest := manifestFixture("demo", "1.0.0")
			manifest.Resources = []string{"resource"}
			writeAuthorFile(t, dir, "plugin", "artifact")
			writeAuthorFile(t, dir, "resource", "data")
			switch name {
			case "missing":
				manifest.Resources = []string{"absent"}
			case "directory":
				manifest.Resources = []string{"assets"}
				writeAuthorFile(t, dir, "assets/a", "data")
			case "hardlink":
				if err := os.Link(filepath.Join(dir, "resource"), filepath.Join(t.TempDir(), "alias")); err != nil {
					t.Fatal(err)
				}
			case "duplicate":
				manifest.Resources = []string{"resource", "resource"}
			case "artifact":
				manifest.Resources = []string{"plugin"}
			case "metadata":
				manifest.Resources = []string{IntegrityFile}
			case "metadata case":
				manifest.Resources = []string{"Signature.json"}
				writeAuthorFile(t, dir, "Signature.json", "{}")
			case "metadata directory":
				manifest.Resources = []string{"integrity.json/child"}
				writeAuthorFile(t, dir, "integrity.json/child", "data")
			case "traversal":
				manifest.Resources = []string{"../resource"}
			case "glob":
				manifest.Resources = []string{"assets/*"}
			case "case":
				manifest.Resources = []string{"resource", "Resource"}
			case "prefix":
				manifest.Resources = []string{"resource", "resource/child"}
			}
			raw, err := json.Marshal(manifest)
			if err != nil {
				t.Fatal(err)
			}
			writeAuthorFile(t, dir, ManifestFile, string(raw))
			archive := filepath.Join(t.TempDir(), "release.zip")
			writeAuthorFile(t, filepath.Dir(archive), filepath.Base(archive), "previous archive")
			if _, err := Pack(t.Context(), dir, archive); err == nil {
				t.Fatal("accepted invalid resource")
			}
			previous, err := os.ReadFile(archive)
			if err != nil || string(previous) != "previous archive" {
				t.Fatalf("failed Pack changed archive: %q %v", previous, err)
			}
		})
	}
}

func TestResourceSchemaInteroperability(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile("../schema/testdata/interoperability.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Vectors []struct {
			Name     string          `json:"name"`
			Schema   string          `json:"schema"`
			Pointer  string          `json:"pointer"`
			Accept   bool            `json:"accept"`
			Instance json.RawMessage `json:"instance"`
		} `json:"schemaVectors"`
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	for _, vector := range fixture.Vectors {
		if vector.Schema != "schema/package.schema.json" || vector.Pointer != "/properties/resources" {
			continue
		}
		t.Run(vector.Name, func(t *testing.T) {
			m := manifestFixture("demo", "1.0.0")
			err := contract.Decode(vector.Instance, &m.Resources)
			if err == nil {
				err = ValidateManifest(m)
			}
			if (err == nil) != vector.Accept {
				t.Fatalf("resource schema/runtime mismatch: %v", err)
			}
		})
	}
}
