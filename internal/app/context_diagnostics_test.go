package app

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/dwrtz/purepy/internal/model"
)

func contextSpan(t *testing.T, root, file, source, target string, occurrence int) model.Span {
	t.Helper()
	path, err := filepath.EvalSymlinks(filepath.Join(root, file))
	if err != nil {
		t.Fatal(err)
	}
	start := 0
	for index := 0; index <= occurrence; index++ {
		next := strings.Index(source[start:], target)
		if next < 0 {
			t.Fatalf("missing target %q in %s", target, file)
		}
		start += next
		if index < occurrence {
			start += len(target)
		}
	}
	end := start + len(target)
	lineStart := strings.LastIndexByte(source[:start], '\n') + 1
	endLineStart := strings.LastIndexByte(source[:end], '\n') + 1
	return model.Span{File: path, Start: start, End: end, Line: 1 + strings.Count(source[:start], "\n"), Column: 1 + utf8.RuneCountInString(source[lineStart:start]), EndLine: 1 + strings.Count(source[:end], "\n"), EndColumn: 1 + utf8.RuneCountInString(source[endLineStart:end])}
}

func requireContextReportEqual(t *testing.T, baseline, actual *Report) {
	t.Helper()
	if cliJSON(t, baseline) != cliJSON(t, actual) {
		t.Fatalf("full public report changed:\n%s\n%s", cliJSON(t, baseline), cliJSON(t, actual))
	}
	// Include facts, calls, and configuration in the equivalence check. The
	// mutable program image and measured cache/timing telemetry are not reports.
	left, right := *baseline, *actual
	left.Program, right.Program = nil, nil
	left.Timings, right.Timings = nil, nil
	left.CacheHits, right.CacheHits = 0, 0
	if !reflect.DeepEqual(left, right) {
		t.Fatalf("semantic report metadata changed with cache/workers:\n%+v\n%+v", left, right)
	}
}

func TestDiagnosticDeclarationPathsAcrossCacheAndWorkers(t *testing.T) {
	const project = "def take(value: int) -> int:\n    return value\n"
	const main = "from a import take\ndef bad() -> int:\n    return take(True)\n"
	const later = "from host.ops import external\ndef bad() -> int:\n    return external('wrong')\n"
	const external = "schema = 1\n[[module]]\nname = 'host.ops'\nimport_safe = true\n[[function]]\nname = 'host.ops.external'\nkind = 'sync'\ntrust = 'pure'\nparameters = [{name = 'value', type = 'int'}]\nreturns = 'int'\n"
	root := cliProject(t, map[string]string{"src/a.py": project, "src/main.py": main, "src/z.py": later, "host.toml": external}, nil, []string{"host.toml"})
	baseline := Check(Options{Path: root, Jobs: 1})
	if baseline.OK || baseline.CacheHits != 0 || len(baseline.Diagnostics) != 2 {
		t.Fatalf("expected exactly two cold type mismatches: %s", cliJSON(t, baseline))
	}
	for index, want := range []struct {
		symbol  string
		actual  model.Type
		primary model.Span
		related []model.Span
	}{
		{"a.take.value", model.Bool, contextSpan(t, root, "src/main.py", main, "True", 0), []model.Span{
			contextSpan(t, root, "src/main.py", main, "take", 0),
			contextSpan(t, root, "src/a.py", project, strings.TrimSuffix(project, "\n"), 0),
			contextSpan(t, root, "src/a.py", project, "value: int", 0),
		}},
		{"host.ops.external.value", model.Str, contextSpan(t, root, "src/z.py", later, "'wrong'", 0), []model.Span{
			contextSpan(t, root, "src/z.py", later, "external", 0),
			contextSpan(t, root, "host.toml", external, "'host.ops.external'", 0),
			contextSpan(t, root, "host.toml", external, "'value'", 0),
		}},
	} {
		d := baseline.Diagnostics[index]
		types := map[string]model.Type{"expected": model.Int, "actual": want.actual, "return": model.Int, "parameter.value": model.Int}
		if d.Code != "PP205" || d.Symbol != want.symbol || d.Span != want.primary || !reflect.DeepEqual(d.Types, types) || !reflect.DeepEqual(d.Related, want.related) {
			t.Fatalf("diagnostic %d lost exact signature or declaration path:\n%+v\nwant symbol %s, primary %+v, types %+v, related %+v", index, d, want.symbol, want.primary, types, want.related)
		}
	}
	for _, options := range []Options{{Path: root, Jobs: 8}, {Path: root, Jobs: 1}, {Path: root, Jobs: 8, NoCache: true}, {Path: root, Jobs: 1, NoCache: true}} {
		report := Check(options)
		requireContextReportEqual(t, baseline, report)
		if !options.NoCache && report.CacheHits != report.Files {
			t.Fatalf("warm metadata test did not use all cache entries: %d/%d", report.CacheHits, report.Files)
		}
	}
	for _, format := range []string{"json", "text"} {
		status, output, stderr := cliRun("check", root, "--format", format, "--jobs", "1", "--no-cache")
		if status != 1 || stderr != "" {
			t.Fatalf("unexpected %s result: %d %s %s", format, status, output, stderr)
		}
		if format == "json" {
			var decoded Report
			if err := json.Unmarshal([]byte(output), &decoded); err != nil || cliJSON(t, &decoded) != cliJSON(t, baseline) {
				t.Fatalf("JSON context differs from the full report: %v\n%s", err, output)
			}
		} else {
			for _, required := range []string{"symbol: a.take.value", "symbol: host.ops.external.value", "actual type: bool", "actual type: str", "expected type: int", "parameter.value type: int", "return type: int"} {
				if !strings.Contains(output, required+"\n") {
					t.Fatalf("text report omitted %q:\n%s", required, output)
				}
			}
			for _, d := range baseline.Diagnostics {
				for _, related := range d.Related {
					if !strings.Contains(output, fmt.Sprintf("  declaration at %s:%d:%d\n", related.File, related.Line, related.Column)) {
						t.Fatalf("text report omitted declaration %+v:\n%s", related, output)
					}
				}
			}
		}
		nextStatus, nextOutput, nextStderr := cliRun("check", root, "--format", format, "--jobs", "8")
		if nextStatus != status || nextOutput != output || nextStderr != stderr {
			t.Fatalf("%s rendering changed with cache/workers:\n%s\n%s", format, output, nextOutput)
		}
	}
}

func TestManifestErrorsPreserveDeclarationContextInReports(t *testing.T) {
	const module = "schema = 1\n[[module]]\nname = 'host.ops'\nimport_safe = true\n"
	for _, tc := range []struct {
		name                                                           string
		files                                                          map[string]string
		manifests                                                      []string
		symbol, primaryFile, primaryTarget, relatedFile, relatedTarget string
	}{
		{"duplicate", map[string]string{"first.toml": module, "second.toml": "# second definition\n" + module}, []string{"first.toml", "second.toml"}, "host.ops", "second.toml", "'host.ops'", "first.toml", "'host.ops'"},
		{"parameter", map[string]string{"host.toml": module + "[[function]]\nname = 'host.ops.run'\nkind = 'sync'\ntrust = 'pure'\nparameters = [{name = 'argument', type = 'Callable'}]\nreturns = 'int'\n"}, []string{"host.toml"}, "host.ops.run.argument", "host.toml", "'Callable'", "host.toml", "'argument'"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := cliProject(t, tc.files, nil, tc.manifests)
			baseline := Check(Options{Path: root, Jobs: 1})
			if baseline.OK || len(baseline.Diagnostics) != 1 {
				t.Fatalf("invalid manifest must reject once: %s", cliJSON(t, baseline))
			}
			d := baseline.Diagnostics[0]
			primary := contextSpan(t, root, tc.primaryFile, tc.files[tc.primaryFile], tc.primaryTarget, 0)
			related := contextSpan(t, root, tc.relatedFile, tc.files[tc.relatedFile], tc.relatedTarget, 0)
			if d.Code != "PP601" || d.Symbol != tc.symbol || d.Span != primary || len(d.Related) == 0 || d.Related[0] != related {
				t.Fatalf("manifest metadata lost at report boundary: %+v", d)
			}
			for _, format := range []string{"json", "text"} {
				status, output, stderr := cliRun("check", root, "--format", format, "--jobs", "8")
				if status != 2 || stderr != "" {
					t.Fatalf("unexpected manifest result: %d %s %s", status, output, stderr)
				}
				if format == "json" {
					var decoded Report
					if err := json.Unmarshal([]byte(output), &decoded); err != nil || cliJSON(t, &decoded) != cliJSON(t, baseline) {
						t.Fatalf("manifest JSON lost report metadata: %v\n%s", err, output)
					}
				} else if !strings.Contains(output, "symbol: "+tc.symbol+"\n") || !strings.Contains(output, fmt.Sprintf("declaration at %s:%d:%d\n", related.File, related.Line, related.Column)) {
					t.Fatalf("manifest text lost declaration context:\n%s", output)
				}
			}
		})
	}
}

func TestInvalidEntrypointLinksConfigurationToDeclaration(t *testing.T) {
	const source = "from typing import Final\nanswer: Final[int] = 1\n"
	root := cliProject(t, map[string]string{"src/main.py": source}, []string{"main.answer"}, nil)
	report := Check(Options{Path: root, NoCache: true})
	if report.OK || len(report.Diagnostics) != 1 {
		t.Fatalf("constant entrypoint must reject once: %s", cliJSON(t, report))
	}
	d := report.Diagnostics[0]
	if d.Code != "PP701" || d.Symbol != "main.answer" || d.Span != report.Config.EntrypointSpans["main.answer"] || !reflect.DeepEqual(d.Related, []model.Span{contextSpan(t, root, "src/main.py", source, "answer: Final[int] = 1", 0)}) {
		t.Fatalf("entrypoint lacks path from configuration to invalid target: %+v", d)
	}
}

func TestConfigurationErrorsPreserveDeclarationContextInReports(t *testing.T) {
	root := cliProject(t, map[string]string{"src/main.py": "def run() -> None:\n    pass\n"}, nil, nil)
	const source = "[tool.purepy]\nlanguage = '0.1'\npython_syntax = '3.14'\nsource_root = 'src'\nentrypoints = ['main.run', 'main.run']\nmanifests = []\n"
	cliWrite(t, root, "purepy.toml", source)
	report := Check(Options{Path: root, Jobs: 1, NoCache: true})
	if report.OK || len(report.Diagnostics) != 1 {
		t.Fatalf("duplicate configured entrypoint must reject once: %s", cliJSON(t, report))
	}
	d := report.Diagnostics[0]
	primary := contextSpan(t, root, "purepy.toml", source, "'main.run'", 1)
	original := contextSpan(t, root, "purepy.toml", source, "'main.run'", 0)
	if d.Code != "PP001" || d.Symbol != "main.run" || d.Span != primary || !reflect.DeepEqual(d.Related, []model.Span{original}) {
		t.Fatalf("configuration context lost at report boundary: %+v", d)
	}
	for _, format := range []string{"json", "text"} {
		status, output, stderr := cliRun("check", root, "--format", format)
		if status != 2 || stderr != "" {
			t.Fatalf("unexpected configuration result: %d %s %s", status, output, stderr)
		}
		if format == "json" {
			var decoded Report
			if err := json.Unmarshal([]byte(output), &decoded); err != nil || cliJSON(t, &decoded) != cliJSON(t, report) {
				t.Fatalf("configuration JSON lost report metadata: %v\n%s", err, output)
			}
		} else if !strings.Contains(output, "symbol: main.run\n") || !strings.Contains(output, fmt.Sprintf("declaration at %s:%d:%d\n", original.File, original.Line, original.Column)) {
			t.Fatalf("configuration text lost declaration context:\n%s", output)
		}
	}
}

func TestDiscoveryDiagnosticIdentifiesConfigurationSymbol(t *testing.T) {
	root := cliProject(t, map[string]string{"src/pkg/main.py": ""}, nil, nil)
	report := Check(Options{Path: root})
	if report.OK || len(report.Diagnostics) != 1 || report.Diagnostics[0].Symbol != "tool.purepy.source_root" || report.Diagnostics[0].Span != report.Config.FieldSpans["source_root"] {
		t.Fatalf("discovery error lacks its configuration field: %s", cliJSON(t, report))
	}
}
