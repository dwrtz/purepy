// Package diag owns the versioned, deterministic diagnostic interface.
package diag

import (
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
	return Diagnostic{Code: code, Severity: "error", Message: message, Span: span, Notes: []string{}, Related: []model.Span{}}
}

func Sort(ds []Diagnostic) {
	sort.SliceStable(ds, func(i, j int) bool {
		a, b := ds[i], ds[j]
		if a.Span.File != b.Span.File {
			return a.Span.File < b.Span.File
		}
		if a.Span.Start != b.Span.Start {
			return a.Span.Start < b.Span.Start
		}
		if a.Code != b.Code {
			return a.Code < b.Code
		}
		return a.Message < b.Message
	})
}

func Text(w io.Writer, ds []Diagnostic) {
	for _, d := range ds {
		fmt.Fprintf(w, "%s:%d:%d %s %s\n", d.Span.File, d.Span.Line, d.Span.Column, d.Code, d.Message)
		for _, n := range d.Notes {
			fmt.Fprintf(w, "  %s\n", n)
		}
	}
}
