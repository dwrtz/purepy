package manifest

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBoundaryRequiredFunctionFields(t *testing.T) {
	for _, tc := range []struct{ name, before, after string }{
		{"name", `name = "host.database.load"`, ""},
		{"kind", `kind = "async"`, ""},
		{"return", `returns = "app.records.Result | None"`, ""},
		{"trust", `trust = "host"`, ""},
		{"parameter_name", `{ name = "read", type = "host.database.Read" }`, `{ type = "host.database.Read" }`},
		{"parameter_type", `{ name = "read", type = "host.database.Read" }`, `{ name = "read" }`},
		{"unknown_module_field", "import_safe = true", "import_safe = true\nexecute = 'unsafe.py'"},
		{"unknown_type_field", `category = "host_ref"`, "category = \"host_ref\"\nmethods = []"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			content := strings.Replace(validManifest, tc.before, tc.after, 1)
			if content == validManifest {
				t.Fatal("test did not modify the manifest")
			}
			if _, err := Load([]string{writeManifest(t, content)}); err == nil {
				t.Fatalf("accepted invalid function/field declaration:\n%s", content)
			}
		})
	}
}

func TestBoundaryManifestErrorLocations(t *testing.T) {
	for _, tc := range []struct {
		name, content, target string
		line, column          int
	}{
		{"unknown_field", "schema = 1\n[[module]]\nname = 'host.ops'\nimport_safe = true\nexecute = 'bad.py'\n", "e", 5, 1},
		{"schema", "# header\nschema = 2\n", "schema", 2, 1},
		{"declaration", "schema = 1\n[[module]]\nname = 'host.ops'\nimport_safe = true\n[[function]]\nname = 'host.ops.bad'\nkind = 'generator'\nparameters = []\nreturns = 'int'\ntrust = 'pure'\n", "'host.ops.bad'", 6, 8},
		{"unicode_column", "schema = 1\n[[module]]\nname = 'host.β'\nimport_safe = true\n[[function]]\nname = 'host.β.bad'\nkind = 'generator'\nparameters = []\nreturns = 'int'\ntrust = 'pure'\n", "'host.β.bad'", 6, 8},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := writeManifest(t, tc.content)
			_, err := Load([]string{path})
			var located *Error
			if !errors.As(err, &located) {
				t.Fatalf("error lacks source metadata: %v", err)
			}
			s := located.Span
			if s.File != path || s.Line != tc.line || s.Column != tc.column || s.EndLine != tc.line || tc.content[s.Start:s.End] != tc.target {
				t.Fatalf("location %+v does not select %q at %d:%d", s, tc.target, tc.line, tc.column)
			}
		})
	}
	missing := filepath.Join(t.TempDir(), "missing.toml")
	_, err := Load([]string{missing})
	var located *Error
	if !errors.As(err, &located) || !errors.Is(err, os.ErrNotExist) || located.Span.File != missing || located.Span.Line != 1 || located.Span.EndLine != 1 {
		t.Fatalf("missing-file location or underlying cause lost: %v", err)
	}
}

func TestBoundaryManifestConflictLocation(t *testing.T) {
	first := writeManifest(t, validManifest)
	secondText := "# second manifest\n" + validManifest
	second := writeManifest(t, secondText)
	_, err := Load([]string{first, second})
	var located *Error
	if !errors.As(err, &located) || located.Span.File != second || located.Span.Line != 4 || secondText[located.Span.Start:located.Span.End] != `"host.database"` || !strings.Contains(err.Error(), first) {
		t.Fatalf("conflict must identify the rejected later declaration and retain first provenance: %v, %+v", err, located)
	}
}
