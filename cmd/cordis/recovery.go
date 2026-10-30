package main

import (
	"fmt"
	"time"

	cordis "github.com/Cyenoch/cordis"
	"github.com/Cyenoch/cordis/config"
)

func loadRecovery(path string) (map[string]cordis.RecoveryPolicy, error) {
	document, err := config.Load(path, "")
	if err != nil {
		return nil, fmt.Errorf("load recovery: %w", err)
	}
	var input map[string]struct {
		MaxRetries   int    `json:"maxRetries"`
		InitialDelay string `json:"initialDelay"`
		MaxDelay     string `json:"maxDelay"`
	}
	if err := document.Decode(&input); err != nil {
		return nil, fmt.Errorf("load recovery: %w", err)
	}
	if input == nil {
		return nil, fmt.Errorf("recovery file must contain an object keyed by entry ID")
	}
	result := make(map[string]cordis.RecoveryPolicy, len(input))
	for id, entry := range input {
		initial, err := time.ParseDuration(entry.InitialDelay)
		if err != nil {
			return nil, fmt.Errorf("recovery %q initialDelay: %w", id, err)
		}
		maximum, err := time.ParseDuration(entry.MaxDelay)
		if err != nil {
			return nil, fmt.Errorf("recovery %q maxDelay: %w", id, err)
		}
		result[id] = cordis.RecoveryPolicy{MaxRetries: entry.MaxRetries, InitialDelay: initial, MaxDelay: maximum}
	}
	return result, nil
}
