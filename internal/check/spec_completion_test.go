package check

import (
	"fmt"
	"testing"
)

// Exercise forbidden return expressions with otherwise valid Pure Value
// signatures. Unsupported annotations alone must not be the reason these fail.
func TestSpecCompletionReturnExpressions(t *testing.T) {
	for _, tc := range []struct{ name, prefix, expression, code string }{
		{"range", "", "range(3)", "PP304"},
		{"nested_range", "", "(range(3),)", "PP304"},
		{"conditional_range", "", "range(3) if True else range(4)", "PP304"},
		{"function", "def helper() -> int:\n    return 1\n", "helper", "PP205"},
		{"record_class", "from typing import NamedTuple\n\nclass Record(NamedTuple):\n    value: int\n", "Record", "PP301"},
		{"support_base", "from typing import NamedTuple\n", "NamedTuple", "PP301"},
		{"suspended_call", "async def helper() -> int:\n    return 1\n", "helper()", "PP401"},
		{"nested_suspended_call", "async def helper() -> int:\n    return 1\n", "(helper(),)", "PP401"},
		{"generator_expression", "", "(item for item in (1,))", "PP003"},
	} {
		for _, kind := range []string{"def", "async def"} {
			t.Run(tc.name+"/"+kind, func(t *testing.T) {
				specCoreCheck(t, tc.prefix+fmt.Sprintf("%s run() -> int:\n    return %s\n", kind, tc.expression), tc.code)
			})
		}
	}
	for _, authority := range []struct{ name, code string }{{"Read", "PP313"}, {"Connection", "PP334"}} {
		t.Run(authority.name, func(t *testing.T) {
			source := "from host.io import Read, Connection\ndef run(item: " + authority.name + ") -> int:\n    return item\n"
			if ds := securityVerify(t, map[string]string{"app": source}, securityHost()); !boundaryCode(ds, authority.code) {
				t.Fatalf("authority return expression did not reject with %s: %v", authority.code, ds)
			}
		})
	}
	// Modules have no admitted binding form. Check both attempted import forms
	// explicitly, instead of citing a generic forbidden-name test as evidence.
	t.Run("module_plain_import", func(t *testing.T) {
		specCoreCheck(t, "import helper\ndef run() -> int:\n    return helper\n", "PP003")
	})
	t.Run("module_from_import", func(t *testing.T) {
		ds := securityVerify(t, map[string]string{
			"pkg": "", "pkg.helper": "def one() -> int:\n    return 1\n",
			"app": "from pkg import helper\ndef run() -> int:\n    return helper\n",
		}, nil)
		if !boundaryCode(ds, "PP104") {
			t.Fatalf("importing a module as a returnable symbol did not reject with PP104: %v", ds)
		}
	})
	// The corresponding evaluated values remain valid in sync and async code.
	specCoreCheck(t, "def helper() -> int:\n    return 1\ndef run() -> int:\n    total = 0\n    for value in range(3):\n        total = total + helper()\n    return total\nasync def async_helper() -> int:\n    return helper()\nasync def async_run() -> int:\n    return await async_helper()\n", "")
}

func TestSpecCompletionArgumentUnpacking(t *testing.T) {
	prefix := "from typing import NamedTuple\n\nclass Record(NamedTuple):\n    number: int\ndef helper(number: int) -> int:\n    return number\nasync def async_helper(number: int) -> int:\n    return number\n"
	for _, tc := range []struct{ name, expression string }{
		{"function_star", "helper(*items)"},
		{"function_double_star", "helper(**items)"},
		{"constructor_star", "Record(*items)"},
		{"constructor_double_star", "Record(**items)"},
		{"async_star", "await async_helper(*items)"},
		{"async_double_star", "await async_helper(**items)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			specCoreCheck(t, prefix+"async def run(items: tuple[int, ...]) -> int:\n    return "+tc.expression+"\n", "PP003")
		})
	}
	specCoreCheck(t, prefix+"async def run() -> int:\n    item = Record(number=helper(number=1))\n    return await async_helper(number=item.number)\n", "")
}

func TestSpecCompletionNestedUnsupportedTypes(t *testing.T) {
	// A supported outer container cannot launder an unsupported inner type into
	// the closed annotation grammar. Exercise both parameters and return types.
	for _, forbidden := range []string{"Any", "object", "Callable", "list[int]", "dict[str, int]", "Iterator[int]", "Generator[int]", "Coroutine[int]", "Task[int]", "int | str", "tuple[int, list[str]]", "\"int\""} {
		for _, wrap := range []string{"tuple[%s, ...]", "(%s) | None", "tuple[(%s) | None, ...]"} {
			annotation := fmt.Sprintf(wrap, forbidden)
			t.Run(annotation, func(t *testing.T) {
				specCoreCheck(t, "def parameter(item: "+annotation+") -> None:\n    return\n", "PP203")
				specCoreCheck(t, "def result() -> "+annotation+":\n    return None\n", "PP203")
			})
		}
	}
	specCoreCheck(t, "def identity(item: tuple[tuple[int, ...] | None, ...]) -> tuple[tuple[int, ...] | None, ...]:\n    return item\n", "")
}
