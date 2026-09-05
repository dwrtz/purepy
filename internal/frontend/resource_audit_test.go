package frontend

import (
	"runtime"
	"strings"
	"testing"
)

func TestParserBoundsRepeatedLexicalDiagnostics(t *testing.T) {
	tree, ds := Parse("gaps.py", []byte(strings.Repeat("\u200b", 4_000)))
	if tree != nil || len(ds) != 1_025 || ds[len(ds)-1].Code != "PP003" || !strings.Contains(ds[len(ds)-1].Message, "diagnostic resource limit") {
		t.Fatalf("expected bounded diagnostics and an explicit truncation reason: tree=%v diagnostics=%d", tree != nil, len(ds))
	}
}

func TestParserRejectsExcessiveSyntaxResources(t *testing.T) {
	for name, source := range map[string]string{
		"depth":            "def f(x: int) -> int:\n    return x" + strings.Repeat(" + x", 4000) + "\n",
		"nodes":            strings.Repeat("pass\n", 100_001),
		"overlapping text": "def f() -> str:\n    return " + strings.Repeat("+", 160) + "'" + strings.Repeat("x", 512<<10) + "'\n",
	} {
		t.Run(name, func(t *testing.T) {
			tree, ds := Parse("resource.py", []byte(source))
			if tree != nil || len(ds) != 1 || ds[0].Code != "PP003" || !strings.Contains(ds[0].Message, "resource limit") {
				t.Fatalf("excessive syntax should stop before recursive lowering: tree=%v diagnostics=%v", tree != nil, ds)
			}
		})
	}
}

func TestNestedSourceTextAllocationAndOwnership(t *testing.T) {
	// Large overlapping expression spellings used to allocate one full copy
	// per subtree, causing quadratic cumulative copying. Keep a generous bound
	// around this 257 KiB source's detached IR, excluding native allocations.
	name := "x" + strings.Repeat("y", 1023)
	source := []byte("def f(" + name + ": int) -> int:\n    return " + name + strings.Repeat(" + "+name, 255) + "\n")
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	tree, ds := Parse("ownership.py", source)
	runtime.ReadMemStats(&after)
	if tree == nil || len(ds) != 0 {
		t.Fatalf("ordinary bounded nested expressions should parse: %v", ds)
	}
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 16<<20 {
		t.Fatalf("nested subtree text amplified %d source bytes into %d Go allocation bytes", len(source), allocated)
	}
	want := tree.Items("body")[0].Items("body")[0].Get("value").Text
	for i := range source {
		source[i] = ' '
	}
	if got := tree.Items("body")[0].Items("body")[0].Get("value").Text; got != want || !strings.Contains(got, name) {
		t.Fatal("detached IR text aliases the caller's mutable source buffer")
	}
}
