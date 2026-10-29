//go:build integration

package packagehost

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"

	"github.com/Cyenoch/cordis/contract"
)

type queryingHandler struct {
	slog.Handler
	query func()
}

func (h queryingHandler) Handle(ctx context.Context, r slog.Record) error {
	h.query()
	return h.Handler.Handle(ctx, r)
}

func TestOperationDiagnosticsReportCommitBeforeSyncFailure(t *testing.T) {
	key, trust := signingFixture(t)
	root := t.TempDir()
	s := openFixture(t, root, trust)
	archive, _ := archiveFixture(t, manifestFixture("demo", "1.0.0"), key)
	p, err := s.Install(t.Context(), archive)
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	s.logger = slog.New(queryingHandler{
		Handler: slog.NewJSONHandler(&logs, nil),
		query: func() {
			if _, err := s.Entries(); err != nil {
				t.Error(err)
			}
		},
	})
	if err := s.Select(t.Context(), contract.PackageRef{ID: "missing"}, ChangeOptions{}); err == nil {
		t.Fatal("invalid selection succeeded")
	}
	s.commitObserver = func(stage commitStage) {
		if stage == commitCatalogSwapped {
			// The catalog is committed; make only the final directory sync fail.
			s.root = root + "\x00"
		}
	}
	err = s.Select(t.Context(), p.Ref, ChangeOptions{})
	s.root, s.commitObserver = root, nil
	var committed *CommitError
	if !errors.As(err, &committed) {
		t.Fatalf("missing post-commit error: %v", err)
	}
	decoder := json.NewDecoder(&logs)
	for _, want := range []string{"rejected", "committed_error"} {
		var record struct {
			Operation string `json:"operation"`
			Outcome   string `json:"outcome"`
		}
		if err := decoder.Decode(&record); err != nil {
			t.Fatal(err)
		}
		if record.Operation != "select" || record.Outcome != want {
			t.Fatalf("misreported mutation: %+v want=%s", record, want)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened := openFixture(t, root, trust)
	if selected, err := reopened.Selected(p.Ref.ID); err != nil || selected.Ref != p.Ref {
		t.Fatalf("committed selection not recovered: %+v %v", selected.Ref, err)
	}
}
