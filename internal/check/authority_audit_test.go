package check

import (
	"fmt"
	"testing"
)

// Original authority parameters must survive the function-wide local-name
// collection and every control-flow binding site. The ordinary-data variants
// exercise the same paths without imposing the authority-only restriction.
func TestAuthorityAuditControlFlowBindings(t *testing.T) {
	for _, category := range []struct{ typ, code string }{{"Read", "PP313"}, {"Connection", "PP334"}} {
		for _, tc := range []struct{ name, body string }{
			{"loop_target", "    for item in (1,):\n        pass\n"},
			{"unreachable_loop_target", "    return\n    for item in (1,):\n        pass\n"},
			{"conditional_rebinding", "    if flag:\n        item = 1\n    else:\n        pass\n"},
			{"annotation_only", "    item: %s\n"},
			{"annotated_rebinding", "    item: int = 1\n"},
		} {
			t.Run(category.typ+"_"+tc.name, func(t *testing.T) {
				body := tc.body
				if tc.name == "annotation_only" {
					body = fmt.Sprintf(body, category.typ)
				}
				source := "from host.io import Read, Connection\ndef run(item: " + category.typ + ", flag: bool) -> None:\n" + body
				if ds := securityVerify(t, map[string]string{"app": source}, securityHost()); !boundaryCode(ds, category.code) {
					t.Fatalf("authority binding escaped %s: %+v\n%s", category.code, ds, source)
				}
			})
		}
	}
	for _, body := range []string{
		"    for item in (1,):\n        pass\n",
		"    if flag:\n        item = 1\n    else:\n        pass\n",
		"    item: int\n",
		"    item: int = 1\n",
	} {
		source := "def run(item: int, flag: bool) -> None:\n" + body
		if ds := securityVerify(t, map[string]string{"app": source}, nil); len(ds) != 0 {
			t.Fatalf("ordinary-data binding rejected: %+v\n%s", ds, source)
		}
	}
}

// Parentheses preserve the direct parameter expression. Operators that select
// or compute a value cannot become capability/reference transformations even
// when both branches happen to name the same original parameter.
func TestAuthorityAuditForwardingExpressions(t *testing.T) {
	prefix := "from host.io import Read, Connection, read\nasync def run(cap: Read, conn: Connection, flag: bool) -> bytes:\n"
	for _, tc := range []struct{ name, args, code string }{
		{"parenthesized", "(((cap))), (((conn)))", ""},
		{"keyword_reordered", "conn=(conn), cap=(cap)", ""},
		{"capability_boolean", "cap or cap, conn", "PP312"},
		{"reference_boolean", "cap, conn or conn", "PP334"},
		{"capability_constant_selection", "cap if True else cap, conn", "PP312"},
		{"reference_constant_selection", "cap, conn if True else conn", "PP334"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := prefix + "    return await read(" + tc.args + ")\n"
			ds := securityVerify(t, map[string]string{"app": source}, securityHost())
			if tc.code == "" && len(ds) != 0 || tc.code != "" && !boundaryCode(ds, tc.code) {
				t.Fatalf("expected %q, got %+v\n%s", tc.code, ds, source)
			}
		})
	}
}
