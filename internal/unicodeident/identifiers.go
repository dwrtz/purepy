// Package unicodeident implements Python 3.14 identifier properties and Unicode
// normalization using pinned Unicode 16.0 data, independent of the Go toolchain.
package unicodeident

import (
	_ "embed"
	"encoding/binary"
	"slices"
	"strings"
	"unicode/utf8"
)

//go:embed identifiers.bin
var tables string

func uint32At(at int) uint32 {
	return binary.LittleEndian.Uint32([]byte(tables[at : at+4]))
}

// row searches fixed-width records sorted by their first uint32 field.
func row(offset, width, count int, key uint32) (int, bool) {
	low, high := 0, count
	for low < high {
		middle := low + (high-low)/2
		at := offset + middle*width
		value := uint32At(at)
		if value == key {
			return at, true
		}
		if value < key {
			low = middle + 1
		} else {
			high = middle
		}
	}
	return offset + low*width, false
}

func inRanges(r rune, offset, count int) bool {
	if r < 0 {
		return false
	}
	key := uint32(r)
	at, exact := row(offset, 8, count, key)
	return exact || at > offset && key <= uint32At(at-4)
}

// Start reports Python's xid_start property, including Python's underscore.
func Start(r rune) bool {
	if r < utf8.RuneSelf {
		return r == '_' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z'
	}
	return inRanges(r, startOffset, startCount)
}

// Continue reports Python's xid_continue property.
func Continue(r rune) bool {
	if r < utf8.RuneSelf {
		return Start(r) || r >= '0' && r <= '9'
	}
	return inRanges(r, continueOffset, continueCount)
}

// Canonical reports whether name has Python identifier syntax in NFKC form.
// Reserved keywords are the caller's responsibility.
func Canonical(name string) bool {
	if name == "" || !utf8.ValidString(name) {
		return false
	}
	for i, r := range name {
		if i == 0 && !Start(r) || i != 0 && !Continue(r) {
			return false
		}
	}
	return NFKC(name) == name
}

func combiningClass(r rune) uint8 {
	if r < 0x300 {
		return 0
	}
	if at, ok := row(combiningOffset, 8, combiningCount, uint32(r)); ok {
		return uint8(uint32At(at + 4))
	}
	return 0
}

type character struct {
	r  rune
	cc uint8
}

// Hangul constants and algorithms are specified by Unicode UAX #15.
const (
	hangulBase    = 0xAC00
	leadBase      = 0x1100
	vowelBase     = 0x1161
	tailBase      = 0x11A7
	leadCount     = 19
	vowelCount    = 21
	tailCount     = 28
	blockCount    = vowelCount * tailCount
	syllableCount = leadCount * blockCount
)

func decompose(out []character, r rune) []character {
	if index := r - hangulBase; index >= 0 && index < syllableCount {
		out = append(out, character{r: leadBase + index/blockCount}, character{r: vowelBase + index%blockCount/tailCount})
		if tail := index % tailCount; tail != 0 {
			out = append(out, character{r: tailBase + tail})
		}
		return out
	}
	if r >= utf8.RuneSelf {
		if at, ok := row(decompositionOffset, 12, decompositionCount, uint32(r)); ok {
			start, length := uint32At(at+4), uint32At(at+8)
			for _, part := range tables[start : start+length] {
				out = decompose(out, part)
			}
			return out
		}
	}
	return append(out, character{r: r, cc: combiningClass(r)})
}

func compose(first, second rune) (rune, bool) {
	if lead, vowel := first-leadBase, second-vowelBase; lead >= 0 && lead < leadCount && vowel >= 0 && vowel < vowelCount {
		return hangulBase + (lead*vowelCount+vowel)*tailCount, true
	}
	if syllable, tail := first-hangulBase, second-tailBase; syllable >= 0 && syllable < syllableCount && syllable%tailCount == 0 && tail > 0 && tail < tailCount {
		return first + tail, true
	}
	key := uint64(first)<<21 | uint64(second)
	low, high := 0, compositionCount
	for low < high {
		middle := low + (high-low)/2
		at := compositionOffset + middle*12
		value := binary.LittleEndian.Uint64([]byte(tables[at : at+8]))
		if value == key {
			return rune(uint32At(at + 8)), true
		}
		if value < key {
			low = middle + 1
		} else {
			high = middle
		}
	}
	return 0, false
}

// NFKC returns Unicode 16.0 compatibility decomposition followed by canonical
// ordering and composition. ASCII names take an allocation-free fast path.
func NFKC(name string) string {
	nonASCII := false
	for i := range len(name) {
		if name[i] >= utf8.RuneSelf {
			nonASCII = true
			break
		}
	}
	if !nonASCII {
		return name
	}
	characters := make([]character, 0, utf8.RuneCountInString(name))
	for _, r := range name {
		characters = decompose(characters, r)
	}
	// Sort each run of combining marks stably, avoiding quadratic insertion
	// for adversarial identifiers with many reordered marks.
	start := 0
	ordered := true
	for i := 0; i <= len(characters); i++ {
		if i == len(characters) || characters[i].cc == 0 {
			if !ordered {
				slices.SortStableFunc(characters[start:i], func(a, b character) int { return int(a.cc) - int(b.cc) })
			}
			start, ordered = i+1, true
		} else if i > start && characters[i-1].cc > characters[i].cc {
			ordered = false
		}
	}
	out := characters[:0]
	starter := -1
	lastClass := uint8(0)
	for _, ch := range characters {
		if starter >= 0 && (lastClass == 0 || lastClass < ch.cc) {
			if combined, ok := compose(out[starter].r, ch.r); ok {
				out[starter].r = combined
				continue
			}
		}
		if ch.cc == 0 {
			starter = len(out)
		}
		out = append(out, ch)
		lastClass = ch.cc
	}
	var result strings.Builder
	result.Grow(len(name))
	for _, ch := range out {
		result.WriteRune(ch.r)
	}
	return result.String()
}
