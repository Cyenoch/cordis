package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Cyenoch/cordis/contract"
)

func TestWaitingDiagnosticsContainRangeAndVisibleProviders(t *testing.T) {
	required := contract.Requirement{Name: "echo", Major: 1, MinMinor: 1, MaxMinor: 3}
	instance := waitingInstance{status: contract.Status{
		State: contract.Waiting,
		Waiting: []contract.WaitReason{{Kind: "service", Requirement: &required,
			Visible: []contract.Service{{Name: "echo", Major: 1, Minor: 4}}, Reason: "outside minor range"}},
	}}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer cancel()
	var diagnostics bytes.Buffer
	if err := waitForStartup(ctx, instance, "main", &diagnostics); err == nil {
		t.Fatal("unresolved instance succeeded")
	}
	line := strings.TrimPrefix(strings.TrimSpace(diagnostics.String()), "startup ")
	var message struct {
		Definition string                `json:"definition"`
		Waiting    []contract.WaitReason `json:"waiting"`
	}
	if err := json.Unmarshal([]byte(line), &message); err != nil {
		t.Fatal(err, diagnostics.String())
	}
	if message.Definition != "main" || len(message.Waiting) != 1 || *message.Waiting[0].Requirement != required || message.Waiting[0].Visible[0].Minor != 4 {
		t.Fatalf("waiting detail lost: %+v", message)
	}
}

type waitingInstance struct {
	contract.Instance
	status contract.Status
}

func (i waitingInstance) Status() contract.Status { return i.status }
func (i waitingInstance) Wait(ctx context.Context) error {
	<-ctx.Done()
	return ctx.Err()
}
