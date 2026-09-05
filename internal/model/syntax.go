// Package model contains the parser-independent PurePy syntax representation.
package model

// Span is a half-open byte range with one-based Unicode code-point positions.
// EndLine and EndColumn point immediately after the final source character.
type Span struct {
	File      string `json:"file"`
	Start     int    `json:"start"`
	End       int    `json:"end"`
	Line      int    `json:"line"`
	Column    int    `json:"column"`
	EndLine   int    `json:"end_line"`
	EndColumn int    `json:"end_column"`
}

// Node is a normalized syntax node, independent of parser handles or lifetimes.
// Text retains the original source spelling. Fields contains singleton children,
// Lists ordered children, and Attr scalar properties such as names and operators.
type Node struct {
	Kind   string             `json:"kind"`
	Text   string             `json:"text,omitempty"`
	Span   Span               `json:"span"`
	Fields map[string]*Node   `json:"fields,omitempty"`
	Lists  map[string][]*Node `json:"lists,omitempty"`
	Attr   map[string]string  `json:"attr,omitempty"`
}

func (n *Node) Get(key string) *Node {
	if n == nil {
		return nil
	}
	return n.Fields[key]
}

func (n *Node) Items(key string) []*Node {
	if n == nil {
		return nil
	}
	return n.Lists[key]
}

func (n *Node) A(key string) string {
	if n == nil {
		return ""
	}
	return n.Attr[key]
}
