package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
)

type jsonParser struct {
	source    string
	data      []byte
	dec       *json.Decoder
	positions sourcePositions
}

func parseJSON(source string, data []byte) (*value, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	p := jsonParser{source: source, data: data, dec: dec, positions: indexPositions(data)}
	root, err := p.value("", 0)
	if err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, p.errorAt("", p.nextPosition(), errors.New("trailing JSON data"))
	}
	return root, nil
}

func (p *jsonParser) nextPosition() Position {
	offset := int(p.dec.InputOffset())
	for offset < len(p.data) {
		switch p.data[offset] {
		case ' ', '\t', '\r', '\n', ':', ',':
			offset++
		default:
			return p.positions.at(offset)
		}
	}
	return p.positions.at(offset)
}

func (p *jsonParser) errorAt(path string, pos Position, err error) error {
	var syntax *json.SyntaxError
	if errors.As(err, &syntax) && syntax.Offset > 0 && syntax.Offset <= int64(len(p.data)) {
		pos = p.positions.at(int(syntax.Offset) - 1)
	}
	return sourceError(p.source, path, pos, err)
}

func (p *jsonParser) value(path string, depth int) (*value, error) {
	pos := p.nextPosition()
	if err := checkLimits(depth, 0); err != nil {
		return nil, p.errorAt(path, pos, err)
	}
	token, err := p.dec.Token()
	if err != nil {
		return nil, p.errorAt(path, pos, err)
	}
	switch token := token.(type) {
	case json.Delim:
		var v *value
		if token == '{' {
			v = objectValue(pos)
		} else if token == '[' {
			v = arrayValue(pos)
		} else {
			return nil, p.errorAt(path, pos, fmt.Errorf("unexpected JSON delimiter %q", token))
		}
		for p.dec.More() {
			if err := checkLimits(depth, v.size()+1); err != nil {
				return nil, p.errorAt(path, p.nextPosition(), err)
			}
			itemPath := childPath(path, strconv.Itoa(v.size()))
			key := ""
			keyPos := p.nextPosition()
			if token == '{' {
				keyToken, err := p.dec.Token()
				if err != nil {
					return nil, p.errorAt(path, keyPos, err)
				}
				key = keyToken.(string)
				itemPath = childPath(path, key)
				if _, exists := v.object[key]; exists {
					return nil, p.errorAt(itemPath, keyPos, fmt.Errorf("duplicate JSON key %q", key))
				}
			}
			item, err := p.value(itemPath, depth+1)
			if err != nil {
				return nil, err
			}
			if token == '{' {
				item.pos = keyPos
				v.object[key] = item
			} else {
				v.array = append(v.array, item)
			}
		}
		if _, err := p.dec.Token(); err != nil {
			return nil, p.errorAt(path, p.nextPosition(), err)
		}
		return v, nil
	case string:
		return stringValue(token, pos), nil
	case json.Number:
		return &value{raw: []byte(token), pos: pos}, nil
	default:
		raw, _ := json.Marshal(token)
		return &value{raw: raw, pos: pos}, nil
	}
}
