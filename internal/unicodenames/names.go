// Package unicodenames validates the character names accepted in Python 3.14
// named Unicode escapes. It uses a pinned UCD, never the host's Unicode tables.
package unicodenames

import (
	_ "embed"
	"strings"
)

// Each generated block starts with a complete name. Subsequent names store a
// one-byte shared-prefix length, one-byte suffix length, and the suffix bytes.
// A lookup binary-searches block heads and decodes at most blockSize names.
//
//go:embed names.bin
var nameData string

// Valid reports whether name names a single character in a Python 3.14 named
// Unicode escape. ASCII letter case is ignored; spaces and hyphens are exact.
// Name aliases are accepted, but Unicode named sequences are not. Lookup uses
// fixed-size stack buffers and does not evaluate or decode the string literal.
func Valid(name string) bool {
	if len(name) == 0 || len(name) > maxNameLength {
		return false
	}
	var upper [maxNameLength]byte
	for i := range len(name) {
		c := name[i]
		if c >= 'a' && c <= 'z' {
			c -= 'a' - 'A'
		}
		if !(c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == ' ' || c == '-') {
			return false
		}
		upper[i] = c
	}
	name = string(upper[:len(name)])
	if strings.HasPrefix(name, "HANGUL SYLLABLE ") {
		return validHangul(name[len("HANGUL SYLLABLE "):])
	}
	for _, r := range hexRanges {
		if !strings.HasPrefix(name, r.prefix) {
			continue
		}
		suffix := name[len(r.prefix):]
		if len(suffix) < 4 || len(suffix) > 6 || suffix[0] == '0' {
			return false
		}
		var code uint32
		for i := range len(suffix) {
			c := suffix[i]
			code *= 16
			switch {
			case c >= '0' && c <= '9':
				code += uint32(c - '0')
			case c >= 'A' && c <= 'F':
				code += uint32(c-'A') + 10
			default:
				return false
			}
		}
		if code >= r.first && code <= r.last {
			return true
		}
	}
	return validTableName(name)
}

func blockHead(block int) string {
	start := int(blockOffsets[block])
	return nameData[start+2 : start+2+int(nameData[start+1])]
}

func validTableName(name string) bool {
	// Find the last block whose first name is no greater than the query.
	lo, hi := 0, len(blockOffsets)-1
	for lo < hi {
		mid := lo + (hi-lo)/2
		if blockHead(mid) <= name {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if lo == 0 {
		return false
	}
	var decoded [maxNameLength]byte
	for start, end := int(blockOffsets[lo-1]), int(blockOffsets[lo]); start < end; {
		shared, size := int(nameData[start]), int(nameData[start+1])
		start += 2
		copy(decoded[shared:], nameData[start:start+size])
		start += size
		candidate := string(decoded[:shared+size])
		if candidate >= name {
			return candidate == name
		}
	}
	return false
}

var hangulLeads = [...]string{"G", "GG", "N", "D", "DD", "R", "M", "B", "BB", "S", "SS", "", "J", "JJ", "C", "K", "T", "P", "H"}
var hangulVowels = [...]string{"A", "AE", "YA", "YAE", "EO", "E", "YEO", "YE", "O", "WA", "WAE", "OE", "YO", "U", "WEO", "WE", "WI", "YU", "EU", "YI", "I"}
var hangulTails = [...]string{"", "G", "GG", "GS", "N", "NJ", "NH", "D", "L", "LG", "LM", "LB", "LS", "LT", "LP", "LH", "M", "B", "BS", "S", "SS", "NG", "J", "C", "K", "T", "P", "H"}

func validHangul(name string) bool {
	for _, parts := range [][]string{hangulLeads[:], hangulVowels[:], hangulTails[:]} {
		longest := -1
		for _, part := range parts {
			if len(part) > longest && strings.HasPrefix(name, part) {
				longest = len(part)
			}
		}
		if longest < 0 {
			return false
		}
		name = name[longest:]
	}
	return name == ""
}
