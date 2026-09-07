// Package diag owns the versioned, deterministic diagnostic interface.
package diag

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/dwrtz/purepy/internal/model"
)

type Diagnostic struct {
	Code     string                `json:"code"`
	Severity string                `json:"severity"`
	Message  string                `json:"message"`
	Span     model.Span            `json:"span"`
	Symbol   string                `json:"symbol,omitempty"`
	Notes    []string              `json:"notes"`
	Related  []model.Span          `json:"related_locations"`
	Types    map[string]model.Type `json:"types,omitempty"`
}

// Context methods are nil-safe so a successful type expectation can be
// annotated without manufacturing a diagnostic. Related locations retain causal
// order; duplicates and locations without a known source file are omitted.
func (d *Diagnostic) WithSymbol(symbol string) *Diagnostic {
	if d != nil && symbol != "" {
		d.Symbol = symbol
	}
	return d
}

func (d *Diagnostic) WithType(role string, typ model.Type) *Diagnostic {
	if d != nil && role != "" && typ.Kind != "" && typ.Kind != "invalid" && typ.WithinLimit() {
		if d.Types == nil {
			d.Types = map[string]model.Type{}
		}
		d.Types[role] = typ
	}
	return d
}

func (d *Diagnostic) WithRelated(locations ...model.Span) *Diagnostic {
	if d == nil {
		return d
	}
	for _, at := range locations {
		if at.File == "" {
			continue
		}
		at = New("", "", at).Span
		if at == d.Span {
			continue
		}
		duplicate := false
		for _, previous := range d.Related {
			if previous == at {
				duplicate = true
				break
			}
		}
		if !duplicate {
			d.Related = append(d.Related, at)
		}
	}
	return d
}

func (d *Diagnostic) WithNote(note string) *Diagnostic {
	if d != nil && note != "" {
		for _, previous := range d.Notes {
			if previous == note {
				return d
			}
		}
		d.Notes = append(d.Notes, note)
	}
	return d
}

func New(code, message string, span model.Span) Diagnostic {
	if span.Line > 0 && span.EndLine == 0 {
		span.EndLine = span.Line
		span.EndColumn = span.Column
	}
	return Diagnostic{Code: code, Severity: "error", Message: message, Span: span, Notes: []string{}, Related: []model.Span{}}
}

func Sort(ds []Diagnostic) {
	SortModules(ds, nil)
}

// SortModules orders project diagnostics by module identity, including package
// initializers before their children. Non-project locations use their file path.
func SortModules(ds []Diagnostic, modules map[string]string) {
	sort.SliceStable(ds, func(i, j int) bool {
		a, b := ds[i], ds[j]
		moduleA, moduleB := modules[a.Span.File], modules[b.Span.File]
		if moduleA == "" {
			moduleA = a.Span.File
		}
		if moduleB == "" {
			moduleB = b.Span.File
		}
		if moduleA != moduleB {
			return moduleA < moduleB
		}
		if a.Span.File != b.Span.File {
			return a.Span.File < b.Span.File
		}
		if a.Span.Start != b.Span.Start {
			return a.Span.Start < b.Span.Start
		}
		if a.Code != b.Code {
			return a.Code < b.Code
		}
		if a.Message != b.Message {
			return a.Message < b.Message
		}
		// Equal locations/codes/messages can still have different symbols,
		// notes, or declaration paths. Compare the complete immutable report
		// to avoid inheriting worker arrival or map iteration order.
		x, _ := json.Marshal(a)
		y, _ := json.Marshal(b)
		return bytes.Compare(x, y) < 0
	})
}

func Text(w io.Writer, ds []Diagnostic) {
	for _, d := range ds {
		fmt.Fprintf(w, "%s:%d:%d %s %s\n", d.Span.File, d.Span.Line, d.Span.Column, d.Code, d.Message)
		if d.Symbol != "" {
			fmt.Fprintf(w, "  symbol: %s\n", d.Symbol)
		}
		roles := make([]string, 0, len(d.Types))
		for role := range d.Types {
			roles = append(roles, role)
		}
		sort.Strings(roles)
		for _, role := range roles {
			fmt.Fprintf(w, "  %s type: %s\n", strings.ReplaceAll(role, "_", " "), d.Types[role])
		}
		for _, n := range d.Notes {
			fmt.Fprintf(w, "  %s\n", n)
		}
		for _, at := range d.Related {
			fmt.Fprintf(w, "  declaration at %s:%d:%d\n", at.File, at.Line, at.Column)
		}
	}
}
