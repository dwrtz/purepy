package check

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/dwrtz/purepy/internal/diag"
	"github.com/dwrtz/purepy/internal/frontend"
	"github.com/dwrtz/purepy/internal/manifest"
	"github.com/dwrtz/purepy/internal/model"
)

const (
	semanticFuzzMaxInput  = 256
	semanticFuzzMaxSource = 16 << 10
	checkerFuzzMaxSource  = 4 << 10
)

// FuzzCheckerSemantics constructs a well-typed program and a related program
// containing a specific forbidden operation. The expected verdict and rejection
// code come from the construction, never from the verifier under test. Both
// programs must reach semantic checking: parser rejection cannot satisfy the
// negative oracle. No generated Python is imported or executed.
func FuzzCheckerSemantics(f *testing.F) {
	for family := byte(0); family < 7; family++ {
		for primitive := byte(0); primitive < 5; primitive++ {
			for variant := byte(0); variant < 4; variant++ {
				f.Add([]byte{family, primitive, variant, 2, 3, 1, 4, 2, 5, 0})
			}
		}
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > semanticFuzzMaxInput {
			t.Skip()
		}
		pair := generateSemanticPair(data)
		for _, tc := range []struct{ source, code string }{{pair.good, ""}, {pair.bad, pair.code}} {
			if len(tc.source) > semanticFuzzMaxSource {
				t.Fatalf("generator exceeded source bound: %d bytes", len(tc.source))
			}
			result, parsed := fuzzCheckDeterministic(t, tc.source, pair.external)
			if !parsed {
				t.Fatalf("%s generator produced unsupported syntax: %+v\n%s", pair.rule, result.Diagnostics, tc.source)
			}
			if tc.code == "" {
				if len(result.Diagnostics) != 0 {
					t.Fatalf("%s rejected its valid construction: %+v\n%s", pair.rule, result.Diagnostics, tc.source)
				}
			} else {
				found := false
				for _, d := range result.Diagnostics {
					found = found || d.Code == tc.code
				}
				if !found {
					t.Fatalf("%s requires %s, got %+v\n%s", pair.rule, tc.code, result.Diagnostics, tc.source)
				}
			}
		}
	})
}

// FuzzCheckerSource covers the frontend-to-checker boundary for arbitrary small
// source, including malformed input. Unlike FuzzCheckerSemantics it makes no
// acceptance claim: it checks determinism, source ranges and the invariant that
// successful lowering never produces malformed semantic nodes (PP099).
func FuzzCheckerSource(f *testing.F) {
	for _, source := range []string{
		"", "\x00", "\xff", "def", "return 1\n",
		"def f(x: int) -> int:\n    return x + 1\n",
		"def f(x: int | None) -> int:\n    if x is None:\n        return 0\n    return x\n",
		"def f(flag: bool) -> int:\n    if flag:\n        x = 1\n    return x\n",
		"def f(xs: tuple[int, ...]) -> int:\n    for x in xs:\n        pass\n    return x\n",
		"async def f() -> None:\n    await f()\n",
		"from purepy import value\n@value\nclass R:\n    x: int\ndef f(x: R) -> int:\n    return x.x\n",
		"def f(é: str) -> str:\n    return é\n",
		"import os\nos.system('never executed')\n",
		"def f() -> None:\n    exec('never executed')\n",
	} {
		f.Add([]byte(source))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > checkerFuzzMaxSource {
			t.Skip()
		}
		fuzzCheckDeterministic(t, string(data), nil)
	})
}

type semanticPair struct {
	rule, good, bad, code string
	external              *manifest.Set
}

type semanticBytes struct {
	data []byte
	pos  int
}

func (b *semanticBytes) next(n int) int {
	if b.pos >= len(b.data) {
		return 0
	}
	v := int(b.data[b.pos]) % n
	b.pos++
	return v
}

var semanticPrimitives = []string{"int", "bool", "float", "str", "bytes"}
var semanticLiterals = []string{"7", "True", "2.5", "'λ'", "b'abc'"}
var semanticOther = []int{1, 0, 0, 4, 3}

// The expression grammar has maximum depth 3 (at most 15 expression nodes),
// contains only explicitly type-preserving constructions, and cannot refer to
// generated locals before their declaration. Helpers are identity functions;
// their number is 2..4, so each job comparison schedules multiple functions.
func semanticExpr(b *semanticBytes, primitive, depth int) string {
	if depth == 0 {
		return semanticLiterals[primitive]
	}
	switch b.next(4) {
	case 0:
		return semanticLiterals[primitive]
	case 1:
		return "helper0(" + semanticExpr(b, primitive, depth-1) + ")"
	case 2:
		return "(" + semanticExpr(b, primitive, depth-1) + " if flag else " + semanticExpr(b, primitive, depth-1) + ")"
	default:
		op := "+"
		if primitive == 1 {
			op = "and"
		}
		return "(" + semanticExpr(b, primitive, depth-1) + " " + op + " " + semanticExpr(b, primitive, depth-1) + ")"
	}
}

func generateSemanticPair(data []byte) semanticPair {
	b := &semanticBytes{data: data}
	family, primitive, variant := b.next(7), b.next(5), b.next(4)
	typ, literal := semanticPrimitives[primitive], semanticLiterals[primitive]
	other := semanticLiterals[semanticOther[primitive]]
	depth, helperCount := 1+b.next(3), 2+b.next(3)
	var helpers strings.Builder
	for i := 0; i < helperCount; i++ {
		fmt.Fprintf(&helpers, "def helper%d(x: %s) -> %s:\n    return x\n", i, typ, typ)
	}
	expr := semanticExpr(b, primitive, depth)
	header := fmt.Sprintf("def run(flag: bool) -> %s:\n", typ)
	pair := semanticPair{code: "PP205"}
	switch family {
	case 0:
		pair.rule = "exact return types (including bool versus int)"
		pair.good = header + "    return " + expr + "\n"
		pair.bad = header + "    return " + other + "\n"
	case 1:
		pair.rule = "local types survive returning branches"
		// Vary nesting independently of expression depth. The declaration must
		// remain known even though its assignment occurs on a returning path.
		body := ""
		for i := 1; i <= variant+1; i++ {
			body += strings.Repeat("    ", i) + "if flag:\n"
		}
		indent := strings.Repeat("    ", variant+2)
		body += indent + "x = " + expr + "\n" + indent + "return " + literal + "\n"
		pair.good = header + body + "    x = " + literal + "\n    return x\n"
		pair.bad = header + body + "    x = " + other + "\n    return " + literal + "\n"
	case 2:
		pair.rule, pair.code = "definite assignment at control-flow joins", "PP206"
		if variant%2 == 0 {
			body := "    if flag:\n        x = " + expr + "\n    else:\n"
			pair.good = header + body + "        x = " + literal + "\n    return x\n"
			pair.bad = header + body + "        pass\n    return x\n"
		} else {
			body := "    for item in range(3):\n        x = " + expr + "\n    return x\n"
			pair.good = header + "    x = " + literal + "\n" + body
			pair.bad = header + body
		}
	case 3:
		pair.rule = "optional refinement and invalidation"
		header = fmt.Sprintf("def run(x: %s | None, flag: bool) -> %s:\n", typ, typ)
		guard := "    if x is None:\n        return " + literal + "\n"
		if variant%2 == 1 {
			guard = "    if None is x:\n        return " + literal + "\n"
		}
		pair.good = header + guard + "    return x\n"
		if variant < 2 {
			pair.bad = header + guard + "    x = None\n    return x\n"
		} else {
			pair.bad = header + guard + "    while flag:\n        x = None\n        break\n    return x\n"
		}
	case 4:
		pair.rule = "direct calls bind exact argument types"
		arg, badArg := expr, other
		if variant%2 == 1 {
			arg, badArg = "x="+arg, "x="+badArg
		}
		if variant < 2 {
			pair.good = header + "    return helper0(" + arg + ")\n"
			pair.bad = header + "    return helper0(" + badArg + ")\n"
		} else {
			pair.rule, pair.code = "async results require direct await", "PP401"
			async := fmt.Sprintf("async def pending(x: %s) -> %s:\n    return x\n", typ, typ)
			header = "async " + header
			pair.good = async + header + "    return await pending(" + arg + ")\n"
			pair.bad = async + header + "    return pending(" + arg + ")\n"
		}
	case 5:
		pair.rule, pair.code = "authority is forwarded from exact original parameters", "PP312"
		pair.external = securityHost()
		prefix := "from host.io import Read, Write, Connection, read\n"
		header = "async def run(cap: Read, conn: Connection, flag: bool) -> bytes:\n"
		call := "read(cap, conn)"
		if variant%2 == 1 {
			call = "read(conn=conn, cap=cap)"
		}
		pair.good = prefix + header + "    return await " + call + "\n"
		switch variant {
		case 0, 1:
			pair.bad = prefix + strings.Replace(header, "cap: Read", "cap: Write", 1) + "    return await " + call + "\n"
		case 2:
			pair.code = "PP313"
			pair.bad = prefix + header + "    alias = cap\n    return await read(alias, conn)\n"
		case 3:
			pair.code = "PP334"
			pair.bad = prefix + header + "    alias = conn\n    return await read(cap, alias)\n"
		}
	case 6:
		pair.rule = "conditions require exact bool"
		keyword := "if"
		if variant%2 == 1 {
			keyword = "while"
		}
		body := ":\n        return " + expr + "\n    return " + literal + "\n"
		pair.good = header + "    " + keyword + " flag" + body
		pair.bad = header + "    " + keyword + " 1" + body
	}
	pair.good = helpers.String() + pair.good
	pair.bad = helpers.String() + pair.bad
	return pair
}

func fuzzCheckDeterministic(t *testing.T, source string, external *manifest.Set) (Result, bool) {
	t.Helper()
	if external == nil {
		external = &manifest.Set{}
	}
	check := func(jobs int) (Result, bool) {
		tree, ds := frontend.Parse("fuzz.py", []byte(source))
		result := Result{Diagnostics: ds}
		parsed := len(ds) == 0
		if parsed {
			program := Link([]*Module{{Name: "fuzz", Path: "fuzz.py", Tree: tree}}, external, nil)
			result = program.CheckFunctions(jobs)
		}
		diag.Sort(result.Diagnostics)
		for _, d := range result.Diagnostics {
			if d.Code == "PP099" {
				t.Fatalf("frontend produced malformed semantic IR: %+v\n%q", d, source)
			}
			fuzzSourceSpan(t, source, d.Span)
			for _, at := range d.Related {
				fuzzSourceSpan(t, source, at)
			}
		}
		for _, fact := range result.Facts {
			fuzzSourceSpan(t, source, fact.Span)
		}
		return result, parsed
	}
	one, parsed := check(1)
	many, parsedMany := check(4)
	a, err := json.Marshal(one)
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(many)
	if err != nil {
		t.Fatal(err)
	}
	if parsed != parsedMany || !bytes.Equal(a, b) {
		t.Fatalf("jobs=1 and jobs=4 disagree\none: %s\nmany: %s\nsource:\n%q", a, b, source)
	}
	return one, parsed
}

func fuzzSourceSpan(t *testing.T, source string, span model.Span) {
	t.Helper()
	if span.File != "fuzz.py" {
		return // External manifest declarations do not refer to this source.
	}
	if span.Start < 0 || span.End < span.Start || span.End > len(source) {
		t.Fatalf("out-of-bounds source span %+v for %d bytes", span, len(source))
	}
	position := func(offset int) (line, column int) {
		prefix := source[:offset]
		return strings.Count(prefix, "\n") + 1, utf8.RuneCountInString(prefix[strings.LastIndex(prefix, "\n")+1:]) + 1
	}
	line, column := position(span.Start)
	endLine, endColumn := position(span.End)
	if span.Line != line || span.Column != column || span.EndLine != endLine || span.EndColumn != endColumn {
		t.Fatalf("source span has incorrect Unicode positions: %+v; expected %d:%d-%d:%d", span, line, column, endLine, endColumn)
	}
}
