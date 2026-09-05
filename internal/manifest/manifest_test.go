package manifest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const validManifest = `schema = 1
[[module]]
name = "host.database"
import_safe = true

[[type]]
name = "host.database.Read"
category = "capability"
labels = ["database.read"]

[[type]]
name = "host.database.Connection"
category = "host_ref"

[[type]]
name = "host.database.Row"
category = "value"
immutable = true

[[function]]
name = "host.database.load"
kind = "async"
trust = "host"
parameters = [
  { name = "read", type = "host.database.Read" },
  { name = "connection", type = "host.database.Connection" },
  { name = "ids", type = "tuple[int, ...]" },
]
returns = "app.records.Result | None"
`

func writeManifest(t *testing.T, text string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "host.toml")
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadManifest(t *testing.T) {
	path := writeManifest(t, validManifest)
	set, err := Load([]string{path})
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Modules) != 1 || len(set.Types) != 3 || len(set.Functions) != 1 {
		t.Fatalf("unexpected declarations: %#v", set)
	}
	if set.Modules[0].Source != path || !set.Modules[0].ImportSafe || set.Types[0].Category != "capability" || set.Types[0].Labels[0] != "database.read" || !set.Types[2].Immutable {
		t.Fatalf("declaration metadata lost: %#v", set)
	}
	f := set.Functions[0]
	if f.Name != "host.database.load" || f.Kind != "async" || f.Trust != "host" || f.Source != path || len(f.Parameters) != 3 || f.Parameters[1].Name != "connection" || f.Returns != "app.records.Result | None" {
		t.Fatalf("unexpected signature: %#v", f)
	}
}

func TestRejectInvalidManifest(t *testing.T) {
	tests := []struct{ name, content, want string }{
		{"missing schema", strings.TrimPrefix(validManifest, "schema = 1\n"), "schema"},
		{"schema version", strings.Replace(validManifest, "schema = 1", "schema = 2", 1), "schema"},
		{"unknown root", "unknown = true\n" + validManifest, "fields"},
		{"unknown function field", validManifest + "variadic = true\n", "fields"},
		{"unknown parameter field", strings.Replace(validManifest, `name = "read", type = "host.database.Read"`, `name = "read", type = "host.database.Read", default = 0`, 1), "fields"},
		{"duplicate key", validManifest + `returns = "None"`, "already"},
		{"import safety missing", strings.Replace(validManifest, "import_safe = true\n", "", 1), "import_safe"},
		{"module identifier", strings.Replace(validManifest, `name = "host.database"`, `name = "host.bad-name"`, 1), "module name"},
		{"type identifier", strings.Replace(validManifest, `name = "host.database.Read"`, `name = "Read"`, 1), "qualified"},
		{"function identifier", strings.Replace(validManifest, `name = "host.database.load"`, `name = "load"`, 1), "qualified"},
		{"category", strings.Replace(validManifest, `category = "capability"`, `category = "unknown"`, 1), "category"},
		{"missing labels", strings.Replace(validManifest, "labels = [\"database.read\"]\n", "", 1), "label"},
		{"empty labels", strings.Replace(validManifest, `["database.read"]`, `[]`, 1), "label"},
		{"duplicate labels", strings.Replace(validManifest, `["database.read"]`, `["database.read", "database.read"]`, 1), "duplicate capability label"},
		{"wildcard labels", strings.Replace(validManifest, `["database.read"]`, `["database.*"]`, 1), "wildcards"},
		{"value mutability missing", strings.Replace(validManifest, "immutable = true\n", "", 1), "immutable"},
		{"value mutable", strings.Replace(validManifest, "immutable = true", "immutable = false", 1), "immutable"},
		{"immutable capability", strings.Replace(validManifest, `category = "capability"`, "category = \"capability\"\nimmutable = true", 1), "only on value"},
		{"labels on host ref", strings.Replace(validManifest, `category = "host_ref"`, "category = \"host_ref\"\nlabels = []", 1), "only on capability"},
		{"kind", strings.Replace(validManifest, `kind = "async"`, `kind = "generator"`, 1), "kind"},
		{"trust", strings.Replace(validManifest, `trust = "host"`, `trust = "unsafe"`, 1), "trust"},
		{"duplicate parameter", strings.Replace(validManifest, `name = "connection"`, `name = "read"`, 1), "duplicate parameter"},
		{"bad parameter", strings.Replace(validManifest, `name = "connection"`, `name = "*args"`, 1), "parameter name"},
		{"parameter type", strings.Replace(validManifest, `type = "host.database.Read"`, `type = "Callable[[int], int]"`, 1), "invalid type"},
		{"return type", strings.Replace(validManifest, `returns = "app.records.Result | None"`, `returns = "int | str"`, 1), "return type"},
		{"missing module", strings.Replace(validManifest, "[[module]]\nname = \"host.database\"\nimport_safe = true\n", "", 1), "not declared"},
		{"method", strings.Replace(validManifest, `name = "host.database.load"`, `name = "host.database.Connection.load"`, 1), "methods"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeManifest(t, tt.content)
			if _, err := Load([]string{path}); err == nil || !strings.Contains(err.Error(), tt.want) || !strings.Contains(err.Error(), path) {
				t.Fatalf("expected %q and source path in error, got %v", tt.want, err)
			}
		})
	}
}

func TestDuplicateDeclarationsNeverOverride(t *testing.T) {
	first, second := writeManifest(t, validManifest), writeManifest(t, validManifest)
	_, err := Load([]string{first, second})
	if err == nil || !strings.Contains(err.Error(), "duplicate declaration") || !strings.Contains(err.Error(), first) || !strings.Contains(err.Error(), second) {
		t.Fatalf("expected conflict including both sources, got %v", err)
	}
	const duplicate = `
[[function]]
name = "host.database.Read"
kind = "sync"
trust = "pure"
parameters = []
returns = "int"
`
	if _, err := Load([]string{writeManifest(t, validManifest+duplicate)}); err == nil || !strings.Contains(err.Error(), "duplicate declaration") {
		t.Fatalf("expected cross-kind conflict, got %v", err)
	}
}

func TestUnknownFieldsIdentifyKeysAndPositions(t *testing.T) {
	content := "schema = 1\n[[module]]\nname = \"host.ops\"\nimport_safe = true\nexecute = \"script.py\"\n"
	_, err := Load([]string{writeManifest(t, content)})
	if err == nil || !strings.Contains(err.Error(), "module.execute") || !strings.Contains(err.Error(), "line 5, column 1") {
		t.Fatalf("unknown field needs actionable key and location: %v", err)
	}
}

func TestCrossManifestModuleAndConfiguredOrder(t *testing.T) {
	modulePath := writeManifest(t, "schema = 1\n[[module]]\nname = \"host.math\"\nimport_safe = true\n")
	functionPath := writeManifest(t, `schema = 1
[[function]]
name = "host.math.constant"
kind = "sync"
trust = "pure"
parameters = []
returns = "int"
`)
	set, err := Load([]string{functionPath, modulePath})
	if err != nil {
		t.Fatal(err)
	}
	if set.Functions[0].Source != functionPath || set.Modules[0].Source != modulePath {
		t.Fatalf("source attribution lost: %#v", set)
	}
}

func TestManifestRequiresExplicitEmptyParameters(t *testing.T) {
	content := `schema = 1
[[module]]
name = "host.math"
import_safe = false
[[function]]
name = "host.math.constant"
kind = "sync"
trust = "pure"
returns = "int"
`
	if _, err := Load([]string{writeManifest(t, content)}); err == nil || !strings.Contains(err.Error(), "parameters is required") {
		t.Fatalf("expected missing parameters error, got %v", err)
	}
	set, err := Load([]string{writeManifest(t, content+"parameters = []\n")})
	if err != nil {
		t.Fatal(err)
	}
	if set.Modules[0].ImportSafe {
		t.Fatal("import_safe = false was not retained")
	}
}

func TestTypeGrammar(t *testing.T) {
	for _, text := range []string{"None", "bool", "int", "float", "str", "bytes", "app.Record", "int | None", "tuple[int, ...]", "tuple[tuple[str | None, ...], ...] | None", " app.β "} {
		if !validTypeSyntax(text) {
			t.Errorf("rejected %q", text)
		}
	}
	for _, text := range []string{"", "Any", "object", "Record", "list[int]", "dict[str, int]", "Callable", "None | None", "int | str", "int | None | None", "tuple", "tuple[int]", "tuple[int, str]", "tuple[int,...]garbage", "int; evil()", "int\n", "app.ﬁle", "Final[int]", strings.Repeat("tuple[", 100) + "int" + strings.Repeat(", ...]", 100)} {
		if validTypeSyntax(text) {
			t.Errorf("accepted %q", text)
		}
	}
}

func TestEmptyManifestSet(t *testing.T) {
	set, err := Load(nil)
	if err != nil || set == nil || set.Modules == nil || set.Types == nil || set.Functions == nil {
		t.Fatalf("empty set must be usable and serialize as arrays: %#v, %v", set, err)
	}
}

func FuzzTypeSyntax(f *testing.F) {
	for _, seed := range []string{"int", "tuple[int, ...] | None", "app.Record", "Callable[[int], int]", ""} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		_ = validTypeSyntax(input)
	})
}
