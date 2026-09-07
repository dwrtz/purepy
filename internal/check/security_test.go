package check

import (
	"sort"
	"testing"

	"github.com/dwrtz/purepy/internal/cache"
	"github.com/dwrtz/purepy/internal/diag"
	"github.com/dwrtz/purepy/internal/frontend"
	"github.com/dwrtz/purepy/internal/manifest"
	"github.com/dwrtz/purepy/internal/model"
)

func securityVerify(t *testing.T, sources map[string]string, external *manifest.Set) []diag.Diagnostic {
	t.Helper()
	names := make([]string, 0, len(sources))
	for name := range sources {
		names = append(names, name)
	}
	sort.Strings(names)
	var modules []*Module
	var ds []diag.Diagnostic
	for _, name := range names {
		tree, parsed := frontend.Parse(name+".py", []byte(sources[name]))
		ds = append(ds, parsed...)
		modules = append(modules, &Module{Name: name, Path: name + ".py", Tree: tree})
	}
	if len(ds) > 0 {
		return ds
	}
	if external == nil {
		external = &manifest.Set{}
	}
	p := Link(modules, external, nil)
	return p.CheckFunctions(3).Diagnostics
}

func securityHost() *manifest.Set {
	return &manifest.Set{
		Modules: []manifest.Module{{Name: "host.io", ImportSafe: true, Source: "host.toml"}},
		Types: []manifest.Type{
			{Name: "host.io.Read", Category: "capability", Labels: []string{"network.read"}, Source: "host.toml"},
			{Name: "host.io.Write", Category: "capability", Labels: []string{"network.write"}, Source: "host.toml"},
			{Name: "host.io.Connection", Category: "host_ref", Source: "host.toml"},
		},
		Functions: []manifest.Function{
			{Name: "host.io.read", Kind: "async", Trust: "host", Returns: "bytes", Parameters: []manifest.Parameter{{Name: "cap", Type: "host.io.Read"}, {Name: "conn", Type: "host.io.Connection"}}, Source: "host.toml"},
		},
	}
}

func TestSecurityCategoryAndCallRejections(t *testing.T) {
	prefix := "from host.io import Read, Write, Connection, read\n"
	cases := map[string]string{
		"capability_alias":              "async def f(cap: Read, conn: Connection) -> bytes:\n    alias = cap\n    return await read(alias, conn)\n",
		"host_reference_alias":          "async def f(cap: Read, conn: Connection) -> bytes:\n    alias = conn\n    return await read(cap, alias)\n",
		"wrong_exact_capability":        "async def f(cap: Write, conn: Connection) -> bytes:\n    return await read(cap, conn)\n",
		"capability_rebinding":          "async def f(cap: Read, conn: Connection) -> bytes:\n    cap = 1\n    return await read(cap, conn)\n",
		"capability_in_tuple":           "def f(cap: Read) -> int:\n    packed = (cap,)\n    return 0\n",
		"capability_comparison":         "def f(cap: Read) -> bool:\n    return cap == cap\n",
		"capability_format":             "def f(cap: Read) -> str:\n    return f'{cap}'\n",
		"host_reference_field":          "def f(conn: Connection) -> str:\n    return conn.name\n",
		"host_reference_optional":       "def f(conn: Connection | None) -> None:\n    return\n",
		"authority_return":              "def f(cap: Read) -> Read:\n    return cap\n",
		"capability_construction":       "def f() -> None:\n    cap = Read()\n",
		"bare_coroutine":                "async def f(cap: Read, conn: Connection) -> bytes:\n    pending = read(cap, conn)\n    return await pending\n",
		"nested_coroutine":              "async def f(cap: Read, conn: Connection) -> int:\n    return len(read(cap, conn))\n",
		"unreachable_authority":         "def f() -> int:\n    return 1\n    read(0, 0)\n",
		"unreachable_unsupported":       "def f() -> int:\n    return 1\n    raise ValueError()\n",
		"function_as_integer":           "def f() -> int:\n    other: int = f\n    return other()\n",
		"method_dispatch":               "def f(x: str) -> str:\n    return x.upper()\n",
		"intrinsic_local_shadow":        "def f(len: int, x: str) -> int:\n    return len(x)\n",
		"intrinsic_later_local":         "def f(x: str) -> int:\n    y = len(x)\n    len = 1\n    return y\n",
		"private_field_source_spelling": "from typing import NamedTuple\n\nclass R(NamedTuple):\n    __field: int\ndef f() -> R:\n    return R(__field=1)\n",
		"tuple_subscription":            "def f(xs: tuple[int, ...]) -> int:\n    return xs[0,]\n",
	}
	for name, source := range cases {
		t.Run(name, func(t *testing.T) {
			if ds := securityVerify(t, map[string]string{"app": prefix + source}, securityHost()); len(ds) == 0 {
				t.Fatalf("accepted unsafe or unsupported source:\n%s", source)
			}
		})
	}
}

func TestSecurityFlowRejections(t *testing.T) {
	cases := map[string]string{
		"conditional_assignment":         "def f(flag: bool) -> int:\n    if flag:\n        x = 1\n    return x\n",
		"empty_loop_assignment":          "def f(xs: tuple[int, ...]) -> int:\n    for x in xs:\n        y = x\n    return y\n",
		"empty_loop_target":              "def f(xs: tuple[int, ...]) -> int:\n    for x in xs:\n        pass\n    return x\n",
		"branch_type_conflict":           "def f(flag: bool) -> int:\n    if flag:\n        x = 1\n    else:\n        x = True\n    return x\n",
		"parameter_type_change":          "def f(x: int) -> int:\n    x = True\n    return x\n",
		"optional_rebound_after_narrow":  "def f(x: int | None) -> int:\n    if x is None:\n        return 0\n    x = None\n    return x + 1\n",
		"optional_back_edge":             "def f(x: int | None, active: bool) -> int:\n    if x is None:\n        return 0\n    while active:\n        y = x + 1\n        x = None\n    return 1\n",
		"optional_break_edge":            "def f(x: int | None, active: bool) -> int:\n    if x is None:\n        return 0\n    while active:\n        x = None\n        break\n    return x + 1\n",
		"loop_continue_skips_assignment": "def f(xs: tuple[int, ...], flag: bool) -> int:\n    for x in xs:\n        if flag:\n            continue\n        y = x\n    return y\n",
		"truthy_integer":                 "def f(x: int) -> int:\n    if x:\n        return 1\n    return 0\n",
		"bool_integer_arithmetic":        "def f(x: bool) -> int:\n    return x + 1\n",
		"unknown_return_annotation":      "def f() -> object:\n    return None\n",
		"arbitrary_union":                "def f(x: int | str) -> int:\n    return 0\n",
		"product_with_mutable_element":   "def f(x: tuple[int, list[int]]) -> int:\n    return 0\n",
	}
	for name, source := range cases {
		t.Run(name, func(t *testing.T) {
			if ds := securityVerify(t, map[string]string{"app": source}, nil); len(ds) == 0 {
				t.Fatalf("accepted unsound flow or type:\n%s", source)
			}
		})
	}
}

func TestSecurityAuthorityForwardingAccepted(t *testing.T) {
	source := "from host.io import Read, Connection, read\nasync def f(cap: Read, conn: Connection) -> bytes:\n    return await read(conn=conn, cap=cap)\n"
	if ds := securityVerify(t, map[string]string{"app": source}, securityHost()); len(ds) > 0 {
		t.Fatal(ds)
	}
}

func TestSecurityInitializationOrder(t *testing.T) {
	cases := map[string]map[string]string{
		"record_before_definition": {"app": "from typing import Final\nfrom typing import NamedTuple\nX: Final[R] = R(1)\n\nclass R(NamedTuple):\n    x: int\n"},
		"constant_before_import":   {"other": "from typing import Final\nY: Final[int] = 1\n", "app": "from typing import Final\nX: Final[int] = Y\nfrom other import Y\n"},
		"decorator_before_import":  {"app": "\nclass R(NamedTuple):\n    x: int\nfrom typing import NamedTuple\n"},
	}
	for name, sources := range cases {
		t.Run(name, func(t *testing.T) {
			if ds := securityVerify(t, sources, nil); len(ds) == 0 {
				t.Fatal("accepted unbound module initialization reference")
			}
		})
	}
}

func TestSecuritySealedModuleCollision(t *testing.T) {
	sources := map[string]string{"typing": "def NamedTuple(x: int) -> None:\n    return\n", "app": "from typing import NamedTuple\n\nclass R(NamedTuple):\n    x: int\n"}
	if ds := securityVerify(t, sources, nil); len(ds) == 0 {
		t.Fatal("accepted a project module shadowing sealed standard-library declaration")
	}
}

func TestSecurityOpaqueEqualityCannotHideInsideValues(t *testing.T) {
	external := &manifest.Set{Modules: []manifest.Module{{Name: "host.data", ImportSafe: true, Source: "host.toml"}}, Types: []manifest.Type{{Name: "host.data.Token", Category: "value", Immutable: true, Source: "host.toml"}}}
	prefix := "from typing import NamedTuple\nfrom host.data import Token\n\nclass Inner(NamedTuple):\n    token: Token\n\nclass Outer(NamedTuple):\n    items: tuple[Inner, ...]\n"
	cases := map[string]string{
		"record_equality":                 "def f(a: Inner, b: Inner) -> bool:\n    return a == b\n",
		"nested_record_inequality":        "def f(a: Outer, b: Outer) -> bool:\n    return a != b\n",
		"record_tuple_membership":         "def f(a: Inner, values: tuple[Inner, ...]) -> bool:\n    return a in values\n",
		"opaque_optional_equality_none":   "def f(a: Token | None) -> bool:\n    return a == None\n",
		"none_inequality_opaque_optional": "def f(a: Token | None) -> bool:\n    return None != a\n",
	}
	for name, source := range cases {
		t.Run(name, func(t *testing.T) {
			if ds := securityVerify(t, map[string]string{"app": prefix + source}, external); len(ds) == 0 {
				t.Fatal("accepted implicit equality dispatch on an opaque manifest value")
			}
		})
	}
	if ds := securityVerify(t, map[string]string{"app": prefix + "def f(a: Token | None) -> bool:\n    return a is None\n"}, external); len(ds) > 0 {
		t.Fatalf("safe None identity check rejected: %v", ds)
	}
}

func TestSecurityMalformedCachedAwaitIsNotAccepted(t *testing.T) {
	tree, ds := frontend.Parse("app.py", []byte("async def f() -> None:\n    await f()\n"))
	if len(ds) > 0 {
		t.Fatal(ds)
	}
	await := tree.Items("body")[0].Items("body")[0].Get("value")
	delete(await.Fields, "call")
	dir := t.TempDir()
	key := cache.Key("malformed-await")
	if err := cache.Write(dir, key, cache.Summary{Tree: tree}); err != nil {
		return
	}
	stored, ok := cache.Read(dir, key)
	if !ok {
		return
	}
	p := Link([]*Module{{Name: "app", Path: "app.py", Tree: stored.Tree}}, &manifest.Set{}, nil)
	if ds := p.CheckFunctions(1).Diagnostics; len(ds) == 0 {
		t.Fatal("malformed cached await was accepted without a call or rejection")
	}
}

func TestSecurityMalformedCachedOperatorsAndSuites(t *testing.T) {
	cases := []struct {
		name, source string
		mutate       func(*model.Node)
	}{
		{"boolean_operator", "def f(a: bool, b: bool) -> bool:\n    return a and b\n", func(n *model.Node) { n.Items("body")[0].Items("body")[0].Get("value").Attr["op"] = "+" }},
		{"function_suite", "def f() -> None:\n    pass\n", func(n *model.Node) { delete(n.Items("body")[0].Lists, "body") }},
		{"if_suite", "def f(a: bool) -> None:\n    if a:\n        pass\n", func(n *model.Node) { delete(n.Items("body")[0].Items("body")[0].Lists, "body") }},
		{"while_suite", "def f(a: bool) -> None:\n    while a:\n        pass\n", func(n *model.Node) { delete(n.Items("body")[0].Items("body")[0].Lists, "body") }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tree, ds := frontend.Parse("app.py", []byte(c.source))
			if len(ds) > 0 {
				t.Fatal(ds)
			}
			c.mutate(tree)
			dir := t.TempDir()
			key := cache.Key(c.name)
			if err := cache.Write(dir, key, cache.Summary{Tree: tree}); err != nil {
				return
			}
			stored, ok := cache.Read(dir, key)
			if !ok {
				return
			}
			p := Link([]*Module{{Name: "app", Path: "app.py", Tree: stored.Tree}}, &manifest.Set{}, nil)
			if ds := p.CheckFunctions(1).Diagnostics; len(ds) == 0 {
				t.Fatal("malformed cached syntax was accepted without rejection")
			}
		})
	}
}
