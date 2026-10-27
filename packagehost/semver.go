package packagehost

import (
	"fmt"
	"strings"
)

type version struct {
	core [3]string
	pre  []string
}

func parseVersion(text string) (version, error) {
	var v version
	raw := text
	if text == "" || strings.TrimSpace(text) != text {
		return v, fmt.Errorf("invalid semantic version %q", raw)
	}
	if i := strings.IndexByte(text, '+'); i >= 0 {
		if !identifiers(text[i+1:], false) {
			return v, fmt.Errorf("invalid version build metadata %q", raw)
		}
		text = text[:i]
	}
	if i := strings.IndexByte(text, '-'); i >= 0 {
		if !identifiers(text[i+1:], true) {
			return v, fmt.Errorf("invalid version prerelease %q", raw)
		}
		v.pre = strings.Split(text[i+1:], ".")
		text = text[:i]
	}
	parts := strings.Split(text, ".")
	if len(parts) != 3 {
		return v, fmt.Errorf("semantic version requires three components: %q", raw)
	}
	for i, p := range parts {
		if !numeric(p) || len(p) > 1 && p[0] == '0' {
			return v, fmt.Errorf("invalid version component %q", p)
		}
		v.core[i] = p
	}
	return v, nil
}

func numeric(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func identifiers(s string, prerelease bool) bool {
	for _, p := range strings.Split(s, ".") {
		if p == "" {
			return false
		}
		for _, c := range p {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
				return false
			}
		}
		if prerelease && numeric(p) && len(p) > 1 && p[0] == '0' {
			return false
		}
	}
	return true
}

func compareNumeric(a, b string) int {
	if len(a) < len(b) {
		return -1
	}
	if len(a) > len(b) {
		return 1
	}
	return strings.Compare(a, b)
}

func compareVersion(a, b version) int {
	for i := range a.core {
		if c := compareNumeric(a.core[i], b.core[i]); c != 0 {
			return c
		}
	}
	if len(a.pre) == 0 && len(b.pre) == 0 {
		return 0
	}
	if len(a.pre) == 0 {
		return 1
	}
	if len(b.pre) == 0 {
		return -1
	}
	for i := 0; i < len(a.pre) && i < len(b.pre); i++ {
		x, y := a.pre[i], b.pre[i]
		nx, ny := numeric(x), numeric(y)
		c := 0
		switch {
		case nx && ny:
			c = compareNumeric(x, y)
		case nx:
			c = -1
		case ny:
			c = 1
		default:
			c = strings.Compare(x, y)
		}
		if c != 0 {
			return c
		}
	}
	if len(a.pre) < len(b.pre) {
		return -1
	}
	if len(a.pre) > len(b.pre) {
		return 1
	}
	return 0
}

// ValidateVersion accepts strict SemVer 2.0.0, without a leading v.
func ValidateVersion(text string) error { _, err := parseVersion(text); return err }

// VersionInRange tests an inclusive minimum and exclusive maximum.
func VersionInRange(text, minimum, maximum string) (bool, error) {
	v, err := parseVersion(text)
	if err != nil {
		return false, err
	}
	low, err := parseVersion(minimum)
	if err != nil {
		return false, err
	}
	high, err := parseVersion(maximum)
	if err != nil {
		return false, err
	}
	if compareVersion(low, high) >= 0 {
		return false, fmt.Errorf("invalid version interval [%s,%s)", minimum, maximum)
	}
	return compareVersion(v, low) >= 0 && compareVersion(v, high) < 0, nil
}
