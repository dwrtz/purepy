package unicodeident

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

func input(t *testing.T, path, digest string) []byte {
	t.Helper()
	compressed, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprintf("%x", sha256.Sum256(data)); got != digest {
		t.Fatalf("%s: wrong pinned input hash %s", path, got)
	}
	return data
}

func hexCode(t *testing.T, text string) rune {
	t.Helper()
	n, err := strconv.ParseUint(text, 16, 32)
	if err != nil {
		t.Fatal(err)
	}
	return rune(n)
}

func TestAllUnicode16IdentifierProperties(t *testing.T) {
	data := input(t, "ucd/DerivedCoreProperties.txt.gz", "39d35161f2954497f69e08bdb9e701493f476a3d30222de20028feda36c1dabd")
	start := make([]bool, utf8.MaxRune+1)
	cont := make([]bool, utf8.MaxRune+1)
	start['_'] = true
	for _, line := range strings.Split(string(data), "\n") {
		text, _, _ := strings.Cut(line, "#")
		fields := strings.Split(text, ";")
		if len(fields) < 2 {
			continue
		}
		var property []bool
		switch strings.TrimSpace(fields[1]) {
		case "XID_Start":
			property = start
		case "XID_Continue":
			property = cont
		default:
			continue
		}
		bounds := strings.Split(strings.TrimSpace(fields[0]), "..")
		for cp, last := hexCode(t, bounds[0]), hexCode(t, bounds[len(bounds)-1]); cp <= last; cp++ {
			property[cp] = true
		}
	}
	for cp := rune(0); cp <= utf8.MaxRune; cp++ {
		if Start(cp) != start[cp] || Continue(cp) != cont[cp] {
			t.Fatalf("U+%04X: start=%t/%t continue=%t/%t", cp, Start(cp), start[cp], Continue(cp), cont[cp])
		}
	}
	for _, cp := range []rune{-1, utf8.MaxRune + 1} {
		if Start(cp) || Continue(cp) {
			t.Fatalf("accepted invalid code point %d", cp)
		}
	}
}

func TestUnicode16NormalizationConformance(t *testing.T) {
	data := input(t, "ucd/NormalizationTest.txt.gz", "d811971453e7075e1ad56fb1b301eece5aa80757b81f6156e74a1bfb3ae5ceb1")
	cases := 0
	for lineNumber, line := range strings.Split(string(data), "\n") {
		text, _, _ := strings.Cut(line, "#")
		text = strings.TrimSpace(text)
		if text == "" || strings.HasPrefix(text, "@") {
			continue
		}
		columns := strings.Split(text, ";")
		var values []string
		for _, column := range columns[:5] {
			var value strings.Builder
			for _, cp := range strings.Fields(column) {
				value.WriteRune(hexCode(t, cp))
			}
			values = append(values, value.String())
		}
		for _, value := range values {
			if got := NFKC(value); got != values[3] {
				t.Fatalf("NormalizationTest line %d: NFKC(%U)=%U, want %U", lineNumber+1, []rune(value), []rune(got), []rune(values[3]))
			}
		}
		cases++
	}
	if cases != 19965 {
		t.Fatalf("unexpected Unicode 16.0 normalization corpus size %d", cases)
	}
}

func TestCanonicalIdentifierBoundaries(t *testing.T) {
	for _, name := range []string{"a", "_0", "café", "β2", "\u1c89", "x\U00016D6A", "q\u0307\u0323"} {
		// A noncomposing starter may still need its marks reordered.
		want := name != "q\u0307\u0323"
		if Canonical(name) != want {
			t.Errorf("Canonical(%q)=%t, want %t", name, Canonical(name), want)
		}
	}
	for _, name := range []string{"", "0a", "a b", "a\x00", "\xff", "ﬁle", "cafe\u0301", "x\U00016D63\U00016D67\U00016D67", "\U0010FFFF", "\u0301a"} {
		if Canonical(name) {
			t.Errorf("accepted invalid or noncanonical identifier %q", name)
		}
	}
}

func TestNormalizationLongCombiningSequence(t *testing.T) {
	// Both marks compose with 'A', but canonical order chooses dot below first;
	// repeated equal classes then block later composition.
	name := "A" + strings.Repeat("\u0307\u0323", 4096)
	want := "\u1EA0" + strings.Repeat("\u0323", 4095) + strings.Repeat("\u0307", 4096)
	if got := NFKC(name); got != want {
		t.Fatal("long combining sequence was not stably ordered and composed")
	}
}

func TestASCIINormalizationDoesNotAllocate(t *testing.T) {
	if allocations := testing.AllocsPerRun(100, func() { _ = NFKC("simple_identifier123") }); allocations != 0 {
		t.Fatalf("ASCII normalization allocated %g times", allocations)
	}
}
