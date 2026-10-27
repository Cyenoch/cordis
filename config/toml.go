package config

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/pelletier/go-toml/v2/unstable"
)

type tableState uint8

const (
	implicitTable tableState = iota
	explicitTable
	dottedTable
	inlineTable
)

type tomlParser struct {
	source    string
	positions sourcePositions
	parser    unstable.Parser
}

func parseTOML(source string, data []byte) (*value, error) {
	p := tomlParser{source: source, positions: indexPositions(data)}
	p.parser.Reset(data)
	root := objectValue(Position{Line: 1, Column: 1})
	current, currentPath := root, ""
	for p.parser.NextExpression() {
		n := p.parser.Expression()
		var err error
		switch n.Kind {
		case unstable.KeyValue:
			_, err = p.assign(current, currentPath, n)
		case unstable.Table, unstable.ArrayTable:
			current, currentPath, err = p.table(root, n)
		}
		if err != nil {
			return nil, err
		}
	}
	if err := p.parser.Error(); err != nil {
		pos := Position{Line: 1, Column: 1}
		var syntax *unstable.ParserError
		if errors.As(err, &syntax) {
			pos = p.positions.at(int(p.parser.Range(syntax.Highlight).Offset))
		}
		cause := fmt.Errorf("parse TOML (use YAML or JSON for null or non-object roots): %w", err)
		return nil, sourceError(source, "", pos, cause)
	}
	return root, nil
}

func (p *tomlParser) position(n *unstable.Node) Position {
	raw := n.Raw
	// Some node kinds expose only Data, or a child key, in this AST version.
	if raw.Length == 0 {
		if len(n.Data) > 0 {
			raw = p.parser.Range(n.Data)
		} else if n.Child() != nil {
			return p.position(n.Child())
		}
	}
	return p.positions.at(int(raw.Offset))
}

func (p *tomlParser) fail(path string, n *unstable.Node, err error) error {
	return sourceError(p.source, path, p.position(n), err)
}

func pointerDepth(path string) int {
	depth := 0
	for _, c := range path {
		if c == '/' {
			depth++
		}
	}
	return depth
}

func (p *tomlParser) add(parent *value, key string, child *value, path string, n *unstable.Node) error {
	if err := checkLimits(pointerDepth(path), 0); err != nil {
		return p.fail(path, n, err)
	}
	if err := checkLimits(0, len(parent.object)+1); err != nil {
		return p.fail(path, n, err)
	}
	parent.object[key] = child
	return nil
}

func (p *tomlParser) assign(parent *value, path string, n *unstable.Node) (int, error) {
	keys := n.Key()
	for keys.Next() {
		keyNode := keys.Node()
		key := string(keyNode.Data)
		path = childPath(path, key)
		existing := parent.object[key]
		if keys.IsLast() {
			if existing != nil {
				return 0, p.fail(path, keyNode, fmt.Errorf("duplicate TOML key %q", key))
			}
			offset := p.skipTrivia(int(keyNode.Raw.Offset + keyNode.Raw.Length))
			offset = p.skipTrivia(offset + 1) // The AST has already verified '='.
			item, end, err := p.value(n.Value(), path, offset)
			if err != nil {
				return 0, err
			}
			item.pos = p.position(keyNode)
			return end, p.add(parent, key, item, path, keyNode)
		}
		if existing == nil {
			existing = objectValue(p.position(keyNode))
			existing.table = dottedTable
			if err := p.add(parent, key, existing, path, keyNode); err != nil {
				return 0, err
			}
		} else if existing.kind != '{' || existing.table != dottedTable && existing.table != implicitTable {
			return 0, p.fail(path, keyNode, fmt.Errorf("TOML dotted key redefines %q", key))
		}
		parent = existing
	}
	return 0, nil
}

func (p *tomlParser) table(root *value, n *unstable.Node) (*value, string, error) {
	parent, path := root, ""
	keys := n.Key()
	for keys.Next() {
		keyNode := keys.Node()
		key := string(keyNode.Data)
		path = childPath(path, key)
		existing := parent.object[key]
		if !keys.IsLast() {
			if existing == nil {
				existing = objectValue(p.position(keyNode))
				if err := p.add(parent, key, existing, path, keyNode); err != nil {
					return nil, path, err
				}
			}
			if existing.arrayTable {
				path = childPath(path, strconv.Itoa(len(existing.array)-1))
				existing = existing.array[len(existing.array)-1]
			}
			if existing.kind != '{' || existing.table == inlineTable {
				return nil, path, p.fail(path, keyNode, fmt.Errorf("TOML key %q is not an open table", key))
			}
			parent = existing
			continue
		}
		if n.Kind == unstable.ArrayTable {
			if existing == nil {
				existing = arrayValue(p.position(keyNode))
				existing.arrayTable = true
				if err := p.add(parent, key, existing, path, keyNode); err != nil {
					return nil, path, err
				}
			}
			if !existing.arrayTable {
				return nil, path, p.fail(path, keyNode, fmt.Errorf("TOML key %q is already defined", key))
			}
			path = childPath(path, strconv.Itoa(len(existing.array)))
			if err := checkLimits(pointerDepth(path), len(existing.array)+1); err != nil {
				return nil, path, p.fail(path, keyNode, err)
			}
			item := objectValue(p.position(keyNode))
			item.table = explicitTable
			existing.array = append(existing.array, item)
			return item, path, nil
		}
		if existing == nil {
			existing = objectValue(p.position(keyNode))
			if err := p.add(parent, key, existing, path, keyNode); err != nil {
				return nil, path, err
			}
		} else if existing.kind != '{' || existing.table != implicitTable {
			return nil, path, p.fail(path, keyNode, fmt.Errorf("duplicate or conflicting TOML table %q", key))
		}
		existing.table = explicitTable
		existing.pos = p.position(keyNode)
		return existing, path, nil
	}
	return nil, path, p.fail(path, n, errors.New("missing TOML table key"))
}

// Array nodes have no Raw range in this AST version. Carry their source span
// through the validated children so even an empty nested array has a location.
func (p *tomlParser) value(n *unstable.Node, path string, start int) (*value, int, error) {
	pos := p.positions.at(start)
	if err := checkLimits(pointerDepth(path), 0); err != nil {
		return nil, 0, sourceError(p.source, path, pos, err)
	}
	end := start + len(n.Data)
	if n.Raw.Length > 0 {
		end = int(n.Raw.Offset + n.Raw.Length)
	}
	var raw []byte
	var err error
	switch n.Kind {
	case unstable.String:
		return stringValue(string(n.Data), pos), end, nil
	case unstable.Bool:
		raw = n.Data
	case unstable.Integer:
		raw, err = integerToken(string(n.Data), false)
	case unstable.Float:
		raw, err = decimalToken(string(n.Data), false)
	case unstable.LocalDate, unstable.LocalTime, unstable.LocalDateTime, unstable.DateTime:
		err = errors.New("TOML dates and times must be quoted strings")
	case unstable.Array:
		v := arrayValue(pos)
		offset := start + 1
		children := n.Children()
		// The dependency allocates this expression's AST before these checks.
		for children.Next() {
			child := children.Node()
			if err := checkLimits(0, len(v.array)+1); err != nil {
				return nil, 0, sourceError(p.source, path, pos, err)
			}
			offset = p.skipTrivia(offset)
			item, next, err := p.value(child, childPath(path, strconv.Itoa(len(v.array))), offset)
			if err != nil {
				return nil, 0, err
			}
			offset = next
			v.array = append(v.array, item)
		}
		return v, p.skipTrivia(offset) + 1, nil
	case unstable.InlineTable:
		v := objectValue(pos)
		offset := start + 1
		children := n.Children()
		for children.Next() {
			var err error
			offset, err = p.assign(v, path, children.Node())
			if err != nil {
				return nil, 0, err
			}
		}
		v.table = inlineTable
		return v, p.skipTrivia(offset) + 1, nil
	default:
		err = fmt.Errorf("unsupported TOML node %s", n.Kind)
	}
	if err != nil {
		return nil, 0, sourceError(p.source, path, pos, err)
	}
	return &value{raw: raw, pos: pos}, end, nil
}

func (p *tomlParser) skipTrivia(offset int) int {
	data := p.parser.Data()
	for offset < len(data) {
		switch data[offset] {
		case ' ', '\t', '\r', '\n', ',':
			offset++
		case '#':
			for offset < len(data) && data[offset] != '\n' {
				offset++
			}
		default:
			return offset
		}
	}
	return offset
}
