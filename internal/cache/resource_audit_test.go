package cache

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/dwrtz/purepy/internal/diag"
)

func TestCacheRejectsDecodeAmplificationBeforeTypedAllocation(t *testing.T) {
	large := `[` + strings.Repeat(`{},`, 99_999) + `{}]`
	for _, duplicate := range []bool{false, true} {
		t.Run(map[bool]string{false: "malformed array", true: "earlier duplicate array"}[duplicate], func(t *testing.T) {
			dir, key := t.TempDir(), Key("decode-budget")
			payload := `{"tree":{"kind":"Module"},"diagnostics":` + large
			if duplicate {
				payload += `,"Diagnostics":[]`
			}
			payload += `}`
			encoded, err := json.Marshal(artifact{Schema: Schema, Key: key, Payload: json.RawMessage(payload), Checksum: Digest([]byte(payload))})
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, key+".json"), encoded, 0o600); err != nil {
				t.Fatal(err)
			}
			runtime.GC()
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			_, hit := Read(dir, key)
			runtime.ReadMemStats(&after)
			if hit && !duplicate {
				t.Fatal("malformed diagnostic array must be a miss")
			}
			// This previously allocated 107 MiB for a 300 KiB artifact. The
			// generous budget allows byte copies but no giant typed slice.
			if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 8<<20 {
				t.Fatalf("decoded rejected or overwritten array before checking its budget: %d allocated bytes", allocated)
			}
		})
	}
}

func TestCacheJSONBudgetIgnoresQuotedStructuralCharacters(t *testing.T) {
	dir, key := t.TempDir(), Key("quoted-budget")
	s := testSummary("")
	s.Diagnostics = []diag.Diagnostic{diag.New("PP003", strings.Repeat("braces {}[],: and escaped quote \" slash \\", 1_000), s.Tree.Span)}
	if err := Write(dir, key, s); err != nil {
		t.Fatal(err)
	}
	got, hit := Read(dir, key)
	if !hit || len(got.Diagnostics) != 1 || got.Diagnostics[0].Message != s.Diagnostics[0].Message {
		t.Fatal("ordinary diagnostic text must not consume the structural budget")
	}
}

func TestCacheRejectsStructureBeforeDecode(t *testing.T) {
	// A cache may contain many tiny objects even when its bytes fit the file
	// limit. Reject excessive breadth and depth before recursive typed decode.
	for _, input := range []string{
		`[` + strings.Repeat(`{},`, maxCacheJSONValues/2+1) + `{}]`,
		strings.Repeat(`[`, 2*maxNodeDepth+17) + `0` + strings.Repeat(`]`, 2*maxNodeDepth+17),
	} {
		if withinJSONBudget([]byte(input), maxCacheJSONValues) {
			t.Fatal("accepted an over-budget JSON structure")
		}
	}
}
