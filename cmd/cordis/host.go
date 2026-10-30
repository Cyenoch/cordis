package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"log/slog"
	"time"

	cordis "github.com/Cyenoch/cordis"
	"github.com/Cyenoch/cordis/contract"
)

func hostCommand(ctx context.Context, flags *flag.FlagSet, parse func() (bool, error), out, errOut io.Writer) (result error) {
	directory := flags.String("store", "", "explicit persistent store directory")
	trustFile := flags.String("trust", "", "explicit JSON/TOML/YAML public-key trust file")
	controlFile := flags.String("control-file", "", "create a private local-control connection file")
	recoveryFile := flags.String("recovery", "", "explicit per-entry recovery policy file")
	shutdownTimeout := flags.Duration("shutdown-timeout", 5*time.Second, "maximum shutdown wait")
	if done, err := parse(); done {
		return err
	}
	if flags.NArg() != 0 || *directory == "" || *trustFile == "" || *shutdownTimeout <= 0 {
		return &usageError{message: commandUsage["host"]}
	}
	trust, err := loadTrust(*trustFile)
	if err != nil {
		return err
	}
	logger := slog.New(slog.NewJSONHandler(errOut, nil))
	var recovery map[string]cordis.RecoveryPolicy
	if *recoveryFile != "" {
		recovery, err = loadRecovery(*recoveryFile)
		if err != nil {
			return err
		}
	}
	h, err := cordis.Open(cordis.Options{Directory: *directory, Trust: trust, Logger: logger, Recovery: recovery})
	if err != nil {
		return err
	}
	runCtx, cancel := context.WithCancel(ctx)
	var control *controlServer
	defer func() {
		cancel()
		if control != nil {
			result = errors.Join(result, control.close())
		}
		result = errors.Join(result, closeHost(h, *shutdownTimeout))
	}()
	var controlDone <-chan struct{}
	if *controlFile != "" {
		control, err = startControl(runCtx, h, *controlFile)
		if err != nil {
			return err
		}
		controlDone = control.done
	}
	type restored struct {
		report contract.RestoreReport
		err    error
	}
	restoring := make(chan restored, 1)
	go func() {
		report, err := h.Restore(runCtx)
		restoring <- restored{report, err}
	}()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	var last []byte
	for {
		select {
		case <-controlDone:
			return control.err
		case <-ctx.Done():
			cancel()
			if restoring != nil {
				finished := <-restoring
				if err := writeRestore(out, finished.report); err != nil {
					return err
				}
			}
			return nil
		case finished := <-restoring:
			restoring = nil
			if err := writeRestore(out, finished.report); err != nil {
				return err
			}
			if finished.err != nil {
				logger.Error("restore has entry failures", "error", finished.err)
			}
		case <-ticker.C:
			status := struct {
				Instances []contract.InstanceInfo `json:"instances"`
				Recovery  []cordis.RecoveryInfo   `json:"recovery"`
			}{h.Instances(), h.RecoveryStatus()}
			encoded, err := json.Marshal(status)
			if err != nil {
				return err
			}
			if !bytes.Equal(last, encoded) {
				if err := writeJSON(out, status); err != nil {
					return err
				}
				last = encoded
			}
		}
	}
}

func closeHost(h *cordis.Host, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return h.Close(ctx)
}

func writeRestore(out io.Writer, report contract.RestoreReport) error {
	type entry struct {
		ID     string          `json:"id"`
		Action string          `json:"action"`
		Status contract.Status `json:"status"`
		Error  string          `json:"error,omitempty"`
	}
	entries := make([]entry, 0, len(report.Entries))
	for _, item := range report.Entries {
		record := entry{ID: item.ID, Action: item.Action, Status: item.Status}
		if item.Error != nil {
			record.Error = item.Error.Error()
		}
		entries = append(entries, record)
	}
	return writeJSON(out, struct {
		Restore []entry `json:"restore"`
	}{entries})
}
