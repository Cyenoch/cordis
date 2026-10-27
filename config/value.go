package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
)

// Scalar tokens stay JSON bytes; no decoded floating-point intermediary exists.
type value struct {
	kind       byte
	raw        []byte
	object     map[string]*value
	array      []*value
	pos        Position
	table      tableState
	arrayTable bool
}

func objectValue(pos Position) *value {
	return &value{kind: '{', object: make(map[string]*value), pos: pos}
}

func arrayValue(pos Position) *value {
	return &value{kind: '[', array: make([]*value, 0), pos: pos}
}

func stringValue(text string, pos Position) *value {
	raw, _ := json.Marshal(text)
	return &value{raw: raw, pos: pos}
}

func (v *value) writeJSON(out *bytes.Buffer, doc *Document, path string, depth int) error {
	if err := checkLimits(depth, v.size()); err != nil {
		return sourceError(doc.Source, path, v.pos, err)
	}
	doc.Positions[path] = v.pos
	switch v.kind {
	case '{':
		out.WriteByte('{')
		keys := make([]string, 0, len(v.object))
		for key := range v.object {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for i, key := range keys {
			if i > 0 {
				out.WriteByte(',')
			}
			encoded, _ := json.Marshal(key)
			out.Write(encoded)
			out.WriteByte(':')
			if err := v.object[key].writeJSON(out, doc, childPath(path, key), depth+1); err != nil {
				return err
			}
		}
		out.WriteByte('}')
	case '[':
		out.WriteByte('[')
		for i, item := range v.array {
			if i > 0 {
				out.WriteByte(',')
			}
			if err := item.writeJSON(out, doc, childPath(path, strconv.Itoa(i)), depth+1); err != nil {
				return err
			}
		}
		out.WriteByte(']')
	default:
		out.Write(v.raw)
	}
	return nil
}

func (v *value) size() int {
	if v.kind == '{' {
		return len(v.object)
	}
	return len(v.array)
}

func checkLimits(depth, size int) error {
	if depth > maxDepth {
		return fmt.Errorf("configuration nesting exceeds %d", maxDepth)
	}
	if size > maxCollection {
		return fmt.Errorf("configuration collection exceeds %d entries", maxCollection)
	}
	return nil
}
