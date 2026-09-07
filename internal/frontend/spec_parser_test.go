package frontend

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestUnsupportedSyntaxRetainsExactSourceRanges(t *testing.T) {
	for _, tc := range []struct{ name, source, selected string }{
		{"expression", "# Unicode é 😀 before the declaration\ndef f(é: int) -> int:\n    return {é, 2}\n", "{é, 2}"},
		{"statement", "def f() -> None:\n    assert True\n", "assert True"},
		{"multiline", "def f() -> None:\n    try:\n        pass\n    except:\n        pass\n", "try:\n        pass\n    except:\n        pass"},
		{"lambda", "def f() -> None:\n    x = lambda: 1\n", "lambda: 1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, diagnostics := Parse("unsupported.py", []byte(tc.source))
			if len(diagnostics) != 1 {
				t.Fatalf("expected one diagnostic, got %+v", diagnostics)
			}
			d := diagnostics[0]
			start := strings.Index(tc.source, tc.selected)
			end := start + len(tc.selected)
			line := 1 + strings.Count(tc.source[:start], "\n")
			endLine := 1 + strings.Count(tc.source[:end], "\n")
			column := 1 + utf8.RuneCountInString(tc.source[strings.LastIndexByte(tc.source[:start], '\n')+1:start])
			endColumn := 1 + utf8.RuneCountInString(tc.source[strings.LastIndexByte(tc.source[:end], '\n')+1:end])
			if d.Code != "PP003" || d.Span.File != "unsupported.py" || d.Span.Start != start || d.Span.End != end || d.Span.Line != line || d.Span.Column != column || d.Span.EndLine != endLine || d.Span.EndColumn != endColumn {
				t.Fatalf("unsupported syntax lost source coordinates: %+v; want bytes [%d,%d), %d:%d-%d:%d", d, start, end, line, column, endLine, endColumn)
			}
		})
	}
}

func TestParserDependenciesStayBehindFrontend(t *testing.T) {
	count := 0
	err := filepath.WalkDir("..", func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, imported := range file.Imports {
			name, err := strconv.Unquote(imported.Path.Value)
			if err != nil {
				return err
			}
			if strings.Contains(name, "tree-sitter") {
				count++
				if file.Name.Name != "frontend" {
					t.Errorf("parser dependency escapes the adapter in %s: %s", path, name)
				}
			}
			if file.Name.Name == "check" && strings.HasSuffix(name, "/internal/frontend") {
				t.Errorf("checker depends on frontend instead of detached semantic IR: %s", path)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if count == 0 {
		t.Fatal("architecture check did not inspect the parser dependency")
	}
}
