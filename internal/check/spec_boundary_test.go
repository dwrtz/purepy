package check

import (
	"fmt"
	"testing"

	"github.com/dwrtz/purepy/internal/diag"
	"github.com/dwrtz/purepy/internal/manifest"
)

func boundaryCode(ds []diag.Diagnostic, code string) bool {
	for _, d := range ds {
		if d.Code == code {
			return true
		}
	}
	return false
}

// Equal or overlapping reporting labels never make distinct nominal
// capabilities interchangeable (17.2). Forwarding uses original parameters
// even when keyword binding changes the order of the call (16.4, 17.4, 18.3).
func TestBoundaryAuthorityForwarding(t *testing.T) {
	external := securityHost()
	external.Types = append(external.Types,
		manifest.Type{Name: "host.io.OtherRead", Category: "capability", Labels: []string{"network.read"}, Source: "host.toml"},
		manifest.Type{Name: "host.io.BroadRead", Category: "capability", Labels: []string{"network", "network.read", "network.write"}, Source: "host.toml"},
		manifest.Type{Name: "host.io.OtherConnection", Category: "host_ref", Source: "host.toml"},
	)
	prefix := "from host.io import Read, OtherRead, BroadRead, Connection, OtherConnection, read\n"
	cases := []struct{ name, source, code string }{
		{"repeated_forwarding", "async def forward(cap: Read, conn: Connection) -> bytes:\n    first = await read(conn=conn, cap=cap)\n    second = await read(cap, conn)\n    return first + second\nasync def run(cap: Read, conn: Connection) -> bytes:\n    return await forward(conn=conn, cap=cap)\n", ""},
		{"unused_authority", "def run(cap: Read, conn: Connection) -> None:\n    return\n", ""},
		{"same_labels_different_type", "async def run(cap: OtherRead, conn: Connection) -> bytes:\n    return await read(cap, conn)\n", "PP312"},
		{"overlapping_labels_different_type", "async def run(cap: BroadRead, conn: Connection) -> bytes:\n    return await read(cap, conn)\n", "PP312"},
		{"different_reference_type", "async def run(cap: Read, conn: OtherConnection) -> bytes:\n    return await read(cap, conn)\n", "PP334"},
		{"capability_conditional", "async def run(cap: Read, conn: Connection, flag: bool) -> bytes:\n    return await read(cap if flag else cap, conn)\n", "PP312"},
		{"reference_conditional", "async def run(cap: Read, conn: Connection, flag: bool) -> bytes:\n    return await read(cap, conn if flag else conn)\n", "PP334"},
		{"handle_does_not_authorize", "async def run(conn: Connection) -> bytes:\n    return await read(conn=conn)\n", "PP312"},
		{"constructed_capability", "async def run(conn: Connection) -> bytes:\n    return await read(Read(), conn)\n", "PP312"},
		{"constructed_reference", "async def run(cap: Read) -> bytes:\n    return await read(cap, Connection())\n", "PP334"},
		{"pure_caller_dead_branch", "async def run() -> bytes:\n    if False:\n        return await read(0, 0)\n    return b''\n", "PP312"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ds := securityVerify(t, map[string]string{"app": prefix + tc.source}, external)
			if tc.code == "" && len(ds) != 0 || tc.code != "" && !boundaryCode(ds, tc.code) {
				t.Fatalf("expected diagnostic %q, got %+v", tc.code, ds)
			}
		})
	}
}

// Exercise the sync/async call matrix for local and trusted declarations,
// including the expression contexts that could otherwise expose a coroutine.
func TestBoundaryAsyncCalls(t *testing.T) {
	external := securityHost()
	external.Functions = append(external.Functions,
		manifest.Function{Name: "host.io.sync_value", Kind: "sync", Trust: "pure", Parameters: []manifest.Parameter{{Name: "n", Type: "int"}}, Returns: "int", Source: "host.toml"},
		manifest.Function{Name: "host.io.async_value", Kind: "async", Trust: "pure", Parameters: []manifest.Parameter{{Name: "n", Type: "int"}}, Returns: "int", Source: "host.toml"},
		manifest.Function{Name: "host.io.async_none", Kind: "async", Trust: "pure", Parameters: []manifest.Parameter{}, Returns: "None", Source: "host.toml"},
	)
	prefix := "from host.io import sync_value, async_value, async_none\ndef local_sync(n: int) -> int:\n    return n\nasync def local_async(n: int) -> int:\n    return n\n"
	cases := []struct{ name, source, code string }{
		{"sync_calls_sync", "def run(n: int) -> int:\n    return local_sync(sync_value(n))\n", ""},
		{"async_calls_sync", "async def run(n: int) -> int:\n    return local_sync(sync_value(n))\n", ""},
		{"async_awaits_async", "async def run(n: int) -> int:\n    x = await async_value(n)\n    return await local_async(x)\n", ""},
		{"nested_direct_await", "async def run(n: int) -> int:\n    return await local_async(await async_value(n))\n", ""},
		{"await_none_statement", "async def run() -> None:\n    await async_none()\n", ""},
		{"await_in_ordinary_loops", "async def run(n: int) -> int:\n    total = 0\n    for i in range(n):\n        total = total + await async_value(i)\n    while total < n:\n        total = await local_async(total + 1)\n    return total\n", ""},
		{"sync_calls_async", "def run(n: int) -> int:\n    return local_async(n)\n", "PP401"},
		{"sync_awaits_async", "def run(n: int) -> int:\n    return await async_value(n)\n", "PP404"},
		{"async_awaits_sync", "async def run(n: int) -> int:\n    return await local_sync(n)\n", "PP403"},
		{"async_awaits_sync_external", "async def run(n: int) -> int:\n    return await sync_value(n)\n", "PP403"},
		{"async_awaits_intrinsic", "async def run(n: int) -> int:\n    return await abs(n)\n", "PP403"},
		{"coroutine_assignment", "async def run(n: int) -> int:\n    pending = async_value(n)\n    return n\n", "PP401"},
		{"coroutine_return", "async def run(n: int) -> int:\n    return local_async(n)\n", "PP401"},
		{"coroutine_argument", "async def run(n: int) -> int:\n    return sync_value(async_value(n))\n", "PP401"},
		{"coroutine_tuple", "async def run(n: int) -> tuple[int, ...]:\n    return (async_value(n),)\n", "PP401"},
		{"coroutine_comparison", "async def run(n: int) -> bool:\n    return async_value(n) == async_value(n)\n", "PP401"},
		{"coroutine_conditional", "async def run(n: int, flag: bool) -> int:\n    return async_value(n) if flag else local_async(n)\n", "PP401"},
		{"async_call_discard", "async def run() -> None:\n    async_none()\n", "PP401"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ds := securityVerify(t, map[string]string{"app": prefix + tc.source}, external)
			if tc.code == "" && len(ds) != 0 || tc.code != "" && !boundaryCode(ds, tc.code) {
				t.Fatalf("expected diagnostic %q, got %+v", tc.code, ds)
			}
		})
	}
}

func TestBoundaryImportSafety(t *testing.T) {
	for _, tc := range []struct {
		name, source string
		safe         bool
		code         string
	}{
		{"safe_function", "from host.io import Read, Connection, read\nasync def run(cap: Read, conn: Connection) -> bytes:\n    return await read(cap, conn)\n", true, ""},
		{"unsafe_function", "from host.io import read\ndef run() -> None:\n    return\n", false, "PP604"},
		{"unsafe_type", "from host.io import Read\ndef run(cap: Read) -> None:\n    return\n", false, "PP604"},
		{"unsafe_unused_import", "from host.io import Connection\ndef run() -> None:\n    return\n", false, "PP604"},
		{"unimported_unsafe_module", "def run() -> None:\n    return\n", false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			external := securityHost()
			external.Modules[0].ImportSafe = tc.safe
			ds := securityVerify(t, map[string]string{"app": tc.source}, external)
			if tc.code == "" && len(ds) != 0 || tc.code != "" && !boundaryCode(ds, tc.code) {
				t.Fatalf("expected diagnostic %q, got %+v", tc.code, ds)
			}
		})
	}
}

// The verifier checks declared categories, not the truth of host purity,
// import safety, deep immutability, or effect-completeness assertions.
func TestBoundaryExternalContracts(t *testing.T) {
	for _, tc := range []struct {
		name, trust, returns string
		parameters           []manifest.Parameter
		code                 string
	}{
		{"host_capability_and_reference", "host", "bytes", []manifest.Parameter{{Name: "cap", Type: "host.io.Read"}, {Name: "conn", Type: "host.io.Connection"}}, ""},
		{"pure_external_values", "pure", "tuple[host.io.Row, ...] | None", []manifest.Parameter{{Name: "ids", Type: "tuple[int, ...]"}}, ""},
		{"host_without_capability", "host", "int", []manifest.Parameter{}, "PP603"},
		{"reference_without_capability", "host", "int", []manifest.Parameter{{Name: "conn", Type: "host.io.Connection"}}, "PP603"},
		{"pure_with_capability", "pure", "int", []manifest.Parameter{{Name: "cap", Type: "host.io.Read"}}, "PP602"},
		{"pure_with_reference", "pure", "int", []manifest.Parameter{{Name: "conn", Type: "host.io.Connection"}}, "PP602"},
		{"capability_return", "host", "host.io.Read", []manifest.Parameter{{Name: "cap", Type: "host.io.Read"}}, "PP602"},
		{"reference_return", "host", "host.io.Connection", []manifest.Parameter{{Name: "cap", Type: "host.io.Read"}}, "PP602"},
		{"reference_inside_tuple_return", "host", "tuple[host.io.Connection, ...]", []manifest.Parameter{{Name: "cap", Type: "host.io.Read"}}, "PP602"},
		{"unknown_parameter_type", "pure", "int", []manifest.Parameter{{Name: "row", Type: "host.io.Unknown"}}, "PP602"},
		{"unknown_return_type", "pure", "host.io.Unknown", []manifest.Parameter{}, "PP602"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			external := securityHost()
			external.Types = append(external.Types, manifest.Type{Name: "host.io.Row", Category: "value", Immutable: true, Source: "host.toml"})
			external.Functions = []manifest.Function{{Name: "host.io.operation", Kind: "async", Trust: tc.trust, Returns: tc.returns, Parameters: tc.parameters, Source: "host.toml"}}
			ds := securityVerify(t, map[string]string{"app": "def run() -> None:\n    return\n"}, external)
			if tc.code == "" && len(ds) != 0 || tc.code != "" && !boundaryCode(ds, tc.code) {
				t.Fatalf("expected diagnostic %q, got %+v", tc.code, ds)
			}
		})
	}
}

func TestBoundaryExpectedFailuresAreValues(t *testing.T) {
	for _, tc := range []struct{ name, source, code string }{
		{"explicit_result", "from typing import NamedTuple\n\nclass Result(NamedTuple):\n    value: int | None\n    error: str | None\ndef run(n: int) -> Result:\n    if n < 0:\n        return Result(None, 'negative input')\n    return Result(n, None)\n", ""},
		{"primitive_failure_allowed", "def run(n: int) -> int:\n    return 1 // n\n", ""},
		{"raise_failure", "def run() -> None:\n    raise ValueError('expected failure')\n", "PP003"},
		{"catch_failure", "def run(n: int) -> int:\n    try:\n        return 1 // n\n    except ZeroDivisionError:\n        return 0\n", "PP003"},
		{"exception_as_result", "def run() -> str:\n    return ValueError('expected failure')\n", "PP303"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ds := securityVerify(t, map[string]string{"app": tc.source}, nil)
			if tc.code == "" && len(ds) != 0 || tc.code != "" && !boundaryCode(ds, tc.code) {
				t.Fatalf("expected diagnostic %q, got %+v", tc.code, ds)
			}
		})
	}
}

// The same category restrictions apply to capabilities and host references;
// exercising both prevents one opaque kind from inheriting Pure Value rules.
func TestBoundaryAuthorityCategoryRestrictions(t *testing.T) {
	for _, typ := range []string{"Read", "Connection"} {
		for _, tc := range []struct{ name, source string }{
			{"returned", "def run(item: %s) -> %s:\n    return item\n"},
			{"tuple", "def run(item: %s) -> None:\n    packed = (item,)\n"},
			{"record_field", "from typing import NamedTuple\n\nclass Record(NamedTuple):\n    field: %s\n"},
			{"module_constant", "from typing import Final\nCONSTANT: Final[%s] = None\n"},
			{"compared", "def run(item: %s) -> bool:\n    return item == item\n"},
			{"formatted", "def run(item: %s) -> str:\n    return f'{item}'\n"},
			{"hashed", "def run(item: %s) -> int:\n    return hash(item)\n"},
			{"indexed", "def run(item: %s) -> int:\n    return item[0]\n"},
			{"attribute", "def run(item: %s) -> int:\n    return item.field\n"},
			{"converted", "def run(item: %s) -> str:\n    return str(item)\n"},
			{"passed_to_pure", "def identity(number: int) -> int:\n    return number\ndef run(item: %s) -> int:\n    return identity(item)\n"},
			{"passed_to_unknown", "def run(item: %s) -> int:\n    return unknown(item)\n"},
			{"aliased", "def run(item: %s) -> None:\n    other = item\n"},
			{"rebound", "def run(item: %s) -> None:\n    item = 0\n"},
		} {
			t.Run(typ+"_"+tc.name, func(t *testing.T) {
				args := []any{typ}
				if tc.name == "returned" {
					args = append(args, typ)
				}
				source := "from host.io import Read, Connection\n" + fmt.Sprintf(tc.source, args...)
				if ds := securityVerify(t, map[string]string{"app": source}, securityHost()); len(ds) == 0 {
					t.Fatalf("accepted forbidden %s use:\n%s", typ, source)
				}
			})
		}
	}
}
