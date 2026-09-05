package app

import (
	"strings"
	"testing"
)

func TestNamedUnicodeEscapesAcrossCacheStates(t *testing.T) {
	for _, tc := range []struct {
		name, literal string
		valid         bool
	}{
		{"names_and_aliases", `f'\N{LATIN CAPITAL LETTER A}\N{nul}{1}'`, true},
		{"unknown_name", `'\N{PUREPY UNKNOWN CHARACTER}'`, false},
		{"named_sequence", `'\N{KEYCAP DIGIT ONE}'`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := cliProject(t, map[string]string{
				"src/text.py": "from typing import Final\nTEXT: Final[str] = '\\N{CJK UNIFIED IDEOGRAPH-4E00}'\n",
				"src/app.py":  "from text import TEXT\ndef run() -> str:\n    return TEXT + " + tc.literal + "\n",
			}, []string{"app.run"}, nil)
			uncached := Check(Options{Path: root, NoCache: true, Jobs: 1})
			if uncached.OK != tc.valid || !tc.valid && !strings.Contains(cliJSON(t, uncached), "PP002") {
				t.Fatalf("unexpected Unicode escape verdict: %s", cliJSON(t, uncached))
			}
			for i, jobs := range []int{1, 4, 1} {
				r := Check(Options{Path: root, Jobs: jobs})
				wantHits := 0
				if i > 0 {
					wantHits = r.Files
				}
				if r.CacheHits != wantHits || cliJSON(t, r) != cliJSON(t, uncached) {
					t.Fatalf("cache state changed Unicode escape verification (hits %d, want %d): %s", r.CacheHits, wantHits, cliJSON(t, r))
				}
			}
		})
	}
}
