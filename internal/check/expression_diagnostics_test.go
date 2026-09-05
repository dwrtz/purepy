package check

import (
	"testing"

	"github.com/dwrtz/purepy/internal/model"
)

func TestExpressionDiagnosticTypesAndDeclarations(t *testing.T) {
	for _, tc := range []struct {
		name, parameter, expression, returns, code string
		types                                      map[string]model.Type
	}{
		{"binary", "value: int", "value + 'x'", "int", "PP209", map[string]model.Type{"left": model.Int, "right": model.Str}},
		{"unary", "value: str", "-value", "str", "PP209", map[string]model.Type{"operand": model.Str}},
		{"boolean", "value: int", "True and value", "bool", "PP205", map[string]model.Type{"expected": model.Bool, "actual": model.Int}},
		{"comparison", "value: str", "value == 1", "bool", "PP212", map[string]model.Type{"left": model.Str, "right": model.Int}},
		{"tuple", "value: int", "(value, 'x')", "tuple[int, ...]", "PP207", map[string]model.Type{"expected": model.Int, "actual": model.Str}},
		{"conditional", "value: int", "value if True else 'x'", "int", "PP205", map[string]model.Type{"left": model.Int, "right": model.Str}},
		{"index_receiver", "value: int", "value[0]", "int", "PP210", map[string]model.Type{"receiver": model.Int}},
		{"index", "value: bool", "'text'[value]", "str", "PP205", map[string]model.Type{"expected": model.Int, "actual": model.Bool}},
		{"slice", "value: str", "'text'[:value]", "str", "PP205", map[string]model.Type{"expected": model.Optional(model.Int), "actual": model.Str}},
		{"format", "value: bytes", "f'{value}'", "str", "PP211", map[string]model.Type{"actual": model.Bytes}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := "def run(" + tc.parameter + ") -> " + tc.returns + ":\n    return " + tc.expression + "\n"
			d := declarationDiagnostic(t, callDiagnostics(t, map[string]string{"app": source}, nil), tc.code, "app.run")
			callDiagnosticTypes(t, d, tc.types)
			callRelatedText(t, d, "app.py", source, tc.parameter)
		})
	}

	source := "from purepy import value\n@value\nclass Record:\n    count: int\ndef run(record: Record) -> int:\n    return record.missing\n"
	d := declarationDiagnostic(t, callDiagnostics(t, map[string]string{"app": source}, nil), "PP208", "app.Record.missing")
	requireDiagnosticType(t, d, "receiver", model.Type{Kind: "record", Name: "app.Record"})
	callRelatedText(t, d, "app.py", source, "record: Record")
	requireDeclaration(t, d, "app.py", 2)
}

func TestLocalDiagnosticDeclarationPaths(t *testing.T) {
	t.Run("reannotation", func(t *testing.T) {
		source := "def run() -> None:\n    local: int = 1\n    local: str = 'x'\n"
		d := declarationDiagnostic(t, callDiagnostics(t, map[string]string{"app": source}, nil), "PP205", "app.run.local")
		callDiagnosticTypes(t, d, map[string]model.Type{"expected": model.Int, "actual": model.Str})
		callRelatedText(t, d, "app.py", source, "local: int = 1")
	})
	t.Run("assignment_origins", func(t *testing.T) {
		source := "def run(value: str) -> None:\n    local: int = 1\n    local = value\n"
		d := declarationDiagnostic(t, callDiagnostics(t, map[string]string{"app": source}, nil), "PP205", "app.run.local")
		callDiagnosticTypes(t, d, map[string]model.Type{"expected": model.Int, "actual": model.Str})
		callRelatedText(t, d, "app.py", source, "local: int = 1")
		callRelatedText(t, d, "app.py", source, "value: str")
	})
	t.Run("return_annotation_and_operand", func(t *testing.T) {
		source := "def run(value: str) -> int:\n    return value\n"
		d := declarationDiagnostic(t, callDiagnostics(t, map[string]string{"app": source}, nil), "PP205", "app.run")
		callDiagnosticTypes(t, d, map[string]model.Type{"expected": model.Int, "actual": model.Str})
		callRelatedText(t, d, "app.py", source, "int")
		callRelatedText(t, d, "app.py", source, "value: str")
	})
	t.Run("branch_origins", func(t *testing.T) {
		source := "def run(flag: bool) -> None:\n    if flag:\n        local = 1\n    else:\n        local = 'x'\n"
		d := declarationDiagnostic(t, callDiagnostics(t, map[string]string{"app": source}, nil), "PP205", "app.run.local")
		callDiagnosticTypes(t, d, map[string]model.Type{"left": model.Int, "right": model.Str})
		if len(d.Related) != 2 || d.Related[0].Line != 3 || d.Related[1].Line != 5 {
			t.Fatalf("branch conflict must preserve the two actual type origins in order: %+v", d)
		}
	})
	t.Run("later_declaration", func(t *testing.T) {
		source := "def run() -> int:\n    return local\n    local = 1\n"
		d := declarationDiagnostic(t, callDiagnostics(t, map[string]string{"app": source}, nil), "PP206", "app.run.local")
		callRelatedText(t, d, "app.py", source, "local = 1")
		if len(d.Types) != 0 {
			t.Fatalf("unknown local type must not be invented: %+v", d)
		}
	})
	t.Run("fallthrough", func(t *testing.T) {
		source := "def run() -> int:\n    pass\n"
		d := declarationDiagnostic(t, callDiagnostics(t, map[string]string{"app": source}, nil), "PP213", "app.run")
		callDiagnosticTypes(t, d, map[string]model.Type{"expected": model.Int, "actual": model.None})
		callRelatedText(t, d, "app.py", source, "int")
	})
	t.Run("authority_read", func(t *testing.T) {
		source := "from host.io import Read\ndef run(cap: Read) -> None:\n    local = cap\n"
		d := declarationDiagnostic(t, callDiagnostics(t, map[string]string{"app": source}, securityHost()), "PP313", "app.run.cap")
		capType := model.Type{Kind: "capability", Name: "host.io.Read"}
		callDiagnosticTypes(t, d, map[string]model.Type{"actual": capType, "declared": capType})
		callRelatedText(t, d, "app.py", source, "cap: Read")
	})
}

func TestImportedValueDiagnosticPaths(t *testing.T) {
	library := "def operation() -> None:\n    return\n"
	for _, tc := range []struct{ name, source, code, symbol string }{
		{"read", "def run() -> None:\n    local = operation\n", "PP301", "library.operation"},
		{"rebind", "def run() -> None:\n    operation = 1\n", "PP503", "library.operation"},
		{"parameter_shadow", "def run(operation: int) -> None:\n    return\n", "PP503", "app.run.operation"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := "from library import operation\n" + tc.source
			d := declarationDiagnostic(t, callDiagnostics(t, map[string]string{"app": source, "library": library}, nil), tc.code, tc.symbol)
			callRelatedText(t, d, "app.py", source, "operation")
			requireDeclaration(t, d, "library.py", 1)
		})
	}
}

func TestExpressionDiagnosticFieldAndCallOrigins(t *testing.T) {
	source := "from purepy import value\n@value\nclass Inner:\n    flag: str\n@value\nclass Outer:\n    inner: Inner\ndef make() -> Outer:\n    return Outer(Inner('yes'))\ndef run() -> None:\n    if make().inner.flag:\n        pass\n"
	d := declarationDiagnostic(t, callDiagnostics(t, map[string]string{"app": source}, nil), "PP205", "app.run")
	callDiagnosticTypes(t, d, map[string]model.Type{"expected": model.Bool, "actual": model.Str})
	callRelatedText(t, d, "app.py", source, "flag: str")
	callRelatedText(t, d, "app.py", source, "inner: Inner")
	callRelatedText(t, d, "app.py", source, "Outer")
	requireDeclaration(t, d, "app.py", 8)
}

func TestRejectedStatementDiagnosticOrigins(t *testing.T) {
	for _, tc := range []struct{ name, expression, code string }{
		{"mutation", "value.field = 1", "PP503"},
		{"unused_expression", "value + 1", "PP003"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := "def run(value: int) -> None:\n    " + tc.expression + "\n"
			d := declarationDiagnostic(t, callDiagnostics(t, map[string]string{"app": source}, nil), tc.code, "app.run")
			requireDiagnosticType(t, d, "actual", model.Int)
			callRelatedText(t, d, "app.py", source, "value: int")
		})
	}
}
