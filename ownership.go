package cordis

import "github.com/Cyenoch/cordis/contract"

func ownedEntries(entries []contract.Entry, roots map[string]bool) []contract.Entry {
	ordered := orderedEntries(entries)
	result := make([]contract.Entry, 0, len(entries))
	for _, entry := range ordered {
		if roots[entry.ID] || roots[entry.Parent] {
			roots[entry.ID] = true
			result = append(result, entry)
		}
	}
	return result
}
