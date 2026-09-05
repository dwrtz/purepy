package cache

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/dwrtz/purepy/internal/check"
	"github.com/dwrtz/purepy/internal/diag"
	"github.com/dwrtz/purepy/internal/frontend"
	"github.com/dwrtz/purepy/internal/manifest"
	"github.com/dwrtz/purepy/internal/model"
)

const cacheFuzzSource = `from purepy import value
from typing import Final

LIMIT: Final[int] = 3

@value
class Box:
    number: int

def run(x: int, text: str, maybe: int | None) -> int:
    n = abs(x)
    pair: tuple[int, ...] = (x, n)
    box = Box(n)
    shown = f"{text!s} {n}"
    part = text[:2]
    first = pair[0]
    if (x < n <= LIMIT) and maybe is not None:
        n = maybe if x == 0 else -x
    else:
        n = box.number
    for i in range(3):
        n = n + i
    while False:
        continue
        break
    return n

async def later(x: int) -> int:
    return abs(x)

async def entry(x: int) -> int:
    return await later(x)
`

func parsedCacheSummary(t testing.TB, source string) Summary {
	t.Helper()
	tree, diagnostics := frontend.Parse("fuzz.py", []byte(source))
	if tree == nil {
		t.Fatalf("cache seed has no lowered tree: %v", diagnostics)
	}
	return Summary{Tree: tree, Diagnostics: diagnostics}
}

func copyCacheSummary(t testing.TB, payload []byte) Summary {
	t.Helper()
	var s Summary
	if err := json.Unmarshal(payload, &s); err != nil {
		t.Fatal(err)
	}
	return s
}

// Keep traversals ordered: one corpus byte must select the same node on replay.
func cacheNodes(n *model.Node) []*model.Node {
	out := []*model.Node{n}
	keys := make([]string, 0, len(n.Fields))
	for key := range n.Fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if child := n.Fields[key]; child != nil {
			out = append(out, cacheNodes(child)...)
		}
	}
	keys = keys[:0]
	for key := range n.Lists {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		for _, child := range n.Lists[key] {
			out = append(out, cacheNodes(child)...)
		}
	}
	return out
}

func findCacheNode(t testing.TB, s Summary, kind string, selection byte) *model.Node {
	t.Helper()
	var matches []*model.Node
	for _, n := range cacheNodes(s.Tree) {
		if n.Kind == kind {
			matches = append(matches, n)
		}
	}
	if len(matches) == 0 {
		t.Fatalf("cache seed has no %s node", kind)
	}
	return matches[int(selection)%len(matches)]
}

const cacheMutationCount = 28

// Every malformed operation has an independent miss oracle, including mutations
// whose JSON envelope and checksum remain valid. Positive operations ensure the
// fuzz target cannot pass by disabling caching altogether.
func mutateCacheSummary(t testing.TB, s *Summary, operation, selection byte) (name string, mustMiss, mustHit bool) {
	t.Helper()
	node := func(kind string) *model.Node { return findCacheNode(t, *s, kind, selection) }
	leaf := func(kind string) *model.Node {
		return &model.Node{Kind: kind, Span: s.Tree.Span, Attr: map[string]string{"name": "x"}}
	}
	switch int(operation) % cacheMutationCount {
	case 0:
		return "unchanged parser tree", false, true
	case 1:
		n := node("Literal")
		n.Attr["type"] = "float"
		return "valid literal type change", false, true
	case 2:
		s.Tree.Lists["body"] = append(s.Tree.Lists["body"], nil)
		return "nil statement", true, false
	case 3:
		node("Call").Lists["args"] = []*model.Node{leaf("Name")}
		return "naked call argument", true, false
	case 4:
		node("Compare").Attr["ops"] = "<|<=|==|!="
		return "comparison arity", true, false
	case 5:
		delete(node("Function").Fields, "returns")
		return "missing return annotation", true, false
	case 6:
		node("Function").Lists["params"] = []*model.Node{leaf("Name")}
		return "parameter role", true, false
	case 7:
		node("Function").Lists["body"] = []*model.Node{leaf("Name")}
		return "statement role", true, false
	case 8:
		s.Tree.Fields = map[string]*model.Node{"hidden": leaf("Name")}
		return "unknown child field", true, false
	case 9:
		s.Tree.Lists["hidden"] = []*model.Node{leaf("Name")}
		return "unknown child list", true, false
	case 10:
		s.Tree.Attr = map[string]string{"hidden": "value"}
		return "unknown attribute", true, false
	case 11:
		s.Tree.Span.Start = -1
		return "negative root span", true, false
	case 12:
		node("Return").Span.End = s.Tree.Span.End + 1
		return "outside root span", true, false
	case 13:
		node("Return").Fields["value"] = &model.Node{Kind: "Module", Span: s.Tree.Span}
		return "nested module expression", true, false
	case 14:
		node("Bool").Attr["op"] = "xor"
		return "boolean operator", true, false
	case 15:
		node("Literal").Attr["type"] = "float int"
		return "literal enum", true, false
	case 16:
		node("AwaitCall").Fields["call"] = leaf("Name")
		return "await call role", true, false
	case 17:
		s.Diagnostics = []diag.Diagnostic{diag.New("PP099", "internal error", s.Tree.Span)}
		return "diagnostic code", true, false
	case 18:
		s.Diagnostics = []diag.Diagnostic{diag.New("PP003", "unsupported syntax", s.Tree.Span)}
		return "parser diagnostic barrier", false, true
	case 19:
		node("Return").Fields["value"] = leaf("Name")
		return "valid expression replacement", false, true
	case 20:
		delete(node("Return").Fields, "value")
		return "optional return value", false, true
	case 21:
		delete(node("If").Lists, "else")
		return "optional else suite", false, true
	case 22:
		node("Call").Lists["args"] = []*model.Node{{Kind: "Unsupported", Span: s.Tree.Span, Attr: map[string]string{"syntax": "generator_expression"}}}
		return "unsupported argument without diagnostic", true, false
	case 23:
		node("Binary").Attr["op"] = "+ -"
		return "binary operator enum", true, false
	case 24:
		node("Arg").Fields["value"] = nil
		return "nil argument value", true, false
	case 25:
		node("Return").Kind = "Unknown"
		return "unknown node kind", true, false
	case 26:
		node("Name").Span.EndColumn = -1
		return "negative source coordinate", true, false
	default:
		// Role-preserving edits reach the checker beyond happy-path typing.
		n := node("Name")
		n.Attr["name"] = []string{"abs", "int", "run", "missing", "Box", "__class__", "None", "x"}[int(selection)%8]
		return "valid name replacement", false, true
	}
}

func checkCacheHit(t testing.TB, s Summary) {
	t.Helper()
	// Production stops at cached parser diagnostics before linking. Follow the
	// same gate: unsupported partial IR must never bypass those diagnostics.
	if len(s.Diagnostics) != 0 {
		return
	}
	p := check.Link([]*check.Module{{Name: "fuzz", Path: "fuzz.py", Tree: s.Tree}}, &manifest.Set{}, nil)
	result := p.CheckFunctions(1)
	for _, d := range result.Diagnostics {
		if d.Code == "PP099" {
			t.Fatalf("cache hit reached malformed semantic IR: %s", d.Message)
		}
	}
}

func TestMalformedCacheSummaryRegression(t *testing.T) {
	s := parsedCacheSummary(t, cacheFuzzSource)
	if len(s.Diagnostics) != 0 {
		t.Fatal(s.Diagnostics)
	}
	payload, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	for operation := 0; operation < cacheMutationCount; operation++ {
		for _, selection := range []byte{0, 1, 31} {
			mutated := copyCacheSummary(t, payload)
			name, mustMiss, mustHit := mutateCacheSummary(t, &mutated, byte(operation), selection)
			t.Run(fmt.Sprintf("%s/%d", name, selection), func(t *testing.T) {
				dir, key := t.TempDir(), Key("structural-regression")
				if err := Write(dir, key, mutated); err != nil {
					t.Fatal(err)
				}
				hit, ok := Read(dir, key)
				if mustMiss && ok || mustHit && !ok {
					t.Fatalf("cache hit=%v, required miss=%v hit=%v", ok, mustMiss, mustHit)
				}
				if ok {
					checkCacheHit(t, hit)
				}
			})
		}
	}
}

func TestCachedParserDiagnosticsRemainReadable(t *testing.T) {
	for _, source := range []string{
		"def f(x: int) -> int:\n    return [x]\n",
		"def f(x: int) -> int:\n    return sum(y for y in (x,))\n",
		"def f(x: int) -> int:\n    return x ** 2\n",
		"def f(x: str) -> str:\n    return f'{x!r}'\n",
		"def f(x: str) -> str:\n    return x[::2]\n",
	} {
		t.Run(source, func(t *testing.T) {
			s := parsedCacheSummary(t, source)
			dir, key := t.TempDir(), Key(source)
			if err := Write(dir, key, s); err != nil {
				t.Fatal(err)
			}
			hit, ok := Read(dir, key)
			if !ok || len(hit.Diagnostics) != len(s.Diagnostics) {
				t.Fatalf("lost parser summary: hit=%v original=%v", ok, s.Diagnostics)
			}
			checkCacheHit(t, hit)
		})
	}
}

func TestCacheTreeBudgets(t *testing.T) {
	for _, depth := range []int{maxNodeDepth - 2, maxNodeDepth + 1} {
		s := testSummary("")
		n := &model.Node{Kind: "Name", Attr: map[string]string{"name": "x"}}
		for i := 0; i < depth; i++ {
			n = &model.Node{Kind: "Unary", Attr: map[string]string{"op": "-"}, Fields: map[string]*model.Node{"operand": n}}
		}
		s.Tree.Lists["body"] = []*model.Node{{Kind: "ExprStmt", Fields: map[string]*model.Node{"value": n}}}
		if validSummary(s) != (depth+2 <= maxNodeDepth) {
			t.Fatalf("wrong depth budget result for %d", depth)
		}
	}
	for _, count := range []int{maxNodes - 1, maxNodes} {
		s := testSummary("")
		for i := 0; i < count; i++ {
			s.Tree.Lists["body"] = append(s.Tree.Lists["body"], &model.Node{Kind: "Pass"})
		}
		if validSummary(s) != (count+1 <= maxNodes) {
			t.Fatalf("wrong total-node budget result for %d", count)
		}
	}
}

func FuzzCacheSummary(f *testing.F) {
	s := parsedCacheSummary(f, cacheFuzzSource)
	payload, err := json.Marshal(s)
	if err != nil {
		f.Fatal(err)
	}
	for operation := 0; operation < cacheMutationCount; operation++ {
		f.Add([]byte{byte(operation), 0})
		f.Add([]byte{byte(operation), 31})
	}
	f.Fuzz(func(t *testing.T, controls []byte) {
		if len(controls) < 2 || len(controls) > 64 {
			return
		}
		mutated := copyCacheSummary(t, payload)
		// Exactly one structural edit keeps the miss oracle independent of
		// later edits that might repair it or remove a required seed node.
		_, mustMiss, mustHit := mutateCacheSummary(t, &mutated, controls[0], controls[1])
		dir, key := t.TempDir(), Key("summary-fuzz")
		if err := Write(dir, key, mutated); err != nil {
			t.Fatal(err)
		}
		hit, ok := Read(dir, key)
		if mustMiss && ok || mustHit && !ok {
			t.Fatalf("cache hit=%v, required miss=%v hit=%v, controls=%x", ok, mustMiss, mustHit, controls)
		}
		if ok {
			checkCacheHit(t, hit)
		}
	})
}

// Artifact mode 0 fuzzes the envelope; mode 1 wraps arbitrary JSON as payload
// with the correct checksum, so mutations exercise decoding and IR validation.
func FuzzCacheArtifact(f *testing.F) {
	key := Key("artifact-fuzz")
	for _, source := range []string{"def f(x: int) -> int:\n    return abs(x)\n", "", "def f(x: int) -> int:\n    return [x]\n"} {
		s := parsedCacheSummary(f, source)
		payload, err := json.Marshal(s)
		if err != nil {
			f.Fatal(err)
		}
		envelope, err := json.Marshal(artifact{Schema: Schema, Key: key, Checksum: Digest(payload), Payload: payload})
		if err != nil {
			f.Fatal(err)
		}
		f.Add(append([]byte{0}, envelope...))
		f.Add(append([]byte{1}, payload...))
	}
	for _, payload := range []string{"null", "[]", "{}", `{"tree":{"kind":"Module","lists":{"body":[null]}}}`, `{"tree":{"kind":"Module"},"diagnostics":[{"code":"PP099"}]}`, strings.Repeat("[", 128)} {
		f.Add(append([]byte{1}, payload...))
	}
	f.Fuzz(func(t *testing.T, input []byte) {
		if len(input) < 1 || len(input) > 32<<10 {
			return
		}
		data := input[1:]
		if input[0]&1 != 0 {
			var compact bytes.Buffer
			if err := json.Compact(&compact, data); err == nil {
				data = compact.Bytes()
			}
			// Assemble even invalid JSON, allowing the decoder's rejection path
			// to run without a pre-decoder accepting it on the harness's behalf.
			data = []byte(fmt.Sprintf(`{"schema":%d,"key":%q,"checksum":%q,"payload":%s}`, Schema, key, Digest(data), data))
		}
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, key+".json"), data, 0o600); err != nil {
			t.Fatal(err)
		}
		if hit, ok := Read(dir, key); ok {
			checkCacheHit(t, hit)
		}
	})
}
