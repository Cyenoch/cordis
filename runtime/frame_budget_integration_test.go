//go:build integration

package runtime_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Cyenoch/cordis/contract"
	"github.com/Cyenoch/cordis/runtime/native"
)

func TestIndependentSDKFrameBudget(t *testing.T) {
	root := t.TempDir()
	moduleRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	module := fmt.Sprintf("module example.net/budget\n\ngo 1.27.3\n\nrequire github.com/Cyenoch/cordis v0.0.0\n\nreplace github.com/Cyenoch/cordis => %s\n", filepath.ToSlash(moduleRoot))
	source := `package main
import (
 "context"
 "encoding/json"
 "os"
 "strings"
 "sync/atomic"
 "github.com/Cyenoch/cordis"
 "github.com/Cyenoch/cordis/contract"
 "github.com/Cyenoch/cordis/sdk"
)
func main() {
 plugin := cordis.Plugin[struct{}]{Name:"budget", Start:func(_ context.Context, s *cordis.Scope, _ struct{}) error {
  var calls atomic.Int32
  identity, _ := sdk.Identity(s)
  return contract.EndpointKey("budget",1).Provide(s, contract.Methods(contract.Service{Name:"budget",Major:1}, map[string]contract.Handler{"run":func(ctx context.Context, raw json.RawMessage)(json.RawMessage,error){
   var operation string
   if err:=json.Unmarshal(raw,&operation);err!=nil{return nil,err}
   switch operation {
   case "large": calls.Add(1); return json.Marshal(strings.Repeat("<",20000))
   case "error": calls.Add(1); return nil,contract.Fail(contract.Internal,strings.Repeat("private",20000))
   case "reverse":
    large,err:=contract.EndpointKey("large",1).Get(s)
    if err!=nil{return nil,err}
    _,err=large.Call(ctx,"read",json.RawMessage("{}"))
    if coded,ok:=err.(*contract.Error);!ok||coded.Code!=contract.PluginFailed{return nil,contract.Fail(contract.Internal,"reverse result was not isolated")}
   }
   return json.Marshal(struct{Calls int ` + "`json:\"calls\"`" + `;Activation string ` + "`json:\"activation\"`" + `;PID int ` + "`json:\"pid\"`" + `}{int(calls.Load()),identity.Activation,os.Getpid()})
  }}))
 }}
 if err := sdk.Run(context.Background(), sdk.Define(plugin)); err!=nil{os.Exit(1)}
}
`
	for name, content := range map[string]string{"go.mod": module, "main.go": source} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	service := contract.Service{Name: "budget", Major: 1}
	artifact := filepath.Join(root, "budget")
	if runtime.GOOS == "windows" {
		artifact += ".exe"
	}
	cmd := exec.Command("go", "build", "-mod=mod", "-o", artifact, ".")
	cmd.Dir, cmd.Env = root, os.Environ()
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("independent native build: %v\n%s", err, output)
	}
	for _, budget := range []int{4096, 16384} {
		t.Run(fmt.Sprint(budget), func(t *testing.T) {
			adapter := native.New()
			start := func() contract.Session {
				identity := contract.Identity{Package: "budget", Entry: "one", Activation: contract.NewID()}
				session, err := adapter.Start(t.Context(), contract.Launch{FrameBytes: budget, Identity: identity, Directory: root, Artifact: artifact, Handle: func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
					return json.Marshal(strings.Repeat("x", budget))
				}})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { session.Close() })
				input, _ := json.Marshal(contract.Initialize{FrameBytes: budget, Inflight: contract.DefaultInflight, Identity: identity, Protocol: contract.Protocol{Major: contract.ProtocolMajor},
					Definition: "budget", Config: json.RawMessage(`{}`), Features: []string{"cancel", "parent", "events", "children", "subscriptions"},
					Services: []contract.Service{{Name: "large", Major: 1}}, Provides: []contract.Service{service}})
				if _, err := session.Call(t.Context(), "initialize", input); err != nil {
					t.Fatal(err)
				}
				return session
			}
			first, neighbor := start(), start()
			call := func(session contract.Session, operation string) (json.RawMessage, error) {
				input, _ := json.Marshal(operation)
				request, _ := json.Marshal(contract.Invocation{Service: service, Method: "run", Input: input})
				return session.Call(t.Context(), "call", request)
			}
			before, err := call(first, "read")
			if err != nil {
				t.Fatal(err)
			}
			for _, operation := range []string{"large", "error"} {
				_, err := call(first, operation)
				coded, ok := errors.AsType[*contract.Error](err)
				if !ok || coded.Code != contract.PluginFailed {
					t.Fatalf("%s: %v", operation, err)
				}
			}
			after, err := call(first, "reverse")
			if err != nil {
				t.Fatal(err)
			}
			var previous, current struct {
				Calls      int    `json:"calls"`
				Activation string `json:"activation"`
				PID        int    `json:"pid"`
			}
			if err := json.Unmarshal(before, &previous); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(after, &current); err != nil {
				t.Fatal(err)
			}
			if current.Calls != 2 || previous.Activation != current.Activation || previous.PID != current.PID {
				t.Fatalf("replayed or replaced activation: %s -> %s", before, after)
			}
			if output, err := call(neighbor, "read"); err != nil || !strings.Contains(string(output), `"calls":0`) {
				t.Fatalf("neighbor affected: %s, %v", output, err)
			}
			for _, session := range []contract.Session{first, neighbor} {
				if _, err := session.Call(t.Context(), "shutdown", nil); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
