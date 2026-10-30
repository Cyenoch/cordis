// Package packagehost validates and durably manages immutable external packages.
package packagehost

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"path"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Cyenoch/cordis/contract"
	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

const ManifestFile = "plugin.json"
const IntegrityFile = "integrity.json"
const SignatureFile = "signature.json"

type IntegrityEntry struct {
	Path   string `json:"path"`
	Size   uint64 `json:"size"`
	SHA256 string `json:"sha256"`
}
type Integrity struct {
	Files []IntegrityEntry `json:"files"`
}
type Signature struct {
	KeyID     string `json:"keyId"`
	Signature string `json:"signature"`
}
type Package struct {
	Ref         contract.PackageRef `json:"ref"`
	Manifest    contract.Manifest   `json:"manifest"`
	Directory   string              `json:"-"`
	Source      string              `json:"source"`
	Development bool                `json:"development"`
}
type TrustedKey struct {
	PublicKey ed25519.PublicKey
	Publisher string
	Revoked   bool
}

// TrustPolicy is copied when opening a Store. UpdateTrust replaces it atomically.
// Publishers maps publisher names to exact permitted plugin IDs (no wildcards).
type TrustPolicy struct {
	Keys              map[string]TrustedKey
	Publishers        map[string][]string
	RevokedPublishers map[string]bool
	DevDigests        map[string]bool
	PublisherChanges  map[string]map[string]bool
}

func validatePath(name string) error {
	if name == "" || len(name) > 4096 || !utf8.ValidString(name) || !norm.NFC.IsNormalString(name) || path.IsAbs(name) || path.Clean(name) != name || strings.ContainsAny(name, "\\:") {
		return fmt.Errorf("non-canonical package path %q", name)
	}
	for _, c := range name {
		if unicode.IsControl(c) {
			return fmt.Errorf("control character in path %q", name)
		}
	}
	for _, part := range strings.Split(name, "/") {
		if part == "" || part == "." || part == ".." || len(part) > 255 || strings.HasSuffix(part, ".") || strings.HasSuffix(part, " ") {
			return fmt.Errorf("invalid path component in %q", name)
		}
		base := strings.ToUpper(strings.SplitN(part, ".", 2)[0])
		device := strings.NewReplacer("¹", "1", "²", "2", "³", "3").Replace(base)
		if device == "CON" || device == "PRN" || device == "AUX" || device == "NUL" || device == "CONIN$" || device == "CONOUT$" || len(device) == 4 && (strings.HasPrefix(device, "COM") || strings.HasPrefix(device, "LPT")) && (device[3] >= '1' && device[3] <= '9') {
			return fmt.Errorf("reserved device path %q", name)
		}
		if strings.ContainsAny(part, "<>\"|?*") || strings.HasPrefix(part, " ") {
			return fmt.Errorf("non-portable path %q", name)
		}
	}
	return nil
}

func validName(name string) bool {
	return validSymbol(name, 200) && !strings.Contains(name, "..")
}

// Service names are logical symbols, never filesystem paths.
func validSymbol(name string, maximum int) bool {
	if name == "" || len(name) > maximum {
		return false
	}
	for _, c := range name {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '-' || c == '_' || c == '/') {
			return false
		}
	}
	return !strings.HasPrefix(name, "/") && !strings.HasSuffix(name, "/") && !strings.Contains(name, "//")
}

// ValidateManifest validates structural metadata independently of execution.
func ValidateManifest(m contract.Manifest) error {
	if m.Schema != contract.PackageSchema {
		return fmt.Errorf("unsupported package schema %q", m.Schema)
	}
	if !validName(m.ID) || !validName(m.Publisher) {
		return fmt.Errorf("invalid package id or publisher")
	}
	if err := ValidateVersion(m.Version); err != nil {
		return err
	}
	if m.Protocol.Major != 1 || m.Protocol.MinMinor > m.Protocol.MaxMinor || m.Protocol.MinMinor > 0 {
		return fmt.Errorf("unsupported session protocol %d [%d,%d]", m.Protocol.Major, m.Protocol.MinMinor, m.Protocol.MaxMinor)
	}
	if len(m.Definitions) == 0 {
		return fmt.Errorf("package has no definitions")
	}
	defs := map[string]bool{}
	for _, d := range m.Definitions {
		if !validName(d.ID) || defs[d.ID] {
			return fmt.Errorf("invalid or duplicate definition %q", d.ID)
		}
		defs[d.ID] = true
		if len(d.ConfigSchema) == 0 {
			return fmt.Errorf("definition %s requires configSchema", d.ID)
		}
		if err := validateConfigSchema(d.ConfigSchema); err != nil {
			return fmt.Errorf("definition %s configSchema: %w", d.ID, err)
		}
		requirements := map[string]bool{}
		provides := map[string]bool{}
		for _, consumer := range d.Requires {
			r := consumer.Requirement
			if !validSymbol(r.Name, 512) || r.MinMinor > r.MaxMinor || requirements[r.Key()] {
				return fmt.Errorf("invalid or duplicate requirement %q", r.Key())
			}
			requirements[r.Key()] = true
			if hash, err := hex.DecodeString(consumer.Contract); err != nil || len(hash) != sha256.Size || strings.ToLower(consumer.Contract) != consumer.Contract {
				return fmt.Errorf("invalid consumer method contract for %s", r.Key())
			}
		}
		for _, s := range d.Provides {
			if !validSymbol(s.Name, 512) || provides[s.Key()] {
				return fmt.Errorf("invalid or duplicate service %q", s.Key())
			}
			provides[s.Key()] = true
		}
	}
	if !defs[m.DefaultDefinition] {
		return fmt.Errorf("default definition %q does not exist", m.DefaultDefinition)
	}
	switch m.Runtime.Kind {
	case "native":
		if len(m.Runtime.Artifacts) == 0 {
			return fmt.Errorf("native package requires explicit artifacts only")
		}
		platforms := map[string]bool{}
		for _, a := range m.Runtime.Artifacts {
			if !validName(a.OS) || !validName(a.Arch) || platforms[a.OS+"/"+a.Arch] {
				return fmt.Errorf("invalid or duplicate native platform")
			}
			platforms[a.OS+"/"+a.Arch] = true
			if err := validatePath(a.Path); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("unsupported runtime %q", m.Runtime.Kind)
	}
	if _, err := releaseFiles(m); err != nil {
		return err
	}
	dependencies := map[string]bool{}
	for _, list := range [][]contract.Dependency{m.Dependencies, m.OptionalDependencies} {
		for _, d := range list {
			if !validName(d.ID) || d.ID == m.ID || dependencies[d.ID] {
				return fmt.Errorf("invalid or duplicate dependency %q", d.ID)
			}
			dependencies[d.ID] = true
			if d.Range != "" {
				if d.Min != "" || d.Max != "" {
					return fmt.Errorf("dependency %s mixes interval and comparators", d.ID)
				}
				if _, err := VersionSatisfies("0.0.0", d.Range); err != nil {
					return err
				}
			} else if _, err := VersionInRange(d.Min, d.Min, d.Max); err != nil {
				return fmt.Errorf("dependency %s: %w", d.ID, err)
			}
		}
	}
	return nil
}

// Artifact selects exactly the requested target; no runtime fallback is allowed.
func Artifact(m contract.Manifest, goos, goarch string) (string, error) {
	for _, a := range m.Runtime.Artifacts {
		if a.OS == goos && a.Arch == goarch {
			return a.Path, nil
		}
	}
	return "", fmt.Errorf("package %s has no native artifact for %s/%s", m.ID, goos, goarch)
}

// SignatureInput implements the fixed binary package-signing format.
func SignatureInput(entries []IntegrityEntry) ([]byte, error) {
	if len(entries) > 10000 {
		return nil, fmt.Errorf("too many integrity entries")
	}
	sorted := slices.Clone(entries)
	slices.SortFunc(sorted, func(a, b IntegrityEntry) int { return strings.Compare(a.Path, b.Path) })
	var b bytes.Buffer
	b.WriteString("cordis-go-package/v1\x00")
	if err := binary.Write(&b, binary.BigEndian, uint32(len(sorted))); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, e := range sorted {
		if err := validatePath(e.Path); err != nil {
			return nil, err
		}
		if e.Path == IntegrityFile || e.Path == SignatureFile || seen[e.Path] {
			return nil, fmt.Errorf("invalid integrity entry %q", e.Path)
		}
		seen[e.Path] = true
		hash, err := hex.DecodeString(e.SHA256)
		if err != nil || len(hash) != sha256.Size || e.SHA256 != strings.ToLower(e.SHA256) {
			return nil, fmt.Errorf("invalid sha256 for %q", e.Path)
		}
		if err := binary.Write(&b, binary.BigEndian, uint32(len(e.Path))); err != nil {
			return nil, err
		}
		b.WriteString(e.Path)
		if err := binary.Write(&b, binary.BigEndian, e.Size); err != nil {
			return nil, err
		}
		b.Write(hash)
	}
	return b.Bytes(), nil
}

func (t TrustPolicy) verify(m contract.Manifest, digest string, input []byte, sig *Signature, development bool) error {
	if t.RevokedPublishers[m.Publisher] {
		return fmt.Errorf("publisher %q is revoked", m.Publisher)
	}
	if !slices.Contains(t.Publishers[m.Publisher], m.ID) {
		return fmt.Errorf("publisher %q is not authorized for plugin %q", m.Publisher, m.ID)
	}
	if sig == nil {
		if development && t.DevDigests[digest] {
			return nil
		}
		return fmt.Errorf("package %s requires an authorized signature or exact development digest permission", m.ID)
	}
	key, ok := t.Keys[sig.KeyID]
	if !ok || key.Revoked || key.Publisher != m.Publisher || len(key.PublicKey) != ed25519.PublicKeySize {
		return fmt.Errorf("untrusted, revoked or publisher-mismatched signing key %q", sig.KeyID)
	}
	signature, err := base64.StdEncoding.Strict().DecodeString(sig.Signature)
	if err != nil {
		return fmt.Errorf("decode signature: %w", err)
	}
	if !ed25519.Verify(key.PublicKey, input, signature) {
		return fmt.Errorf("invalid package signature")
	}
	return nil
}

func checkDirectories(files, dirs []string) error {
	components := map[string]string{}
	fileSet := map[string]bool{}
	seen := map[string]bool{}
	for _, f := range files {
		fileSet[cases.Fold().String(f)] = true
	}
	for _, name := range slices.Concat(files, dirs) {
		if err := validatePath(name); err != nil {
			return err
		}
		if seen[name] {
			return fmt.Errorf("duplicate package path %q", name)
		}
		seen[name] = true
		for p := name; p != "."; p = path.Dir(p) {
			fold := cases.Fold().String(p)
			if other, ok := components[fold]; ok && other != p {
				return fmt.Errorf("case-folding component conflict %q and %q", other, p)
			}
			components[fold] = p
			if p != name && fileSet[fold] {
				return fmt.Errorf("file is used as a directory: %q", p)
			}
		}
	}
	for _, d := range dirs {
		if fileSet[cases.Fold().String(d)] {
			return fmt.Errorf("file/directory conflict %q", d)
		}
	}
	return nil
}

func decodeManifest(data []byte) (contract.Manifest, error) {
	var m contract.Manifest
	if err := contract.Decode(data, &m); err != nil {
		return m, err
	}
	if err := ValidateManifest(m); err != nil {
		return m, err
	}
	return m, nil
}
