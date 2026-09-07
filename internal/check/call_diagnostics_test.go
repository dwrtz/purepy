package check

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/dwrtz/purepy/internal/diag"
	"github.com/dwrtz/purepy/internal/frontend"
	"github.com/dwrtz/purepy/internal/manifest"
	"github.com/dwrtz/purepy/internal/model"
)

// These fixtures must reach semantic checking: an unrelated parser rejection
// cannot satisfy a diagnostic-context assertion. Rebuild the program for each
// worker count so no previously attached context can leak into the comparison.
func callDiagnostics(t *testing.T, sources map[string]string, external *manifest.Set) []diag.Diagnostic {
	t.Helper()
	if external == nil {
		external = &manifest.Set{}
	}
	names := make([]string, 0, len(sources))
	for name := range sources {
		names = append(names, name)
	}
	sort.Strings(names)
	var previous Result
	for _, jobs := range []int{1, 4} {
		modules := make([]*Module, 0, len(names))
		for _, name := range names {
			path := name + ".py"
			tree, ds := frontend.Parse(path, []byte(sources[name]))
			if len(ds) != 0 {
				t.Fatalf("fixture must parse cleanly: %+v\n%s", ds, sources[name])
			}
			modules = append(modules, &Module{Name: name, Path: path, Tree: tree})
		}
		result := Link(modules, external, nil).CheckFunctions(jobs)
		diag.Sort(result.Diagnostics)
		if jobs > 1 && !reflect.DeepEqual(previous, result) {
			t.Fatalf("worker count changed semantic report:\n1: %+v\n4: %+v", previous, result)
		}
		previous = result
	}
	return previous.Diagnostics
}

func callDiagnostic(t *testing.T, ds []diag.Diagnostic, code, message string) diag.Diagnostic {
	t.Helper()
	for _, d := range ds {
		if d.Code == code && strings.Contains(d.Message, message) {
			return d
		}
	}
	t.Fatalf("missing %s (%s) in %+v", code, message, ds)
	return diag.Diagnostic{}
}

func callDiagnosticTypes(t *testing.T, d diag.Diagnostic, expected map[string]model.Type) {
	t.Helper()
	for role, typ := range expected {
		if actual, ok := d.Types[role]; !ok || !actual.Equal(typ) {
			t.Errorf("%s type %q: want %s, got %s (present=%t): %+v", d.Code, role, typ, actual, ok, d)
		}
	}
}

func callRelatedText(t *testing.T, d diag.Diagnostic, file, source, text string) {
	t.Helper()
	for _, at := range d.Related {
		if at.File == file && at.Start >= 0 && at.End <= len(source) && at.End > at.Start && source[at.Start:at.End] == text {
			return
		}
	}
	t.Errorf("%s is missing related %s declaration %q: %+v", d.Code, file, text, d.Related)
}

func TestCallDiagnosticParameterContext(t *testing.T) {
	library := "def consume(value: int) -> int:\n    return value\n"
	application := "from library import consume\ndef run() -> int:\n    return consume(True)\n"
	ds := callDiagnostics(t, map[string]string{"library": library, "app": application}, nil)
	d := callDiagnostic(t, ds, "PP205", "expected int")
	if d.Symbol != "library.consume.value" {
		t.Fatalf("argument error should identify its parameter: %+v", d)
	}
	callDiagnosticTypes(t, d, map[string]model.Type{"expected": model.Int, "actual": model.Bool, "return": model.Int, "parameter.value": model.Int})
	callRelatedText(t, d, "app.py", application, "consume")
	callRelatedText(t, d, "library.py", library, "value: int")
}

func TestCallDiagnosticArgumentOrigin(t *testing.T) {
	library := "def consume(value: int) -> int:\n    return value\n"
	for _, tc := range []struct{ name, function, declaration string }{
		{"parameter", "def run(actual: str) -> int:\n    return consume(actual)\n", "actual: str"},
		{"local", "def run() -> int:\n    actual: str = 'wrong'\n    return consume(actual)\n", "actual: str = 'wrong'"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := "from library import consume\n" + tc.function
			d := callDiagnostic(t, callDiagnostics(t, map[string]string{"app": source, "library": library}, nil), "PP205", "expected int")
			if d.Symbol != "library.consume.value" {
				t.Errorf("argument origin must not replace the responsible parameter symbol: %+v", d)
			}
			callDiagnosticTypes(t, d, map[string]model.Type{"actual": model.Str, "expected": model.Int})
			callRelatedText(t, d, "app.py", source, tc.declaration)
			callRelatedText(t, d, "app.py", source, "consume")
			callRelatedText(t, d, "library.py", library, "value: int")
		})
	}
}

func TestCallDiagnosticArgumentBinding(t *testing.T) {
	prefix := "def consume(value: int) -> int:\n    return value\ndef run() -> int:\n    return "
	for _, tc := range []struct {
		name, call, message, symbol string
		actual                      model.Type
	}{
		{"missing", "consume()", "requires argument value", "app.consume.value", model.Invalid},
		{"extra_positional", "consume(1, True)", "unexpected argument", "app.consume", model.Bool},
		{"extra_expression_error", "consume(1, consume(True))", "unexpected argument", "app.consume", model.Int},
		{"extra_keyword", "consume(value=1, other=True)", "unexpected argument other", "app.consume", model.Bool},
		{"duplicate", "consume(1, value=2)", "duplicate argument for value", "app.consume.value", model.Invalid},
		{"keyword_order", "consume(value=1, 2)", "positional arguments cannot follow keyword", "app.consume", model.Invalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := prefix + tc.call + "\n"
			d := callDiagnostic(t, callDiagnostics(t, map[string]string{"app": source}, nil), "PP302", tc.message)
			if d.Symbol != tc.symbol {
				t.Errorf("expected symbol %s: %+v", tc.symbol, d)
			}
			callDiagnosticTypes(t, d, map[string]model.Type{"return": model.Int, "parameter.value": model.Int})
			if tc.actual.Kind != "invalid" {
				callDiagnosticTypes(t, d, map[string]model.Type{"actual": tc.actual})
			}
			if tc.name == "missing" || tc.name == "duplicate" {
				callDiagnosticTypes(t, d, map[string]model.Type{"expected": model.Int})
				callRelatedText(t, d, "app.py", source, "value: int")
			}
			if tc.name == "duplicate" {
				callRelatedText(t, d, "app.py", source, "1")
			}
		})
	}

	source := "from typing import NamedTuple\n\nclass Record(NamedTuple):\n    count: int\ndef run() -> Record:\n    return Record(True)\n"
	d := callDiagnostic(t, callDiagnostics(t, map[string]string{"app": source}, nil), "PP205", "expected int")
	if d.Symbol != "app.Record.count" {
		t.Errorf("record constructor should identify its field: %+v", d)
	}
	callDiagnosticTypes(t, d, map[string]model.Type{"expected": model.Int, "actual": model.Bool})
	callRelatedText(t, d, "app.py", source, "count: int")
}

func TestCallDiagnosticTargetsAndAsync(t *testing.T) {
	for _, tc := range []struct{ name, source, code, symbol string }{
		{"local", "def run(candidate: int) -> int:\n    return candidate()\n", "PP301", "app.run.candidate"},
		{"method", "def run(text: str) -> str:\n    return text.upper()\n", "PP301", "app.run.text"},
		{"constant", "from typing import Final\nVALUE: Final[int] = 1\ndef run() -> int:\n    return VALUE()\n", "PP301", "app.VALUE"},
		{"coroutine", "async def operation(value: int) -> int:\n    return value\ndef run() -> int:\n    return operation(1)\n", "PP401", "app.operation"},
		{"sync", "def operation(value: int) -> int:\n    return value\nasync def run() -> int:\n    return await operation(1)\n", "PP403", "app.operation"},
		{"caller", "async def operation(value: int) -> int:\n    return value\ndef run() -> int:\n    return await operation(1)\n", "PP404", "app.operation"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := callDiagnostic(t, callDiagnostics(t, map[string]string{"app": tc.source}, nil), tc.code, "")
			if d.Symbol != tc.symbol || len(d.Related) == 0 {
				t.Errorf("call error needs relevant symbol and declaration: %+v", d)
			}
			if tc.name == "local" || tc.name == "constant" {
				callDiagnosticTypes(t, d, map[string]model.Type{"actual": model.Int})
			} else if tc.name == "method" {
				callDiagnosticTypes(t, d, map[string]model.Type{"actual": model.Str})
			} else {
				callDiagnosticTypes(t, d, map[string]model.Type{"return": model.Int, "parameter.value": model.Int})
			}
			if tc.name == "caller" {
				callRelatedText(t, d, "app.py", tc.source, "def run() -> int:\n    return await operation(1)")
			}
		})
	}
}

func TestCallDiagnosticIntrinsics(t *testing.T) {
	for _, tc := range []struct {
		call, code, name string
		types            map[string]model.Type
	}{
		{"len(True)", "PP303", "len", map[string]model.Type{"arg1": model.Bool}},
		{"min(1, 2.0)", "PP303", "min", map[string]model.Type{"arg1": model.Int, "arg2": model.Float}},
		{"len(value='text')", "PP302", "len", map[string]model.Type{"arg1": model.Str}},
		{"range(1, 3)", "PP304", "range", map[string]model.Type{"arg1": model.Int, "arg2": model.Int}},
		{"unknown()", "PP303", "unknown", nil},
	} {
		t.Run(tc.call, func(t *testing.T) {
			source := "def run() -> None:\n    result = " + tc.call + "\n"
			d := callDiagnostic(t, callDiagnostics(t, map[string]string{"app": source}, nil), tc.code, "")
			if d.Symbol != tc.name {
				t.Errorf("intrinsic error should identify target %s: %+v", tc.name, d)
			}
			callDiagnosticTypes(t, d, tc.types)
		})
	}
}

func TestCallDiagnosticManifestForwarding(t *testing.T) {
	host := `schema = 1
[[module]]
name = "host.io"
import_safe = true
[[type]]
name = "host.io.Read"
category = "capability"
labels = ["read"]
[[type]]
name = "host.io.Write"
category = "capability"
labels = ["write"]
[[function]]
name = "host.io.read"
kind = "async"
trust = "host"
parameters = [
    {name = "cap", type = "host.io.Read"},
]
returns = "bytes"
`
	path := filepath.Join(t.TempDir(), "host.purepy.toml")
	if err := os.WriteFile(path, []byte(host), 0600); err != nil {
		t.Fatal(err)
	}
	external, err := manifest.Load([]string{path})
	if err != nil {
		t.Fatal(err)
	}
	source := "from host.io import Write, read\nasync def run(cap: Write) -> bytes:\n    return await read(cap)\n"
	ds := callDiagnostics(t, map[string]string{"app": source}, external)
	d := callDiagnostic(t, ds, "PP312", "direct forwarding")
	if d.Symbol != "host.io.read.cap" {
		t.Errorf("forwarding error should identify manifest parameter: %+v", d)
	}
	callDiagnosticTypes(t, d, map[string]model.Type{
		"expected": {Kind: "capability", Name: "host.io.Read"},
		"actual":   {Kind: "capability", Name: "host.io.Write"},
	})
	callRelatedText(t, d, "app.py", source, "read")
	callRelatedText(t, d, "app.py", source, "cap: Write")
	parameterStart := strings.Index(host, `{name = "cap"`)
	foundParameter := false
	for _, at := range d.Related {
		if at.File == path && at.Start >= parameterStart && at.End <= parameterStart+len(`{name = "cap", type = "host.io.Read"}`) && at.End > at.Start {
			foundParameter = true
		}
	}
	if !foundParameter {
		t.Errorf("forwarding error must point into the manifest parameter, not line 1: %+v", d.Related)
	}
	if boundaryCode(ds, "PP313") {
		t.Errorf("diagnostic context must not read authority as an ordinary expression: %+v", ds)
	}
	conditional := "from host.io import Write, read\nasync def run(cap: Write, flag: bool) -> bytes:\n    return await read(cap if flag else cap)\n"
	ds = callDiagnostics(t, map[string]string{"app": conditional}, external)
	d = callDiagnostic(t, ds, "PP312", "direct forwarding")
	callRelatedText(t, d, "app.py", conditional, "cap: Write")
	callRelatedText(t, d, "app.py", conditional, "flag: bool")
	if boundaryCode(ds, "PP313") {
		t.Errorf("compound argument context must not evaluate authority operands: %+v", ds)
	}
}

func TestCallDiagnosticConstantConstructorPath(t *testing.T) {
	library := "from typing import NamedTuple\n\nclass Record(NamedTuple):\n    count: int\n"
	source := "from typing import Final\nRECORD: Final[Record] = Record(1)\nfrom library import Record\n"
	d := callDiagnostic(t, callDiagnostics(t, map[string]string{"library": library, "app": source}, nil), "PP502", "constructor used before its import")
	if d.Symbol != "library.Record" {
		t.Errorf("constant constructor error should identify record: %+v", d)
	}
	callDiagnosticTypes(t, d, map[string]model.Type{"return": {Kind: "record", Name: "library.Record"}, "parameter.count": model.Int})
	callRelatedText(t, d, "app.py", source, "Record")
}
