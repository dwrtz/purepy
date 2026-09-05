// Package diag owns the versioned, deterministic diagnostic interface.
package diag

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"

	"github.com/dwrtz/purepy/internal/model"
)

type Diagnostic struct {
	Code     string       `json:"code"`
	Severity string       `json:"severity"`
	Message  string       `json:"message"`
	Span     model.Span   `json:"span"`
	Symbol   string       `json:"symbol,omitempty"`
	Notes    []string     `json:"notes"`
	Related  []model.Span `json:"related_locations"`
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
		for _, n := range d.Notes {
			fmt.Fprintf(w, "  %s\n", n)
		}
		for _, at := range d.Related {
			fmt.Fprintf(w, "  declaration at %s:%d:%d\n", at.File, at.Line, at.Column)
		}
	}
}
