package unicodenames

import (
	"bufio"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"testing"
)

func unicodeInput(t *testing.T, name, digest string) string {
	t.Helper()
	file, err := os.Open("ucd/" + name + ".gz")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	reader, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprintf("%x", sha256.Sum256(data)); got != digest {
		t.Fatalf("%s SHA-256 = %s, want %s", name, got, digest)
	}
	return string(data)
}

func TestUnicodeDataNamesAndAliases(t *testing.T) {
	// The source files, rather than generated entries, are the acceptance oracle.
	names := map[string]bool{}
	data := unicodeInput(t, "UnicodeData.txt", "ff58e5823bd095166564a006e47d111130813dcf8bf234ef79fa51a870edb48f")
	for _, line := range strings.Split(strings.TrimSpace(data), "\n") {
		name := strings.Split(line, ";")[1]
		if !strings.HasPrefix(name, "<") {
			names[name] = true
		}
	}
	aliases := unicodeInput(t, "NameAliases.txt", "9953f0fcebf5ea8091c5c581e4df0e43f20d2533c84ccca7987a9bb819a896a8")
	scanner := bufio.NewScanner(strings.NewReader(aliases))
	for scanner.Scan() {
		line, _, _ := strings.Cut(scanner.Text(), "#")
		if line = strings.TrimSpace(line); line != "" {
			names[strings.Split(line, ";")[1]] = true
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if len(names) != nameCount {
		t.Fatalf("%d source names, %d generated names", len(names), nameCount)
	}
	for name := range names {
		if !Valid(name) || !Valid(strings.ToLower(name)) {
			t.Errorf("source name rejected: %q", name)
		}
	}
	// Check that the compressed table contains exactly those source names. This
	// detects unintended extra entries as well as incomplete or malformed blocks.
	seen := map[string]bool{}
	previous := ""
	var decoded [maxNameLength]byte
	for start := 0; start < len(nameData); {
		if start+2 > len(nameData) {
			t.Fatal("truncated name header")
		}
		shared, size := int(nameData[start]), int(nameData[start+1])
		start += 2
		if shared > len(previous) || shared+size > maxNameLength || start+size > len(nameData) {
			t.Fatal("invalid compressed name")
		}
		copy(decoded[shared:], nameData[start:start+size])
		start += size
		name := string(decoded[:shared+size])
		if !names[name] || seen[name] || name <= previous {
			t.Fatalf("unexpected or unordered compressed name: %q", name)
		}
		seen[name] = true
		previous = name
	}
	if len(seen) != len(names) {
		t.Fatalf("decoded %d names, want %d", len(seen), len(names))
	}
}

func TestAlgorithmicUnicodeNames(t *testing.T) {
	// All 11,172 normative Hangul combinations must survive greedy matching,
	// including the empty initial/trailing component and overlapping prefixes.
	count := 0
	for _, initial := range strings.Split("G GG N D DD R M B BB S SS  J JJ C K T P H", " ") {
		for _, vowel := range strings.Fields("A AE YA YAE EO E YEO YE O WA WAE OE YO U WEO WE WI YU EU YI I") {
			for _, final := range strings.Split(" G GG GS N NJ NH D L LG LM LB LS LT LP LH M B BS S SS NG J C K T P H", " ") {
				name := "HANGUL SYLLABLE " + initial + vowel + final
				if !Valid(name) || !Valid(strings.ToLower(name)) {
					t.Fatalf("Hangul name rejected: %q", name)
				}
				count++
			}
		}
	}
	if count != 11172 {
		t.Fatalf("checked %d Hangul names", count)
	}
	// Read assigned ranges independently from the pinned UCD. Test every point
	// and both neighbors, including gaps inside CJK extension blocks.
	data := unicodeInput(t, "UnicodeData.txt", "ff58e5823bd095166564a006e47d111130813dcf8bf234ef79fa51a870edb48f")
	var first uint64
	for _, line := range strings.Split(strings.TrimSpace(data), "\n") {
		fields := strings.Split(line, ";")
		prefix := ""
		switch {
		case strings.HasPrefix(fields[1], "<CJK Ideograph"):
			prefix = "CJK UNIFIED IDEOGRAPH-"
		case strings.HasPrefix(fields[1], "<Tangut Ideograph"):
			prefix = "TANGUT IDEOGRAPH-"
		default:
			continue
		}
		code, err := strconv.ParseUint(fields[0], 16, 32)
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasSuffix(fields[1], ", First>") {
			first = code
			continue
		}
		for point := first; point <= code; point++ {
			name := fmt.Sprintf("%s%X", prefix, point)
			if !Valid(name) || !Valid(strings.ToLower(name)) {
				t.Fatalf("assigned algorithmic name rejected: %q", name)
			}
		}
		for _, point := range []uint64{first - 1, code + 1} {
			name := fmt.Sprintf("%s%X", prefix, point)
			if Valid(name) {
				t.Fatalf("unassigned algorithmic neighbor accepted: %q", name)
			}
		}
	}
}

func TestNameSpellingBoundaries(t *testing.T) {
	for _, name := range []string{
		"LATIN CAPITAL LETTER A", "latin capital letter a", "NuLl", "BOM", "BYTE ORDER MARK",
		"GREEK CAPITAL LETTER LAMDA", "TIBETAN MARK BKA- SHOG GI MGO RGYAN",
		"CJK COMPATIBILITY IDEOGRAPH-F900", "KHITAN SMALL SCRIPT CHARACTER-18B00",
		"EGYPTIAN HIEROGLYPH-13460", "HANGUL SYLLABLE GAG", "hangul syllable wae",
		"CJK UNIFIED IDEOGRAPH-4e00", "Tangut Ideograph-187F7",
		"BOX DRAWINGS LIGHT DIAGONAL UPPER CENTRE TO MIDDLE LEFT AND MIDDLE RIGHT TO LOWER CENTRE",
	} {
		if !Valid(name) {
			t.Errorf("valid name rejected: %q", name)
		}
	}
	for _, name := range []string{
		"", "A", "NOT A UNICODE NAME", " LATIN CAPITAL LETTER A", "LATIN CAPITAL LETTER A ",
		"LATIN  CAPITAL LETTER A", "LATIN-CAPITAL LETTER A", "LATIN_CAPITAL_LETTER_A",
		"LATIN\tCAPITAL LETTER A", "LATIN CAPITAL LETTER A\x00", "LATIN CAPITAL LETTER A\n",
		"LATIN CAPITAL LETTER K", "LATIN CAPITAL LETTER \xff", "\u017fPACE", "<control>",
		"KEYCAP NUMBER SIGN", "KEYCAP DIGIT ONE", "LATIN CAPITAL LETTER A WITH MACRON AND GRAVE",
		"HANGUL SYLLABLE ", "HANGUL SYLLABLE G", "HANGUL SYLLABLE GAA", "HANGUL SYLLABLE GAZ",
		"CJK UNIFIED IDEOGRAPH-04E00", "CJK UNIFIED IDEOGRAPH-00004E00", "CJK UNIFIED IDEOGRAPH-4E0",
		"CJK UNIFIED IDEOGRAPH-4E00F", "CJK UNIFIED IDEOGRAPH-110000", "CJK UNIFIED IDEOGRAPH-+4E00",
		"TANGUT IDEOGRAPH-017000", "TANGUT IDEOGRAPH-187F8", "TANGUT IDEOGRAPH-18D09",
		"CJK COMPATIBILITY IDEOGRAPH-0F900", "KHITAN SMALL SCRIPT CHARACTER-018B00",
		strings.Repeat("A", maxNameLength+1), strings.Repeat("A", 1<<20),
	} {
		if Valid(name) {
			t.Errorf("invalid name accepted: %q", name)
		}
	}
}
