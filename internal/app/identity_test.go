package app

import (
	"strings"
	"testing"
)

func TestNoneIdentityAcrossCacheStates(t *testing.T) {
	for _, tc := range []struct {
		name, source string
		valid        bool
	}{
		{"exact_and_narrowed", "def run(x: int | None) -> bool:\n    if x is None:\n        return True\n    return x is not None and missing() is not x\n", true},
		{"invalid_later_chain_pair", "def run(x: int, y: int) -> bool:\n    return x is not None is not y is x\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := cliProject(t, map[string]string{
				"src/other.py": "def missing() -> None:\n    return\n",
				"src/app.py":   "from other import missing\n" + tc.source,
			}, []string{"app.run"}, nil)
			baseline := Check(Options{Path: root, NoCache: true, Jobs: 1})
			if baseline.OK != tc.valid || !tc.valid && !strings.Contains(cliJSON(t, baseline), "PP212") {
				t.Fatalf("unexpected identity verdict: %s", cliJSON(t, baseline))
			}
			for i, jobs := range []int{1, 4, 1} {
				r := Check(Options{Path: root, Jobs: jobs})
				wantHits := 0
				if i > 0 {
					wantHits = r.Files
				}
				if r.Files != 2 || r.CacheHits != wantHits || cliJSON(t, r) != cliJSON(t, baseline) {
					t.Fatalf("cache/workers changed identity verdict (hits %d, want %d): %s", r.CacheHits, wantHits, cliJSON(t, r))
				}
			}
		})
	}
}
