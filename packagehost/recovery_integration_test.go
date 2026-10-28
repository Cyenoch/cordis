//go:build integration

package packagehost

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Cyenoch/cordis/contract"
)

const recoveryCrashExitCode = 86
const recoveryCrashConfigEnv = "CORDIS_PACKAGEHOST_CRASH_CONFIG"

type recoveryCrashConfig struct {
	Root      string
	Archive   string
	Trust     TrustPolicy
	Operation string
	Ref       contract.PackageRef
	Stage     commitStage
	Owner     contract.Identity
}

func TestStoreRecoveryProcessExit(t *testing.T) {
	t.Parallel()
	stages := []struct {
		name      string
		stage     commitStage
		committed bool
	}{
		{name: "prepared", stage: commitPrepared},
		{name: "content-moved", stage: commitContentMoved},
		{name: "catalog-next", stage: commitCatalogSynced},
		{name: "catalog-swapped", stage: commitCatalogSwapped, committed: true},
		{name: "root-synced", stage: commitRootSynced, committed: true},
	}
	for _, operation := range []string{"install", "select"} {
		t.Run(operation, func(t *testing.T) {
			for _, stage := range stages {
				if operation == "select" && stage.stage == commitContentMoved {
					continue // Selection only replaces catalog metadata.
				}
				t.Run(stage.name, func(t *testing.T) {
					t.Parallel()
					ctx := context.Background()
					key, trust := signingFixture(t)
					root := t.TempDir()
					archive1, _ := archiveFixture(t, manifestFixture("demo", "1.0.0"), key)
					archive2, p2 := archiveFixture(t, manifestFixture("demo", "2.0.0"), key)
					s := openFixture(t, root, trust)
					p1, err := s.Install(ctx, archive1)
					if err != nil {
						t.Fatal(err)
					}
					if err = s.Select(ctx, p1.Ref, ChangeOptions{}); err != nil {
						t.Fatal(err)
					}
					entry := contract.Entry{
						ID: "entry", Package: "demo", Definition: "main", Realm: "root",
						Config: []byte(`{}`), Enabled: true,
					}
					if err = s.SaveEntry(ctx, entry); err != nil {
						t.Fatal(err)
					}
					if operation == "select" {
						if _, err = s.Install(ctx, archive2); err != nil {
							t.Fatal(err)
						}
					}
					before := readRecoveryCatalog(t, root)
					if err = s.Close(); err != nil {
						t.Fatal(err)
					}

					runRecoveryCrash(t, recoveryCrashConfig{
						Root: root, Archive: archive2, Trust: trust,
						Operation: operation, Ref: p2.Ref, Stage: stage.stage,
					})

					wantSelected := p1.Ref
					if operation == "select" && stage.committed {
						wantSelected = p2.Ref
					}
					wantV2 := operation == "select" || stage.committed
					// Read catalog.json itself before recovery; catalog.next cannot
					// determine the committed state.
					after := readRecoveryCatalog(t, root)
					if after.Selected["demo"] != wantSelected {
						t.Fatalf("durable selection = %+v, want %+v", after.Selected["demo"], wantSelected)
					}
					_, hasV2 := after.Installed[p2.Ref.Digest]
					if hasV2 != wantV2 {
						t.Fatalf("durable v2 installed = %v, want %v", hasV2, wantV2)
					}
					wantRevision := before.Revision
					if stage.committed {
						wantRevision++
					} else if !reflect.DeepEqual(after, before) {
						t.Fatal("pre-commit exit changed the durable catalog")
					}
					if after.Revision != wantRevision {
						t.Fatalf("catalog revision = %d, want %d", after.Revision, wantRevision)
					}
					assertRecoveryPath(t, filepath.Join(root, "catalog.next"), stage.stage == commitCatalogSynced)
					wantStaging := 0
					if operation == "install" && stage.stage == commitPrepared {
						wantStaging = 1
					}
					assertRecoveryDirectoryCount(t, filepath.Join(root, "staging"), wantStaging)
					assertRecoveryDirectoryCount(t, filepath.Join(root, "snapshots"), 1)
					contentMoved := operation == "select" || stage.stage != commitPrepared
					assertRecoveryPath(t, filepath.Join(root, "content", p2.Ref.Digest), contentMoved)

					s = openFixture(t, root, trust)
					selected, err := s.Selected("demo")
					if err != nil || selected.Ref != wantSelected {
						t.Fatalf("recovered selection = %+v, %v; want %+v", selected.Ref, err, wantSelected)
					}
					installed, err := s.Installed()
					if err != nil {
						t.Fatal(err)
					}
					wantCount := 1
					if wantV2 {
						wantCount++
					}
					if len(installed) != wantCount {
						t.Fatalf("installed count = %d, want %d", len(installed), wantCount)
					}
					for _, p := range installed {
						if p.Ref != p1.Ref && (!wantV2 || p.Ref != p2.Ref) {
							t.Fatalf("unexpected installed reference: %+v", p.Ref)
						}
						checked, input, sig, err := inspect(ctx, p.Directory, p.Source, p.Development, s.limits, nil)
						if err != nil || checked.Ref != p.Ref {
							t.Fatalf("recovered content is inconsistent for %+v: %+v, %v", p.Ref, checked.Ref, err)
						}
						if err = trust.verify(checked.Manifest, p.Ref.Digest, input, sig, p.Development); err != nil {
							t.Fatal(err)
						}
					}
					_, err = s.Get(p2.Ref)
					if (err == nil) != wantV2 {
						t.Fatalf("recovered v2 lookup = %v, want installed %v", err, wantV2)
					}
					entries, err := s.Entries()
					if err != nil || !reflect.DeepEqual(entries, []contract.Entry{entry}) {
						t.Fatalf("recovered entries = %+v, %v", entries, err)
					}
					assertRecoveryDirectoryCount(t, filepath.Join(root, "content"), wantCount)
					for _, name := range []string{"staging", "snapshots", "trash"} {
						assertRecoveryDirectoryCount(t, filepath.Join(root, name), 0)
					}
					assertRecoveryPath(t, filepath.Join(root, "catalog.next"), false)
					assertRecoveryPath(t, filepath.Join(root, "content", p2.Ref.Digest), wantV2)
					if operation == "install" && wantV2 {
						if snapshot, err := s.Snapshot(ctx, p2.Ref); err == nil {
							_ = snapshot.Close()
							t.Fatal("install made unselected v2 executable")
						}
					}
					if recovered := readRecoveryCatalog(t, root); !reflect.DeepEqual(recovered, after) {
						t.Fatal("recovery rewrote committed catalog state")
					}
				})
			}
		})
	}
}

// This test is the child entry point, not a production environment hook. os.Exit
// deliberately bypasses deferred Store/Snapshot cleanup while holding the lock.
func TestStoreRecoveryCrashChild(t *testing.T) {
	configPath := os.Getenv(recoveryCrashConfigEnv)
	if configPath == "" {
		t.Skip("only run by the process-exit recovery test")
	}
	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	var config recoveryCrashConfig
	if err = json.Unmarshal(raw, &config); err != nil {
		t.Fatal(err)
	}
	s, err := Open(config.Root, config.Trust)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	selected, err := s.Selected("demo")
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := s.Snapshot(context.Background(), selected.Ref)
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.Close()
	s.commitObserver = func(stage commitStage) {
		if stage == config.Stage {
			os.Exit(recoveryCrashExitCode)
		}
	}
	switch config.Operation {
	case "install":
		_, err = s.Install(context.Background(), config.Archive)
	case "select":
		err = s.Select(context.Background(), config.Ref, ChangeOptions{})
	case "begin-run":
		err = s.BeginRun(context.Background(), config.Owner)
	default:
		t.Fatalf("unknown child operation %q", config.Operation)
	}
	t.Fatalf("transaction returned instead of exiting at %s: %v", config.Stage, err)
}

func TestStoreResignRecoveryHasOneProofCommitPoint(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"process-exit", "cancel"} {
		for _, stage := range []commitStage{commitCatalogSynced, commitCatalogSwapped} {
			t.Run(mode+"/"+string(stage), func(t *testing.T) {
				t.Parallel()
				ctx := context.Background()
				key, trust := signingFixture(t)
				root := t.TempDir()
				archive, p := archiveFixture(t, manifestFixture("demo", "1.0.0"), key)
				s := openFixture(t, root, trust)
				if _, err := s.Install(ctx, archive); err != nil {
					t.Fatal(err)
				}
				if err := s.Select(ctx, p.Ref, ChangeOptions{}); err != nil {
					t.Fatal(err)
				}
				pub, rotated, err := ed25519.GenerateKey(rand.Reader)
				if err != nil {
					t.Fatal(err)
				}
				trust.Keys["rotated"] = TrustedKey{PublicKey: pub, Publisher: "example"}
				if _, err = Sign(ctx, archive, "rotated", rotated); err != nil {
					t.Fatal(err)
				}
				if mode == "process-exit" {
					if err = s.Close(); err != nil {
						t.Fatal(err)
					}
					runRecoveryCrash(t, recoveryCrashConfig{Root: root, Archive: archive, Trust: trust, Operation: "install", Stage: stage})
				} else {
					if err = s.UpdateTrust(trust); err != nil {
						t.Fatal(err)
					}
					canceled, cancel := context.WithCancel(ctx)
					defer cancel()
					s.commitObserver = func(observed commitStage) {
						if observed == stage {
							cancel()
						}
					}
					_, err = s.Install(canceled, archive)
					if stage == commitCatalogSynced && !errors.Is(err, context.Canceled) || stage == commitCatalogSwapped && err != nil {
						t.Fatalf("refresh cancellation: %v", err)
					}
					if err = s.Close(); err != nil {
						t.Fatal(err)
					}
				}
				old := trust.Keys["test"]
				old.Revoked = true
				trust.Keys["test"] = old
				s = openFixture(t, root, trust)
				snapshot, err := s.Snapshot(ctx, p.Ref)
				if stage == commitCatalogSynced {
					if err == nil {
						_ = snapshot.Close()
						t.Fatal("uncommitted signature refresh became executable")
					}
					if !strings.Contains(err.Error(), "signing key") {
						t.Fatal("unexpected recovery failure", err)
					}
				} else {
					if err != nil {
						t.Fatal("committed signature refresh lost", err)
					}
					if err = snapshot.Close(); err != nil {
						t.Fatal(err)
					}
				}
				if selected, err := s.Selected("demo"); err != nil || selected.Ref != p.Ref {
					t.Fatal("signature refresh changed selection", selected, err)
				}
			})
		}
	}
}

func TestRunsProcessExitRecovery(t *testing.T) {
	t.Parallel()
	for _, stage := range []commitStage{commitRunsSynced, commitRunsSwapped} {
		t.Run(string(stage), func(t *testing.T) {
			t.Parallel()
			key, trust := signingFixture(t)
			root := t.TempDir()
			s := openFixture(t, root, trust)
			archive, p := archiveFixture(t, manifestFixture("demo", "1.0.0"), key)
			if _, err := s.Install(context.Background(), archive); err != nil {
				t.Fatal(err)
			}
			if err := s.Select(context.Background(), p.Ref, ChangeOptions{}); err != nil {
				t.Fatal(err)
			}
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			owner := runOwner("crashed-activation")
			runRecoveryCrash(t, recoveryCrashConfig{Root: root, Trust: trust, Operation: "begin-run", Owner: owner, Stage: stage})
			s = openFixture(t, root, trust)
			runs, err := s.Runs()
			if err != nil {
				t.Fatal(err)
			}
			if stage == commitRunsSynced {
				if len(runs) != 0 {
					t.Fatal("uncommitted run recovered", runs)
				}
			} else if len(runs) != 1 || runs[0].Owner != owner || runs[0].State != "interrupted" {
				t.Fatal("committed run not interrupted", runs)
			}
			assertRecoveryPath(t, filepath.Join(root, "runs.next"), false)
		})
	}
}

func TestStoreRecoveryCancellationBoundary(t *testing.T) {
	t.Parallel()
	stages := []struct {
		name      string
		stage     commitStage
		committed bool
	}{
		{name: "before-commit", stage: commitPrepared},
		{name: "catalog-ready", stage: commitCatalogSynced},
		{name: "after-commit", stage: commitCatalogSwapped, committed: true},
	}
	for _, operation := range []string{"install", "select"} {
		t.Run(operation, func(t *testing.T) {
			for _, stage := range stages {
				t.Run(stage.name, func(t *testing.T) {
					t.Parallel()
					key, trust := signingFixture(t)
					root := t.TempDir()
					archive1, _ := archiveFixture(t, manifestFixture("demo", "1.0.0"), key)
					archive2, p2 := archiveFixture(t, manifestFixture("demo", "2.0.0"), key)
					s := openFixture(t, root, trust)
					p1, err := s.Install(context.Background(), archive1)
					if err != nil {
						t.Fatal(err)
					}
					if err = s.Select(context.Background(), p1.Ref, ChangeOptions{}); err != nil {
						t.Fatal(err)
					}
					if operation == "select" {
						if _, err = s.Install(context.Background(), archive2); err != nil {
							t.Fatal(err)
						}
					}
					ctx, cancel := context.WithCancel(context.Background())
					defer cancel()
					s.commitObserver = func(observed commitStage) {
						if observed == stage.stage {
							cancel()
						}
					}
					if operation == "install" {
						var installed Package
						installed, err = s.Install(ctx, archive2)
						if stage.committed && installed.Ref != p2.Ref {
							t.Fatalf("post-commit install returned %+v, want %+v", installed.Ref, p2.Ref)
						}
					} else {
						err = s.Select(ctx, p2.Ref, ChangeOptions{})
					}
					if !errors.Is(ctx.Err(), context.Canceled) {
						t.Fatal("transaction never reached the cancellation boundary")
					}
					if stage.committed && err != nil {
						t.Fatalf("post-commit cancellation misreported failure: %v", err)
					}
					if !stage.committed && !errors.Is(err, context.Canceled) {
						t.Fatalf("pre-commit cancellation = %v, want context.Canceled", err)
					}
					if err = s.Close(); err != nil {
						t.Fatal(err)
					}
					s = openFixture(t, root, trust)
					wantSelected := p1.Ref
					if operation == "select" && stage.committed {
						wantSelected = p2.Ref
					}
					selected, err := s.Selected("demo")
					if err != nil || selected.Ref != wantSelected {
						t.Fatalf("selection after cancellation = %+v, %v", selected.Ref, err)
					}
					wantV2 := operation == "select" || stage.committed
					_, err = s.Get(p2.Ref)
					if (err == nil) != wantV2 {
						t.Fatalf("v2 after cancellation = %v, want installed %v", err, wantV2)
					}
					assertRecoveryPath(t, filepath.Join(root, "content", p2.Ref.Digest), wantV2)
					assertRecoveryDirectoryCount(t, filepath.Join(root, "staging"), 0)
				})
			}
		})
	}
}

func runRecoveryCrash(t *testing.T, config recoveryCrashConfig) {
	t.Helper()
	raw, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(t.TempDir(), "crash-config.json")
	if err = os.WriteFile(configPath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, executable, "-test.run=^TestStoreRecoveryCrashChild$")
	cmd.Env = make([]string, 0, len(os.Environ())+1)
	for _, variable := range os.Environ() {
		if !strings.HasPrefix(variable, recoveryCrashConfigEnv+"=") {
			cmd.Env = append(cmd.Env, variable)
		}
	}
	cmd.Env = append(cmd.Env, recoveryCrashConfigEnv+"="+configPath)
	output, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != recoveryCrashExitCode {
		t.Fatalf("child did not exit at %s: %v (context: %v)\n%s", config.Stage, err, ctx.Err(), output)
	}
}

func readRecoveryCatalog(t *testing.T, root string) catalog {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, "catalog.json"))
	if err != nil {
		t.Fatal(err)
	}
	var c catalog
	if err = json.Unmarshal(raw, &c); err != nil {
		t.Fatal(err)
	}
	return c
}

func assertRecoveryPath(t *testing.T, path string, wantExists bool) {
	t.Helper()
	_, err := os.Lstat(path)
	if wantExists && err != nil {
		t.Fatalf("expected %s to exist: %v", path, err)
	}
	if !wantExists && !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected %s absent: %v", path, err)
	}
}

func assertRecoveryDirectoryCount(t *testing.T, path string, want int) {
	t.Helper()
	entries, err := os.ReadDir(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != want {
		t.Fatalf("%s has %d entries, want %d: %v", path, len(entries), want, entries)
	}
}
