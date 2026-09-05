package cache

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/dwrtz/purepy/internal/diag"
	"github.com/dwrtz/purepy/internal/frontend"
	"github.com/dwrtz/purepy/internal/model"
)

func testSummary(text string) Summary {
	return Summary{Tree: &model.Node{Kind: "Module", Text: text, Fields: map[string]*model.Node{}, Lists: map[string][]*model.Node{"body": {}}, Attr: map[string]string{}}}
}

func TestKeySeparatesEveryInputAndBoundary(t *testing.T) {
	keys := map[string]bool{}
	for _, parts := range [][]string{{"ab", "c"}, {"a", "bc"}, {"v1", "language", "syntax", "config", "manifest", "module", "source"}, {"v2", "language", "syntax", "config", "manifest", "module", "source"}, {"v1", "language", "syntax", "config", "changed manifest", "module", "source"}} {
		key := Key(parts...)
		if !validKey(key) || keys[key] || key != Key(parts...) {
			t.Fatalf("invalid, colliding or unstable key for %q: %q", parts, key)
		}
		keys[key] = true
	}
}

func TestRoundTripAndMalformedArtifactsAreMisses(t *testing.T) {
	dir := t.TempDir()
	key := Key("roundtrip")
	if err := Write(dir, key, testSummary("original")); err != nil {
		t.Fatal(err)
	}
	got, ok := Read(dir, key)
	if !ok || got.Tree.Text != "original" {
		t.Fatalf("cache roundtrip failed: %#v, %v", got, ok)
	}
	path := filepath.Join(dir, key+".json")
	valid, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var original artifact
	if err := json.Unmarshal(valid, &original); err != nil {
		t.Fatal(err)
	}
	tests := map[string][]byte{"empty": {}, "truncated": valid[:len(valid)/2], "invalid JSON": []byte("{"), "wrong JSON kind": []byte("[]"), "trailing JSON": append(append([]byte{}, valid...), []byte("{}")...)}
	for name, mutate := range map[string]func(*artifact){
		"schema":          func(a *artifact) { a.Schema++ },
		"key":             func(a *artifact) { a.Key = Key("different") },
		"checksum":        func(a *artifact) { a.Checksum = strings.Repeat("0", 64) },
		"null tree":       func(a *artifact) { a.Payload = []byte(`{"tree":null}`); a.Checksum = Digest(a.Payload) },
		"wrong tree kind": func(a *artifact) { a.Payload = []byte(`{"tree":{"kind":"Call"}}`); a.Checksum = Digest(a.Payload) },
	} {
		a := original
		mutate(&a)
		data, err := json.Marshal(a)
		if err != nil {
			t.Fatal(err)
		}
		tests[name] = data
	}
	for name, data := range tests {
		t.Run(name, func(t *testing.T) {
			if err := os.WriteFile(path, data, 0o644); err != nil {
				t.Fatal(err)
			}
			if _, ok := Read(dir, key); ok {
				t.Fatal("malformed artifact must be a safe cache miss")
			}
		})
	}
}

func TestMalformedSummaryNodesAreMisses(t *testing.T) {
	dir := t.TempDir()
	key := Key("null-child")
	s := testSummary("malformed")
	s.Tree.Lists["body"] = []*model.Node{nil}
	if err := Write(dir, key, s); err != nil {
		return // Rejecting malformed trees at write time is also safe.
	}
	if _, ok := Read(dir, key); ok {
		t.Fatal("nil statement nodes must be rejected before the checker can dereference them")
	}
}

func TestCacheRejectsSemanticTypeContext(t *testing.T) {
	dir := t.TempDir()
	key := Key("parser-context")
	tree, ds := frontend.Parse("app.py", []byte("def f(x: int) -> int:\n    return [x]\n"))
	if len(ds) == 0 {
		t.Fatal("fixture must have a parser diagnostic")
	}
	summary := Summary{Tree: tree, Diagnostics: ds}
	if err := Write(dir, key, summary); err != nil {
		t.Fatal(err)
	}
	if _, ok := Read(dir, key); !ok {
		t.Fatal("ordinary parser diagnostics must remain cacheable")
	}
	for _, types := range []map[string]model.Type{
		{"actual": model.Int},
		{"actual": {Kind: "tuple"}},
		{"actual": {Kind: "unknown", Name: "forged"}},
	} {
		summary.Diagnostics = append([]diag.Diagnostic{}, ds...)
		summary.Diagnostics[0].Types = types
		if err := Write(dir, key, summary); err != nil {
			continue // Rejecting semantic context at write time is also safe.
		}
		if _, ok := Read(dir, key); ok {
			t.Fatalf("semantic type context must be recomputed, not trusted from parser cache: %+v", types)
		}
	}
}

func TestCachedValidationAllocationBudget(t *testing.T) {
	// The common arithmetic-function summary is visited on every warm check.
	// Structural validation should not allocate a slice for every fixed shape,
	// attribute, or operator table lookup as the number of syntax nodes grows.
	source := strings.Repeat("def operation(value: int) -> int:\n    return value + 1\n", 100)
	tree, ds := frontend.Parse("app.py", []byte(source))
	if len(ds) != 0 {
		t.Fatal(ds)
	}
	summary := Summary{Tree: tree, Diagnostics: ds}
	valid := true
	allocations := testing.AllocsPerRun(20, func() { valid = validSummary(summary) && valid })
	if !valid || allocations != 0 {
		t.Fatalf("cached shape validation: valid=%v allocations=%g, want valid with no per-node allocations", valid, allocations)
	}
}

func TestConcurrentAtomicWrites(t *testing.T) {
	dir := t.TempDir()
	key := Key("concurrent")
	const workers = 12
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 12; j++ {
				if err := Write(dir, key, testSummary("complete")); err != nil {
					t.Error(err)
					return
				}
				if s, ok := Read(dir, key); ok && (s.Tree == nil || s.Tree.Text != "complete") {
					t.Error("reader observed an incomplete write")
				}
			}
		}()
	}
	wg.Wait()
	if s, ok := Read(dir, key); !ok || s.Tree.Text != "complete" {
		t.Fatalf("final artifact unavailable: %#v, %v", s, ok)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 || entries[0].Name() != key+".json" {
		t.Fatalf("temporary writes leaked: %v, %v", entries, err)
	}
}

func TestCleanOwnsOnlyRegularArtifacts(t *testing.T) {
	dir := t.TempDir()
	key := Key("owned")
	if err := Write(dir, key, testSummary("owned")); err != nil {
		t.Fatal(err)
	}
	keep := []string{"notes.json", "README", ".purepy-in-progress"}
	for _, name := range keep {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("user data"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	subdir := Key("directory") + ".json"
	if err := os.Mkdir(filepath.Join(dir, subdir), 0o755); err != nil {
		t.Fatal(err)
	}
	keep = append(keep, subdir)
	target := filepath.Join(t.TempDir(), "sentinel")
	if err := os.WriteFile(target, []byte("untouched"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := Key("symlink") + ".json"
	if err := os.Symlink(target, filepath.Join(dir, link)); err != nil {
		t.Fatal(err)
	}
	keep = append(keep, link)
	count, err := Clean(dir)
	if err != nil || count != 1 {
		t.Fatalf("got %d removals, %v", count, err)
	}
	for _, name := range keep {
		if _, err := os.Lstat(filepath.Join(dir, name)); err != nil {
			t.Errorf("removed unowned entry %s: %v", name, err)
		}
	}
	if data, err := os.ReadFile(target); err != nil || string(data) != "untouched" {
		t.Fatalf("symlink target modified: %q, %v", data, err)
	}
	if count, err := Clean(filepath.Join(dir, "absent")); err != nil || count != 0 {
		t.Fatalf("missing cache should clean harmlessly: %d, %v", count, err)
	}
}

func TestSymlinkAndInvalidKeySafety(t *testing.T) {
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "cache")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	key := Key("safe")
	if err := Write(link, key, testSummary("bad")); err == nil {
		t.Fatal("cache writes must reject a symlink directory")
	}
	if _, ok := Read(link, key); ok {
		t.Fatal("cache reads must reject a symlink directory")
	}
	if _, err := Clean(link); err == nil {
		t.Fatal("cache clean must reject a symlink directory")
	}
	for _, key := range []string{"", "../outside", strings.Repeat("z", 64), strings.Repeat("a", 63)} {
		if err := Write(real, key, testSummary("bad")); err == nil {
			t.Errorf("accepted invalid key %q", key)
		}
		if _, ok := Read(real, key); ok {
			t.Errorf("read invalid key %q", key)
		}
	}
}
