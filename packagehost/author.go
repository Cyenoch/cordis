package packagehost

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/Cyenoch/cordis/config"
	"github.com/Cyenoch/cordis/contract"
	"github.com/Cyenoch/cordis/schema"
)

var authorCandidates = []string{"plugin.toml", "plugin.yaml", "plugin.yml", ManifestFile}

// Author is the parsed declaration shared by generation, building and packing.
// A machine plugin.json has no Contracts and cannot be used to generate code.
type Author struct {
	Manifest    contract.Manifest
	Contracts   []schema.Definition
	Fingerprint string
	Source      string
}

type authorSource struct {
	ID                   string                  `json:"id"`
	Version              string                  `json:"version"`
	Publisher            string                  `json:"publisher"`
	Runtime              contract.RuntimeSpec    `json:"runtime"`
	DefaultDefinition    string                  `json:"defaultDefinition,omitempty"`
	Dependencies         []contract.Dependency   `json:"dependencies,omitempty"`
	OptionalDependencies []contract.Dependency   `json:"optionalDependencies,omitempty"`
	Capabilities         []contract.Capability   `json:"capabilities,omitempty"`
	Contributions        []contract.Contribution `json:"contributions,omitempty"`
	DataVersion          uint32                  `json:"dataVersion,omitempty"`
	Contract             json.RawMessage         `json:"contract,omitempty"`
	Contracts            []string                `json:"contracts,omitempty"`
}

// LoadAuthor loads exactly one candidate, even when source names a file.
// It never falls back from an invalid current machine Manifest to author syntax.
func LoadAuthor(sourceDir string) (*Author, error) {
	dir, source, err := authorSourcePath(sourceDir)
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	loader := declarationLoader{root: root, active: map[string]bool{}, loaded: map[string]bool{}}
	doc, err := loader.read(source)
	if err != nil {
		return nil, err
	}
	var header struct {
		Schema string `json:"schema"`
	}
	if err := json.Unmarshal(doc.JSON, &header); err != nil {
		return nil, fmt.Errorf("%s: %w", source, err)
	}
	a := &Author{Source: source, Contracts: []schema.Definition{}}
	if header.Schema == contract.PackageSchema {
		if source != ManifestFile {
			return nil, fmt.Errorf("%s: machine Manifest must be plugin.json", source)
		}
		a.Manifest, err = decodeManifest(doc.JSON)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", source, err)
		}
		a.Fingerprint, err = authorFingerprint(a)
		return a, err
	}
	var input authorSource
	if err := doc.Decode(&input); err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(doc.JSON, &fields); err != nil {
		return nil, err
	}
	_, inline := fields["contract"]
	_, references := fields["contracts"]
	if inline == references || references && len(input.Contracts) == 0 {
		return nil, fmt.Errorf("%s: provide exactly one of contract or nonempty contracts", source)
	}
	loader.active[source] = true
	if inline {
		d, err := parseAuthorContract(input.Contract)
		if err != nil {
			return nil, fmt.Errorf("%s contract: %w", source, err)
		}
		a.Contracts = append(a.Contracts, d)
	} else {
		for _, ref := range input.Contracts {
			if err := loader.load(ref, 0); err != nil {
				return nil, fmt.Errorf("%s: %w", source, err)
			}
		}
		a.Contracts = loader.contracts
	}
	m := contract.Manifest{
		Schema: contract.PackageSchema, ID: input.ID, Version: input.Version, Publisher: input.Publisher,
		Protocol: contract.Protocol{Major: 1}, Runtime: input.Runtime,
		DefaultDefinition: input.DefaultDefinition, DataVersion: input.DataVersion,
		Definitions: []contract.DefinitionSpec{}, Dependencies: append([]contract.Dependency{}, input.Dependencies...),
		OptionalDependencies: append([]contract.Dependency{}, input.OptionalDependencies...),
		Capabilities:         append([]contract.Capability{}, input.Capabilities...),
		Contributions:        append([]contract.Contribution{}, input.Contributions...),
	}
	for _, d := range a.Contracts {
		specs, err := d.Specs()
		if err != nil {
			return nil, fmt.Errorf("%s: compile definitions: %w", source, err)
		}
		m.Definitions = append(m.Definitions, specs...)
	}
	slices.SortFunc(m.Definitions, func(a, b contract.DefinitionSpec) int { return strings.Compare(a.ID, b.ID) })
	if m.DefaultDefinition == "" && len(m.Definitions) == 1 {
		m.DefaultDefinition = m.Definitions[0].ID
	}
	if err := ValidateManifest(m); err != nil {
		return nil, fmt.Errorf("%s: %w", source, err)
	}
	a.Manifest = m
	a.Fingerprint, err = authorFingerprint(a)
	return a, err
}

// Generate validates all declarations before writing a formatted Go file.
func (a *Author) Generate(w io.Writer, pkg string) error {
	if len(a.Contracts) == 0 {
		return fmt.Errorf("%s: machine Manifest has no author contracts", a.Source)
	}
	return schema.GenerateMany(w, pkg, a.Contracts...)
}

func authorSourcePath(source string) (string, string, error) {
	dir := source
	info, err := os.Lstat(source)
	if err != nil {
		return "", "", err
	}
	explicit := ""
	if !info.IsDir() {
		dir, explicit = filepath.Dir(source), filepath.Base(source)
	}
	found := []string{}
	for _, name := range authorCandidates {
		_, err := os.Lstat(filepath.Join(dir, name))
		if err == nil {
			found = append(found, name)
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", "", err
		}
	}
	if len(found) != 1 {
		return "", "", fmt.Errorf("%s: expected one package source, found %v", dir, found)
	}
	if explicit != "" && explicit != found[0] {
		return "", "", fmt.Errorf("%s: not a package source candidate", source)
	}
	return dir, found[0], nil
}

type declarationLoader struct {
	root      *os.Root
	active    map[string]bool
	loaded    map[string]bool
	bytes     int
	contracts []schema.Definition
}

func (l *declarationLoader) read(name string) (*config.Document, error) {
	if err := validatePath(name); err != nil {
		return nil, err
	}
	if err := checkSourceParents(l.root, name); err != nil {
		return nil, err
	}
	f, err := openRegular(l.root, name)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err == nil {
		err = checkLinks(f, info)
	}
	if err != nil {
		return nil, errors.Join(err, f.Close())
	}
	data, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	err = errors.Join(err, f.Close())
	if err != nil {
		return nil, err
	}
	l.bytes += len(data)
	if l.bytes > 1<<20 {
		return nil, fmt.Errorf("%s: declaration graph exceeds 1 MiB", name)
	}
	return config.Parse(name, data, "")
}

func (l *declarationLoader) load(name string, depth int) error {
	if l.active[name] {
		return fmt.Errorf("cyclic contract reference %q", name)
	}
	if l.loaded[name] {
		return nil
	}
	if depth >= 32 {
		return fmt.Errorf("%s: contract reference depth exceeds 32", name)
	}
	doc, err := l.read(name)
	if err != nil {
		return err
	}
	l.active[name] = true
	defer delete(l.active, name)
	var object map[string]json.RawMessage
	if err := json.Unmarshal(doc.JSON, &object); err != nil {
		return err
	}
	if refs, ok := object["contracts"]; ok {
		var list []string
		if len(object) != 1 {
			return fmt.Errorf("%s: contract collection accepts only contracts", name)
		}
		if err := contract.Decode(refs, &list); err != nil {
			return fmt.Errorf("%s contracts: %w", name, err)
		}
		if len(list) == 0 {
			return fmt.Errorf("%s: empty contracts", name)
		}
		for _, ref := range list {
			if err := l.load(ref, depth+1); err != nil {
				return fmt.Errorf("%s: %w", name, err)
			}
		}
	} else {
		d, err := parseAuthorContract(doc.JSON)
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		l.contracts = append(l.contracts, d)
	}
	l.loaded[name] = true
	return nil
}

func parseAuthorContract(raw json.RawMessage) (schema.Definition, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil || object == nil {
		return schema.Definition{}, fmt.Errorf("contract must be an object")
	}
	if _, ok := object["schema"]; !ok {
		object["schema"], _ = json.Marshal(schema.DefinitionSchema)
	}
	data, err := json.Marshal(object)
	if err != nil {
		return schema.Definition{}, err
	}
	return schema.Parse(data)
}

func authorFingerprint(a *Author) (string, error) {
	hashes := make([]string, 0, len(a.Contracts))
	for _, d := range a.Contracts {
		hash, err := d.Fingerprint()
		if err != nil {
			return "", err
		}
		hashes = append(hashes, hash)
	}
	slices.Sort(hashes)
	data, err := json.Marshal(struct {
		Manifest  contract.Manifest `json:"manifest"`
		Contracts []string          `json:"contracts"`
	}{Manifest: a.Manifest, Contracts: hashes})
	if err != nil {
		return "", err
	}
	return hashBytes(data), nil
}

func hashBytes(data []byte) string {
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}
