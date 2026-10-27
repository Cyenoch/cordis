package contract

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
