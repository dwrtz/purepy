package frontend

import (
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/dwrtz/purepy/internal/model"
)

func parseGood(t *testing.T, source string) *model.Node {
	t.Helper()
	n, ds := Parse("source.py", []byte(source))
	if len(ds) != 0 {
		t.Fatalf("parse diagnostics: %+v\nsource:\n%s", ds, source)
	}
	if n == nil || n.Kind != "Module" {
		t.Fatalf("invalid module: %+v", n)
	}
	return n
}

func TestNormalizedDeclarationsAndAnnotations(t *testing.T) {
	m := parseGood(t, `from purepy import value
from typing import Final
MAX: Final[int] = 10
@value
class Item:
    key: int
    name: str
async def load(x: tuple[int, ...], key: int | None) -> Item:
    return await fetch(x, key=key)
`)
	b := m.Items("body")
	if len(b) != 5 {
		t.Fatalf("body: %+v", b)
	}
	if b[0].A("module") != "purepy" || b[0].Items("names")[0].A("name") != "value" {
		t.Fatal("import normalization")
	}
	if b[2].Get("annotation").Kind != "Index" || b[2].Get("target").A("name") != "MAX" {
		t.Fatal("constant normalization")
	}
	if b[3].Kind != "Record" || b[3].Items("decorators")[0].A("name") != "value" || len(b[3].Items("body")) != 2 {
		t.Fatal("record normalization")
	}
	f := b[4]
	if f.Kind != "Function" || f.A("async") != "true" || f.A("name") != "load" || f.Get("returns").A("name") != "Item" {
		t.Fatalf("function: %+v", f)
	}
	params := f.Items("params")
	if len(params) != 2 || params[0].A("name") != "x" || params[0].Get("annotation").Get("index").Items("elements")[1].A("type") != "ellipsis" {
		t.Fatal("tuple annotation")
	}
	if params[1].Get("annotation").A("op") != "|" {
		t.Fatal("optional annotation")
	}
	await := f.Items("body")[0].Get("value")
	if await.Kind != "AwaitCall" || await.Get("call").Get("target").A("name") != "fetch" {
		t.Fatal("await normalization")
	}
	if await.Get("call").Items("args")[1].A("name") != "key" {
		t.Fatal("keyword argument")
	}
}

func TestExpressionStatementsAndDocstrings(t *testing.T) {
	m := parseGood(t, "\"\"\"Module documentation.\"\"\"\nasync def f() -> None:\n    \"\"\"Function documentation.\"\"\"\n    await sleep()\n    work()\n")
	if n := m.Items("body")[0]; n.Kind != "ExprStmt" || n.Get("value").Kind != "Literal" {
		t.Fatalf("module docstring: %+v", n)
	}
	for _, n := range m.Items("body")[1].Items("body") {
		if n.Kind != "ExprStmt" {
			t.Fatalf("expression statement: %+v", n)
		}
	}
}

func TestPrivateRecordNamesMatchPythonMangling(t *testing.T) {
	m := parseGood(t, "from purepy import value\n@value\nclass __Record:\n    __field: int\ndef f(x: __Record) -> int:\n    return x._Record__field\n")
	field := m.Items("body")[1].Items("body")[0].Get("target")
	if field.A("name") != "_Record__field" || field.Text != "__field" {
		t.Fatalf("private field normalization: %+v", field)
	}
}

func TestPythonLexicalWhitespaceAndEncoding(t *testing.T) {
	for _, source := range []string{"x =\u200b 1\n", "x =\u2060 1\n", "x =\ufeff 1\n", "x =\u00a0 1\n", "x =\v 1\n", "\u200b", "# coding: latin-1\nx = 'é'\n", "\ufeff    x = 1\n"} {
		if _, ds := Parse("bad.py", []byte(source)); len(ds) == 0 {
			t.Errorf("accepted non-Python whitespace or unsupported encoding: %q", source)
		}
	}
	for _, source := range []string{"# coding: utf-8\nx = 'é'\n", "x = '\u200b\u2060\ufeff\u00a0'\n", "# \u200b comment\nx = 1\n", "\ufeffx = 1\n"} {
		parseGood(t, source)
	}
}

func TestControlFlowAndExpressions(t *testing.T) {
	m := parseGood(t, `def f(x: int, data: bytes) -> int:
    y: int = -x + 2 * 3
    if x is None:
        return 0
    elif x > 1 and not False:
        y = x if True else 0
    elif 0 <= x < 10:
        pass
    else:
        y = 2
    while True:
        for z in (1, 2):
            if z == x:
                break
            continue
        break
    sliced = data[:2]
    element = data[0]
    return y
`)
	body := m.Items("body")[0].Items("body")
	if body[0].Get("value").A("op") != "+" || body[0].Get("value").Get("left").Kind != "Unary" {
		t.Fatal("arithmetic normalization")
	}
	cond := body[1]
	if cond.Kind != "If" || cond.Items("else")[0].Items("else")[0].Get("test").A("ops") != "<=|<" {
		t.Fatal("elif chain or comparison normalization")
	}
	if body[2].Kind != "While" || body[2].Items("body")[0].Kind != "For" {
		t.Fatal("loop normalization")
	}
	if body[3].Get("value").Kind != "Slice" || body[3].Get("value").Get("start") != nil || body[3].Get("value").Get("stop").Text != "2" {
		t.Fatal("slice normalization")
	}
	if body[4].Get("value").Kind != "Index" {
		t.Fatal("index normalization")
	}
}

func TestStringFormatting(t *testing.T) {
	m := parseGood(t, `def f(x: int, y: str) -> str:
    return f"answer {x:04d}: {y!s}" "!"
`)
	f := m.Items("body")[0].Items("body")[0].Get("value")
	if f.Kind != "FString" {
		t.Fatalf("format: %+v", f)
	}
	var formats []*model.Node
	for _, p := range f.Items("parts") {
		if p.Kind == "Format" {
			formats = append(formats, p)
		}
	}
	if len(formats) != 2 || formats[0].A("format") != "04d" || formats[1].A("conversion") != "s" {
		t.Fatalf("formats: %+v", formats)
	}
}

func TestSpansUnicodeAndNFKC(t *testing.T) {
	source := "def ｆ(é: int) -> int:\n    return é + 𝒙\n"
	m := parseGood(t, source)
	f := m.Items("body")[0]
	if f.A("name") != "f" || f.Items("params")[0].A("name") != "é" {
		t.Fatal("NFKC names")
	}
	right := f.Items("body")[0].Get("value").Get("right")
	if right.A("name") != "x" || right.Span.Column != 16 || right.Span.EndColumn != 17 || right.Span.End-right.Span.Start != 4 {
		t.Fatalf("Unicode span or normalization: %+v", right)
	}
	if source[right.Span.Start:right.Span.End] != right.Text || right.Span.Line != 2 || right.Span.File != "source.py" {
		t.Fatalf("incorrect source range: %+v", right.Span)
	}
}

func TestUnsupportedSyntaxIsNeverDropped(t *testing.T) {
	cases := []string{
		"import os\n", "from .app import f\n", "from app import *\n", "from app import f as g\n", "from __future__ import annotations\n",
		"def f(x: int = 1) -> int:\n    return x\n", "def f(x: int, /) -> int:\n    return x\n", "def f(*, x: int) -> int:\n    return x\n", "def f(*xs: int) -> int:\n    return 0\n", "def f(**xs: int) -> int:\n    return 0\n", "def f[T](x: T) -> T:\n    return x\n",
		"assert True\n", "raise ValueError()\n", "try:\n    pass\nexcept:\n    pass\n", "with x:\n    pass\n", "match x:\n    case 1:\n        pass\n", "global x\n", "nonlocal x\n", "del x\n", "type X = int\n",
		"x += 1\n", "x = y = 1\n", "x = [1]\n", "x = {1}\n", "x = {'a': 1}\n", "x = [a for a in b]\n", "x = (a for a in b)\n", "x = lambda: 1\n", "x = (a := 1)\n", "x = yield 1\n", "x = 1j\n",
		"for x in y:\n    pass\nelse:\n    pass\n", "while True:\n    pass\nelse:\n    pass\n", "async for x in y:\n    pass\n", "x = await pending\n", "f(*xs)\n", "f(**xs)\n", "x = a[::2]\n", "x = f'{a!r}'\n", "x = f'{a:{b}}'\n", "x = t'{a}'\n",
	}
	for _, source := range cases {
		t.Run(strings.Split(source, "\n")[0], func(t *testing.T) {
			_, ds := Parse("bad.py", []byte(source))
			if len(ds) == 0 {
				t.Fatalf("accepted unsupported syntax: %s", source)
			}
			for _, d := range ds {
				if d.Span.File != "bad.py" || d.Span.Line < 1 || d.Span.Column < 1 || d.Span.Start > d.Span.End {
					t.Fatalf("invalid diagnostic range: %+v", d)
				}
			}
		})
	}
}

func TestInvalidPythonRejected(t *testing.T) {
	cases := []string{"def f(:\n    return 1\n", "def f() -> None:\n", "def f() -> None:\n    # empty\n", "x = (1\n", "x = 012\n", "x = 1L\n", "x = 1__0\n", "x = 1_\n", "x = 1_.0\n", "x = '\\x0'\n", "x = '\\xGG'\n", "x = '\\u123'\n", "x = '\\U00110000'\n", "x = b'é'\n", "x = b'' ''\n", "x = 1 <> 2\n", "x = \x00\n", "x = '\xff'\n"}
	for _, source := range cases {
		t.Run(source, func(t *testing.T) {
			_, ds := Parse("bad.py", []byte(source))
			if len(ds) == 0 {
				t.Fatalf("accepted invalid Python: %q", source)
			}
		})
	}
}

func TestPrimitiveLiteralSpellings(t *testing.T) {
	for _, literal := range []string{"0", "0_0", "0x_FF", "0b_01", "0o_77", "12_345", "1.", ".5", "1.2e-3", "1_0e1_0", "None", "True", "False", "'é'", "b'\\xff'", "r'\\x'", "'\\U0001f600'", "'''multi\nline'''"} {
		t.Run(literal, func(t *testing.T) { parseGood(t, "x = "+literal+"\n") })
	}
}

func TestInvalidLayout(t *testing.T) {
	for _, source := range []string{
		"x = 1\n    y = 2\n",
		"def f() -> None:\n    pass\n  pass\n",
		"def f() -> None:\n\tpass\n        pass\n",
	} {
		_, ds := Parse("bad.py", []byte(source))
		if len(ds) == 0 {
			t.Errorf("accepted invalid indentation: %q", source)
		}
	}
}

func TestParallelParsingDeterministicAndDetached(t *testing.T) {
	source := []byte("def f(x: int) -> int:\n    return x + 1\n")
	expected, ds := Parse("app.py", source)
	if len(ds) > 0 {
		t.Fatal(ds)
	}
	want, _ := json.Marshal(expected)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			n, ds := Parse("app.py", source)
			got, _ := json.Marshal(n)
			if len(ds) > 0 || string(got) != string(want) {
				t.Errorf("non-deterministic result: %s %v", got, ds)
			}
		}()
	}
	wg.Wait()
	for i := range source {
		source[i] = 'x'
	}
	got, _ := json.Marshal(expected)
	if string(got) != string(want) {
		t.Fatal("IR retained source storage")
	}
}

func FuzzParseNeverPanics(f *testing.F) {
	for _, source := range []string{"", "# comment\n", "def f(x: int) -> int:\n    return x\n", "x = f'{a:{b}}'\n", "x = '\\u123'\n", "x = '\\N{LATIN CAPITAL LETTER A}'\n", "x = b'\\N{UNKNOWN}\\N{}\\N'\n", "x = b'\\N''\n", "def f(:\n", "\xff\x00"} {
		f.Add([]byte(source))
	}
	f.Fuzz(func(t *testing.T, source []byte) {
		if len(source) > 16384 {
			t.Skip()
		}
		n, ds := Parse("fuzz.py", source)
		if n == nil && len(ds) == 0 {
			t.Fatal("parse failed without a diagnostic")
		}
		for _, d := range ds {
			if d.Span.Start < 0 || d.Span.End < d.Span.Start || d.Span.End > len(source) {
				t.Fatalf("out-of-bounds diagnostic: %+v", d)
			}
		}
	})
}

func BenchmarkParse(b *testing.B) {
	source := []byte("from purepy import value\n@value\nclass Item:\n    key: int\n    name: str\nasync def f(x: int, data: bytes) -> int:\n    total = 0\n    for item in data:\n        if item > x:\n            total = total + item\n    return total\n")
	b.SetBytes(int64(len(source)))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, ds := Parse("bench.py", source)
		if len(ds) > 0 {
			b.Fatal(ds)
		}
	}
}

func TestSharedDifferentialSyntaxEdges(t *testing.T) {
	data, err := os.ReadFile("../../fixtures/conformance/syntax_edges.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name   string `json:"name"`
		Source string `json:"source"`
		Valid  bool   `json:"frontend_valid"`
	}
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			_, ds := Parse(c.Name+".py", []byte(c.Source))
			if (len(ds) == 0) != c.Valid {
				t.Fatalf("frontend valid=%t, expected=%t: %v", len(ds) == 0, c.Valid, ds)
			}
		})
	}
}
