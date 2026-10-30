package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRecoveryPolicyFilesAreStrictAndUseDurationStrings(t *testing.T) {
	for _, test := range []struct{ name, source string }{
		{"policy.json", `{"entry":{"maxRetries":3,"initialDelay":"50ms","maxDelay":"1s"}}`},
		{"policy.toml", "[entry]\nmaxRetries=3\ninitialDelay='50ms'\nmaxDelay='1s'\n"},
		{"policy.yaml", "entry:\n  maxRetries: 3\n  initialDelay: 50ms\n  maxDelay: 1s\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), test.name)
			if err := os.WriteFile(path, []byte(test.source), 0600); err != nil {
				t.Fatal(err)
			}
			policies, err := loadRecovery(path)
			if err != nil || policies["entry"].MaxRetries != 3 || policies["entry"].InitialDelay != 50*time.Millisecond || policies["entry"].MaxDelay != time.Second {
				t.Fatalf("policy file: %+v %v", policies, err)
			}
		})
	}
	for _, source := range []string{
		`null`, `[]`, `{"entry":{"maxRetries":1,"initialDelay":10,"maxDelay":"1s"}}`,
		`{"entry":{"maxRetries":1,"initialDelay":"50ms","maxDelay":"1s","retryForever":true}}`,
		`{"entry":{"maxRetries":1,"initialDelay":"never","maxDelay":"1s"}}`,
	} {
		path := filepath.Join(t.TempDir(), "invalid.json")
		if err := os.WriteFile(path, []byte(source), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := loadRecovery(path); err == nil {
			t.Fatalf("invalid policy file accepted: %s", source)
		}
	}
}
