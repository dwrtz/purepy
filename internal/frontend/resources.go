package frontend

import sitter "github.com/tree-sitter/go-tree-sitter"

// These are implementation resource limits, not language typing rules. Count
// every native node, including punctuation and rejected syntax, before any Go
// recursive visitor or detached IR allocation. The sum of source spans also
// bounds overlapping Text values when a cache entry is serialized to JSON.
const (
	maxSyntaxDepth       = 512
	maxSyntaxNodes       = 100_000
	maxSyntaxTextBytes   = 64 << 20
	maxSyntaxDiagnostics = 1_024
)

func (a *adapter) checkTreeResources(root *sitter.Node) bool {
	cursor := root.Walk()
	defer cursor.Close()
	nodes, textBytes := 0, uint64(0)
	for {
		n := cursor.Node()
		nodes++
		textBytes += uint64(n.EndByte() - n.StartByte())
		reason := ""
		switch {
		case cursor.Depth() > maxSyntaxDepth:
			reason = "syntax tree exceeds nesting resource limit (512)"
		case nodes > maxSyntaxNodes:
			reason = "syntax tree exceeds node resource limit (100000)"
		case textBytes > maxSyntaxTextBytes:
			reason = "syntax tree exceeds overlapping-text resource limit (64 MiB)"
		}
		if reason != "" {
			a.report("PP003", reason, a.span(int(n.StartByte()), int(n.EndByte())))
			return false
		}
		if cursor.GotoFirstChild() {
			continue
		}
		for !cursor.GotoNextSibling() {
			if !cursor.GotoParent() {
				return true
			}
		}
	}
}
