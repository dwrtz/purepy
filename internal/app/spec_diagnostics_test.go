package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRejectedReportsAreEquivalentAcrossCacheAndWorkers(t *testing.T) {
	for _, tc := range []struct {
		name, source, code string
	}{
		{"syntax", "def bad() -> int:\n    return [1, 2]\n", "PP003"},
		{"link", "from absent import unknown\ndef bad() -> int:\n    return unknown()\n", "PP102"},
		{"local", "def bad() -> int:\n    return unknown()\n", "PP303"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := cliProject(t, map[string]string{
				"src/z.py": tc.source,
				"src/a.py": tc.source,
			}, nil, nil)
			baseline := Check(Options{Path: root, Jobs: 1})
			want := cliJSON(t, baseline)
			if baseline.OK || !strings.Contains(want, tc.code) || baseline.CacheHits != 0 {
				t.Fatalf("expected cold rejection %s: %s", tc.code, want)
			}
			for _, opts := range []Options{{Path: root, Jobs: 8}, {Path: root, Jobs: 1}, {Path: root, Jobs: 8, NoCache: true}, {Path: root, Jobs: 1, NoCache: true}} {
				got := Check(opts)
				if cliJSON(t, got) != want {
					t.Fatalf("rejection changed with %+v:\n%s\n%s", opts, want, cliJSON(t, got))
				}
				if !opts.NoCache && got.CacheHits != got.Files {
					t.Fatalf("test failed to exercise warm rejection: hits=%d/%d", got.CacheHits, got.Files)
				}
			}
			for _, format := range []string{"text", "json"} {
				status, output, stderr := cliRun("check", root, "--jobs", "1", "--format", format, "--no-cache")
				for i := 0; i < 4; i++ {
					nextStatus, nextOutput, nextStderr := cliRun("check", root, "--jobs", "8", "--format", format)
					if status != 1 || nextStatus != status || nextOutput != output || stderr != nextStderr {
						t.Fatalf("%s CLI rejection depends on workers/cache: %d/%d\n%s\n%s", format, status, nextStatus, output, nextOutput)
					}
				}
			}
		})
	}
}

func TestEntrypointRejectionPointsToConfigurationDeclaration(t *testing.T) {
	root := cliProject(t, map[string]string{"src/main.py": "def run() -> None:\n    pass\n"}, nil, nil)
	source := "# main.absent is a comment, not the declaration\n[tool.purepy]\nlanguage = '0.2'\npython_syntax = '3.14'\nsource_root = 'src'\nentrypoints = [\n  'main.absent',\n]\nmanifests = []\n"
	cliWrite(t, root, "purepy.toml", source)
	r := Check(Options{Path: root, NoCache: true})
	if r.OK || len(r.Diagnostics) != 1 {
		t.Fatalf("missing entrypoint should reject once: %s", cliJSON(t, r))
	}
	d := r.Diagnostics[0]
	if d.Code != "PP701" || d.Span.File != r.Config.Path || d.Span.Line != 7 || d.Span.EndLine != 7 || d.Span.Column != 3 || d.Span.EndColumn != 16 || d.Symbol != "main.absent" || source[d.Span.Start:d.Span.End] != "'main.absent'" {
		t.Fatalf("entrypoint diagnostic lacks its configuration declaration: %+v", d)
	}
	status, output, stderr := cliRun("explain", filepath.Join(root, "purepy.toml")+":7:4", "--config", root, "--format", "json")
	if status != 0 || !strings.Contains(output, "PP701") || stderr != "" {
		t.Fatalf("entrypoint location is not explainable: %d %s %s", status, output, stderr)
	}
}

func TestDiagnosticOrderUsesModuleNames(t *testing.T) {
	for _, source := range []string{"import os\n", "x = 1\n"} {
		root := cliProject(t, map[string]string{"src/pkg/__init__.py": source, "src/pkg/A.py": source}, nil, nil)
		r := Check(Options{Path: root, NoCache: true, Jobs: 4})
		if len(r.Diagnostics) < 2 || !strings.HasSuffix(r.Diagnostics[0].Span.File, "pkg/__init__.py") {
			t.Fatalf("package module must sort before child pkg.A: %s", cliJSON(t, r))
		}
	}
}

func TestSchemaMismatchReanalyzesRejectedProject(t *testing.T) {
	root := cliProject(t, map[string]string{"src/main.py": "def run() -> int:\n    return unknown()\n"}, nil, nil)
	baseline := Check(Options{Path: root, Jobs: 1})
	if baseline.OK {
		t.Fatal("unknown call accepted")
	}
	artifacts, err := filepath.Glob(filepath.Join(root, ".purepy-cache", "*.json"))
	if err != nil || len(artifacts) != 1 {
		t.Fatalf("expected a cache artifact: %v %v", artifacts, err)
	}
	data, err := os.ReadFile(artifacts[0])
	if err != nil {
		t.Fatal(err)
	}
	var artifact map[string]json.RawMessage
	if err := json.Unmarshal(data, &artifact); err != nil {
		t.Fatal(err)
	}
	artifact["schema"] = json.RawMessage("999")
	data, err = json.Marshal(artifact)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(artifacts[0], data, 0o644); err != nil {
		t.Fatal(err)
	}
	got := Check(Options{Path: root, Jobs: 8})
	if got.CacheHits != 0 || cliJSON(t, got) != cliJSON(t, baseline) {
		t.Fatalf("schema mismatch must reanalyze and preserve rejection: hits=%d, %s", got.CacheHits, cliJSON(t, got))
	}
}

func TestDiagnosticMetadataForVerificationAndConfigurationErrors(t *testing.T) {
	root := cliProject(t, map[string]string{"src/main.py": "def run(x: int) -> int:\n    return x\n"}, []string{"main.run"}, nil)
	if good := Check(Options{Path: root, NoCache: true}); !good.OK || len(good.Diagnostics) != 0 {
		t.Fatalf("valid program unexpectedly diagnosed: %s", cliJSON(t, good))
	}
	for _, source := range []string{
		"def run(x: int) -> int:\n    return True\n",
		"def run(x: int) -> int:\n    return [x]\n",
		"from missing import run\n",
	} {
		cliWrite(t, root, "src/main.py", source)
		r := Check(Options{Path: root, NoCache: true})
		if r.OK || len(r.Diagnostics) == 0 {
			t.Fatalf("expected rejected source: %q", source)
		}
		for _, d := range r.Diagnostics {
			if len(d.Code) != 5 || !strings.HasPrefix(d.Code, "PP") || d.Severity != "error" || d.Message == "" || d.Span.File == "" || d.Span.Line < 1 || d.Span.Column < 1 || d.Span.EndLine < d.Span.Line || d.Span.EndColumn < 1 || d.Notes == nil || d.Related == nil {
				t.Fatalf("incomplete diagnostic metadata: %+v", d)
			}
		}
	}
	cliWrite(t, root, "purepy.toml", "[tool.purepy]\nlanguage = 'future'\npython_syntax = '3.14'\nsource_root = 'src'\nentrypoints = []\nmanifests = []\n")
	status, output, stderr := cliRun("check", root, "--format", "json")
	var rejected Report
	if status != 2 || stderr != "" || json.Unmarshal([]byte(output), &rejected) != nil || len(rejected.Diagnostics) != 1 {
		t.Fatalf("configuration rejection contract: %d %s %s", status, output, stderr)
	}
	d := rejected.Diagnostics[0]
	if d.Code != "PP001" || filepath.Base(d.Span.File) != "purepy.toml" || d.Span.Line != 2 || d.Span.EndLine != 2 || d.Span.Column != 1 || d.Span.EndColumn != 9 {
		t.Fatalf("CLI lost structured configuration location: %+v", d)
	}
}

func TestReportsIdentifySpecificationAndLanguageVersions(t *testing.T) {
	root := cliProject(t, map[string]string{"src/main.py": "def run() -> None:\n    pass\n"}, nil, nil)
	for _, valid := range []bool{true, false} {
		if !valid {
			cliWrite(t, root, "src/main.py", "def run() -> None:\n    unknown()\n")
		}
		status, output, stderr := cliRun("check", root, "--format", "json", "--no-cache")
		var report map[string]any
		if json.Unmarshal([]byte(output), &report) != nil || stderr != "" || (status == 0) != valid {
			t.Fatalf("unexpected verification result: %d %s %s", status, output, stderr)
		}
		for key, want := range map[string]string{"verifier_version": Version, "specification_version": "0.4-draft", "language": "0.2", "python_syntax": "3.14"} {
			if report[key] != want {
				t.Errorf("%s metadata=%v want=%s", key, report[key], want)
			}
		}
	}
}

func TestManifestRejectionPointsToManifestDeclaration(t *testing.T) {
	manifestSource := "# schema = 1 is only a comment\nschema = 999\n"
	root := cliProject(t, map[string]string{"src/main.py": "", "host.toml": manifestSource}, nil, []string{"host.toml"})
	r := Check(Options{Path: root, NoCache: true})
	if r.OK || len(r.Diagnostics) != 1 {
		t.Fatalf("invalid manifest must reject: %s", cliJSON(t, r))
	}
	d := r.Diagnostics[0]
	if d.Code != "PP601" || filepath.Base(d.Span.File) != "host.toml" || d.Span.Line != 2 || d.Span.Column != 1 || d.Span.EndLine != 2 || d.Span.EndColumn != 7 || manifestSource[d.Span.Start:d.Span.End] != "schema" {
		t.Fatalf("manifest rejection lost declaration location: %+v", d)
	}
}

func TestDiscoveryFailurePointsToSourceRootDeclaration(t *testing.T) {
	root := cliProject(t, map[string]string{"src/pkg/child.py": "def run() -> None:\n    pass\n"}, nil, nil)
	r := Check(Options{Path: root, NoCache: true})
	if r.OK || len(r.Diagnostics) != 1 {
		t.Fatalf("namespace package must reject: %s", cliJSON(t, r))
	}
	d := r.Diagnostics[0]
	if d.Code != "PP101" || d.Span.File != r.Config.Path || d.Span.Line != 4 || d.Span.EndLine != 4 || d.Span.Column != 1 || d.Span.EndColumn != 12 {
		t.Fatalf("discovery error must locate configuration declaration rather than a directory: %+v", d)
	}
}
