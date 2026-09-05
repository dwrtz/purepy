// semantic_probe is a development-only adapter for generated differential tests.
// It checks source in memory and reports expression types without running Python.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/dwrtz/purepy/internal/app"
	"github.com/dwrtz/purepy/internal/check"
	"github.com/dwrtz/purepy/internal/diag"
	"github.com/dwrtz/purepy/internal/frontend"
	"github.com/dwrtz/purepy/internal/manifest"
	"github.com/dwrtz/purepy/internal/model"
)

const (
	maxInputBytes  = 16 << 20
	maxSourceBytes = 64 << 10
	maxCases       = 20000
	maxNameBytes   = 256
)

type inputCase struct {
	Name   *string `json:"name"`
	Source *string `json:"source"`
	Start  *int    `json:"start"`
	End    *int    `json:"end"`
}

type request struct {
	Cases *[]inputCase `json:"cases"`
}

type probeCase struct {
	name, source string
	start, end   int
}

type caseResult struct {
	Name          string            `json:"name"`
	OK            bool              `json:"ok"`
	InferredTypes []string          `json:"inferred_types"`
	Diagnostics   []diag.Diagnostic `json:"diagnostics"`
}

type response struct {
	Schema          int          `json:"schema"`
	VerifierVersion string       `json:"verifier_version"`
	Results         []caseResult `json:"results"`
}

func main() {
	os.Exit(run(os.Stdin, os.Stdout, os.Stderr))
}

func run(in io.Reader, out, errOut io.Writer) int {
	cases, err := readCases(in)
	if err != nil {
		fmt.Fprintf(errOut, "semantic_probe: %v\n", err)
		return 2
	}
	r := response{Schema: 1, VerifierVersion: app.Version, Results: make([]caseResult, 0, len(cases))}
	for _, c := range cases {
		r.Results = append(r.Results, probe(c))
	}
	if err := json.NewEncoder(out).Encode(r); err != nil {
		fmt.Fprintf(errOut, "semantic_probe: write response: %v\n", err)
		return 2
	}
	return 0
}

// Validate the entire batch before checking it, so malformed requests cannot
// produce partial results that a caller might mistake for a completed matrix.
func readCases(in io.Reader) ([]probeCase, error) {
	data, err := io.ReadAll(io.LimitReader(in, maxInputBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read request: %w", err)
	}
	if len(data) > maxInputBytes {
		return nil, fmt.Errorf("request exceeds %d bytes", maxInputBytes)
	}
	if !utf8.Valid(data) {
		return nil, fmt.Errorf("request must be valid UTF-8 JSON")
	}
	var req request
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(&req); err != nil {
		return nil, fmt.Errorf("decode request: %w", err)
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return nil, fmt.Errorf("request must contain exactly one JSON object")
	}
	if req.Cases == nil {
		return nil, fmt.Errorf("cases must be an array")
	}
	if len(*req.Cases) > maxCases {
		return nil, fmt.Errorf("request exceeds %d cases", maxCases)
	}
	cases := make([]probeCase, 0, len(*req.Cases))
	seen := make(map[string]bool, len(*req.Cases))
	for i, raw := range *req.Cases {
		if raw.Name == nil || raw.Source == nil || raw.Start == nil || raw.End == nil {
			return nil, fmt.Errorf("case %d requires name, source, start and end", i)
		}
		c := probeCase{name: *raw.Name, source: *raw.Source, start: *raw.Start, end: *raw.End}
		if strings.TrimSpace(c.name) == "" || len(c.name) > maxNameBytes {
			return nil, fmt.Errorf("case %d requires a nonempty name of at most %d bytes", i, maxNameBytes)
		}
		if seen[c.name] {
			return nil, fmt.Errorf("duplicate case name %q", c.name)
		}
		seen[c.name] = true
		if len(c.source) > maxSourceBytes {
			return nil, fmt.Errorf("case %q source exceeds %d bytes", c.name, maxSourceBytes)
		}
		if c.start < 0 || c.end <= c.start || c.end > len(c.source) {
			return nil, fmt.Errorf("case %q requires 0 <= start < end <= source byte length", c.name)
		}
		if !utf8.RuneStart(c.source[c.start]) || c.end < len(c.source) && !utf8.RuneStart(c.source[c.end]) {
			return nil, fmt.Errorf("case %q offsets must fall on UTF-8 boundaries", c.name)
		}
		cases = append(cases, c)
	}
	return cases, nil
}

func probe(c probeCase) caseResult {
	r := caseResult{Name: c.name, InferredTypes: []string{}, Diagnostics: []diag.Diagnostic{}}
	tree, diagnostics := frontend.Parse("main.py", []byte(c.source))
	r.Diagnostics = append(r.Diagnostics, diagnostics...)
	if len(diagnostics) == 0 {
		p := check.Link([]*check.Module{{Name: "main", Path: "main.py", Tree: tree}}, &manifest.Set{}, nil)
		checked := p.CheckFunctions(1)
		r.Diagnostics = append(r.Diagnostics, checked.Diagnostics...)
		r.InferredTypes = expressionTypes(checked.Facts, c.start, c.end)
	}
	diag.Sort(r.Diagnostics)
	r.OK = len(r.Diagnostics) == 0
	return r
}

func expressionTypes(facts []model.Fact, start, end int) []string {
	types := []string{}
	seen := map[string]bool{}
	for _, fact := range facts {
		if fact.Span.File != "main.py" || fact.Span.Start != start || fact.Span.End != end || fact.Description != "expression" {
			continue
		}
		name := fact.Type.String()
		if !seen[name] {
			types = append(types, name)
			seen[name] = true
		}
	}
	sort.Strings(types)
	return types
}
