package hosting

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/Cyenoch/cordis"
	"github.com/Cyenoch/cordis/contract"
	"github.com/Cyenoch/cordis/sdk"
)

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) records(t *testing.T) []map[string]any {
	l.mu.Lock()
	defer l.mu.Unlock()
	var records []map[string]any
	for line := range bytes.Lines(l.b.Bytes()) {
		var record map[string]any
		must(t, json.Unmarshal(line, &record))
		records = append(records, record)
	}
	return records
}

// Records about an activation name it, so a failure can be matched with
// Status and the run history, and one instance's failure is not another's.
func TestActivationRecordsNameTheActivation(t *testing.T) {
	var out lockedBuffer
	f := newFixture(t)
	manifest := echoManifest("test.provider", true)
	manifest.Definitions = append(manifest.Definitions, contract.DefinitionSpec{ID: "broken"})
	f.pack(manifest, sdk.Define(f.echoPlugin("main", "")), sdk.Define(cordis.Plugin[struct{}]{Name: "broken",
		Start: func(context.Context, *cordis.Scope, struct{}) error { return errors.New("broken on purpose") }}))
	f.open(func(o *Options) { o.Logger = slog.New(slog.NewJSONHandler(&out, nil)) })
	good := f.enable(contract.Entry{ID: "good", Package: "test.provider"})
	must(t, good.Wait(f.ctx()))
	bad := f.enable(contract.Entry{ID: "bad", Package: "test.provider", Definition: "broken"})
	if err := bad.Wait(f.ctx()); err == nil {
		t.Fatal("broken definition started")
	}
	must(t, good.Stop(f.ctx()))
	records := map[string][]map[string]any{}
	for _, record := range out.records(t) {
		if message := record["msg"].(string); strings.HasPrefix(message, "activation ") {
			key := message + " " + record["entry"].(string)
			records[key] = append(records[key], record)
		}
	}
	for _, key := range []string{"activation started good", "activation stopped good", "activation failed bad"} {
		if len(records[key]) != 1 {
			t.Fatalf("want one %q record, got: %v", key, records)
		}
	}
	if len(records) != 3 {
		t.Fatalf("unexpected activation records: %v", records)
	}
	started, stopped, failed := records["activation started good"][0], records["activation stopped good"][0], records["activation failed bad"][0]
	for _, named := range []struct {
		record   map[string]any
		instance contract.Instance
	}{{started, good}, {stopped, good}, {failed, bad}} {
		identity := named.instance.Status().Identity
		if named.record["activation"] != identity.Activation || named.record["digest"] != identity.Digest || named.record["package"] != "test.provider" {
			t.Fatalf("record %v names another activation than %+v", named.record, identity)
		}
	}
	if started["startup"] == nil || stopped["stage"] != nil {
		t.Fatalf("started or stopped record lacks its startup, or the stop claims it never started: %v %v", started, stopped)
	}
	if failed["stage"] != "starting" || failed["errorCode"] != string(contract.PluginFailed) || failed["duration"] == nil {
		t.Fatalf("failure before active lacks its stage, class or duration: %v", failed)
	}
}
