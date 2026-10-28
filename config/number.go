package config

import (
	"fmt"
	"math/big"
	"regexp"
	"strings"
)

var (
	tomlInteger = regexp.MustCompile(`^[+-]?(0|[1-9](_?[0-9])*)$`)
	tomlDecimal = regexp.MustCompile(`^[+-]?(0|[1-9](_?[0-9])*)(\.[0-9](_?[0-9])*)?([eE][+-]?[0-9](_?[0-9])*)?$`)
	yamlInteger = regexp.MustCompile(`^[+-]?[0-9](_?[0-9])*$`)
	yamlDecimal = regexp.MustCompile(`^[+-]?([0-9](_?[0-9])*(\.[0-9](_?[0-9])*)?|[0-9](_?[0-9])*\.|\.[0-9](_?[0-9])*)([eE][+-]?[0-9](_?[0-9])*)?$`)
)

func integerToken(text string, yaml bool) ([]byte, error) {
	unsigned := text
	if len(unsigned) > 0 && (unsigned[0] == '+' || unsigned[0] == '-') {
		unsigned = unsigned[1:]
	}
	base := 10
	digits := unsigned
	if len(unsigned) >= 2 && unsigned[0] == '0' {
		switch unsigned[1] {
		case 'x':
			base, digits = 16, unsigned[2:]
		case 'o':
			base, digits = 8, unsigned[2:]
		case 'b':
			base, digits = 2, unsigned[2:]
		}
	}
	if base != 10 {
		if !yaml && unsigned != text || !validRadixDigits(digits, base) {
			return nil, fmt.Errorf("invalid integer %q", text)
		}
	} else {
		pattern := tomlInteger
		if yaml {
			pattern = yamlInteger
		}
		if !pattern.MatchString(text) {
			return nil, fmt.Errorf("invalid integer %q", text)
		}
		// yaml.v3 also resolves legacy leading-zero octal integers.
		if yaml && len(digits) > 1 && digits[0] == '0' && validRadixDigits(digits, 8) {
			base = 8
		}
	}
	n, ok := new(big.Int).SetString(strings.ReplaceAll(digits, "_", ""), base)
	if !ok {
		return nil, fmt.Errorf("invalid integer %q", text)
	}
	if strings.HasPrefix(text, "-") {
		n.Neg(n)
	}
	return []byte(n.String()), nil
}

func validRadixDigits(text string, base int) bool {
	if text == "" || text[0] == '_' || text[len(text)-1] == '_' || strings.Contains(text, "__") {
		return false
	}
	for _, c := range text {
		if c == '_' {
			continue
		}
		digit := int(c - '0')
		if c >= 'a' && c <= 'f' {
			digit = int(c-'a') + 10
		} else if c >= 'A' && c <= 'F' {
			digit = int(c-'A') + 10
		}
		if digit < 0 || digit >= base {
			return false
		}
	}
	return true
}

func decimalToken(text string, yaml bool) ([]byte, error) {
	pattern := tomlDecimal
	if yaml {
		pattern = yamlDecimal
	}
	if !pattern.MatchString(text) {
		return nil, fmt.Errorf("invalid or non-finite decimal %q", text)
	}
	number := strings.TrimPrefix(strings.ReplaceAll(text, "_", ""), "+")
	if !yaml {
		return []byte(number), nil
	}
	sign := ""
	if strings.HasPrefix(number, "-") {
		sign, number = "-", number[1:]
	}
	mantissa, exponent := number, ""
	if index := strings.IndexAny(number, "eE"); index >= 0 {
		mantissa, exponent = number[:index], number[index:]
	}
	if strings.HasPrefix(mantissa, ".") {
		mantissa = "0" + mantissa
	}
	if strings.HasSuffix(mantissa, ".") {
		mantissa += "0"
	}
	for len(mantissa) > 1 && mantissa[0] == '0' && mantissa[1] != '.' {
		mantissa = mantissa[1:]
	}
	return []byte(sign + mantissa + exponent), nil
}
