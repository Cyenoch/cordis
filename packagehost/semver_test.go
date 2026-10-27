package packagehost

import "testing"

func TestVersionInRange(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, value, low, high string
		want                   bool
	}{
		{"inclusive minimum", "1.2.3", "1.2.3", "2.0.0", true},
		{"exclusive maximum", "2.0.0", "1.2.3", "2.0.0", false},
		{"prerelease ordering", "1.2.3-alpha.2", "1.2.3-alpha.1", "1.2.3", true},
		{"numeric prerelease", "1.2.3-alpha.10", "1.2.3-alpha.2", "1.2.3", true},
		{"metadata ignored", "1.2.3+build", "1.2.3", "2.0.0", true},
		{"large components", "999999999999999999999999.0.0", "1.0.0", "9999999999999999999999999.0.0", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := VersionInRange(tt.value, tt.low, tt.high)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("got %v want %v", got, tt.want)
			}
		})
	}
	for _, text := range []string{"v1.2.3", "01.2.3", "1.2", "1.2.3-01", "1.2.3+", "1.2.3-alpha..one", "1.2.3 "} {
		t.Run(text, func(t *testing.T) {
			if err := ValidateVersion(text); err == nil {
				t.Fatal("invalid version accepted")
			}
		})
	}
}
func FuzzVersionInRange(f *testing.F) {
	f.Add("1.2.3")
	f.Add("1.0.0-alpha")
	f.Fuzz(func(t *testing.T, text string) {
		if len(text) > 4096 {
			t.Skip()
		}
		if ValidateVersion(text) == nil {
			ok, err := VersionInRange(text, text, "9999999999999999999999999999999999999999999999.0.0")
			if err == nil && !ok {
				t.Fatal("version below its own inclusive bound")
			}
		}
	})
}
