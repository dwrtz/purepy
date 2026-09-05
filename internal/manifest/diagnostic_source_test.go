package manifest

import (
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/dwrtz/purepy/internal/model"
)

func assertManifestSpan(t *testing.T, path, text string, span model.Span, target string, occurrence int) {
	t.Helper()
	start := 0
	for index := 0; index <= occurrence; index++ {
		next := strings.Index(text[start:], target)
		if next < 0 {
			t.Fatalf("missing test target %q occurrence %d", target, occurrence)
		}
		start += next
		if index < occurrence {
			start += len(target)
		}
	}
	end := start + len(target)
	lineStart := strings.LastIndexByte(text[:start], '\n') + 1
	endLineStart := strings.LastIndexByte(text[:end], '\n') + 1
	want := model.Span{File: path, Start: start, End: end, Line: 1 + strings.Count(text[:start], "\n"), Column: 1 + utf8.RuneCountInString(text[lineStart:start]), EndLine: 1 + strings.Count(text[:end], "\n"), EndColumn: 1 + utf8.RuneCountInString(text[endLineStart:end])}
	if span != want {
		t.Fatalf("span %+v does not select %q occurrence %d; want %+v", span, target, occurrence, want)
	}
}

func TestManifestDeclarationSourceMetadata(t *testing.T) {
	for _, tc := range []struct {
		name string
		text string
	}{
		{"table_declarations_inline_parameters", `schema = 1
[[module]]
name = 'host.β'
import_safe = true
[[type]]
name = 'host.β.Value'
category = 'value'
immutable = true
[[function]]
name = 'host.β.run'
kind = 'sync'
trust = 'pure'
returns = 'host.β.Value'
parameters = [{name = 'α', type = 'int'}, {name = 'β', type = 'str'}]
`},
		{"table_parameters", `schema = 1
[[module]]
name = 'host.β'
import_safe = true
[[type]]
name = 'host.β.Value'
category = 'value'
immutable = true
[[function]]
name = 'host.β.run'
kind = 'sync'
trust = 'pure'
returns = 'host.β.Value'
[[function.parameters]]
name = 'α'
type = 'int'
[[function.parameters]]
name = 'β'
type = 'str'
`},
		{"inline_declarations", `schema = 1
module = [{name = 'host.β', import_safe = true}]
type = [{name = 'host.β.Value', category = 'value', immutable = true}]
function = [{name = 'host.β.run', kind = 'sync', trust = 'pure', returns = 'host.β.Value', parameters = [{name = 'α', type = 'int'}, {name = 'β', type = 'str'}]}]
`},
		{"quoted_keys", `"schema" = 1
[["module"]]
"name" = 'host.β'
"import_safe" = true
[["type"]]
"name" = 'host.β.Value'
"category" = 'value'
"immutable" = true
[["function"]]
"name" = 'host.β.run'
"kind" = 'sync'
"trust" = 'pure'
"returns" = 'host.β.Value'
"parameters" = [{"name" = 'α', "type" = 'int'}, {"name" = 'β', "type" = 'str'}]
`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := writeManifest(t, tc.text)
			set, err := Load([]string{path})
			if err != nil {
				t.Fatal(err)
			}
			assertManifestSpan(t, path, tc.text, set.Modules[0].Span, "'host.β'", 0)
			assertManifestSpan(t, path, tc.text, set.Types[0].Span, "'host.β.Value'", 0)
			f := set.Functions[0]
			assertManifestSpan(t, path, tc.text, f.Span, "'host.β.run'", 0)
			assertManifestSpan(t, path, tc.text, f.ReturnSpan, "'host.β.Value'", 1)
			if len(f.Parameters) != 2 || f.Parameters[0].Name != "α" || f.Parameters[1].Name != "β" {
				t.Fatalf("parameter order changed: %+v", f.Parameters)
			}
			assertManifestSpan(t, path, tc.text, f.Parameters[0].Span, "'α'", 0)
			assertManifestSpan(t, path, tc.text, f.Parameters[0].TypeSpan, "'int'", 0)
			assertManifestSpan(t, path, tc.text, f.Parameters[1].Span, "'β'", 0)
			assertManifestSpan(t, path, tc.text, f.Parameters[1].TypeSpan, "'str'", 0)
		})
	}
}

func TestManifestDuplicateDeclarationContext(t *testing.T) {
	const module = "[[module]]\nname = 'host.ops'\nimport_safe = true\n"
	const function = "[[function]]\nname = 'host.ops.Value'\nkind = 'sync'\ntrust = 'pure'\nparameters = []\nreturns = 'int'\n"
	const typ = "[[type]]\nname = 'host.ops.Value'\ncategory = 'value'\nimmutable = true\n"
	for _, tc := range []struct {
		name, text, symbol, target string
	}{
		{"same_kind", "schema = 1\n" + module + module + module, "host.ops", "'host.ops'"},
		{"type_before_function", "schema = 1\n" + module + typ + function, "host.ops.Value", "'host.ops.Value'"},
		{"function_before_type", "schema = 1\n" + module + function + typ, "host.ops.Value", "'host.ops.Value'"},
		{"three_kinds", "schema = 1\n" + module + function + "[[module]]\nname = 'host.ops.Value'\nimport_safe = true\n" + typ, "host.ops.Value", "'host.ops.Value'"},
		{"inline_duplicates", "schema = 1\nmodule = [{name = 'host.ops', import_safe = true}, {name = 'host.ops', import_safe = true}, {name = 'host.ops', import_safe = true}]\n", "host.ops", "'host.ops'"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := writeManifest(t, tc.text)
			_, err := Load([]string{path})
			var located *Error
			if !errors.As(err, &located) || located.Symbol != tc.symbol || len(located.Related) != 1 {
				t.Fatalf("duplicate lacks symbol and original declaration: %v, %+v", err, located)
			}
			assertManifestSpan(t, path, tc.text, located.Span, tc.target, 1)
			assertManifestSpan(t, path, tc.text, located.Related[0], tc.target, 0)
		})
	}
	firstText, secondText := "schema = 1\n"+module, "# second manifest\nschema = 1\n"+module
	first, second := writeManifest(t, firstText), writeManifest(t, secondText)
	_, err := Load([]string{first, second})
	var located *Error
	if !errors.As(err, &located) || located.Symbol != "host.ops" || len(located.Related) != 1 {
		t.Fatalf("cross-file duplicate lost context: %v, %+v", err, located)
	}
	assertManifestSpan(t, second, secondText, located.Span, "'host.ops'", 0)
	assertManifestSpan(t, first, firstText, located.Related[0], "'host.ops'", 0)
}

func TestManifestParameterAndReturnErrorContext(t *testing.T) {
	const prefix = "schema = 1\n[[module]]\nname = 'host.ops'\nimport_safe = true\n[[function]]\nname = 'host.ops.run'\nkind = 'sync'\ntrust = 'pure'\n"
	for _, tc := range []struct {
		name, tail, symbol, target, related string
		occurrence                          int
	}{
		{"inline_duplicate", "returns = 'int'\nparameters = [{name = 'x', type = 'int'}, {name = 'x', type = 'int'}, {name = 'x', type = 'int'}]\n", "host.ops.run.x", "'x'", "'x'", 1},
		{"table_duplicate", "returns = 'int'\n[[function.parameters]]\nname = 'x'\ntype = 'int'\n[[function.parameters]]\nname = 'x'\ntype = 'int'\n", "host.ops.run.x", "'x'", "'x'", 1},
		{"parameter_type", "returns = 'int'\nparameters = [{name = 'x', type = 'Callable'}]\n", "host.ops.run.x", "'Callable'", "'x'", 0},
		{"parameter_name", "returns = 'int'\nparameters = [{name = '*args', type = 'int'}]\n", "host.ops.run.*args", "'*args'", "'host.ops.run'", 0},
		{"return", "returns = 'int | str'\nparameters = []\n", "host.ops.run", "'int | str'", "'host.ops.run'", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			text := prefix + tc.tail
			path := writeManifest(t, text)
			_, err := Load([]string{path})
			var located *Error
			if !errors.As(err, &located) || located.Symbol != tc.symbol || len(located.Related) == 0 {
				t.Fatalf("invalid signature lost declaration context: %v, %+v", err, located)
			}
			assertManifestSpan(t, path, text, located.Span, tc.target, tc.occurrence)
			assertManifestSpan(t, path, text, located.Related[0], tc.related, 0)
		})
	}
}

func TestManifestSourceMetadataDoesNotWidenSchema(t *testing.T) {
	for _, field := range []string{"Span", "span", "TypeSpan", "type_span"} {
		t.Run(field, func(t *testing.T) {
			text := strings.Replace(validManifest, `name = "read", type = "host.database.Read"`, `name = "read", type = "host.database.Read", `+field+` = {file = "forged.py", line = 999}`, 1)
			_, err := Load([]string{writeManifest(t, text)})
			if err == nil || !strings.Contains(err.Error(), "unknown fields") {
				t.Fatalf("source metadata must not be accepted as a manifest field: %v", err)
			}
		})
	}
}
