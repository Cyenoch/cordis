package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

func parseYAML(source string, data []byte) (*value, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	var document yaml.Node
	// The dependency builds the document AST before our depth/collection checks.
	if err := dec.Decode(&document); err != nil {
		return nil, yamlSyntaxError(source, err)
	}
	root, err := yamlValue(source, document.Content[0], "", 0)
	if err != nil {
		return nil, err
	}
	var trailing yaml.Node
	if err := dec.Decode(&trailing); err != io.EOF {
		if err != nil {
			return nil, yamlSyntaxError(source, err)
		}
		pos := Position{Line: trailing.Line, Column: trailing.Column}
		return nil, sourceError(source, "", pos, errors.New("trailing YAML document"))
	}
	return root, nil
}

var yamlLine = regexp.MustCompile(`line ([0-9]+)`)

func yamlSyntaxError(source string, err error) error {
	pos := Position{Line: 1, Column: 1}
	if match := yamlLine.FindStringSubmatch(err.Error()); match != nil {
		pos.Line, _ = strconv.Atoi(match[1])
		pos.Column = 0 // yaml.v3 syntax errors expose lines, not columns.
	}
	return sourceError(source, "", pos, err)
}

func yamlNodeError(n *yaml.Node) error {
	if n.Kind == yaml.AliasNode || n.Anchor != "" {
		return errors.New("YAML aliases and anchors are not supported")
	}
	switch n.Tag {
	case "!!map", "!!seq", "!!str", "!!null", "!!bool", "!!int", "!!float":
		return nil
	case "!!timestamp":
		return errors.New("YAML dates and times must be quoted strings")
	case "!!merge":
		return errors.New("YAML merge keys are not supported")
	default:
		return fmt.Errorf("unsupported YAML tag %q", n.Tag)
	}
}

func yamlValue(source string, n *yaml.Node, path string, depth int) (*value, error) {
	pos := Position{Line: n.Line, Column: n.Column}
	fail := func(err error) (*value, error) { return nil, sourceError(source, path, pos, err) }
	if err := yamlNodeError(n); err != nil {
		return fail(err)
	}
	size := len(n.Content)
	if n.Kind == yaml.MappingNode {
		size /= 2
	}
	if err := checkLimits(depth, size); err != nil {
		return fail(err)
	}
	switch n.Kind {
	case yaml.MappingNode:
		if n.Tag != "!!map" {
			return fail(errors.New("YAML mapping must have a map tag"))
		}
		v := objectValue(pos)
		for i := 0; i < len(n.Content); i += 2 {
			key := n.Content[i]
			keyPos := Position{Line: key.Line, Column: key.Column}
			keyPath := childPath(path, key.Value)
			if err := yamlNodeError(key); err != nil {
				return nil, sourceError(source, keyPath, keyPos, err)
			}
			if key.Kind != yaml.ScalarNode || key.Tag != "!!str" {
				return nil, sourceError(source, path, keyPos, errors.New("YAML object keys must be strings"))
			}
			keyJSON, err := yamlScalar(key)
			if err != nil {
				return nil, sourceError(source, path, keyPos, err)
			}
			if keyJSON[0] != '"' {
				return nil, sourceError(source, path, keyPos, errors.New("YAML object keys must be strings"))
			}
			if _, exists := v.object[key.Value]; exists {
				return nil, sourceError(source, keyPath, keyPos, fmt.Errorf("duplicate YAML key %q", key.Value))
			}
			item, err := yamlValue(source, n.Content[i+1], keyPath, depth+1)
			if err != nil {
				return nil, err
			}
			item.pos = keyPos
			v.object[key.Value] = item
		}
		return v, nil
	case yaml.SequenceNode:
		if n.Tag != "!!seq" {
			return fail(errors.New("YAML sequence must have a seq tag"))
		}
		v := arrayValue(pos)
		for i, node := range n.Content {
			item, err := yamlValue(source, node, childPath(path, strconv.Itoa(i)), depth+1)
			if err != nil {
				return nil, err
			}
			v.array = append(v.array, item)
		}
		return v, nil
	case yaml.ScalarNode:
		raw, err := yamlScalar(n)
		if err != nil {
			return fail(err)
		}
		return &value{raw: raw, pos: pos}, nil
	default:
		return fail(errors.New("unsupported YAML node"))
	}
}

func yamlScalar(n *yaml.Node) ([]byte, error) {
	switch n.Tag {
	case "!!str":
		// The resolver labels overflowing numbers as strings. Keep their exact
		// numeric tokens instead, but never reinterpret quoted or tagged strings.
		if n.Style == 0 {
			if yamlInteger.MatchString(n.Value) {
				return integerToken(n.Value, true)
			}
			unsigned := strings.TrimLeft(n.Value, "+-")
			if strings.HasPrefix(unsigned, "0x") || strings.HasPrefix(unsigned, "0o") || strings.HasPrefix(unsigned, "0b") {
				if raw, err := integerToken(n.Value, true); err == nil {
					return raw, nil
				}
			}
			if yamlDecimal.MatchString(n.Value) {
				return decimalToken(n.Value, true)
			}
		}
		return stringValue(n.Value, Position{}).raw, nil
	case "!!null":
		switch n.Value {
		case "", "~", "null", "Null", "NULL":
			return []byte("null"), nil
		}
	case "!!bool":
		switch strings.ToLower(n.Value) {
		case "true", "false":
			return []byte(strings.ToLower(n.Value)), nil
		}
	case "!!int":
		return integerToken(n.Value, true)
	case "!!float":
		return decimalToken(n.Value, true)
	}
	return nil, fmt.Errorf("invalid YAML scalar %q for tag %s", n.Value, n.Tag)
}
