package packagehost_test

import (
	"github.com/Cyenoch/cordis/packagehost"
	"testing"
)

func TestVersionSatisfies(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, version, expression string
		want                      bool
	}{
		{"intersection lower", "1.2.0", ">=1.2.0 <2.0.0", true},
		{"intersection upper", "2.0.0", ">=1.2.0 <2.0.0", false},
		{"exclusive lower", "1.2.0", ">1.2.0 <=2.0.0", false},
		{"inclusive upper", "2.0.0", ">1.2.0 <=2.0.0", true},
		{"caret stable", "1.9.9", "^1.2.3", true},
		{"caret major excluded", "2.0.0", "^1.2.3", false},
		{"caret zero minor", "0.3.0", "^0.2.3", false},
		{"caret zero patch", "0.0.4", "^0.0.3", false},
		{"tilde patch", "1.2.99", "~1.2.3", true},
		{"tilde minor excluded", "1.3.0", "~1.2.3", false},
		{"prerelease ordering", "1.2.3-alpha.2", ">=1.2.3-alpha.1 <1.2.3", true},
		{"exact ignores build", "1.2.3+host", "=1.2.3", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := packagehost.VersionSatisfies(tc.version, tc.expression)
			if err != nil || got != tc.want {
				t.Fatalf("got %v,%v want %v", got, err, tc.want)
			}
		})
	}
	for _, expression := range []string{"", "1.2.3", ">=1.2", "^1.2.3 || ^2.0.0", "*", ">>1.2.3"} {
		t.Run("invalid "+expression, func(t *testing.T) {
			if _, err := packagehost.VersionSatisfies("1.2.3", expression); err == nil {
				t.Fatalf("invalid expression accepted: %q", expression)
			}
		})
	}
}
