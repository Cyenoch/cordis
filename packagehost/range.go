package packagehost

import (
	"fmt"
	"math/big"
	"strings"

	"github.com/Cyenoch/cordis/contract"
)

// VersionSatisfies accepts fixed SemVer comparator intersections; no OR,
// wildcards, tags, implicit fetching or coercion is supported.
func VersionSatisfies(text, expression string) (bool, error) {
	value, err := parseVersion(text)
	if err != nil {
		return false, err
	}
	terms := strings.Fields(expression)
	if len(terms) == 0 {
		return false, fmt.Errorf("empty dependency range")
	}
	accepted := true
	for _, term := range terms {
		base := strings.TrimLeft(term, "><=^~")
		op := strings.TrimSuffix(term, base)
		bound, err := parseVersion(base)
		if err != nil {
			return false, err
		}
		comparison := compareVersion(value, bound)
		switch op {
		case ">=":
			accepted = accepted && comparison >= 0
		case ">":
			accepted = accepted && comparison > 0
		case "<=":
			accepted = accepted && comparison <= 0
		case "<":
			accepted = accepted && comparison < 0
		case "=":
			accepted = accepted && comparison == 0
		case "^", "~":
			index := 1
			if op == "^" {
				index = 0
				for index < 2 && bound.core[index] == "0" {
					index++
				}
			}
			upper := bound
			upper.pre = nil
			number, ok := new(big.Int).SetString(upper.core[index], 10)
			if !ok {
				return false, fmt.Errorf("invalid range bound")
			}
			upper.core[index] = number.Add(number, big.NewInt(1)).String()
			for next := index + 1; next < 3; next++ {
				upper.core[next] = "0"
			}
			accepted = accepted && comparison >= 0 && compareVersion(value, upper) < 0
		default:
			return false, fmt.Errorf("unsupported dependency comparator %q", op)
		}
	}
	return accepted, nil
}

func acceptsDependency(version string, dependency contract.Dependency) (bool, error) {
	if dependency.Range != "" {
		return VersionSatisfies(version, dependency.Range)
	}
	return VersionInRange(version, dependency.Min, dependency.Max)
}

func dependencyConstraint(dependency contract.Dependency) string {
	if dependency.Range != "" {
		return dependency.Range
	}
	return fmt.Sprintf("[%s,%s)", dependency.Min, dependency.Max)
}
