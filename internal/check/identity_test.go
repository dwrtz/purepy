package check

import (
	"fmt"
	"testing"

	"github.com/dwrtz/purepy/internal/manifest"
)

// Identity against exact None is safe for every Pure Value, independent of
// whether the value's type supports equality or can actually contain None.
func TestNoneIdentityPureValues(t *testing.T) {
	for _, typ := range []string{
		"None", "bool", "int", "float", "str", "bytes",
		"tuple[int, ...]", "tuple[tuple[str, ...], ...]",
		"bool | None", "int | None", "float | None", "str | None",
		"bytes | None", "tuple[int, ...] | None",
	} {
		for _, op := range []string{"is", "is not"} {
			for _, reversed := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/reversed=%t", typ, op, reversed), func(t *testing.T) {
					left, right := "item", "None"
					if reversed {
						left, right = right, left
					}
					specCoreCheck(t, fmt.Sprintf("def f(item: %s) -> bool:\n    return %s %s %s\n", typ, left, op, right), "")
				})
			}
		}
	}
}

func TestNoneIdentityExactNoneExpressions(t *testing.T) {
	prefix := "from typing import Final\nNOTHING: Final[None] = None\ndef nothing() -> None:\n    return\n"
	for _, expr := range []string{"None", "empty", "NOTHING", "nothing()"} {
		for _, op := range []string{"is", "is not"} {
			for _, reversed := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/reversed=%t", expr, op, reversed), func(t *testing.T) {
					left, right := "item", expr
					if reversed {
						left, right = right, left
					}
					specCoreCheck(t, prefix+fmt.Sprintf("def f(item: int, empty: None) -> bool:\n    return %s %s %s\n", left, op, right), "")
				})
			}
		}
	}
	specCoreCheck(t, "async def nothing() -> None:\n    return\nasync def f(item: int) -> bool:\n    return item is await nothing()\n", "")
}

func identityOpaqueHost() *manifest.Set {
	return &manifest.Set{
		Modules: []manifest.Module{{Name: "host.data", ImportSafe: true, Source: "host.toml"}},
		Types:   []manifest.Type{{Name: "host.data.Token", Category: "value", Immutable: true, Source: "host.toml"}},
	}
}

func TestNoneIdentityNominalValues(t *testing.T) {
	prefix := "from purepy import value\nfrom host.data import Token\n@value\nclass Plain:\n    number: int\n@value\nclass Wrapped:\n    token: Token\n@value\nclass Nested:\n    values: tuple[Wrapped, ...]\n"
	for _, typ := range []string{
		"Plain", "Plain | None", "Token", "Token | None",
		"Wrapped", "Wrapped | None", "Nested", "Nested | None",
		"tuple[Token, ...]", "tuple[Token, ...] | None",
	} {
		for _, op := range []string{"is", "is not"} {
			for _, reversed := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/reversed=%t", typ, op, reversed), func(t *testing.T) {
					left, right := "item", "None"
					if reversed {
						left, right = right, left
					}
					source := prefix + fmt.Sprintf("def f(item: %s) -> bool:\n    return %s %s %s\n", typ, left, op, right)
					if ds := securityVerify(t, map[string]string{"app": source}, identityOpaqueHost()); len(ds) != 0 {
						t.Fatalf("safe nominal None identity rejected: %v\n%s", ds, source)
					}
				})
			}
		}
	}
	// None comparisons must not expose non-None allocation identity or enable
	// operations that could invoke an opaque value's unsealed equality.
	for _, typ := range []string{"Token", "Wrapped", "Nested", "tuple[Token, ...]"} {
		for _, op := range []string{"==", "!=", "<", "<=", "is", "is not"} {
			t.Run(typ+"/reject_"+op, func(t *testing.T) {
				source := prefix + fmt.Sprintf("def f(item: %s) -> bool:\n    return item %s item\n", typ, op)
				if ds := securityVerify(t, map[string]string{"app": source}, identityOpaqueHost()); !boundaryCode(ds, "PP212") {
					t.Fatalf("expected PP212 for unsealed comparison, got %v\n%s", ds, source)
				}
			})
		}
		for _, expr := range []string{"item == None", "None != item"} {
			t.Run(typ+"/optional_"+expr, func(t *testing.T) {
				source := prefix + fmt.Sprintf("def f(item: %s | None) -> bool:\n    return %s\n", typ, expr)
				if ds := securityVerify(t, map[string]string{"app": source}, identityOpaqueHost()); !boundaryCode(ds, "PP212") {
					t.Fatalf("expected PP212 for unsealed optional equality, got %v\n%s", ds, source)
				}
			})
		}
	}
}

func TestNoneIdentityRequiresAnExactNoneOperand(t *testing.T) {
	for _, typ := range []string{"bool", "int", "float", "str", "bytes", "tuple[int, ...]", "int | None"} {
		for _, op := range []string{"is", "is not"} {
			for _, right := range []string{"left", "right"} {
				t.Run(typ+"/"+op+"/"+right, func(t *testing.T) {
					source := fmt.Sprintf("def f(left: %s, right: %s) -> bool:\n    return left %s %s\n", typ, typ, op, right)
					specCoreCheck(t, source, "PP212")
				})
			}
		}
	}
	// The identity exception does not change equality or ordering signatures.
	for _, expr := range []string{"item == None", "None != item", "item < None", "None <= item"} {
		t.Run(expr, func(t *testing.T) {
			specCoreCheck(t, "def f(item: int) -> bool:\n    return "+expr+"\n", "PP212")
		})
	}
}

func TestNoneIdentityChainedComparisons(t *testing.T) {
	for _, tc := range []struct{ expr, code string }{
		{"None is left is None", ""},
		{"left is not None is not right", ""},
		{"None is left == right", ""},
		{"left == right is not None", ""},
		{"left is right is None", "PP212"},
		{"None is left is not right", "PP212"},
		{"left is None < right", "PP212"},
	} {
		t.Run(tc.expr, func(t *testing.T) {
			specCoreCheck(t, "def f(left: int, right: int) -> bool:\n    return "+tc.expr+"\n", tc.code)
		})
	}
	specCoreCheck(t, "def f(left: int | None, right: int | None) -> bool:\n    return None is left is right\n", "PP212")
}

func TestNoneIdentityNarrowingAndReassignment(t *testing.T) {
	for _, tc := range []struct{ name, source, code string }{
		{"repeated_non_none_guard", "def f(item: int | None) -> bool:\n    if item is not None:\n        return item is not None\n    return item is None\n", ""},
		{"returning_none_branch", "def f(item: int | None) -> bool:\n    if item is None:\n        return None is item\n    return None is not item\n", ""},
		{"repeated_short_circuit_guard", "def f(item: int | None) -> bool:\n    return item is not None and None is not item\n", ""},
		{"conditional_after_guard", "def f(item: int | None) -> bool:\n    return item is not None if item is not None else item is None\n", ""},
		{"narrowed_none_operand", "def f(item: int | None, other: int | None) -> bool:\n    if item is None:\n        return item is other\n    return False\n", ""},
		{"typed_none_does_not_narrow", "def f(item: int | None, empty: None) -> int:\n    if item is not empty:\n        return item + 1\n    return 0\n", "PP209"},
		{"reversed_typed_none_does_not_narrow", "def f(item: int | None, empty: None) -> int:\n    if empty is not item:\n        return item + 1\n    return 0\n", "PP209"},
		{"reassigned_none", "def f(item: int | None) -> bool:\n    if item is None:\n        return True\n    item = None\n    return item is None\n", ""},
		{"reassigned_value", "def f(item: int | None) -> bool:\n    if item is not None:\n        return True\n    item = 1\n    return item is not None\n", ""},
		{"reassigned_optional", "def f(item: int | None, other: int | None) -> bool:\n    if item is None:\n        return True\n    item = other\n    return None is item\n", ""},
		{"guard_after_reassignment_narrows", "def f(item: int | None, other: int | None) -> int:\n    if item is None:\n        return 0\n    item = other\n    if item is not None:\n        return item + 1\n    return 0\n", ""},
		{"reassignment_invalidates_arithmetic", "def f(item: int | None, other: int | None) -> int:\n    if item is None:\n        return 0\n    item = other\n    checked = item is None\n    return item + 1\n", "PP209"},
	} {
		t.Run(tc.name, func(t *testing.T) { specCoreCheck(t, tc.source, tc.code) })
	}
}

func TestNoneIdentityRejectsNonValues(t *testing.T) {
	prefix := "from host.io import Read, Connection\nfrom purepy import value\n@value\nclass Record:\n    number: int\ndef helper() -> None:\n    return\n"
	for _, tc := range []struct{ name, params, operand, code string }{
		{"capability", "item: Read", "item", "PP313"},
		{"host_reference", "item: Connection", "item", "PP334"},
		{"ephemeral_range", "", "range(1)", "PP304"},
		{"function_declaration", "", "helper", "PP301"},
		{"record_declaration", "", "Record", "PP301"},
		{"opaque_type_declaration", "", "Read", "PP301"},
	} {
		for _, op := range []string{"is", "is not"} {
			for _, reversed := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/reversed=%t", tc.name, op, reversed), func(t *testing.T) {
					left, right := tc.operand, "None"
					if reversed {
						left, right = right, left
					}
					source := prefix + fmt.Sprintf("def f(%s) -> bool:\n    return %s %s %s\n", tc.params, left, op, right)
					if ds := securityVerify(t, map[string]string{"app": source}, securityHost()); !boundaryCode(ds, tc.code) {
						t.Fatalf("expected %s for forbidden operand, got %v\n%s", tc.code, ds, source)
					}
				})
			}
		}
	}
}
