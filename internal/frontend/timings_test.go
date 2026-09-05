package frontend

import (
	"reflect"
	"testing"
	"time"
)

func TestParseTimedPreservesResultsAndPartitionsWork(t *testing.T) {
	for _, test := range []struct {
		name   string
		source []byte
		lower  bool
	}{
		{"valid", []byte("def f(x: int) -> int:\n    return x + 1\n"), true},
		{"normalization", []byte("from purepy import value\n@value\nclass V:\n    __field: int\n"), true},
		{"lowering rejection", []byte("x = [1]\n"), true},
		{"syntax rejection", []byte("def f(:\n"), false},
		{"UTF-8 rejection", []byte{0xff}, false},
		{"NUL rejection", []byte("x = '\x00'\n"), false},
		{"encoding rejection", []byte("# coding: latin-1\n"), false},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			wantTree, wantDiagnostics := Parse("sample.py", test.source)
			start := time.Now()
			tree, diagnostics, measured := ParseTimed("sample.py", test.source)
			elapsed := time.Since(start)
			if !reflect.DeepEqual(tree, wantTree) || !reflect.DeepEqual(diagnostics, wantDiagnostics) {
				t.Fatalf("timing changed parser output: %+v %+v", tree, diagnostics)
			}
			if measured.Parse <= 0 || measured.Lower < 0 || measured.Parse+measured.Lower > elapsed {
				t.Fatalf("invalid or overlapping durations: %+v within %s", measured, elapsed)
			}
			if test.lower != (measured.Lower > 0) {
				t.Fatalf("lower timing does not reflect whether IR normalization ran: %+v", measured)
			}
		})
	}
}
