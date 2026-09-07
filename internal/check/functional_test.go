package check

import (
	"encoding/json"
	"fmt"
	"github.com/dwrtz/purepy/internal/frontend"
	"github.com/dwrtz/purepy/internal/manifest"
	"os"
	"strings"
	"testing"
)

func TestFunctionalProposal(t *testing.T) {
	src, err := os.ReadFile("../../examples/functional_core/src/core.py")
	if err != nil {
		t.Fatal(err)
	}
	n, ds := frontend.Parse("core.py", src)
	if len(ds) > 0 {
		t.Fatal(ds)
	}
	p := Link([]*Module{{Name: "core", Tree: n}}, &manifest.Set{}, []string{"core.group_labels", "core.lower_ascii"})
	r := p.CheckFunctions(4)
	for _, d := range r.Diagnostics {
		t.Errorf("%s:%d %s %s", d.Span.File, d.Span.Line, d.Code, d.Message)
	}
}

func checkFunctional(t *testing.T, source string, entries ...string) (*Program, Result) {
	t.Helper()
	n, ds := frontend.Parse("core.py", []byte(source))
	p := Link([]*Module{{Name: "core", Tree: n}}, &manifest.Set{}, entries)
	r := p.CheckFunctions(4)
	r.Diagnostics = append(ds, r.Diagnostics...)
	return p, r
}
func TestFunctionalAccepted(t *testing.T) {
	cases := map[string]string{
		"compose":          "from collections.abc import Callable\ndef compose[A,B,C](f:Callable[[B],C],g:Callable[[A],B])->Callable[[A],C]:\n    def h(x:A)->C:\n        return f(g(x))\n    return h\ndef add(x:int)->int:\n    return x+1\ndef main(x:int)->int:\n    h=compose(add,add)\n    return h(x)\n",
		"generic_callback": "from collections.abc import Callable\ndef identity[T](x:T)->T:\n    return x\ndef apply[A,B](f:Callable[[A],B],x:A)->B:\n    return f(x)\ndef main(x:int)->int:\n    return apply(identity,x)\n",
		"stable_capture":   "from collections.abc import Callable\ndef main(x:int)->int:\n    offset=x+1\n    def f(y:int)->int:\n        return offset+y\n    return f(x)\n",
		"recursive_record": "from typing import NamedTuple\nclass Tree[T](NamedTuple):\n    head:T\n    children:tuple[Tree[T],...]\ndef main(x:int)->Tree[int]:\n    return Tree(x,())\n",
		"products":         "type State=tuple[int,str]\ndef main(x:int)->str:\n    state:State=(x,\"ok\")\n    return state[-1]\n",
	}
	for name, source := range cases {
		t.Run(name, func(t *testing.T) {
			_, r := checkFunctional(t, source, "core.main")
			for _, d := range r.Diagnostics {
				t.Errorf("line %d: %s %s", d.Span.Line, d.Code, d.Message)
			}
		})
	}
}
func TestFunctionalRejected(t *testing.T) {
	cases := map[string]string{
		"typevar_intrinsic_shadow":     "def main[len](x:str)->int:\n    return len(x)\n",
		"typevar_parameter_shadow":     "def f[T](T:int)->int:\n    return T\ndef main()->int:\n    return 0\n",
		"typevar_local_shadow":         "def f[T](x:T)->T:\n    T=x\n    return T\ndef main()->int:\n    return 0\n",
		"mutable_capture":              "def main(x:int)->int:\n    def f(y:int)->int:\n        return x+y\n    x=3\n    return f(1)\n",
		"loop_capture":                 "def main(x:int)->int:\n    for i in range(x):\n        def f(y:int)->int:\n            return i+y\n    return 0\n",
		"late_capture":                 "def main(x:int)->int:\n    def f(y:int)->int:\n        return z+y\n    z=x\n    return f(1)\n",
		"host_callback":                "from collections.abc import Callable\ndef main(f:Callable[[int],int])->int:\n    return f(1)\n",
		"callable_record":              "from collections.abc import Callable\nfrom typing import NamedTuple\nclass Bad(NamedTuple):\n    f:Callable[[int],int]\ndef main()->int:\n    return 0\n",
		"method":                       "def main(x:str)->str:\n    return x.lower()\n",
		"replace_dispatch":             "from copy import replace\ndef main(x:int)->int:\n    return replace(x)\n",
		"record_method":                "from typing import NamedTuple\nclass Bad(NamedTuple):\n    x:int\n    def evil(x:int)->int:\n        return x\ndef main()->int:\n    return 0\n",
		"mutable_list":                 "def main(x:int)->int:\n    xs=[x]\n    return x\n",
		"callback_comparison":          "def f(x:int)->int:\n    return x\ndef main()->bool:\n    return f==f\n",
		"generic_arithmetic":           "def f[T](x:T)->T:\n    return x+1\ndef main()->int:\n    return f(1)\n",
		"nonregular_record":            "from typing import NamedTuple\nclass Bad[T](NamedTuple):\n    tail:Bad[tuple[T,...]]|None\ndef main()->int:\n    return 0\n",
		"alias_cycle":                  "type Bad=Bad|None\ndef main()->int:\n    return 0\n",
		"polymorphic_recursion":        "def f[T](x:T)->int:\n    return f((x,))\ndef main()->int:\n    return f(0)\n",
		"mutual_polymorphic_recursion": "def f[T](x:T)->int:\n    return g((x,))\ndef g[T](x:T)->int:\n    return f(x)\ndef main()->int:\n    return f(0)\n",
		"reassigned_definition":        "def main(x:int)->int:\n    def f(y:int)->int:\n        return y\n    f=main\n    return f(x)\n",
		"generic_entrypoint":           "def main[T](x:T)->T:\n    return x\n",
		"wrong_replace_type":           "from copy import replace\nfrom typing import NamedTuple\nclass R(NamedTuple):\n    x:int\ndef main(x:R)->R:\n    return replace(x,x=\"oops\")\n",
		"nested_capture_write":         "def main(x:int)->int:\n    def first(y:int)->int:\n        def second(z:int)->int:\n            return x+z\n        return second(y)\n    x=0\n    return first(x)\n",
	}
	for name, source := range cases {
		t.Run(name, func(t *testing.T) {
			_, r := checkFunctional(t, source, "core.main")
			if len(r.Diagnostics) == 0 {
				t.Fatal("accepted prohibited program")
			}
		})
	}
}

func TestFunctionalTypeGrowthIsBounded(t *testing.T) {
	source := "def duplicate[T](x:T)->tuple[T,T]:\n    return (x,x)\ndef main()->None:\n    v0=1\n"
	for i := 1; i <= 30; i++ {
		source += fmt.Sprintf("    v%d=duplicate(v%d)\n", i, i-1)
	}
	source += "    return None\n"
	_, r := checkFunctional(t, source, "core.main")
	limited := false
	for _, d := range r.Diagnostics {
		if strings.Contains(d.Message, "analysis limit") {
			limited = true
		}
	}
	if !limited {
		t.Fatal("type growth did not hit explicit budget")
	}
	if _, err := json.Marshal(r.Diagnostics); err != nil {
		t.Fatal(err)
	}
}
func FuzzFunctionalSource(f *testing.F) {
	f.Add("from typing import NamedTuple\nclass Link[T](NamedTuple):\n    head:T\n    tail:Link[T]|None\ndef main()->None:\n    return None\n")
	f.Add("from collections.abc import Callable\ndef identity[T](x:T)->T:\n    return x\n")
	f.Fuzz(func(t *testing.T, source string) {
		if len(source) > 32768 {
			t.Skip()
		}
		n, ds := frontend.Parse("core.py", []byte(source))
		if n == nil || len(ds) > 0 {
			return
		}
		p := Link([]*Module{{Name: "core", Tree: n}}, &manifest.Set{}, nil)
		p.CheckFunctions(1)
	})
}

func TestFunctionalAliasProvenanceDoesNotMultiply(t *testing.T) {
	source := "def identity(x:int)->int:\n    return x\ndef main(x:int)->int:\n    f=identity\n"
	for i := 0; i < 40; i++ {
		source += "    f=f if x>0 else f\n"
	}
	source += "    return f(x)\n"
	_, r := checkFunctional(t, source, "core.main")
	if len(r.Diagnostics) > 0 {
		t.Fatal(r.Diagnostics)
	}
	if len(r.Calls) > 5 {
		t.Fatalf("duplicate provenance: %d calls", len(r.Calls))
	}
}

func TestTypeDepthIsBounded(t *testing.T) {
	annotation := "int"
	for i := 0; i < 140; i++ {
		annotation = "tuple[" + annotation + ", ...]"
	}
	_, r := checkFunctional(t, "def main(x:"+annotation+")->"+annotation+":\n    return x\n", "core.main")
	if len(r.Diagnostics) == 0 {
		t.Fatal("type depth budget not enforced")
	}
}
