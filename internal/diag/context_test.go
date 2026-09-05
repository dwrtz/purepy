package diag

import (
	"bytes"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/dwrtz/purepy/internal/model"
)

func TestDiagnosticContextBuilders(t *testing.T) {
	primary := model.Span{File: "app.py", Start: 40, End: 44, Line: 3, Column: 9, EndLine: 3, EndColumn: 13}
	imported := model.Span{File: "app.py", Start: 20, End: 27, Line: 1, Column: 21, EndLine: 1, EndColumn: 28}
	declaration := model.Span{File: "library.py", Start: 12, End: 22, Line: 1, Column: 13, EndLine: 1, EndColumn: 23}
	fallback := model.Span{File: "missing.toml", Line: 1, Column: 1}
	d := New("PP205", "expected int, got bool", primary)
	if got := d.WithSymbol("library.consume.value").WithSymbol("").
		WithType("expected", model.Int).WithType("actual", model.Bool).
		WithType("", model.Float).WithType("unknown", model.Invalid).WithType("unset", model.Type{}).
		WithRelated(imported, declaration, imported, primary, model.Span{}, fallback).
		WithRelated(New("", "", fallback).Span).
		WithNote("Pass an exact int.").WithNote("").WithNote("Pass an exact int."); got != &d {
		t.Fatal("context builders should retain the diagnostic pointer")
	}
	if d.Symbol != "library.consume.value" || !reflect.DeepEqual(d.Types, map[string]model.Type{"expected": model.Int, "actual": model.Bool}) {
		t.Fatalf("unknown or empty context replaced known information: %+v", d)
	}
	wantRelated := []model.Span{imported, declaration, New("", "", fallback).Span}
	if !reflect.DeepEqual(d.Related, wantRelated) || !reflect.DeepEqual(d.Notes, []string{"Pass an exact int."}) {
		t.Fatalf("context path must preserve causal order and suppress duplicate/empty entries: %+v", d)
	}
	var absent *Diagnostic
	if got := absent.WithSymbol("no diagnostic").WithType("actual", model.Int).WithRelated(primary).WithNote("no diagnostic"); got != nil {
		t.Fatal("annotating a successful expectation must not manufacture an error")
	}
}

func TestDiagnosticTextSortsStructuredTypeContext(t *testing.T) {
	d := New("PP205", "expected int, got bool", model.Span{File: "app.py", Line: 3, Column: 9})
	d.WithSymbol("library.consume.value").
		WithType("return", model.Tuple(model.Int)).
		WithType("expected", model.Optional(model.Int)).
		WithType("actual", model.Bool).
		WithType("parameter.value", model.Int).
		WithNote("Parameter value is declared in library.py.").
		WithRelated(model.Span{File: "library.py", Line: 1, Column: 13})
	var out bytes.Buffer
	Text(&out, []Diagnostic{d})
	want := "app.py:3:9 PP205 expected int, got bool\n" +
		"  symbol: library.consume.value\n" +
		"  actual type: bool\n" +
		"  expected type: int | None\n" +
		"  parameter.value type: int\n" +
		"  return type: tuple[int, ...]\n" +
		"  Parameter value is declared in library.py.\n" +
		"  declaration at library.py:1:13\n"
	if out.String() != want {
		t.Fatalf("unstable or incomplete text context:\nwant: %s\ngot: %s", want, out.String())
	}
}

func TestDiagnosticSortIncludesTypesNotesAndFullSpan(t *testing.T) {
	base := func() Diagnostic {
		return New("PP205", "incompatible type", model.Span{File: "app.py", Start: 10, End: 11, Line: 2, Column: 1, EndLine: 2, EndColumn: 2})
	}
	intArgument, boolArgument := base(), base()
	intArgument.WithType("actual", model.Int).WithType("expected", model.Str)
	boolArgument.WithType("expected", model.Str).WithType("actual", model.Bool)
	withNote := base()
	withNote.WithType("actual", model.Int).WithType("expected", model.Str).WithNote("Earlier binding has another type.")
	withRelated := base()
	withRelated.WithType("actual", model.Int).WithType("expected", model.Str).WithRelated(model.Span{File: "library.py", Line: 2, Column: 7})
	withLongerSpan := base()
	withLongerSpan.Span.End = 12
	withLongerSpan.Span.EndColumn = 3
	withLongerSpan.WithType("actual", model.Int).WithType("expected", model.Str)
	withReversedMapInsertion := base()
	withReversedMapInsertion.WithType("expected", model.Str).WithType("actual", model.Int)
	input := []Diagnostic{intArgument, boolArgument, withNote, withRelated, withLongerSpan, withReversedMapInsertion}
	var expected []byte
	for offset := range input {
		for _, reverse := range []bool{false, true} {
			var ds []Diagnostic
			for i := range input {
				index := (i + offset) % len(input)
				if reverse {
					index = len(input) - index - 1
				}
				ds = append(ds, input[index])
			}
			Sort(ds)
			actual, err := json.Marshal(ds)
			if err != nil {
				t.Fatal(err)
			}
			if expected == nil {
				expected = actual
			} else if !bytes.Equal(actual, expected) {
				t.Fatalf("complete diagnostic ordering changed with incoming order:\n%s\n%s", expected, actual)
			}
		}
	}
}

// Check the published diagnostic projection against the real Go wire shape.
// This is a compatibility test for this repository's schema, not a general JSON
// Schema validator; the field's arbitrary role names reuse the existing type
// definition instead of introducing a separate diagnostic type encoding.
func TestDiagnosticContextSchemaCompatibility(t *testing.T) {
	data, err := os.ReadFile("../../docs/schema/diagnostics-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Definitions map[string]struct {
			Required   []string                   `json:"required"`
			Properties map[string]json.RawMessage `json:"properties"`
		} `json:"$defs"`
	}
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatal(err)
	}
	definition := schema.Definitions["diagnostic"]
	var typesProperty struct {
		Type                 string `json:"type"`
		AdditionalProperties struct {
			Ref string `json:"$ref"`
		} `json:"additionalProperties"`
	}
	if err := json.Unmarshal(definition.Properties["types"], &typesProperty); err != nil {
		t.Fatal(err)
	}
	if typesProperty.Type != "object" || typesProperty.AdditionalProperties.Ref != "#/$defs/type" {
		t.Fatalf("type roles must reference the existing structured type definition: %+v", typesProperty)
	}
	for _, field := range definition.Required {
		if field == "types" {
			t.Fatal("type context must remain optional for schema-1 diagnostics without known types")
		}
	}
	plain := New("PP002", "invalid syntax", model.Span{File: "app.py", Line: 1, Column: 1})
	contextual := New("PP205", "type mismatch", model.Span{File: "app.py", Line: 1, Column: 1})
	contextual.WithSymbol("app.run.value").WithType("expected", model.Optional(model.Tuple(model.Int))).
		WithType("actual", model.Type{Kind: "record", Name: "library.Record"})
	typeDefinition := schema.Definitions["type"]
	var kinds struct {
		Enum []string `json:"enum"`
	}
	if err := json.Unmarshal(typeDefinition.Properties["kind"], &kinds); err != nil {
		t.Fatal(err)
	}
	var checkTypeProjection func(model.Type)
	checkTypeProjection = func(typ model.Type) {
		t.Helper()
		data, err := json.Marshal(typ)
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(data, &fields); err != nil {
			t.Fatal(err)
		}
		for field := range fields {
			if _, declared := typeDefinition.Properties[field]; !declared {
				t.Errorf("diagnostic type property %q is absent from the shared type schema", field)
			}
		}
		for _, field := range typeDefinition.Required {
			if _, present := fields[field]; !present {
				t.Errorf("diagnostic type is missing required property %q", field)
			}
		}
		knownKind := false
		for _, kind := range kinds.Enum {
			knownKind = knownKind || kind == typ.Kind
		}
		if !knownKind {
			t.Errorf("diagnostic type kind %q is absent from the shared type schema", typ.Kind)
		}
		if typ.Elem != nil {
			checkTypeProjection(*typ.Elem)
		}
	}
	for _, diagnostic := range []Diagnostic{plain, contextual} {
		data, err := json.Marshal(diagnostic)
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(data, &fields); err != nil {
			t.Fatal(err)
		}
		for field := range fields {
			if _, declared := definition.Properties[field]; !declared {
				t.Errorf("serialized diagnostic property %q is absent from the strict schema", field)
			}
		}
		for _, field := range definition.Required {
			if _, present := fields[field]; !present {
				t.Errorf("serialized diagnostic is missing required property %q", field)
			}
		}
		_, hasTypes := fields["types"]
		if hasTypes != (len(diagnostic.Types) > 0) {
			t.Errorf("empty type context must be omitted: %s", data)
		}
		var decoded Diagnostic
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&decoded); err != nil || !reflect.DeepEqual(diagnostic, decoded) {
			t.Errorf("schema-1 diagnostic failed typed round trip: %s: %+v (%v)", data, decoded, err)
		}
		for _, typ := range diagnostic.Types {
			checkTypeProjection(typ)
		}
	}

	// An older report without the optional field remains readable and does not
	// acquire an empty object when serialized by the current verifier.
	legacy := `{"code":"PP002","severity":"error","message":"invalid syntax","span":{"file":"app.py","start":0,"end":0,"line":1,"column":1,"end_line":1,"end_column":1},"notes":[],"related_locations":[]}`
	var decoded Diagnostic
	if err := json.Unmarshal([]byte(legacy), &decoded); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(decoded)
	if err != nil || strings.Contains(string(encoded), `"types"`) || decoded.Types != nil {
		t.Fatalf("legacy diagnostics gained synthetic context: %s (%v)", encoded, err)
	}
}
