package check

import (
	"fmt"
	"strings"
	"testing"
)

func nominalAuditDAGSource(depth int) string {
	var source strings.Builder
	source.WriteString("from purepy import value\n@value\nclass R0:\n    n: int\n")
	for i := 1; i <= depth; i++ {
		fmt.Fprintf(&source, "@value\nclass R%d:\n    left: R%d\n    right: R%d\n", i, i-1, i-1)
	}
	fmt.Fprintf(&source, "def equal(a: R%d, b: R%d) -> bool:\n    return a == b\n", depth, depth)
	return source.String()
}

func TestNominalAuditSharedRecordEquality(t *testing.T) {
	// Only 33 record declarations, but revisiting every field occurrence rather
	// than each exact record type expands this DAG into over 8 billion visits.
	source := nominalAuditDAGSource(32) + "def compose(a: R32, b: R32, maybe: R32 | None, items: tuple[R32, ...]) -> bool:\n    return a != b or maybe == None or a in items or items == items\n"
	if ds := securityVerify(t, map[string]string{"app": source}, nil); len(ds) != 0 {
		t.Fatal(ds)
	}
}

func TestNominalAuditSharedRecordsDoNotHideOpaqueEquality(t *testing.T) {
	prefix := "from host.data import Token\n" + nominalAuditDAGSource(32) + "@value\nclass Box:\n    safe: R32\n    tokens: tuple[Token | None, ...]\n"
	// Construction, indexing, and field forwarding must remain available even
	// when equality of that exact type is forbidden by an opaque nested field.
	allowed := "def wrap(safe: R32, token: Token | None) -> Box:\n    tokens: tuple[Token | None, ...] = (token,)\n    return Box(safe=safe, tokens=tokens)\ndef unwrap(box: Box) -> Token | None:\n    return box.tokens[0]\n"
	if ds := securityVerify(t, map[string]string{"app": prefix + allowed}, identityOpaqueHost()); len(ds) != 0 {
		t.Fatalf("opaque value construction/forwarding rejected: %v", ds)
	}
	comparisons := "def compare(a: Box, b: Box, maybe: Box | None, items: tuple[Box, ...]) -> bool:\n    return a == b or a != b or maybe == None or a in items\n"
	ds := securityVerify(t, map[string]string{"app": prefix + comparisons}, identityOpaqueHost())
	if len(ds) != 4 {
		t.Fatalf("each unsealed equality operation must be rejected, got %v", ds)
	}
	for _, d := range ds {
		if d.Code != "PP212" {
			t.Fatalf("unexpected rejection: %v", d)
		}
	}
}
