package schema

import (
	"encoding/json"
	"fmt"
	"go/token"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Cyenoch/cordis/contract"
)

const DefinitionSchema = "cordis-go.contract/v1"

// Definition is the explicit .schema.json input to Generate. It is not a Go
// interface description: only data schemas and declared service methods cross ABI.
type Definition struct {
	Schema   string    `json:"schema"`
	Services []Service `json:"services"`
}

type Service struct {
	Name    string   `json:"name"`
	Major   uint32   `json:"major"`
	Minor   uint32   `json:"minor"`
	Methods []Method `json:"methods"`
}

type Method struct {
	Name     string          `json:"name"`
	Request  json.RawMessage `json:"request"`
	Response json.RawMessage `json:"response"`
}

// Parse strictly decodes and validates a service definition document.
func Parse(data []byte) (Definition, error) {
	if !utf8.Valid(data) {
		return Definition{}, field("", "json", "invalid UTF-8")
	}
	var definition Definition
	if err := contract.Decode(data, &definition); err != nil {
		return Definition{}, fmt.Errorf("decode service definition: %w", err)
	}
	var document struct {
		Services []map[string]json.RawMessage `json:"services"`
	}
	if err := json.Unmarshal(data, &document); err != nil {
		return Definition{}, fmt.Errorf("decode definition metadata: %w", err)
	}
	for i, service := range document.Services {
		for _, key := range []string{"name", "major", "minor", "methods"} {
			if value, exists := service[key]; exists && isNull(value) {
				return Definition{}, field(fmt.Sprintf("/services/%d/%s", i, key), "type", "null metadata is not allowed")
			}
		}
	}
	if err := definition.Check(); err != nil {
		return Definition{}, err
	}
	return definition, nil
}

// Check checks every declaration, including collisions in generated Go names.
func (d Definition) Check() error {
	if d.Schema != DefinitionSchema {
		return field("/schema", "schema", "expected "+DefinitionSchema)
	}
	if len(d.Services) == 0 {
		return field("/services", "required", "at least one service is required")
	}
	services := map[string]bool{}
	goNames := map[string]bool{"Caller": true, "Registrar": true}
	for i, service := range d.Services {
		path := fmt.Sprintf("/services/%d", i)
		name, err := exportedName(service.Name)
		if err != nil {
			return field(pointer(path, "name"), "name", err.Error())
		}
		key := fmt.Sprintf("%s@%d", service.Name, service.Major)
		if services[key] {
			return field(path, "name", "duplicate service and major")
		}
		services[key] = true
		if service.Major == 0 {
			return field(pointer(path, "major"), "major", "service major must be positive")
		}
		if len(service.Methods) == 0 {
			return field(pointer(path, "methods"), "required", "at least one method is required")
		}
		for _, symbol := range []string{name + "Client", name + "Server", "New" + name + "Client", "Register" + name + "Server"} {
			if goNames[symbol] {
				return field(path, "name", "generated Go identifier collision: "+symbol)
			}
			goNames[symbol] = true
		}
		methods := map[string]bool{}
		for j, method := range service.Methods {
			mp := fmt.Sprintf("%s/methods/%d", path, j)
			mn, err := exportedName(method.Name)
			if err != nil {
				return field(pointer(mp, "name"), "name", err.Error())
			}
			if methods[mn] {
				return field(mp, "name", "duplicate or colliding method name")
			}
			methods[mn] = true
			for _, schema := range []struct {
				name string
				raw  json.RawMessage
			}{{"request", method.Request}, {"response", method.Response}} {
				if err := Check(schema.raw); err != nil {
					return fmt.Errorf("%s/%s: %w", mp, schema.name, err)
				}
			}
		}
	}
	return nil
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
