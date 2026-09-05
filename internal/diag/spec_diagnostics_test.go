package diag

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/dwrtz/purepy/internal/model"
)

func TestDiagnosticSortHasCompleteDeterministicTieBreaker(t *testing.T) {
	base := New("PP312", "requires capability", model.Span{File: "app.py", Start: 20, End: 25, Line: 2, Column: 5, EndLine: 2, EndColumn: 10})
	a, b, c := base, base, base
	a.Symbol = "app.a"
	b.Symbol = "app.b"
	c.Symbol = "app.b"
	b.Related = []model.Span{{File: "b.py", Line: 1, Column: 1, EndLine: 1, EndColumn: 2}}
	c.Related = []model.Span{{File: "c.py", Line: 1, Column: 1, EndLine: 1, EndColumn: 2}}
	var want []byte
	for _, ds := range [][]Diagnostic{{a, b, c}, {c, b, a}, {b, a, c}} {
		Sort(ds)
		got, err := json.Marshal(ds)
		if err != nil {
			t.Fatal(err)
		}
		if want == nil {
			want = got
		} else if !bytes.Equal(got, want) {
			t.Fatalf("tie depends on incoming order:\n%s\n%s", want, got)
		}
	}
}

func TestDiagnosticTextIncludesDeclarationPath(t *testing.T) {
	d := New("PP312", "requires host.ops.Read", model.Span{File: "app.py", Line: 3, Column: 12, EndLine: 3, EndColumn: 22})
	d.Related = []model.Span{{File: "host.toml", Line: 8, Column: 1, EndLine: 8, EndColumn: 20}}
	d.Notes = []string{"Pass the original capability parameter."}
	var out bytes.Buffer
	Text(&out, []Diagnostic{d})
	want := "app.py:3:12 PP312 requires host.ops.Read\n  Pass the original capability parameter.\n  declaration at host.toml:8:1\n"
	if out.String() != want {
		t.Fatalf("missing deterministic declaration path: %q", out.String())
	}
}

func TestDiagnosticZeroWidthFallbackHasCompleteCoordinates(t *testing.T) {
	d := New("PP001", "configuration not readable", model.Span{File: "purepy.toml", Line: 1, Column: 1})
	if d.Span.EndLine != 1 || d.Span.EndColumn != 1 || d.Span.Start != d.Span.End || d.Severity != "error" || d.Notes == nil || d.Related == nil {
		t.Fatalf("invalid zero-width fallback: %+v", d)
	}
}
