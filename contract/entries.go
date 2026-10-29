package contract

import (
	"strconv"
	"strings"
)

// CheckIsolate validates complete, canonical service keys without naming realms.
func CheckIsolate(keys []string) error {
	seen := make(map[string]bool, len(keys))
	for _, key := range keys {
		at := strings.LastIndexByte(key, '@')
		if at <= 0 {
			return Fail(Invalid, "isolation requires full service key: "+key)
		}
		major, err := strconv.ParseUint(key[at+1:], 10, 32)
		if err != nil || strconv.FormatUint(major, 10) != key[at+1:] || seen[key] {
			return Fail(Invalid, "invalid or duplicate isolation key: "+key)
		}
		seen[key] = true
	}
	return nil
}

func CheckEntryGraph(entries []Entry) error {
	byID := make(map[string]Entry, len(entries))
	for _, entry := range entries {
		byID[entry.ID] = entry
	}
	for _, entry := range entries {
		seen := map[string]bool{entry.ID: true}
		for entry.Parent != "" {
			if seen[entry.Parent] {
				return Fail(Reentrant, "persistent entry parent cycle")
			}
			seen[entry.Parent] = true
			parent, exists := byID[entry.Parent]
			if !exists {
				return Fail(Invalid, "persistent entry parent is missing")
			}
			entry = parent
		}
	}
	return nil
}
