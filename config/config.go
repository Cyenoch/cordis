// Package config converts strict TOML, YAML and JSON files to JSON data.
// It does not apply schemas, defaults, includes or environment substitutions.
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/Cyenoch/cordis/contract"
)

// Format selects a parser; an empty format uses the source extension only.
type Format string

const (
	TOML Format = "toml"
	YAML Format = "yaml"
	JSON Format = "json"
)

const (
	maxBytes      = 1 << 20
	maxDepth      = 32
	maxCollection = 10000
)

// Position is a one-based source location.
type Position struct {
	Line, Column int
}

// Document contains exact JSON and source locations keyed by JSON Pointer.
// The empty pointer identifies the root.
type Document struct {
	JSON      json.RawMessage
	Source    string
	Positions map[string]Position
}

// Error associates a failure with its source and JSON Pointer, when known.
type Error struct {
	Source, Path string
	Line, Column int
	Cause        error
}

func (e *Error) Error() string {
	where := e.Source
	if e.Line > 0 {
		where += ":" + strconv.Itoa(e.Line)
		if e.Column > 0 {
			where += ":" + strconv.Itoa(e.Column)
		}
	}
	if e.Path != "" {
		where += " (" + e.Path + ")"
	}
	return where + ": " + e.Cause.Error()
}

func (e *Error) Unwrap() error { return e.Cause }

// Parse converts a bounded source to JSON without rounding numeric tokens.
// Sources without a known extension, including stdin, need an explicit format.
func Parse(source string, data []byte, format Format) (*Document, error) {
	fail := func(err error) (*Document, error) {
		return nil, sourceError(source, "", Position{Line: 1, Column: 1}, err)
	}
	if len(data) > maxBytes {
		return fail(fmt.Errorf("configuration exceeds %d bytes", maxBytes))
	}
	if !utf8.Valid(data) {
		return fail(errors.New("configuration is not valid UTF-8"))
	}
	if format == "" {
		switch strings.ToLower(filepath.Ext(source)) {
		case ".toml":
			format = TOML
		case ".yaml", ".yml":
			format = YAML
		case ".json":
			format = JSON
		default:
			return fail(errors.New("unknown source extension; specify toml, yaml or json explicitly"))
		}
	}
	var root *value
	var err error
	switch format {
	case TOML:
		root, err = parseTOML(source, data)
	case YAML:
		root, err = parseYAML(source, data)
	case JSON:
		root, err = parseJSON(source, data)
	default:
		return fail(fmt.Errorf("unsupported configuration format %q", format))
	}
	if err != nil {
		return nil, err
	}
	doc := &Document{Source: source, Positions: make(map[string]Position)}
	var out bytes.Buffer
	root.writeJSON(&out, doc, "")
	doc.JSON = out.Bytes()
	return doc, nil
}

// Load reads at most 1 MiB plus one byte to detect an oversized file.
// It never interprets a path as stdin.
func Load(path string, format Format) (*Document, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, sourceError(path, "", Position{}, err)
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxBytes+1))
	if err != nil {
		return nil, sourceError(path, "", Position{}, err)
	}
	return Parse(path, data, format)
}

// Decode applies contract.Decode, including its unknown-field checks.
// Location matching is best effort when a codec returns no structured path.
func (d *Document) Decode(target any) error {
	err := contract.Decode(d.JSON, target)
	if err == nil {
		return nil
	}
	path := ""
	var unknown *contract.UnknownFieldError
	if errors.As(err, &unknown) {
		path = d.uniqueField(unknown.Field)
	}
	var typeError *json.UnmarshalTypeError
	if path == "" && errors.As(err, &typeError) && typeError.Field != "" {
		fields := strings.Split(typeError.Field, ".")
		path = d.uniqueField(fields[len(fields)-1])
	}
	return d.Locate(path, err)
}

func (d *Document) uniqueField(name string) string {
	suffix := "/" + escapePointer(name)
	found := ""
	for path := range d.Positions {
		if strings.HasSuffix(path, suffix) {
			if found != "" {
				return ""
			}
			found = path
		}
	}
	return found
}

// Locate wraps cause at path, falling back to the nearest recorded ancestor.
// It retains the requested path, even when only its ancestor has a position.
func (d *Document) Locate(path string, cause error) error {
	if cause == nil {
		return nil
	}
	lookup := path
	for {
		if pos, ok := d.Positions[lookup]; ok {
			return sourceError(d.Source, path, pos, cause)
		}
		if lookup == "" {
			return sourceError(d.Source, path, Position{}, cause)
		}
		if index := strings.LastIndex(lookup, "/"); index >= 0 {
			lookup = lookup[:index]
		} else {
			lookup = ""
		}
	}
}

func sourceError(source, path string, pos Position, cause error) error {
	return &Error{Source: source, Path: path, Line: pos.Line, Column: pos.Column, Cause: cause}
}

func escapePointer(key string) string {
	return strings.ReplaceAll(strings.ReplaceAll(key, "~", "~0"), "/", "~1")
}

func childPath(path, key string) string { return path + "/" + escapePointer(key) }

type sourcePositions struct {
	lines, continuations []int
}

func indexPositions(data []byte) sourcePositions {
	index := sourcePositions{lines: []int{0}, continuations: make([]int, 0)}
	for offset, c := range data {
		if c == '\n' {
			index.lines = append(index.lines, offset+1)
		} else if !utf8.RuneStart(c) {
			index.continuations = append(index.continuations, offset)
		}
	}
	return index
}

func (index sourcePositions) at(offset int) Position {
	line := sort.SearchInts(index.lines, offset+1) - 1
	start := index.lines[line]
	// Index continuation bytes once, rather than rescanning long source lines
	// for every node. Columns count Unicode characters, not UTF-8 bytes.
	extra := sort.SearchInts(index.continuations, offset) - sort.SearchInts(index.continuations, start)
	return Position{Line: line + 1, Column: offset - start + 1 - extra}
}
