package check

import (
	"strings"
	"testing"

	"github.com/dwrtz/purepy/internal/diag"
	"github.com/dwrtz/purepy/internal/manifest"
	"github.com/dwrtz/purepy/internal/model"
)

func declarationDiagnostic(t *testing.T, ds []diag.Diagnostic, code, symbol string) diag.Diagnostic {
	t.Helper()
	for _, d := range ds {
		if d.Code == code && d.Symbol == symbol {
			return d
		}
	}
	t.Fatalf("missing %s diagnostic for %s: %+v", code, symbol, ds)
	return diag.Diagnostic{}
}

func requireDeclaration(t *testing.T, d diag.Diagnostic, file string, line int) {
	t.Helper()
	for _, at := range d.Related {
		if at.File == file && at.Line == line {
			return
		}
	}
	t.Fatalf("%s %s does not identify declaration at %s:%d: %+v", d.Code, d.Symbol, file, line, d.Related)
}

func requireDiagnosticType(t *testing.T, d diag.Diagnostic, role string, want model.Type) {
	t.Helper()
	if got, ok := d.Types[role]; !ok || !got.Equal(want) {
		t.Fatalf("%s %s type %s = %+v, want %+v", d.Code, d.Symbol, role, got, want)
	}
}

func TestDeclarationConflictDiagnosticContext(t *testing.T) {
	t.Run("functions", func(t *testing.T) {
		ds := securityVerify(t, map[string]string{"app": "def f() -> int:\n    return 1\ndef f() -> str:\n    return 'two'\n"}, nil)
		d := declarationDiagnostic(t, ds, "PP103", "app.f")
		if d.Span.Line != 3 {
			t.Fatalf("duplicate points to line %d, want 3", d.Span.Line)
		}
		requireDeclaration(t, d, "app.py", 1)
	})
	t.Run("parameters", func(t *testing.T) {
		ds := securityVerify(t, map[string]string{"app": "def f(x: int, x: str) -> None:\n    return\n"}, nil)
		d := declarationDiagnostic(t, ds, "PP103", "app.f.x")
		requireDiagnosticType(t, d, "previous", model.Int)
		requireDiagnosticType(t, d, "declared", model.Str)
		if len(d.Related) != 1 || d.Related[0].Start >= d.Span.Start {
			t.Fatalf("duplicate parameter must identify the first parameter: %+v", d)
		}
	})
	t.Run("record_fields", func(t *testing.T) {
		ds := securityVerify(t, map[string]string{"app": "from typing import NamedTuple\n\nclass R(NamedTuple):\n    x: int\n    x: str\n"}, nil)
		d := declarationDiagnostic(t, ds, "PP202", "app.R.x")
		requireDiagnosticType(t, d, "previous", model.Int)
		requireDiagnosticType(t, d, "declared", model.Str)
		requireDeclaration(t, d, "app.py", 4)
	})
	t.Run("import_binding_and_origin", func(t *testing.T) {
		ds := securityVerify(t, map[string]string{
			"origin": "def f() -> None:\n    return\n",
			"app":    "from origin import f\nfrom origin import f\n",
		}, nil)
		d := declarationDiagnostic(t, ds, "PP103", "app.f")
		requireDeclaration(t, d, "app.py", 1)
		requireDeclaration(t, d, "origin.py", 1)
	})
}

func TestAnnotationDiagnosticContext(t *testing.T) {
	t.Run("missing_annotations_identify_owners", func(t *testing.T) {
		ds := securityVerify(t, map[string]string{"app": "def f(x):\n    return\n"}, nil)
		declarationDiagnostic(t, ds, "PP203", "app.f.x")
		declarationDiagnostic(t, ds, "PP203", "app.f")
	})
	t.Run("unknown_name_has_no_invented_declaration", func(t *testing.T) {
		ds := securityVerify(t, map[string]string{"app": "def f(x: Missing) -> None:\n    return\n"}, nil)
		d := declarationDiagnostic(t, ds, "PP203", "Missing")
		if len(d.Related) != 0 {
			t.Fatalf("unresolved annotation acquired a declaration: %+v", d)
		}
	})
	t.Run("wrong_role_identifies_import_path", func(t *testing.T) {
		ds := securityVerify(t, map[string]string{
			"origin": "def f() -> None:\n    return\n",
			"app":    "from origin import f\ndef g(x: f) -> None:\n    return\n",
		}, nil)
		d := declarationDiagnostic(t, ds, "PP203", "origin.f")
		requireDeclaration(t, d, "app.py", 1)
		requireDeclaration(t, d, "origin.py", 1)
		if len(d.Notes) == 0 || !strings.Contains(d.Notes[0], "function") {
			t.Fatalf("annotation has no explanation of the declaration's role: %+v", d)
		}
	})
	t.Run("authority_element_identifies_type", func(t *testing.T) {
		ds := securityVerify(t, map[string]string{"app": "from host.io import Read\ndef f(x: tuple[Read, ...]) -> None:\n    return\n"}, securityHost())
		d := declarationDiagnostic(t, ds, "PP203", "app.f.x")
		requireDiagnosticType(t, d, "element", model.Type{Kind: "capability", Name: "host.io.Read"})
		requireDeclaration(t, d, "host.toml", 1)
	})
	t.Run("unsupported_union_retains_known_types", func(t *testing.T) {
		ds := securityVerify(t, map[string]string{"app": "def f(x: int | str) -> None:\n    return\n"}, nil)
		d := declarationDiagnostic(t, ds, "PP203", "app.f.x")
		requireDiagnosticType(t, d, "annotation.left", model.Int)
		requireDiagnosticType(t, d, "annotation.right", model.Str)
	})
	t.Run("record_field_default_identifies_field", func(t *testing.T) {
		ds := securityVerify(t, map[string]string{"app": "from typing import NamedTuple\n\nclass R(NamedTuple):\n    x: int = 1\n"}, nil)
		d := declarationDiagnostic(t, ds, "PP202", "app.R.x")
		requireDiagnosticType(t, d, "annotation", model.Int)
	})
}

func TestConstantDiagnosticContext(t *testing.T) {
	t.Run("missing_final_wrapper_retains_primitive", func(t *testing.T) {
		ds := securityVerify(t, map[string]string{"app": "ANSWER: int = 1\n"}, nil)
		d := declarationDiagnostic(t, ds, "PP501", "app.ANSWER")
		requireDiagnosticType(t, d, "annotation", model.Int)
	})
	t.Run("local_final_spelling_is_not_support", func(t *testing.T) {
		ds := securityVerify(t, map[string]string{"app": "def Final() -> None:\n    return\nANSWER: Final[int] = 1\n"}, nil)
		d := declarationDiagnostic(t, ds, "PP501", "app.ANSWER")
		requireDiagnosticType(t, d, "annotation.index", model.Int)
		requireDeclaration(t, d, "app.py", 1)
	})
	t.Run("imported_final_spelling_retains_origin_and_type", func(t *testing.T) {
		ds := securityVerify(t, map[string]string{
			"origin": "from typing import NamedTuple\n\nclass Final(NamedTuple):\n    item: int\n",
			"app":    "from origin import Final\nANSWER: Final[int] = 1\n",
		}, nil)
		d := declarationDiagnostic(t, ds, "PP501", "app.ANSWER")
		requireDiagnosticType(t, d, "annotation.index", model.Int)
		requireDiagnosticType(t, d, "annotation.value", model.Type{Kind: "record", Name: "origin.Final"})
		requireDeclaration(t, d, "app.py", 1)
		requireDeclaration(t, d, "origin.py", 3)
	})
	t.Run("initializer_type", func(t *testing.T) {
		ds := securityVerify(t, map[string]string{"app": "from typing import Final\nANSWER: Final[int] = 'wrong'\n"}, nil)
		d := declarationDiagnostic(t, ds, "PP205", "app.ANSWER")
		requireDiagnosticType(t, d, "expected", model.Int)
		requireDiagnosticType(t, d, "actual", model.Str)
		requireDeclaration(t, d, "app.py", 2)
		if d.Span.Column <= d.Related[0].Column {
			t.Fatalf("initializer error should precede a path back to its annotation: %+v", d)
		}
	})
	t.Run("missing_initializer", func(t *testing.T) {
		ds := securityVerify(t, map[string]string{"app": "from typing import Final\nANSWER: Final[int]\n"}, nil)
		d := declarationDiagnostic(t, ds, "PP501", "app.ANSWER")
		requireDiagnosticType(t, d, "declared", model.Int)
	})
}

func TestFunctionDecoratorDiagnosticContext(t *testing.T) {
	ds := securityVerify(t, map[string]string{
		"origin": "def decorate() -> None:\n    return\n",
		"app":    "from origin import decorate\n@decorate\ndef run() -> None:\n    return\n",
	}, nil)
	d := declarationDiagnostic(t, ds, "PP003", "app.run")
	requireDeclaration(t, d, "app.py", 1)
	requireDeclaration(t, d, "origin.py", 1)
	if len(ds) != 1 {
		t.Fatalf("decorator context must not evaluate or resolve it again: %+v", ds)
	}
}

func TestCycleDiagnosticDeclarationPaths(t *testing.T) {
	t.Run("imports_exclude_noncycle_prefix", func(t *testing.T) {
		ds := securityVerify(t, map[string]string{
			"a": "from b import b\ndef a() -> None:\n    return\n",
			"b": "from c import c\ndef b() -> None:\n    return\n",
			"c": "from b import b\ndef c() -> None:\n    return\n",
		}, nil)
		d := declarationDiagnostic(t, ds, "PP105", "b")
		if d.Span.File != "c.py" || d.Span.Line != 1 || !strings.Contains(d.Message, "b -> c -> b") || strings.Contains(d.Message, "a ->") {
			t.Fatalf("import cycle must identify its closing edge and exact path: %+v", d)
		}
		if len(d.Related) != 1 {
			t.Fatalf("expected one preceding cycle edge, got %+v", d.Related)
		}
		requireDeclaration(t, d, "b.py", 1)
	})
	t.Run("record_fields", func(t *testing.T) {
		ds := securityVerify(t, map[string]string{"app": "from typing import NamedTuple\n\nclass A(NamedTuple):\n    b: B\n\nclass B(NamedTuple):\n    a: tuple[A, ...]\n"}, nil)
		if len(ds) != 0 {
			t.Fatalf("regular recursive records rejected: %+v", ds)
		}
	})
	t.Run("self_reference", func(t *testing.T) {
		ds := securityVerify(t, map[string]string{"app": "from typing import NamedTuple\n\nclass R(NamedTuple):\n    child: R | None\n"}, nil)
		if len(ds) != 0 {
			t.Fatalf("regular recursive records rejected: %+v", ds)
		}
	})
}

func TestManifestLinkDiagnosticContext(t *testing.T) {
	span := func(line int) model.Span {
		return model.Span{File: "host.toml", Start: line * 10, End: line*10 + 5, Line: line, Column: 1, EndLine: line, EndColumn: 6}
	}
	capType := model.Type{Kind: "capability", Name: "host.io.Read"}
	external := &manifest.Set{
		Modules: []manifest.Module{{Name: "host.io", Source: "host.toml", Span: span(2)}},
		Types:   []manifest.Type{{Name: capType.Name, Category: capType.Kind, Source: "host.toml", Span: span(6)}},
		Functions: []manifest.Function{{
			Name: "host.io.bad", Kind: "sync", Trust: "pure", Returns: capType.Name, Source: "host.toml", Span: span(10), ReturnSpan: span(13),
			Parameters: []manifest.Parameter{{Name: "cap", Type: capType.Name, Span: span(15), TypeSpan: span(16)}},
		}},
	}
	ds := securityVerify(t, map[string]string{"app": "from host.io import Read\n"}, external)
	imported := declarationDiagnostic(t, ds, "PP604", capType.Name)
	requireDeclaration(t, imported, "host.toml", 2)
	requireDeclaration(t, imported, "host.toml", 6)
	returns := declarationDiagnostic(t, ds, "PP602", "host.io.bad")
	if returns.Span.Line != 13 {
		t.Fatalf("return error lost the manifest return location: %+v", returns)
	}
	requireDiagnosticType(t, returns, "return", capType)
	requireDeclaration(t, returns, "host.toml", 6)
	parameter := declarationDiagnostic(t, ds, "PP602", "host.io.bad.cap")
	if parameter.Span.Line != 15 {
		t.Fatalf("parameter error lost the manifest parameter location: %+v", parameter)
	}
	requireDiagnosticType(t, parameter, "parameter", capType)
	requireDeclaration(t, parameter, "host.toml", 6)

	external.Functions[0].Returns = "int"
	external.Functions[0].Parameters[0].Type = "host.io.bad"
	ds = securityVerify(t, map[string]string{}, external)
	wrongRole := declarationDiagnostic(t, ds, "PP602", "host.io.bad")
	if wrongRole.Span.Line != 16 || len(wrongRole.Notes) == 0 {
		t.Fatalf("wrong-role manifest type lost its exact location or explanation: %+v", wrongRole)
	}
	requireDeclaration(t, wrongRole, "host.toml", 10)
}

func TestRecordBaseDiagnosticContext(t *testing.T) {
	library := "from typing import NamedTuple\n\nclass Base(NamedTuple):\n    field: int\n"
	source := "from typing import NamedTuple\nfrom library import Base\n\nclass Derived(Base):\n    field: int\n"
	d := declarationDiagnostic(t, callDiagnostics(t, map[string]string{"app": source, "library": library}, nil), "PP202", "app.Derived")
	requireDiagnosticType(t, d, "base.1", model.Type{Kind: "record", Name: "library.Base"})
	callRelatedText(t, d, "app.py", source, "Base")
	requireDeclaration(t, d, "library.py", 3)
}
