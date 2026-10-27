package schema

import (
	"encoding/json"
	"fmt"
	"go/token"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Cyenoch/cordis/contract"
)

const DefinitionSchema = "cordis-go.contract/v1"

// Definition is the single author declaration shared by generation and packaging.
type Definition struct {
	Schema      string                     `json:"schema"`
	Types       map[string]json.RawMessage `json:"types,omitempty"`
	Services    []Service                  `json:"services,omitempty"`
	Definitions []PluginDefinition         `json:"definitions,omitempty"`
}

type PluginDefinition struct {
	ID       string                `json:"id"`
	Config   json.RawMessage       `json:"config,omitempty"`
	Provides []string              `json:"provides,omitempty"`
	Requires []ConsumerDeclaration `json:"requires,omitempty"`
}

type ConsumerDeclaration struct {
	Name     string `json:"name"`
	Service  string `json:"service"`
	MinMinor uint32 `json:"minMinor"`
	MaxMinor uint32 `json:"maxMinor"`
}

type Service struct {
	Name    string   `json:"name"`
	Major   uint32   `json:"-"`
	Minor   uint32   `json:"-"`
	Methods []Method `json:"methods"`
}

type Method struct {
	Name     string          `json:"name"`
	Request  json.RawMessage `json:"request"`
	Response json.RawMessage `json:"response"`
}

func (s Service) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Name    string   `json:"name"`
		Version string   `json:"version"`
		Methods []Method `json:"methods"`
	}{Name: s.Name, Version: fmt.Sprintf("%d.%d", s.Major, s.Minor), Methods: s.Methods})
}

func (s *Service) UnmarshalJSON(raw []byte) error {
	var wire struct {
		Name    string          `json:"name"`
		Version json.RawMessage `json:"version"`
		Methods []Method        `json:"methods"`
	}
	if err := contract.Decode(raw, &wire); err != nil {
		return err
	}
	var version string
	if len(wire.Version) > 0 && wire.Version[0] == '"' {
		if err := json.Unmarshal(wire.Version, &version); err != nil {
			return err
		}
		if strings.Count(version, ".") != 1 {
			return fmt.Errorf("service version must be major.minor without a patch")
		}
	} else {
		var major uint32
		if err := json.Unmarshal(wire.Version, &major); err != nil || isNull(wire.Version) {
			return fmt.Errorf("service version must be an integer major or a major.minor string")
		}
		version = strconv.FormatUint(uint64(major), 10) + ".0"
	}
	parts := strings.Split(version, ".")
	numbers := [2]uint32{}
	for i, part := range parts {
		n, err := strconv.ParseUint(part, 10, 32)
		if err != nil || strconv.FormatUint(n, 10) != part {
			return fmt.Errorf("service version requires canonical uint32 components")
		}
		numbers[i] = uint32(n)
	}
	if numbers[0] == 0 {
		return fmt.Errorf("service major must be positive")
	}
	*s = Service{Name: wire.Name, Major: numbers[0], Minor: numbers[1], Methods: wire.Methods}
	return nil
}

// Parse strictly decodes and validates an author declaration, not a machine Manifest.
func Parse(data []byte) (Definition, error) {
	if !utf8.Valid(data) {
		return Definition{}, field("", "json", "invalid UTF-8")
	}
	var d Definition
	if err := contract.Decode(data, &d); err != nil {
		return Definition{}, fmt.Errorf("decode declaration: %w", err)
	}
	if err := rejectNullMetadata(data, ""); err != nil {
		return Definition{}, err
	}
	if err := d.Check(); err != nil {
		return Definition{}, err
	}
	return d, nil
}

func rejectNullMetadata(raw json.RawMessage, path string) error {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return err
	}
	for _, key := range sortedKeys(obj) {
		if isNull(obj[key]) {
			return field(pointer(path, key), "type", "null metadata is not allowed")
		}
	}
	for _, key := range []string{"services", "definitions", "requires", "methods"} {
		if value, ok := obj[key]; ok {
			var children []json.RawMessage
			if err := json.Unmarshal(value, &children); err != nil {
				return err
			}
			for i, child := range children {
				childPath := pointer(pointer(path, key), strconv.Itoa(i))
				if err := rejectNullMetadata(child, childPath); err != nil {
					return err
				}
				if key == "requires" {
					var consumer map[string]json.RawMessage
					if err := json.Unmarshal(child, &consumer); err != nil {
						return err
					}
					for _, required := range []string{"name", "service", "minMinor", "maxMinor"} {
						if _, exists := consumer[required]; !exists {
							return field(pointer(childPath, required), "required", "consumer requires an explicit name, service and closed minor range")
						}
					}
				}
			}
		}
	}
	return nil
}

// Check validates the entire graph. GenerateMany additionally checks all emitted symbols.
func (d Definition) Check() error { _, err := d.normalized(); return err }

func (d Definition) normalized() (Definition, error) {
	if d.Schema != DefinitionSchema {
		return Definition{}, field("/schema", "schema", "expected "+DefinitionSchema)
	}
	if len(d.Services) == 0 && len(d.Definitions) == 0 && len(d.Types) == 0 {
		return Definition{}, field("", "required", "at least one type, service or definition is required")
	}
	out := Definition{Schema: d.Schema, Types: map[string]json.RawMessage{}, Services: []Service{}, Definitions: []PluginDefinition{}}
	refs := resolver{types: d.Types, active: map[string]bool{}}
	resolve := func(raw json.RawMessage) (json.RawMessage, error) {
		expanded, err := refs.resolve(raw)
		if err != nil {
			return nil, err
		}
		compiled, err := Compile(expanded)
		if err != nil {
			return nil, err
		}
		return compiled.root.raw, nil
	}
	for _, name := range sortedKeys(d.Types) {
		if _, err := exportedName(name); err != nil {
			return Definition{}, fmt.Errorf("type %q: %w", name, err)
		}
		raw, err := resolve(d.Types[name])
		if err != nil {
			return Definition{}, fmt.Errorf("type %q: %w", name, err)
		}
		out.Types[name] = raw
	}
	services := map[string]Service{}
	goNames := map[string]bool{}
	for _, s := range d.Services {
		name, err := exportedName(s.Name)
		if err != nil {
			return Definition{}, err
		}
		if _, ok := services[s.Name]; ok {
			return Definition{}, fmt.Errorf("duplicate service %q", s.Name)
		}
		if goNames[name] {
			return Definition{}, fmt.Errorf("colliding service name %q", s.Name)
		}
		goNames[name] = true
		if s.Major == 0 || len(s.Methods) == 0 {
			return Definition{}, fmt.Errorf("service %q requires positive major and methods", s.Name)
		}
		methods := []Method{}
		seen := map[string]bool{}
		for _, m := range s.Methods {
			mn, err := exportedName(m.Name)
			if err != nil {
				return Definition{}, err
			}
			if seen[mn] {
				return Definition{}, fmt.Errorf("duplicate or colliding method %q", m.Name)
			}
			seen[mn] = true
			request, err := resolve(m.Request)
			if err != nil {
				return Definition{}, fmt.Errorf("service %s method %s request: %w", s.Name, m.Name, err)
			}
			response, err := resolve(m.Response)
			if err != nil {
				return Definition{}, fmt.Errorf("service %s method %s response: %w", s.Name, m.Name, err)
			}
			methods = append(methods, Method{Name: m.Name, Request: request, Response: response})
		}
		sort.Slice(methods, func(i, j int) bool { return methods[i].Name < methods[j].Name })
		s.Methods = methods
		services[s.Name] = s
		out.Services = append(out.Services, s)
	}
	sort.Slice(out.Services, func(i, j int) bool { return out.Services[i].Name < out.Services[j].Name })
	ids := map[string]bool{}
	for _, pd := range d.Definitions {
		if _, err := exportedName(pd.ID); err != nil {
			return Definition{}, err
		}
		if ids[pd.ID] {
			return Definition{}, fmt.Errorf("duplicate definition %q", pd.ID)
		}
		ids[pd.ID] = true
		if len(pd.Config) == 0 {
			pd.Config = json.RawMessage(`{"type":"object"}`)
		}
		config, err := resolve(pd.Config)
		if err != nil {
			return Definition{}, fmt.Errorf("definition %s config: %w", pd.ID, err)
		}
		pd.Config = config
		pd.Provides = append([]string{}, pd.Provides...)
		pd.Requires = append([]ConsumerDeclaration{}, pd.Requires...)
		seen := map[string]bool{}
		for _, ref := range pd.Provides {
			if _, ok := services[ref]; !ok {
				return Definition{}, fmt.Errorf("definition %s: unknown provided service %q", pd.ID, ref)
			}
			if seen[ref] {
				return Definition{}, fmt.Errorf("duplicate provided service %q", ref)
			}
			seen[ref] = true
		}
		seen = map[string]bool{}
		routes := map[string]bool{}
		for _, c := range pd.Requires {
			if _, err := exportedName(c.Name); err != nil {
				return Definition{}, err
			}
			service, ok := services[c.Service]
			if !ok {
				return Definition{}, fmt.Errorf("unknown required service %q", c.Service)
			}
			if seen[c.Name] || routes[c.Service] {
				return Definition{}, fmt.Errorf("duplicate consumer name or service %q", c.Name)
			}
			seen[c.Name] = true
			routes[c.Service] = true
			if c.MinMinor > c.MaxMinor || service.Minor < c.MinMinor || service.Minor > c.MaxMinor {
				return Definition{}, fmt.Errorf("consumer %s: range must contain declared client minor", c.Name)
			}
		}
		sort.Strings(pd.Provides)
		sort.Slice(pd.Requires, func(i, j int) bool { return pd.Requires[i].Name < pd.Requires[j].Name })
		out.Definitions = append(out.Definitions, pd)
	}
	sort.Slice(out.Definitions, func(i, j int) bool { return out.Definitions[i].ID < out.Definitions[j].ID })
	return out, nil
}

// Specs returns fresh machine metadata derived from the validated declaration graph.
func (d Definition) Specs() ([]contract.DefinitionSpec, error) {
	normalized, err := d.normalized()
	if err != nil {
		return nil, err
	}
	return normalized.specs(), nil
}

func (d Definition) specs() []contract.DefinitionSpec {
	services := map[string]Service{}
	for _, s := range d.Services {
		services[s.Name] = s
	}
	specs := make([]contract.DefinitionSpec, 0, len(d.Definitions))
	for _, pd := range d.Definitions {
		spec := contract.DefinitionSpec{ID: pd.ID, ConfigSchema: append(json.RawMessage{}, pd.Config...), Requires: []contract.Consumer{}, Provides: []contract.Service{}}
		for _, name := range pd.Provides {
			s := services[name]
			spec.Provides = append(spec.Provides, contract.Service{Name: s.Name, Major: s.Major, Minor: s.Minor})
		}
		for _, c := range pd.Requires {
			s := services[c.Service]
			spec.Requires = append(spec.Requires, s.consumer(c.MinMinor, c.MaxMinor))
		}
		specs = append(specs, spec)
	}
	return specs
}

// Fingerprint is a deterministic SHA-256 of the normalized graph, not source bytes.
func (d Definition) Fingerprint() (string, error) {
	normalized, err := d.normalized()
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(normalized)
	if err != nil {
		return "", err
	}
	return fingerprint(raw), nil
}

func (s Service) methodFingerprint() string {
	raw, _ := json.Marshal(struct {
		Name    string   `json:"name"`
		Major   uint32   `json:"major"`
		Methods []Method `json:"methods"`
	}{Name: s.Name, Major: s.Major, Methods: s.Methods})
	return fingerprint(raw)
}

func (s Service) consumer(minMinor, maxMinor uint32) contract.Consumer {
	return contract.Consumer{Requirement: contract.Requirement{Name: s.Name, Major: s.Major, MinMinor: minMinor, MaxMinor: maxMinor}, Contract: s.methodFingerprint()}
}

func exportedName(value string) (string, error) {
	if strings.TrimSpace(value) != value || value == "" {
		return "", fmt.Errorf("name must be nonempty without surrounding whitespace")
	}
	var result strings.Builder
	upper := true
	for _, r := range value {
		if r == '-' || r == '_' || r == '.' || r == '/' || r == ':' {
			upper = true
			continue
		}
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			return "", fmt.Errorf("name contains unsupported character")
		}
		if upper {
			r = unicode.ToUpper(r)
			upper = false
		}
		result.WriteRune(r)
	}
	name := result.String()
	first, _ := utf8.DecodeRuneInString(name)
	if !token.IsIdentifier(name) || !unicode.IsUpper(first) {
		return "", fmt.Errorf("name does not form an exported Go identifier")
	}
	return name, nil
}
