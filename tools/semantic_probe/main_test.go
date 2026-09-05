package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/dwrtz/purepy/internal/app"
	"github.com/dwrtz/purepy/internal/model"
)

func sourceCase(t *testing.T, name, source, expression string) probeCase {
	t.Helper()
	start := strings.LastIndex(source, expression)
	if start < 0 {
		t.Fatalf("expression %q is absent from source", expression)
	}
	return probeCase{name: name, source: source, start: start, end: start + len(expression)}
}

func encodeCases(t *testing.T, cases ...probeCase) []byte {
	t.Helper()
	raw := make([]inputCase, 0, len(cases))
	for _, c := range cases {
		raw = append(raw, inputCase{Name: &c.name, Source: &c.source, Start: &c.start, End: &c.end})
	}
	data, err := json.Marshal(request{Cases: &raw})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestProbeInfersExpressionTypesFromActualChecker(t *testing.T) {
	cases := []struct {
		name, source, expression, want string
	}{
		{"boolean", "def f() -> bool:\n    return True\n", "True", "bool"},
		{"integer", "def f() -> int:\n    return 1\n", "1", "int"},
		{"true_division", "def f(a: int, b: int) -> float:\n    return a / b\n", "a / b", "float"},
		{"intrinsic", "def f(a: str) -> int:\n    return len(a)\n", "len(a)", "int"},
		{"tuple", "def f() -> tuple[int, ...]:\n    return (1, 2)\n", "(1, 2)", "tuple[int, ...]"},
		{"unicode_byte_offsets", "def f() -> str:\n    return 'αβ'\n", "'αβ'", "str"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := probe(sourceCase(t, tc.name, tc.source, tc.expression))
			if !r.OK || len(r.Diagnostics) != 0 || !reflect.DeepEqual(r.InferredTypes, []string{tc.want}) {
				t.Fatalf("unexpected probe result: %+v", r)
			}
		})
	}
}

func TestProbeRejectsParseLinkConstantAndLocalErrors(t *testing.T) {
	cases := []struct {
		name, source, expression, code string
	}{
		{"parser", "def f() -> int:\n    return (1 + )\n", "1 +", "PP002"},
		{"link", "from missing import value\ndef f() -> int:\n    return 1\n", "1", "PP102"},
		{"constant", "from typing import Final\nVALUE: Final[int] = 1 + 2\ndef f() -> int:\n    return VALUE\n", "VALUE", "PP502"},
		{"local", "def f() -> int:\n    return True\n", "True", "PP205"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := probe(sourceCase(t, tc.name, tc.source, tc.expression))
			if r.OK {
				t.Fatal("accepted rejected source")
			}
			found := false
			for _, d := range r.Diagnostics {
				if d.Code == tc.code {
					found = true
				}
				if d.Span.File != "main.py" {
					t.Fatalf("diagnostic has unexpected source path: %+v", d)
				}
			}
			if !found {
				t.Fatalf("missing diagnostic %s: %+v", tc.code, r.Diagnostics)
			}
			if tc.name == "parser" && len(r.InferredTypes) != 0 {
				t.Fatal("parser recovery must not feed inference")
			}
			if tc.name == "local" && !reflect.DeepEqual(r.InferredTypes, []string{"bool"}) {
				t.Fatalf("inference must not substitute the return annotation: %+v", r)
			}
		})
	}
}

func TestExpressionTypesUsesExactExpressionRange(t *testing.T) {
	at := model.Span{File: "main.py", Start: 10, End: 20}
	facts := []model.Fact{
		{Span: at, Type: model.Int, Description: "expression"},
		{Span: at, Type: model.Bool, Description: "expression"},
		{Span: at, Type: model.Int, Description: "expression"},
		{Span: at, Type: model.Float, Description: "pure_sync function"},
		{Span: model.Span{File: "main.py", Start: 11, End: 20}, Type: model.Bytes, Description: "expression"},
		{Span: model.Span{File: "main.py", Start: 10, End: 21}, Type: model.None, Description: "expression"},
		{Span: model.Span{File: "other.py", Start: 10, End: 20}, Type: model.Str, Description: "expression"},
	}
	if got := expressionTypes(facts, 10, 20); !reflect.DeepEqual(got, []string{"bool", "int"}) {
		t.Fatalf("incorrect range selection or type deduplication: %v", got)
	}
	if got := expressionTypes(facts, 0, 1); got == nil || len(got) != 0 {
		t.Fatalf("missing expression must have an empty array: %v", got)
	}
}

func TestProtocolStableOrderAndEmptyArrays(t *testing.T) {
	cases := []probeCase{
		sourceCase(t, "z-last-alphabetically", "def f() -> bool:\n    return True\n", "True"),
		sourceCase(t, "a-first-alphabetically", "def f() -> int:\n    return 1\n", "1"),
		sourceCase(t, "no-expression", "def f() -> int:\n    return 1\n", "def"),
	}
	var out, errOut bytes.Buffer
	if code := run(bytes.NewReader(encodeCases(t, cases...)), &out, &errOut); code != 0 || errOut.Len() != 0 {
		t.Fatalf("run returned %d: %s", code, errOut.String())
	}
	var r response
	if err := json.Unmarshal(out.Bytes(), &r); err != nil {
		t.Fatal(err)
	}
	if r.Schema != 1 || r.VerifierVersion != app.Version || len(r.Results) != len(cases) {
		t.Fatalf("unexpected response metadata: %+v", r)
	}
	for i, result := range r.Results {
		if result.Name != cases[i].name || !result.OK || result.Diagnostics == nil || result.InferredTypes == nil {
			t.Fatalf("invalid response ordering or arrays: %+v", r)
		}
	}
	if len(r.Results[2].InferredTypes) != 0 {
		t.Fatal("non-expression selected a containing function's return type")
	}
	out.Reset()
	if code := run(strings.NewReader(`{"cases":[]}`), &out, &errOut); code != 0 || !strings.Contains(out.String(), `"results":[]`) {
		t.Fatalf("empty batch response: %s (%d)", out.String(), code)
	}
}

func TestMalformedRequestsProduceNoPartialResponse(t *testing.T) {
	valid := `{"name":"first","source":"x","start":0,"end":1}`
	cases := map[string]string{
		"empty":                 "",
		"null":                  `null`,
		"missing_cases":         `{}`,
		"null_cases":            `{"cases":null}`,
		"wrong_cases":           `{"cases":{}}`,
		"unknown_request_field": `{"cases":[],"extra":1}`,
		"trailing_value":        `{"cases":[]} {}`,
		"trailing_junk":         `{"cases":[]} x`,
		"missing_name":          `{"cases":[{"source":"x","start":0,"end":1}]}`,
		"missing_source":        `{"cases":[{"name":"x","start":0,"end":1}]}`,
		"missing_start":         `{"cases":[{"name":"x","source":"x","end":1}]}`,
		"missing_end":           `{"cases":[{"name":"x","source":"x","start":0}]}`,
		"null_case":             `{"cases":[null]}`,
		"unknown_case_field":    `{"cases":[{"name":"x","source":"x","start":0,"end":1,"extra":0}]}`,
		"fractional_offset":     `{"cases":[{"name":"x","source":"x","start":0.5,"end":1}]}`,
		"duplicate_name":        `{"cases":[` + valid + `,` + valid + `]}`,
		"invalid_utf8_json":     "{\"cases\":[],\"\xff\":1}",
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			if code := run(strings.NewReader(data), &out, &errOut); code != 2 || out.Len() != 0 || errOut.Len() == 0 {
				t.Fatalf("malformed request returned code=%d stdout=%q stderr=%q", code, out.String(), errOut.String())
			}
		})
	}
}

func TestCaseBounds(t *testing.T) {
	cases := map[string]probeCase{
		"empty_name":     {name: "  ", source: "x", start: 0, end: 1},
		"long_name":      {name: strings.Repeat("x", maxNameBytes+1), source: "x", start: 0, end: 1},
		"negative_start": {name: "x", source: "x", start: -1, end: 1},
		"empty_range":    {name: "x", source: "x", start: 0, end: 0},
		"reversed_range": {name: "x", source: "xxx", start: 2, end: 1},
		"end_outside":    {name: "x", source: "x", start: 0, end: 2},
		"start_outside":  {name: "x", source: "x", start: 2, end: 3},
		"mid_rune_start": {name: "x", source: "αβ", start: 1, end: 4},
		"mid_rune_end":   {name: "x", source: "αβ", start: 0, end: 3},
		"source_size":    {name: "x", source: strings.Repeat("x", maxSourceBytes+1), start: 0, end: 1},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			valid := probeCase{name: "valid", source: "x", start: 0, end: 1}
			var out, errOut bytes.Buffer
			if code := run(bytes.NewReader(encodeCases(t, valid, c)), &out, &errOut); code != 2 || out.Len() != 0 || errOut.Len() == 0 {
				t.Fatalf("invalid second case returned code=%d stdout=%q stderr=%q", code, out.String(), errOut.String())
			}
		})
	}
	t.Run("request_size", func(t *testing.T) {
		if _, err := readCases(strings.NewReader(strings.Repeat(" ", maxInputBytes+1))); err == nil || !strings.Contains(err.Error(), "exceeds") {
			t.Fatalf("oversized request accepted: %v", err)
		}
	})
	t.Run("case_count", func(t *testing.T) {
		data := `{"cases":[` + strings.Repeat(`{},`, maxCases) + `{}]}`
		if _, err := readCases(strings.NewReader(data)); err == nil || !strings.Contains(err.Error(), "exceeds") {
			t.Fatalf("oversized case array accepted: %v", err)
		}
	})
}

type failingIO struct{}

func (failingIO) Read([]byte) (int, error)  { return 0, errors.New("read failure") }
func (failingIO) Write([]byte) (int, error) { return 0, errors.New("write failure") }

func TestProtocolReportsIOErrors(t *testing.T) {
	var errOut bytes.Buffer
	if code := run(failingIO{}, io.Discard, &errOut); code != 2 || !strings.Contains(errOut.String(), "read failure") {
		t.Fatalf("read error: code=%d stderr=%s", code, errOut.String())
	}
	errOut.Reset()
	if code := run(strings.NewReader(`{"cases":[]}`), failingIO{}, &errOut); code != 2 || !strings.Contains(errOut.String(), "write failure") {
		t.Fatalf("write error: code=%d stderr=%s", code, errOut.String())
	}
}
