package packagehost

import (
	"context"
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
	"github.com/Cyenoch/cordis/schema/gocontract"
)

// ErrNotGenerated reports a declaration whose contract comes from its Go
// source and has not been derived yet.
var ErrNotGenerated = errors.New("the contract has not been derived from the Go source; run cordis generate")

var authorCandidates = []string{"plugin.toml", "plugin.yaml", "plugin.yml", ManifestFile}

// Author is the parsed declaration shared by generation, building and packing.
// A machine plugin.json has no Contracts and cannot be used to generate code.
type Author struct {
	Manifest    contract.Manifest
	Contracts   []schema.Definition
	Fingerprint string
	Source      string
	// Derived reports a contract derived from the Go source of the package
	// rather than written by the author.
	Derived bool
	sources []string
}

// Inputs returns the author and contract files read to produce this declaration.
// Paths are relative to the author directory; the caller owns the returned slice.
func (a *Author) Inputs() []string { return slices.Clone(a.sources) }

type authorSource struct {
	ID                   string                `json:"id"`
	Version              string                `json:"version"`
	Publisher            string                `json:"publisher"`
	Runtime              contract.RuntimeSpec  `json:"runtime"`
	Resources            []string              `json:"resources,omitempty"`
	DefaultDefinition    string                `json:"defaultDefinition,omitempty"`
	Dependencies         []contract.Dependency `json:"dependencies,omitempty"`
	OptionalDependencies []contract.Dependency `json:"optionalDependencies,omitempty"`
	Contract             json.RawMessage       `json:"contract,omitempty"`
	Contracts            []string              `json:"contracts,omitempty"`
}

// LoadAuthor loads exactly one candidate, even when source names a file.
// It never falls back from an invalid current machine Manifest to author syntax.
// A declaration without contract or contracts takes the contract derived from
// its Go source, as last written by GenerateAuthor or cordis generate.
func LoadAuthor(sourceDir string) (*Author, error) { return loadAuthor(sourceDir, nil, false) }

// OutlineAuthor is LoadAuthor for a tool that runs before the first build.
// Where LoadAuthor fails with ErrNotGenerated it returns the declaration
// without definitions: identity, targets and resources are all there is to
// know until the Go source has been analyzed.
func OutlineAuthor(sourceDir string) (*Author, error) { return loadAuthor(sourceDir, nil, true) }

// GenerateAuthor is LoadAuthor after deriving the contract and its bindings
// from the Go source again. It is LoadAuthor for a declaration that writes
// its contract itself.
func GenerateAuthor(ctx context.Context, sourceDir string) (*Author, error) {
	return loadAuthor(sourceDir, func(dir string) error {
		_, err := gocontract.Generate(ctx, dir)
		return err
	}, false)
}

func loadAuthor(sourceDir string, derive func(dir string) error, outline bool) (*Author, error) {
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
	a := &Author{Source: source, sources: []string{source}, Contracts: []schema.Definition{}}
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
	if inline && references || references && len(input.Contracts) == 0 {
		return nil, fmt.Errorf("%s: provide at most one of contract or nonempty contracts", source)
	}
	loader.active[source] = true
	if a.Derived = !inline && !references; a.Derived {
		if derive != nil {
			if err := derive(dir); err != nil {
				return nil, err
			}
		}
		if err := loader.load(gocontract.ContractFile, 0); errors.Is(err, os.ErrNotExist) {
			if !outline {
				return nil, fmt.Errorf("%s: %w", source, ErrNotGenerated)
			}
		} else if err != nil {
			return nil, fmt.Errorf("%s: %w", source, err)
		}
		a.Contracts = loader.contracts
	} else if inline {
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
	for name := range loader.loaded {
		a.sources = append(a.sources, name)
	}
	slices.Sort(a.sources)
	m := contract.Manifest{
		Schema: contract.PackageSchema, ID: input.ID, Version: input.Version, Publisher: input.Publisher,
		Protocol: contract.Protocol{Major: contract.ProtocolMajor}, Runtime: input.Runtime,
		Resources:         slices.Clone(input.Resources),
		DefaultDefinition: input.DefaultDefinition,
		Definitions:       []contract.DefinitionSpec{}, Dependencies: append([]contract.Dependency{}, input.Dependencies...),
		OptionalDependencies: append([]contract.Dependency{}, input.OptionalDependencies...),
	}
	// A package whose contracts have streaming methods needs a host that
	// speaks streams; it fails at negotiation on one that does not.
	for _, d := range a.Contracts {
		for _, service := range d.Services {
			for _, method := range service.Methods {
				if method.Stream && !slices.Contains(m.Protocol.RequiredFeatures, "streams") {
					m.Protocol.RequiredFeatures = append(m.Protocol.RequiredFeatures, "streams")
				}
			}
		}
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
	checked := m
	if a.Derived && len(a.Contracts) == 0 {
		// An outline: everything but the definitions can be checked.
		checked.Definitions = []contract.DefinitionSpec{{ID: "outline", ConfigSchema: json.RawMessage(`{"type":"object"}`)}}
		checked.DefaultDefinition = "outline"
	}
	if err := ValidateManifest(checked); err != nil {
		return nil, fmt.Errorf("%s: %w", source, err)
	}
	a.Manifest = m
	a.Fingerprint, err = authorFingerprint(a)
	return a, err
}

// Generate validates all declarations before writing a formatted Go file.
// A derived contract has no types to generate: its bindings come from the Go
// source, through GenerateAuthor.
func (a *Author) Generate(w io.Writer, pkg string) error {
	if len(a.Contracts) == 0 {
		return fmt.Errorf("%s: machine Manifest has no author contracts", a.Source)
	}
	if a.Derived {
		return fmt.Errorf("%s: bindings of a derived contract are generated from the Go source", a.Source)
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
