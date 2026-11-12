package loader

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Cyenoch/cordis/config"
	"github.com/Cyenoch/cordis/contract"
)

// ErrReadOnly reports an edit of a tree whose file cannot be written back.
// Only YAML files are writable.
var ErrReadOnly = errors.New("loader: configuration file format is read-only")

// isolation is the value of one "isolate" key: true for a private space, or
// the label of a space shared by every entry that names it.
type isolation struct{ label string }

// MarshalJSON writes true for a private space and the label otherwise.
func (i isolation) MarshalJSON() ([]byte, error) {
	if i.label == "" {
		return []byte("true"), nil
	}
	return json.Marshal(i.label)
}

// UnmarshalJSON accepts true or a non-empty label string.
func (i *isolation) UnmarshalJSON(data []byte) error {
	if string(data) == "true" {
		*i = isolation{}
		return nil
	}
	var label string
	if err := json.Unmarshal(data, &label); err != nil || label == "" {
		return errors.New("isolate value must be true or a non-empty label")
	}
	*i = isolation{label: label}
	return nil
}

// spec is one entry as written in a configuration file.
type spec struct {
	ID       string                     `json:"id,omitempty"`
	Plugin   string                     `json:"plugin,omitempty"`
	Config   json.RawMessage            `json:"config,omitempty"`
	Disabled bool                       `json:"disabled,omitempty"`
	Requires []string                   `json:"requires,omitempty"`
	Settings map[string]json.RawMessage `json:"settings,omitempty"`
	Isolate  map[string]isolation       `json:"isolate,omitempty"`
	Plugins  []spec                     `json:"plugins,omitempty"`
	Include  string                     `json:"include,omitempty"`
	Initial  *document                  `json:"initial,omitempty"`
	Patches  []patch                    `json:"patches,omitempty"`
}

type document struct {
	Plugins []spec `json:"plugins"`
}

func (s spec) kind() string {
	switch {
	case s.Include != "":
		return "include"
	case s.Plugin != "":
		return "plugin"
	}
	return "group"
}

// own returns the entry without its children, for comparing two versions of
// one entry.
func (s spec) own() spec {
	s.Plugins = nil
	return s
}

func (s spec) equal(other spec) bool {
	return bytes.Equal(s.canonical(), other.canonical())
}

func (s spec) canonical() []byte {
	data, _ := json.Marshal(s)
	return canonical(data)
}

// canonical re-encodes JSON so that equal values give equal bytes, whatever
// their key order and spacing.
func canonical(data []byte) []byte {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value any
	decoder.Decode(&value)
	data, _ = json.Marshal(value)
	return data
}

// sameData reports whether two JSON documents hold the same data, taking
// numbers by value: a YAML reader may spell -0 as 0 or 1.0 as 1.
func sameData(a, b []byte) bool {
	numeric := func(data []byte) []byte {
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.UseNumber()
		var value any
		decoder.Decode(&value)
		data, _ = json.Marshal(normal(value))
		return data
	}
	return bytes.Equal(numeric(a), numeric(b))
}

// normal replaces every number by a spelling of its exact value.
func normal(value any) any {
	switch value := value.(type) {
	case map[string]any:
		for key, item := range value {
			value[key] = normal(item)
		}
	case []any:
		for index, item := range value {
			value[index] = normal(item)
		}
	case json.Number:
		if exact, ok := new(big.Rat).SetString(value.String()); ok {
			return "number " + exact.RatString()
		}
	}
	return value
}

// key returns the value of one top-level key as canonical JSON, or nil when
// absent.
func (s spec) key(name string) json.RawMessage {
	var fields map[string]json.RawMessage
	json.Unmarshal(s.own().canonical(), &fields)
	return fields[name]
}

// with returns the entry with one top-level key replaced; nil deletes it.
func (s spec) with(name string, value json.RawMessage) (spec, error) {
	data, _ := json.Marshal(s.own())
	var fields map[string]json.RawMessage
	json.Unmarshal(data, &fields)
	if value == nil {
		delete(fields, name)
	} else {
		fields[name] = value
	}
	data, _ = json.Marshal(fields)
	var next spec
	if err := contract.Decode(data, &next); err != nil {
		return spec{}, err
	}
	next.Plugins = s.Plugins
	return next, nil
}

// validate checks one entry and its children. ids collects the identifiers
// used in the file.
func (s spec) validate(ids map[string]bool) error {
	if strings.Contains(s.ID, ":") || strings.HasPrefix(s.ID, "~") {
		return fmt.Errorf("entry id %q must not contain ':' or start with '~'", s.ID)
	}
	if s.ID != "" {
		if ids[s.ID] {
			return fmt.Errorf("duplicate entry id %q", s.ID)
		}
		ids[s.ID] = true
	}
	kinds := 0
	for _, set := range []bool{s.Plugin != "", s.Plugins != nil, s.Include != ""} {
		if set {
			kinds++
		}
	}
	if kinds != 1 {
		return errors.New(`entry needs exactly one of "plugin", "plugins" or "include"`)
	}
	if s.Include == "" && (s.Initial != nil || s.Patches != nil) {
		return errors.New(`"initial" and "patches" need "include"`)
	}
	if s.Include != "" && s.Config != nil {
		return errors.New(`"config" does not apply to "include"`)
	}
	for index, child := range s.Plugins {
		if err := child.validate(ids); err != nil {
			return under("/plugins/"+strconv.Itoa(index), err)
		}
	}
	inserted := map[string]bool{}
	for index, p := range s.Patches {
		for at, child := range p.Insert {
			if err := child.validate(inserted); err != nil {
				return under("/patches/"+strconv.Itoa(index)+"/insert/"+strconv.Itoa(at), err)
			}
		}
	}
	return nil
}

func (d document) validate() error {
	ids := map[string]bool{}
	for index, entry := range d.Plugins {
		if err := entry.validate(ids); err != nil {
			return under("/plugins/"+strconv.Itoa(index), err)
		}
	}
	return nil
}

// under prefixes the location of err with path.
func under(path string, err error) error {
	var located *contract.PathError
	if errors.As(err, &located) {
		return &contract.PathError{Path: path + located.Path, Err: located.Err}
	}
	return &contract.PathError{Path: path, Err: err}
}

// source is one configuration file as last read.
type source struct {
	path     string
	data     []byte
	parsed   *config.Document
	document document
	writable bool // YAML; other formats are read-only
}

// read loads and validates a file. A missing file yields os.ErrNotExist.
func read(path string) (*source, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return parse(path, data)
}

func parse(path string, data []byte) (*source, error) {
	parsed, err := config.Parse(path, data, "")
	if err != nil {
		return nil, err
	}
	s := &source{path: path, data: data, parsed: parsed}
	if err := parsed.Decode(&s.document); err != nil {
		return nil, err
	}
	if err := s.document.validate(); err != nil {
		return nil, s.locate("", err)
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".yaml", ".yml":
		s.writable = true
	}
	return s, nil
}

// locate turns an error carrying a path relative to prefix into one that
// names the file, line and column.
func (s *source) locate(prefix string, err error) error {
	var located *contract.PathError
	if errors.As(err, &located) {
		return s.parsed.Locate(prefix+located.Path, located.Err)
	}
	return s.parsed.Locate(prefix, err)
}
