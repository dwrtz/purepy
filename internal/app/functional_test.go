package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func functionalProject(t *testing.T, files map[string]string, entries, manifests []string) string {
	t.Helper()
	root := cliProject(t, files, entries, manifests)
	path := filepath.Join(root, "purepy.toml")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, []byte(strings.ReplaceAll(string(b), `language = "0.2"`, ``)), 0600); err != nil {
		t.Fatal(err)
	}
	return root
}
func TestFunctionalCacheAndAuthority(t *testing.T) {
	source := `from collections.abc import Callable
from trusted import adjust

def relay(f: Callable[[int], int]) -> Callable[[int], int]:
    def captured(x: int) -> int:
        return f(x)
    return captured

def apply(f: Callable[[int], int], x: int) -> int:
    return f(x)

def identity(x: int) -> int:
    return x

def loopapply(rebound: Callable[[int], int], other: Callable[[int], int], x: int) -> int:
    for i in range(2):
        x = rebound(x)
        rebound = other
    return x

def use(g: Callable[[int], int]) -> int:
    return g(1)

def through(f: Callable[[Callable[[int], int]], int], g: Callable[[int], int]) -> int:
    return f(g)

def factory() -> Callable[[int], int]:
    return adjust

def make(f: Callable[[], Callable[[int], int]]) -> Callable[[int], int]:
    return f()

def main(x: int) -> int:
    outer = make(factory)
    returned = relay(outer)
    return apply(returned, x) + through(use, returned) + loopapply(identity, adjust, x)
`
	contract := `schema = 1
[[module]]
name = "trusted"
import_safe = true
[[function]]
name = "trusted.adjust"
kind = "sync"
trust = "pure"
parameters = [{name = "x", type = "int"}]
returns = "int"
`
	root := functionalProject(t, map[string]string{"src/core.py": source, "trust.toml": contract}, []string{"core.main"}, []string{"trust.toml"})
	status, text, stderr := cliRun("check", root, "--no-cache")
	if status != 0 || stderr != "" || !strings.Contains(text, "PurePy 0.2") {
		t.Fatalf("wrong functional CLI result: %d %s %s", status, text, stderr)
	}
	cold := Check(Options{Path: root, Jobs: 1})
	if !cold.OK {
		t.Fatal(cold.Diagnostics)
	}
	if cold.Schema != 2 || cold.Language != "0.2" {
		t.Fatalf("wrong version %s", cliJSON(t, cold))
	}
	for _, jobs := range []int{1, 4} {
		warm := Check(Options{Path: root, Jobs: jobs})
		if warm.CacheHits != 1 || cliJSON(t, warm) != cliJSON(t, cold) {
			t.Fatalf("cache/worker mismatch: hits=%d %s", warm.CacheHits, cliJSON(t, warm))
		}
	}
	for _, name := range []string{"core.main", "core.apply", "core.relay.captured", "core.use", "core.loopapply"} {
		report, err := cold.Capabilities(name)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, f := range report.Functions[0].ReachableTrustedExternal {
			if f.Name == "trusted.adjust" {
				found = true
			}
		}
		if !found {
			t.Fatalf("lost returned/captured callback trust for %s: %+v", name, report)
		}
	}
	// Reuse cached caller syntax after changing a callback body.
	cliWrite(t, root, "src/core.py", strings.Replace(source, "return adjust", "return missing", 1))
	rejected := Check(Options{Path: root, Jobs: 4})
	if rejected.OK {
		t.Fatal("accepted invalid callback after cache edit")
	}
}
func TestObsoleteLanguageCannotReuseStandardCache(t *testing.T) {
	root := functionalProject(t, map[string]string{"src/core.py": "from typing import NamedTuple\nclass R(NamedTuple):\n    x:int\ndef main(x:int)->R:\n    return R(x)\n"}, []string{"core.main"}, nil)
	if r := Check(Options{Path: root}); !r.OK {
		t.Fatal(r.Diagnostics)
	}
	p := filepath.Join(root, "purepy.toml")
	b, _ := os.ReadFile(p)
	os.WriteFile(p, append(b, []byte("\nlanguage = \"0.1\"\n")...), 0600)
	r := Check(Options{Path: root})
	if r.OK || r.Schema != 2 || r.CacheHits != 0 {
		t.Fatalf("profile cache leak: %+v", r)
	}
}
