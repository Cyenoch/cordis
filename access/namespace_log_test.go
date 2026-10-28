package access

import (
	"errors"
	"fmt"
	"strconv"
	"testing"
)

func TestNamespaceRecoveredQuotaTracksOverwriteAndDelete(t *testing.T) {
	p, s, owner, directory := namespaceFixture(t, 20)
	for _, item := range []struct{ key, value string }{{"a", "1234"}, {"a", "123456789"}, {"bb", "123"}} {
		if err := s.Write(t.Context(), owner, "state", item.key, []byte(item.value)); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Delete(t.Context(), owner, "state", "a"); err != nil {
		t.Fatal(err)
	}
	if err := s.Write(t.Context(), owner, "state", "cc", []byte("12345678")); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenNamespaceStore(p, directory)
	if err != nil {
		t.Fatal(err)
	}
	if err := reopened.Write(t.Context(), owner, "state", "d", []byte("1234")); err != nil {
		t.Fatal("replay retained obsolete bytes", err)
	}
	if err := reopened.Write(t.Context(), owner, "state", "bb", []byte("1234")); !errors.Is(err, ErrQuota) {
		t.Fatal("replay undercounted retained bytes", err)
	}
}

func TestNamespaceReplayRejectsConflictingConfigure(t *testing.T) {
	for _, mode := range []string{"other-package", "other-name", "quota-below-usage"} {
		t.Run(mode, func(t *testing.T) {
			policy, store, owner, directory := namespaceFixture(t, 20)
			if err := store.Write(t.Context(), owner, "state", "k", []byte("1234")); err != nil {
				t.Fatal(err)
			}
			record := namespaceRecord{
				Operation: "configure", Namespace: "state", Package: owner.Package,
				Config: NamespaceConfig{Name: "state", QuotaBytes: 20},
			}
			switch mode {
			case "other-package":
				record.Package = "other"
			case "other-name":
				record.Namespace, record.Config.Name = "other", "other"
			case "quota-below-usage":
				record.Config.QuotaBytes = 4
			}
			if err := store.appendLog(record); err != nil {
				t.Fatal(err)
			}
			if _, err := OpenNamespaceStore(policy, directory); err == nil {
				t.Fatal("recovery accepted conflicting configuration")
			}
		})
	}
}

func BenchmarkNamespaceReplay(b *testing.B) {
	for _, size := range []int{1000, 10000} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			records := make([]namespaceRecord, size)
			for n := range records {
				records[n] = namespaceRecord{Operation: "write", Namespace: "state", Key: strconv.Itoa(n), Value: []byte("value")}
			}
			b.ResetTimer()
			for b.Loop() {
				s := NewNamespaceStore(NewPolicy())
				if err := s.replay(namespaceRecord{Operation: "configure", Namespace: "state", Package: "package", Config: NamespaceConfig{Name: "state", QuotaBytes: 1 << 20}}); err != nil {
					b.Fatal(err)
				}
				for _, record := range records {
					if err := s.replay(record); err != nil {
						b.Fatal(err)
					}
				}
			}
		})
	}
}
