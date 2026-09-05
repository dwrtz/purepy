package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func cliProject(t *testing.T, files map[string]string, entrypoints, manifests []string) string {
	t.Helper()
	root := t.TempDir()
	entries, _ := json.Marshal(entrypoints)
	manifestPaths, _ := json.Marshal(manifests)
	if entrypoints == nil {
		entries = []byte("[]")
	}
	if manifests == nil {
		manifestPaths = []byte("[]")
	}
	cliWrite(t, root, "purepy.toml", fmt.Sprintf("[tool.purepy]\nlanguage = \"0.1\"\npython_syntax = \"3.14\"\nsource_root = \"src\"\nentrypoints = %s\nmanifests = %s\n", entries, manifestPaths))
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	for path, text := range files {
		cliWrite(t, root, path, text)
	}
	return root
}

func cliWrite(t *testing.T, root, path, text string) {
	t.Helper()
	path = filepath.Join(root, path)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func cliRun(args ...string) (int, string, string) {
	var out, errOut bytes.Buffer
	status := Run(args, &out, &errOut)
	return status, out.String(), errOut.String()
}

func cliJSON(t *testing.T, r *Report) string {
	t.Helper()
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestCLIVersionAndHelp(t *testing.T) {
	want := "purepy " + Version + " (language 0.1, Python syntax 3.14)\n"
	for _, command := range []string{"version", "--version"} {
		status, out, errOut := cliRun(command)
		if status != 0 || out != want || errOut != "" {
			t.Fatalf("version output is not deterministic: %d, %q, %q", status, out, errOut)
		}
	}
	status, out, errOut := cliRun()
	if status != 0 || !strings.Contains(out, "Usage:") || errOut != "" {
		t.Fatalf("help failed: %d, %q, %q", status, out, errOut)
	}
}

func TestCLIUsageAndConfigurationFailures(t *testing.T) {
	root := cliProject(t, map[string]string{"src/main.py": "def run() -> None:\n    pass\n"}, []string{"main.run"}, nil)
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"command", []string{"bogus"}, "unknown command"},
		{"jobs zero", []string{"check", root, "--jobs", "0"}, "positive"},
		{"jobs malformed", []string{"check", root, "--jobs", "many"}, "invalid"},
		{"format", []string{"check", root, "--format", "yaml"}, "text or json"},
		{"missing flag value", []string{"check", "--config"}, "requires a value"},
		{"unknown flag", []string{"check", root, "--unsafe"}, "flag provided"},
		{"extra positional", []string{"check", root, root}, "unexpected positional"},
		{"cache command", []string{"cache", "destroy"}, "Usage:"},
		{"missing config", []string{"check", filepath.Join(root, "absent")}, "PP001"},
		{"missing explanation", []string{"explain", "--config", root}, "explain requires"},
		{"invalid explanation", []string{"explain", "main.py:0", "--config", root}, "positive"},
		{"unknown function", []string{"capabilities", "main.absent", "--config", root}, "unknown verified function"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, out, errOut := cliRun(tt.args...)
			if status != 2 || !strings.Contains(out+errOut, tt.want) {
				t.Fatalf("expected usage/config status 2 and %q, got %d, %s%s", tt.want, status, out, errOut)
			}
		})
	}
}

func TestCacheColdWarmDisabledAndWorkersAreEquivalent(t *testing.T) {
	root := cliProject(t, map[string]string{
		"src/dependency.py": "def twice(x: int) -> int:\n    return x + x\n",
		"src/main.py":       "from dependency import twice\ndef run(x: int) -> int:\n    return twice(x)\n",
	}, []string{"main.run"}, nil)
	cold := Check(Options{Path: root, Jobs: 1})
	if !cold.OK || cold.CacheHits != 0 || cold.Files != 2 {
		t.Fatalf("cold check failed: %s, hits=%d", cliJSON(t, cold), cold.CacheHits)
	}
	for _, options := range []Options{{Path: root, Jobs: 8}, {Path: root, Jobs: 1}, {Path: root, Jobs: 4, NoCache: true}} {
		r := Check(options)
		if cliJSON(t, r) != cliJSON(t, cold) {
			t.Fatalf("cache/worker count changed report: %s\n%s", cliJSON(t, cold), cliJSON(t, r))
		}
		if options.NoCache && r.CacheHits != 0 || !options.NoCache && r.CacheHits != r.Files {
			t.Fatalf("unexpected hits %d/%d, options=%#v", r.CacheHits, r.Files, options)
		}
	}
	status, out, errOut := cliRun("check", root, "--jobs", "3", "--format", "json", "--timings")
	if status != 0 || !json.Valid([]byte(out)) || !strings.Contains(errOut, "cache_hits 2/2") {
		t.Fatalf("JSON/timing output failed: %d %s %s", status, out, errOut)
	}
}

func TestDependencySignatureChangeRelinksCachedCaller(t *testing.T) {
	root := cliProject(t, map[string]string{
		"src/dependency.py": "def convert(x: int) -> int:\n    return x\n",
		"src/main.py":       "from dependency import convert\ndef run(x: int) -> int:\n    return convert(x)\n",
	}, []string{"main.run"}, nil)
	if r := Check(Options{Path: root}); !r.OK {
		t.Fatalf("initial project failed: %s", cliJSON(t, r))
	}
	cliWrite(t, root, "src/dependency.py", "def convert(x: str) -> str:\n    return x\n")
	r := Check(Options{Path: root, Jobs: 4})
	if r.OK || r.CacheHits != 1 || !strings.Contains(cliJSON(t, r), "PP205") {
		t.Fatalf("cached caller escaped signature recheck: %s, hits=%d", cliJSON(t, r), r.CacheHits)
	}
	uncached := Check(Options{Path: root, NoCache: true, Jobs: 1})
	if cliJSON(t, r) != cliJSON(t, uncached) {
		t.Fatalf("cached invalid diagnostics differ: %s\n%s", cliJSON(t, r), cliJSON(t, uncached))
	}
}

func TestTruncatedCacheRegeneratesEquivalentReport(t *testing.T) {
	root := cliProject(t, map[string]string{"src/main.py": "def run(x: int) -> int:\n    return x + 1\n"}, []string{"main.run"}, nil)
	cold := Check(Options{Path: root, Jobs: 1})
	if !cold.OK {
		t.Fatal(cliJSON(t, cold))
	}
	dir := filepath.Join(root, ".purepy-cache")
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("expected one cache artifact: %v %v", entries, err)
	}
	if err := os.WriteFile(filepath.Join(dir, entries[0].Name()), []byte(`{"schema":1`), 0o644); err != nil {
		t.Fatal(err)
	}
	reparsed := Check(Options{Path: root, Jobs: 4})
	if reparsed.CacheHits != 0 || cliJSON(t, reparsed) != cliJSON(t, cold) {
		t.Fatalf("truncated cache changed verification: %s", cliJSON(t, reparsed))
	}
}

const cliManifest = `schema = 1
[[module]]
name = "host.ops"
import_safe = true
[[type]]
name = "host.ops.Read"
category = "capability"
labels = ["database.read"]
[[type]]
name = "host.ops.Connection"
category = "host_ref"
[[function]]
name = "host.ops.load"
kind = "async"
trust = "host"
parameters = [{name = "read", type = "host.ops.Read"}, {name = "connection", type = "host.ops.Connection"}]
returns = "int"
`

func TestManifestContentChangeInvalidatesAllModules(t *testing.T) {
	root := cliProject(t, map[string]string{
		"manifest.toml": cliManifest,
		"src/main.py":   "from host.ops import Read, Connection, load\nasync def run(read: Read, connection: Connection) -> int:\n    return await load(read, connection)\n",
	}, []string{"main.run"}, []string{"manifest.toml"})
	if r := Check(Options{Path: root}); !r.OK {
		t.Fatalf("initial project failed: %s", cliJSON(t, r))
	}
	cliWrite(t, root, "manifest.toml", cliManifest+"\n# changed manifest bytes\n")
	if r := Check(Options{Path: root}); !r.OK || r.CacheHits != 0 {
		t.Fatalf("manifest bytes did not invalidate cache: %s, hits=%d", cliJSON(t, r), r.CacheHits)
	}
	cliWrite(t, root, "manifest.toml", strings.Replace(cliManifest, `returns = "int"`, `returns = "str"`, 1))
	if r := Check(Options{Path: root}); r.OK || r.CacheHits != 0 || !strings.Contains(cliJSON(t, r), "PP205") {
		t.Fatalf("manifest signature change escaped verification: %s, hits=%d", cliJSON(t, r), r.CacheHits)
	}
}

func TestCLIReportsAndExplain(t *testing.T) {
	root := cliProject(t, map[string]string{
		"manifest.toml": cliManifest,
		"src/main.py":   "from host.ops import Read, Connection, load\nasync def run(read: Read, connection: Connection) -> int:\n    return await load(read, connection)\n",
	}, []string{"main.run"}, []string{"manifest.toml"})
	status, out, errOut := cliRun("capabilities", "--config", root, "--format", "json")
	var authority AuthorityReport
	if status != 0 || json.Unmarshal([]byte(out), &authority) != nil || len(authority.Functions) != 1 {
		t.Fatalf("capabilities failed: %d %s %s", status, out, errOut)
	}
	a := authority.Functions[0]
	if a.Name != "main.run" || len(a.Capabilities) != 1 || len(a.HostReferences) != 1 || len(a.TrustedExternal) != 1 || len(a.ReachableTrustedExternal) != 1 || len(a.UnusedCapabilities) != 0 || !strings.Contains(out, "database.read") || !strings.Contains(out, "manifest.toml") {
		t.Fatalf("incomplete authority report: %s", out)
	}
	status, out, errOut = cliRun("explain", filepath.Join(root, "src/main.py")+":3:18", "--config", root, "--format", "json")
	var explanation Explanation
	if status != 0 || json.Unmarshal([]byte(out), &explanation) != nil || len(explanation.Facts) == 0 || !strings.Contains(out, "host.ops.load") {
		t.Fatalf("explanation failed: %d %s %s", status, out, errOut)
	}
}

func TestVerificationNeverExecutesProjectOrHost(t *testing.T) {
	root := cliProject(t, map[string]string{}, nil, nil)
	sentinel := filepath.Join(root, "executed")
	cliWrite(t, root, "src/main.py", fmt.Sprintf("open(%q, 'w').write('executed')\n", sentinel))
	status, out, errOut := cliRun("check", root, "--no-cache")
	if status != 1 || !strings.Contains(out+errOut, "PP003") {
		t.Fatalf("executable module was not rejected: %d %s%s", status, out, errOut)
	}
	if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
		t.Fatalf("analyzed project code executed: %v", err)
	}
	cliWrite(t, root, "src/main.py", "from host.ops import Read, Connection, load\nasync def run(read: Read, connection: Connection) -> int:\n    return await load(read, connection)\n")
	cliWrite(t, root, "manifest.toml", cliManifest)
	cliWrite(t, root, "host/ops.py", fmt.Sprintf("open(%q, 'w').write('executed')\nraise RuntimeError('must not import host')\n", sentinel))
	cliWrite(t, root, "host/__init__.py", fmt.Sprintf("open(%q, 'w').write('executed')\n", sentinel))
	cliWrite(t, root, "purepy.toml", "[tool.purepy]\nlanguage = \"0.1\"\npython_syntax = \"3.14\"\nsource_root = \"src\"\nentrypoints = [\"main.run\"]\nmanifests = [\"manifest.toml\"]\n")
	status, out, errOut = cliRun("check", root, "--no-cache")
	if status != 0 {
		t.Fatalf("manifest-backed project failed: %d %s%s", status, out, errOut)
	}
	if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
		t.Fatalf("excluded host code executed: %v", err)
	}
}

func TestCLICacheCleanRejectsSymlink(t *testing.T) {
	root := cliProject(t, map[string]string{"src/main.py": "def run() -> None:\n    pass\n"}, []string{"main.run"}, nil)
	if r := Check(Options{Path: root}); !r.OK {
		t.Fatal(cliJSON(t, r))
	}
	status, out, errOut := cliRun("cache", "clean", "--config", root)
	if status != 0 || !strings.Contains(out, "Removed 1") {
		t.Fatalf("cache clean failed: %d %s %s", status, out, errOut)
	}
	cachePath := filepath.Join(root, ".purepy-cache")
	if err := os.Remove(cachePath); err != nil {
		t.Fatal(err)
	}
	target := t.TempDir()
	if err := os.WriteFile(filepath.Join(target, "sentinel"), []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, cachePath); err != nil {
		t.Fatal(err)
	}
	status, out, errOut = cliRun("cache", "clean", "--config", root)
	if status != 2 || !strings.Contains(out+errOut, "symlink") {
		t.Fatalf("unsafe cache clean accepted: %d %s %s", status, out, errOut)
	}
	if data, err := os.ReadFile(filepath.Join(target, "sentinel")); err != nil || string(data) != "keep" {
		t.Fatalf("cache clean modified symlink target: %q %v", data, err)
	}
}
