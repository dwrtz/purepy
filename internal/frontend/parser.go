// Package frontend parses Python source without importing or executing it.
// Parser-specific nodes are confined to this package and released before Parse
// returns. Each invocation owns its parser, so calls can run concurrently.
package frontend

import (
	"bytes"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/dwrtz/purepy/internal/diag"
	"github.com/dwrtz/purepy/internal/model"
	"github.com/dwrtz/purepy/internal/unicodenames"
	sitter "github.com/tree-sitter/go-tree-sitter"
	python "github.com/tree-sitter/tree-sitter-python/bindings/go"
	"golang.org/x/text/unicode/norm"
)

// Version is part of the cache identity; change it whenever lowering changes.
const Version = "python-3.14/tree-sitter-python-26855eab/ir-3/unicode-" + unicodenames.UnicodeVersion

type adapter struct {
	path        string
	source      []byte
	lines       []int
	diagnostics []diag.Diagnostic
}

// Parse produces a normalized Module and all syntax/lowering diagnostics. A
// nonempty diagnostic list must prevent verification, even if a partial IR exists.
func Parse(path string, source []byte) (*model.Node, []diag.Diagnostic) {
	return parse(path, source, nil)
}

// ParseTimings separates parser work from normalization into detached IR. These
// are elapsed durations in the calling worker, not CPU time. Parse includes
// source validation, parser setup, syntax checks, and native tree/parser cleanup.
// Lower includes IR construction, name normalization, and diagnostic ordering.
type ParseTimings struct {
	Parse time.Duration
	Lower time.Duration
}

// ParseTimed produces exactly the same result as Parse and measures its work.
// Ordinary Parse calls do not read the clock.
func ParseTimed(path string, source []byte) (*model.Node, []diag.Diagnostic, ParseTimings) {
	var timings ParseTimings
	start := time.Now()
	tree, ds := parse(path, source, &timings)
	// parse's lowering defer runs before its native-resource cleanup defers,
	// so cleanup is included in Parse and the two intervals cannot overlap.
	timings.Parse = time.Since(start) - timings.Lower
	return tree, ds, timings
}

func parse(path string, source []byte, timings *ParseTimings) (*model.Node, []diag.Diagnostic) {
	a := &adapter{path: path, source: source, lines: []int{0}}
	for i, b := range source {
		if b == '\n' {
			a.lines = append(a.lines, i+1)
		}
	}
	if !utf8.Valid(source) {
		a.report("PP002", "source must be valid UTF-8", a.span(0, len(source)))
		return nil, a.diagnostics
	}
	if i := bytes.IndexByte(source, 0); i >= 0 {
		a.report("PP002", "source contains a NUL byte", a.span(i, i+1))
		return nil, a.diagnostics
	}
	// The adapter reads UTF-8 bytes, so a different coding cookie would change
	// literal values when CPython later reads the same source file.
	for i, line := range bytes.SplitN(source, []byte{'\n'}, 3) {
		if i >= 2 {
			break
		}
		if match := encodingCookie.FindSubmatch(line); len(match) > 1 {
			encoding := strings.ToLower(strings.ReplaceAll(string(match[1]), "_", "-"))
			if encoding != "utf-8" && encoding != "utf8" {
				a.report("PP003", "only UTF-8 source encoding is supported", a.span(a.lines[i], a.lines[i]+len(line)))
				return nil, a.diagnostics
			}
		}
	}
	p := sitter.NewParser()
	defer p.Close()
	if err := p.SetLanguage(sitter.NewLanguage(python.Language())); err != nil {
		a.report("PP002", fmt.Sprintf("cannot initialize Python parser: %v", err), a.span(0, 0))
		return nil, a.diagnostics
	}
	tree := p.Parse(parserSource(source), nil)
	if tree == nil {
		a.report("PP002", "Python parser could not produce a syntax tree", a.span(0, 0))
		return nil, a.diagnostics
	}
	defer tree.Close()
	root := tree.RootNode()
	a.errors(root)
	a.trivia(root)
	if len(a.diagnostics) != 0 {
		return nil, a.diagnostics
	}
	if timings != nil {
		start := time.Now()
		defer func() { timings.Lower = time.Since(start) }()
	}
	module := a.node("Module", root)
	module.Lists["body"] = a.body(root)
	normalizeNames(module)
	for _, item := range module.Items("body") {
		if item.Kind == "Record" {
			for _, child := range item.Items("body") {
				manglePrivateNames(child, item.A("name"))
			}
		}
	}
	sort.SliceStable(a.diagnostics, func(i, j int) bool {
		x, y := a.diagnostics[i], a.diagnostics[j]
		if x.Span.Start != y.Span.Start {
			return x.Span.Start < y.Span.Start
		}
		if x.Code != y.Code {
			return x.Code < y.Code
		}
		return x.Message < y.Message
	})
	return module, a.diagnostics
}

// parserSource makes bare bytes prefixes raw in the grammar's view. Python
// bytes and raw strings have identical quote/backslash boundaries; the adapter
// checks the original bytes' ASCII and escape rules independently. This avoids
// the grammar's erroneous Unicode-escape recovery in bytes, including errors
// above a string node. Never rewrite part of an identifier or a combined prefix
// such as fb (invalid) or rb (already raw). Replacements inside comments or
// literal text cannot alter their boundaries. All IR text, prefix classification,
// and diagnostics still read the original source with identical byte offsets.
func parserSource(source []byte) []byte {
	var normalized []byte
	for i := 0; i+1 < len(source); i++ {
		if source[i] != 'b' && source[i] != 'B' || source[i+1] != '\'' && source[i+1] != '"' {
			continue
		}
		if i > 0 {
			prev := source[i-1]
			if prev >= 0x80 || prev >= 'a' && prev <= 'z' || prev >= 'A' && prev <= 'Z' || prev >= '0' && prev <= '9' || prev == '_' {
				continue
			}
		}
		if normalized == nil {
			normalized = bytes.Clone(source)
		}
		normalized[i] = 'r'
	}
	if normalized == nil {
		return source
	}
	return normalized
}

func (a *adapter) span(start, end int) model.Span {
	li := sort.Search(len(a.lines), func(i int) bool { return a.lines[i] > start }) - 1
	ei := sort.Search(len(a.lines), func(i int) bool { return a.lines[i] > end }) - 1
	return model.Span{File: a.path, Start: start, End: end, Line: li + 1,
		Column: utf8.RuneCount(a.source[a.lines[li]:start]) + 1, EndLine: ei + 1,
		EndColumn: utf8.RuneCount(a.source[a.lines[ei]:end]) + 1}
}

func (a *adapter) node(kind string, n *sitter.Node) *model.Node {
	if n == nil {
		return nil
	}
	return &model.Node{Kind: kind, Text: n.Utf8Text(a.source),
		Span:   a.span(int(n.StartByte()), int(n.EndByte())),
		Fields: map[string]*model.Node{}, Lists: map[string][]*model.Node{}, Attr: map[string]string{}}
}

func (a *adapter) report(code, message string, span model.Span) {
	a.diagnostics = append(a.diagnostics, diag.New(code, message, span))
}

// CPython normalizes identifiers using NFKC before binding them. Normalize every
// identifier-bearing field, keeping Text and spans faithful to the source.
func normalizeNames(n *model.Node) {
	if n == nil {
		return
	}
	for _, key := range []string{"name", "alias", "module"} {
		if value, ok := n.Attr[key]; ok {
			n.Attr[key] = norm.NFKC.String(value)
		}
	}
	for _, child := range n.Fields {
		normalizeNames(child)
	}
	for _, children := range n.Lists {
		for _, child := range children {
			normalizeNames(child)
		}
	}
}

// Python rewrites private identifiers lexically inside a class body. The
// normalized field and annotation names must match the actual slotted record.
func manglePrivateNames(n *model.Node, class string) {
	if n == nil {
		return
	}
	class = strings.TrimLeft(class, "_")
	if name := n.A("name"); class != "" && strings.HasPrefix(name, "__") && !strings.HasSuffix(name, "__") {
		n.Attr["name"] = "_" + class + name
	}
	for _, child := range n.Fields {
		manglePrivateNames(child, class)
	}
	for _, children := range n.Lists {
		for _, child := range children {
			manglePrivateNames(child, class)
		}
	}
}

func (a *adapter) errors(n *sitter.Node) {
	if _, _, isBytes, complete := a.bytesContent(n); isBytes {
		if !complete {
			a.report("PP002", "invalid bytes literal delimiter or unescaped newline", a.node("", n).Span)
		}
		return // Complete bytes content is checked during lowering.
	}
	if n.IsError() || n.IsMissing() {
		message := "invalid Python syntax"
		if n.IsMissing() {
			message = "missing " + n.Kind()
		}
		a.report("PP002", message, a.span(int(n.StartByte()), int(n.EndByte())))
		return
	}
	for i := uint(0); i < n.ChildCount(); i++ {
		a.errors(n.Child(i))
	}
}

// The upstream grammar treats several Unicode separators and vertical tabs as
// whitespace even though Python rejects them outside comments and literals.
// Inspect only gaps between leaf tokens, leaving literal/identifier text intact.
func (a *adapter) trivia(root *sitter.Node) {
	previous := 0
	checkGap := func(end int) {
		if end < previous {
			return
		}
		for offset, r := range string(a.source[previous:end]) {
			if r == ' ' || r == '\t' || r == '\r' || r == '\n' || r == '\f' || r == '\ufeff' && previous+offset == 0 {
				continue
			}
			a.report("PP002", "invalid character outside a Python token", a.span(previous+offset, previous+offset+utf8.RuneLen(r)))
		}
	}
	var walk func(*sitter.Node)
	walk = func(n *sitter.Node) {
		_, _, bytesLiteral, _ := a.bytesContent(n)
		if bytesLiteral || n.ChildCount() == 0 || n.Kind() == "string_content" || n.Kind() == "format_specifier" {
			checkGap(int(n.StartByte()))
			previous = int(n.EndByte())
			return
		}
		for i := uint(0); i < n.ChildCount(); i++ {
			walk(n.Child(i))
		}
	}
	if root.ChildCount() > 0 {
		walk(root)
	}
	checkGap(len(a.source))
}

func named(n *sitter.Node) []*sitter.Node {
	if n == nil {
		return nil
	}
	var out []*sitter.Node
	for i := uint(0); i < n.NamedChildCount(); i++ {
		c := n.NamedChild(i)
		if c.Kind() != "comment" && c.Kind() != "line_continuation" {
			out = append(out, c)
		}
	}
	return out
}

func (a *adapter) text(n *sitter.Node) string {
	if n == nil {
		return ""
	}
	return n.Utf8Text(a.source)
}

func (a *adapter) qualified(n *sitter.Node) string {
	if n == nil {
		return ""
	}
	if n.Kind() != "dotted_name" {
		return a.text(n)
	}
	var parts []string
	for _, child := range named(n) {
		parts = append(parts, a.text(child))
	}
	return strings.Join(parts, ".")
}

func (a *adapter) flag(out *model.Node, reason string, n *sitter.Node) {
	if out.Attr["unsupported"] != "" {
		out.Attr["unsupported"] += "; "
	}
	out.Attr["unsupported"] += reason
	a.report("PP003", reason+" is not supported in PurePy 0.1", a.span(int(n.StartByte()), int(n.EndByte())))
}

func (a *adapter) unsupported(n *sitter.Node) *model.Node {
	out := a.node("Unsupported", n)
	out.Attr["syntax"] = n.Kind()
	a.flag(out, strings.ReplaceAll(n.Kind(), "_", " "), n)
	// Preserve descendants without diagnosing every detail of an already rejected
	// construct. The enclosing range is the actionable rejection location.
	for _, c := range named(n) {
		out.Lists["children"] = append(out.Lists["children"], a.raw(c))
	}
	return out
}

func (a *adapter) raw(n *sitter.Node) *model.Node {
	out := a.node("Unsupported", n)
	out.Attr["syntax"] = n.Kind()
	for _, c := range named(n) {
		out.Lists["children"] = append(out.Lists["children"], a.raw(c))
	}
	return out
}

func (a *adapter) body(n *sitter.Node) []*model.Node {
	var out []*model.Node
	children := named(n)
	a.layout(n, children)
	for _, c := range children {
		item := a.lower(c)
		// The grammar marks expression_statement as a hidden supertype. Some
		// expressions therefore occur directly beneath module/block nodes.
		switch item.Kind {
		case "Function", "Record", "Import", "Assign", "If", "For", "While", "Return", "Break", "Continue", "Pass", "ExprStmt", "Unsupported":
		default:
			statement := a.node("ExprStmt", c)
			statement.Fields["value"] = item
			item = statement
		}
		out = append(out, item)
	}
	if n != nil && n.Kind() == "block" && len(out) == 0 {
		a.report("PP002", "expected a statement in the suite", a.span(int(n.StartByte()), int(n.EndByte())))
	}
	return out
}

// Tree-sitter's recovery-oriented indentation scanner accepts some layouts
// rejected by Python's tokenizer, including an indented top-level statement and
// ambiguous tab/space indentation. Validate suite membership against both of
// Python's indentation column calculations before trusting the lowered tree.
func (a *adapter) layout(n *sitter.Node, children []*sitter.Node) {
	if n == nil || len(children) == 0 {
		return
	}
	// Recovery can also split a malformed same-line sequence into statements
	// without marking an ERROR (notably after ignored bytes Unicode escapes).
	for i := 1; i < len(children); i++ {
		start, end := int(children[i-1].EndByte()), int(children[i].StartByte())
		if !statementSeparator(a.source[start:end]) {
			a.report("PP002", "statements require a newline or semicolon", a.span(end, end))
		}
	}
	base, alternate := 0, 0
	if n.Kind() == "block" {
		var isLineStart bool
		base, alternate, isLineStart = a.indentation(int(children[0].StartByte()))
		if !isLineStart {
			return
		} // A simple suite after a colon on the same line.
		if parent := n.Parent(); parent != nil {
			parentIndent, parentAlternate, _ := a.indentation(int(parent.StartByte()))
			if base <= parentIndent || alternate <= parentAlternate {
				a.report("PP002", "expected an indented suite", a.node("", children[0]).Span)
			}
		}
	}
	for _, child := range children {
		col, alt, isLineStart := a.indentation(int(child.StartByte()))
		if isLineStart && (col != base || alt != alternate) {
			a.report("PP002", "inconsistent or unexpected indentation", a.node("", child).Span)
		}
	}
}

func statementSeparator(gap []byte) bool {
	for i := 0; i < len(gap); i++ {
		switch gap[i] {
		case '\\':
			if i+1 < len(gap) && gap[i+1] == '\r' {
				i++
			}
			if i+1 < len(gap) && gap[i+1] == '\n' {
				i++
			}
		case '#':
			// A comment's newline terminates a statement even if its text ends
			// with a backslash. Semicolons inside comments are not separators.
			for i < len(gap) && gap[i] != '\n' && gap[i] != '\r' {
				i++
			}
			return i < len(gap)
		case ';', '\n', '\r':
			return true
		}
	}
	return false
}

func (a *adapter) indentation(offset int) (column, alternate int, isLineStart bool) {
	line := sort.Search(len(a.lines), func(i int) bool { return a.lines[i] > offset }) - 1
	for i := a.lines[line]; i < offset; i++ {
		if i == 0 && bytes.HasPrefix(a.source, []byte{0xef, 0xbb, 0xbf}) {
			i += 2
			continue
		}
		switch a.source[i] {
		case ' ':
			column++
			alternate++
		case '\t':
			column = (column/8 + 1) * 8
			alternate++
		case '\f':
			column = 0
			alternate = 0
		default:
			return column, alternate, false
		}
	}
	return column, alternate, true
}

func (a *adapter) lower(n *sitter.Node) *model.Node {
	if n == nil {
		return nil
	}
	f := func(name string) *model.Node { return a.lower(n.ChildByFieldName(name)) }
	children := named(n)
	switch n.Kind() {
	case "type", "parenthesized_expression":
		if len(children) == 1 {
			return a.lower(children[0])
		}
	case "decorated_definition":
		out := f("definition")
		for _, c := range children {
			if c.Kind() == "decorator" {
				dc := named(c)
				if len(dc) == 1 {
					out.Lists["decorators"] = append(out.Lists["decorators"], a.lower(dc[0]))
				}
			}
		}
		out.Span = a.span(int(n.StartByte()), int(n.EndByte()))
		out.Text = a.text(n)
		return out
	case "function_definition", "class_definition":
		kind := "Function"
		if n.Kind() == "class_definition" {
			kind = "Record"
		}
		out := a.node(kind, n)
		out.Attr["name"] = a.text(n.ChildByFieldName("name"))
		out.Lists["body"] = a.body(n.ChildByFieldName("body"))
		if tp := n.ChildByFieldName("type_parameters"); tp != nil {
			a.flag(out, "type parameters", tp)
		}
		if kind == "Function" {
			out.Attr["async"] = "false"
			if n.Child(0).Kind() == "async" {
				out.Attr["async"] = "true"
			}
			for _, c := range named(n.ChildByFieldName("parameters")) {
				out.Lists["params"] = append(out.Lists["params"], a.param(c))
			}
			out.Fields["returns"] = f("return_type")
		} else {
			for _, c := range named(n.ChildByFieldName("superclasses")) {
				out.Lists["bases"] = append(out.Lists["bases"], a.lower(c))
			}
		}
		return out
	case "import_from_statement", "import_statement", "future_import_statement":
		out := a.node("Import", n)
		module := n.ChildByFieldName("module_name")
		out.Attr["module"] = a.qualified(module)
		if n.Kind() == "future_import_statement" {
			out.Attr["module"] = "__future__"
			a.flag(out, "future imports", n)
		}
		if n.Kind() == "import_statement" {
			a.flag(out, "plain imports", n)
		}
		if module != nil && module.Kind() == "relative_import" {
			a.flag(out, "relative imports", module)
		}
		for _, c := range children {
			if module != nil && c.StartByte() == module.StartByte() && c.EndByte() == module.EndByte() {
				continue
			}
			name := a.node("Name", c)
			if c.Kind() == "aliased_import" {
				name.Attr["name"] = a.qualified(c.ChildByFieldName("name"))
				name.Attr["alias"] = a.text(c.ChildByFieldName("alias"))
				a.flag(out, "import aliases", c)
			} else {
				name.Attr["name"] = a.qualified(c)
			}
			if n.Kind() == "import_from_statement" && strings.Contains(name.A("name"), ".") {
				a.report("PP002", "from imports require a single identifier for each imported symbol", name.Span)
			}
			if c.Kind() == "wildcard_import" {
				a.flag(out, "star imports", c)
			}
			out.Lists["names"] = append(out.Lists["names"], name)
		}
		return out
	case "expression_statement":
		if len(children) == 1 {
			value := a.lower(children[0])
			if children[0].Kind() == "assignment" {
				return value
			}
			out := a.node("ExprStmt", n)
			out.Fields["value"] = value
			return out
		}
	case "assignment":
		out := a.node("Assign", n)
		out.Fields["target"] = f("left")
		out.Fields["annotation"] = f("type")
		out.Fields["value"] = f("right")
		if right := n.ChildByFieldName("right"); right != nil && right.Kind() == "assignment" {
			a.flag(out, "chained assignment", n)
		}
		return out
	case "if_statement", "elif_clause":
		out := a.node("If", n)
		out.Fields["test"] = f("condition")
		out.Lists["body"] = a.body(n.ChildByFieldName("consequence"))
		tail := out
		for _, c := range children {
			if c.Kind() == "elif_clause" {
				next := a.lower(c)
				tail.Lists["else"] = []*model.Node{next}
				tail = next
			}
			if c.Kind() == "else_clause" {
				tail.Lists["else"] = a.body(c.ChildByFieldName("body"))
			}
		}
		return out
	case "for_statement", "while_statement":
		kind := "For"
		if n.Kind() == "while_statement" {
			kind = "While"
		}
		out := a.node(kind, n)
		out.Lists["body"] = a.body(n.ChildByFieldName("body"))
		if kind == "For" {
			out.Fields["target"] = f("left")
			out.Fields["iter"] = f("right")
			if n.Child(0).Kind() == "async" {
				a.flag(out, "async for", n)
			}
		} else {
			out.Fields["test"] = f("condition")
		}
		if alt := n.ChildByFieldName("alternative"); alt != nil {
			out.Lists["else"] = a.body(alt.ChildByFieldName("body"))
			a.flag(out, "loop else clauses", alt)
		}
		return out
	case "return_statement":
		out := a.node("Return", n)
		if len(children) > 0 {
			out.Fields["value"] = a.lower(children[0])
		}
		return out
	case "pass_statement", "break_statement", "continue_statement":
		kind := map[string]string{"pass_statement": "Pass", "break_statement": "Break", "continue_statement": "Continue"}[n.Kind()]
		return a.node(kind, n)
	case "identifier", "keyword_identifier":
		out := a.node("Name", n)
		out.Attr["name"] = a.text(n)
		if out.Attr["name"] == "async" || out.Attr["name"] == "await" {
			a.report("PP002", "reserved Python keyword used as a name", out.Span)
		}
		return out
	case "none", "true", "false", "integer", "float", "ellipsis":
		out := a.node("Literal", n)
		out.Attr["type"] = map[string]string{"none": "None", "true": "bool", "false": "bool", "integer": "int", "float": "float", "ellipsis": "ellipsis"}[n.Kind()]
		if n.Kind() == "integer" || n.Kind() == "float" {
			a.number(out, n)
		}
		return out
	case "string", "concatenated_string":
		return a.string(n)
	case "tuple", "expression_list", "tuple_expression", "pattern_list", "tuple_pattern":
		out := a.node("Tuple", n)
		for _, c := range children {
			out.Lists["elements"] = append(out.Lists["elements"], a.lower(c))
		}
		return out
	case "attribute", "member_type":
		out := a.node("Attribute", n)
		if n.Kind() == "member_type" {
			out.Fields["value"] = a.lower(children[0])
			out.Attr["name"] = a.text(children[1])
		} else {
			out.Fields["value"] = f("object")
			out.Attr["name"] = a.text(n.ChildByFieldName("attribute"))
		}
		return out
	case "unary_operator", "not_operator":
		out := a.node("Unary", n)
		out.Fields["operand"] = f("argument")
		out.Attr["op"] = a.text(n.ChildByFieldName("operator"))
		if n.Kind() == "not_operator" {
			out.Attr["op"] = "not"
		}
		return out
	case "binary_operator", "boolean_operator", "union_type":
		kind := "Binary"
		if n.Kind() == "boolean_operator" {
			kind = "Bool"
		}
		out := a.node(kind, n)
		if n.Kind() == "union_type" {
			out.Fields["left"] = a.lower(children[0])
			out.Fields["right"] = a.lower(children[1])
			out.Attr["op"] = "|"
		} else {
			out.Fields["left"] = f("left")
			out.Fields["right"] = f("right")
			out.Attr["op"] = a.text(n.ChildByFieldName("operator"))
		}
		return out
	case "comparison_operator":
		out := a.node("Compare", n)
		for _, c := range children {
			out.Lists["operands"] = append(out.Lists["operands"], a.lower(c))
		}
		var ops []string
		for i := uint(0); i < n.ChildCount(); i++ {
			if n.FieldNameForChild(uint32(i)) == "operators" {
				op := strings.Join(strings.Fields(a.text(n.Child(i))), " ")
				ops = append(ops, op)
				if op == "<>" {
					a.report("PP002", "Python 2 comparison syntax is not valid Python 3.14", a.node("", n.Child(i)).Span)
				}
			}
		}
		out.Attr["ops"] = strings.Join(ops, "|")
		return out
	case "conditional_expression":
		out := a.node("Conditional", n)
		if len(children) == 3 {
			out.Fields["body"] = a.lower(children[0])
			out.Fields["test"] = a.lower(children[1])
			out.Fields["else"] = a.lower(children[2])
			return out
		}
	case "generic_type":
		out := a.node("Index", n)
		out.Fields["value"] = a.lower(children[0])
		args := named(children[1])
		if len(args) == 1 {
			out.Fields["index"] = a.lower(args[0])
		} else {
			tuple := a.node("Tuple", children[1])
			for _, c := range args {
				tuple.Lists["elements"] = append(tuple.Lists["elements"], a.lower(c))
			}
			out.Fields["index"] = tuple
		}
		return out
	case "subscript":
		return a.subscript(n)
	case "call":
		out := a.node("Call", n)
		out.Fields["target"] = f("function")
		args := n.ChildByFieldName("arguments")
		if args.Kind() != "argument_list" {
			out.Lists["args"] = []*model.Node{a.unsupported(args)}
			return out
		}
		for _, c := range named(args) {
			arg := a.node("Arg", c)
			if c.Kind() == "keyword_argument" {
				arg.Attr["name"] = a.text(c.ChildByFieldName("name"))
				arg.Fields["value"] = a.lower(c.ChildByFieldName("value"))
			} else {
				arg.Fields["value"] = a.lower(c)
			}
			out.Lists["args"] = append(out.Lists["args"], arg)
		}
		return out
	case "await":
		out := a.node("AwaitCall", n)
		if len(children) == 1 {
			call := a.lower(children[0])
			if call.Kind == "Call" {
				out.Fields["call"] = call
				return out
			}
			out.Kind = "Await"
			out.Fields["value"] = call
			a.flag(out, "await without a direct call", n)
			return out
		}
	}
	return a.unsupported(n)
}

func (a *adapter) param(n *sitter.Node) *model.Node {
	out := a.node("Param", n)
	switch n.Kind() {
	case "identifier":
		out.Attr["name"] = a.text(n)
	case "typed_parameter":
		c := named(n)[0]
		out.Attr["name"] = a.text(c)
		out.Fields["annotation"] = a.lower(n.ChildByFieldName("type"))
		if c.Kind() != "identifier" {
			a.flag(out, "variadic parameters", c)
		}
	case "default_parameter", "typed_default_parameter":
		out.Attr["name"] = a.text(n.ChildByFieldName("name"))
		out.Fields["annotation"] = a.lower(n.ChildByFieldName("type"))
		out.Fields["default"] = a.lower(n.ChildByFieldName("value"))
		a.flag(out, "default parameter values", n)
	default:
		a.flag(out, strings.ReplaceAll(n.Kind(), "_", " "), n)
	}
	return out
}

func (a *adapter) subscript(n *sitter.Node) *model.Node {
	out := a.node("Index", n)
	out.Fields["value"] = a.lower(n.ChildByFieldName("value"))
	var indices []*sitter.Node
	comma := false
	for i := uint(0); i < n.ChildCount(); i++ {
		if n.Child(i).Kind() == "," {
			comma = true
		}
		if n.FieldNameForChild(uint32(i)) == "subscript" {
			indices = append(indices, n.Child(i))
		}
	}
	if len(indices) == 1 && indices[0].Kind() == "slice" {
		out.Kind = "Slice"
		slice := indices[0]
		part := 0
		names := []string{"start", "stop", "step"}
		for i := uint(0); i < slice.ChildCount(); i++ {
			c := slice.Child(i)
			if c.Kind() == ":" {
				part++
				continue
			}
			if c.IsNamed() && c.Kind() != "comment" && part < len(names) {
				out.Fields[names[part]] = a.lower(c)
			}
		}
		if part > 1 {
			a.flag(out, "extended slicing", slice)
		}
		if comma {
			a.flag(out, "multidimensional subscription", n)
		}
	} else if len(indices) == 1 && !comma {
		out.Fields["index"] = a.lower(indices[0])
	} else {
		tuple := a.node("Tuple", n)
		for _, c := range indices {
			tuple.Lists["elements"] = append(tuple.Lists["elements"], a.lower(c))
		}
		out.Fields["index"] = tuple
	}
	return out
}

var encodingCookie = regexp.MustCompile(`^[\t\f ]*#.*?coding[:=][\t ]*([-_.a-zA-Z0-9]+)`)
var intPattern = regexp.MustCompile(`^(?:0[xX]_?[0-9a-fA-F]+(?:_[0-9a-fA-F]+)*|0[oO]_?[0-7]+(?:_[0-7]+)*|0[bB]_?[01]+(?:_[01]+)*|0(?:_?0)*|[1-9][0-9]*(?:_[0-9]+)*)$`)
var floatPattern = regexp.MustCompile(`^(?:(?:[0-9]+(?:_[0-9]+)*\.(?:[0-9]+(?:_[0-9]+)*)?|\.[0-9]+(?:_[0-9]+)*)(?:[eE][+-]?[0-9]+(?:_[0-9]+)*)?|[0-9]+(?:_[0-9]+)*[eE][+-]?[0-9]+(?:_[0-9]+)*)$`)

func (a *adapter) number(out *model.Node, n *sitter.Node) {
	t := out.Text
	if strings.HasSuffix(strings.ToLower(t), "j") {
		a.flag(out, "complex literals", n)
		return
	}
	valid := intPattern.MatchString(t)
	if n.Kind() == "float" {
		valid = floatPattern.MatchString(t)
	}
	if !valid {
		a.report("PP002", "invalid Python 3.14 numeric literal", out.Span)
	}
}

func (a *adapter) string(n *sitter.Node) *model.Node {
	if n.Kind() == "concatenated_string" {
		out := a.node("Literal", n)
		out.Attr["type"] = "str"
		var parts []*model.Node
		for _, c := range named(n) {
			part := a.string(c)
			parts = append(parts, part)
			if part.Kind == "FString" {
				out.Kind = "FString"
			}
		}
		if len(parts) > 0 {
			out.Attr["type"] = parts[0].A("type")
		}
		for _, part := range parts {
			if part.A("type") != out.A("type") {
				a.report("PP002", "cannot concatenate bytes and string literals", out.Span)
			}
			if part.Kind == "FString" {
				out.Lists["parts"] = append(out.Lists["parts"], part.Items("parts")...)
			} else {
				out.Lists["parts"] = append(out.Lists["parts"], part)
			}
		}
		return out
	}
	out := a.node("Literal", n)
	out.Attr["type"] = "str"
	children := named(n)
	if len(children) < 2 {
		return a.unsupported(n)
	}
	start := strings.ToLower(a.text(children[0]))
	quote := strings.IndexAny(start, "\"'")
	if quote < 0 {
		a.report("PP002", "invalid string delimiter", out.Span)
		return out
	}
	prefix := start[:quote]
	if strings.Contains(prefix, "t") {
		a.flag(out, "template strings", n)
	}
	if strings.Contains(prefix, "b") {
		out.Attr["type"] = "bytes"
	}
	if strings.Contains(prefix, "f") {
		out.Kind = "FString"
	}
	if prefix != "" && prefix != "r" && prefix != "u" && prefix != "b" && prefix != "br" && prefix != "rb" && prefix != "f" && prefix != "fr" && prefix != "rf" && !strings.Contains(prefix, "t") {
		a.report("PP002", "invalid Python string prefix", out.Span)
	}
	if start, end, _, complete := a.bytesContent(n); complete {
		// Bytes have no interpolation. Keep their content contiguous: the grammar
		// can split ignored Unicode escapes into recovery nodes and dangling
		// backslashes, even though the actual bytes literal is well delimited.
		part := &model.Node{Kind: "Literal", Text: string(a.source[start:end]), Span: a.span(start, end), Attr: map[string]string{"type": "bytes"}}
		out.Lists["parts"] = append(out.Lists["parts"], part)
		a.checkStringContent(part, strings.Contains(prefix, "r"))
		return out
	}
	for _, c := range children[1 : len(children)-1] {
		if c.Kind() == "interpolation" {
			part := a.node("Format", c)
			part.Fields["value"] = a.lower(c.ChildByFieldName("expression"))
			part.Attr["conversion"] = strings.TrimPrefix(a.text(c.ChildByFieldName("type_conversion")), "!")
			part.Attr["format"] = strings.TrimPrefix(a.text(c.ChildByFieldName("format_specifier")), ":")
			for i := uint(0); i < c.ChildCount(); i++ {
				if c.Child(i).Kind() == "=" {
					part.Attr["debug"] = "true"
					a.flag(part, "debug f-string formatting", c)
				}
			}
			if spec := c.ChildByFieldName("format_specifier"); spec != nil && len(named(spec)) > 0 {
				a.flag(part, "dynamic f-string format specifications", spec)
			}
			if part.A("conversion") != "" && part.A("conversion") != "s" {
				a.flag(part, "f-string repr or ascii conversion", c)
			}
			out.Lists["parts"] = append(out.Lists["parts"], part)
		} else {
			part := a.node("Literal", c)
			part.Attr["type"] = out.A("type")
			out.Lists["parts"] = append(out.Lists["parts"], part)
			a.checkStringContent(part, strings.Contains(prefix, "r"))
		}
	}
	return out
}

// bytesContent confirms the exact boundaries of a nonformatted bytes literal.
// The grammar recognizes Unicode escapes inside bytes and can recover from a
// valid ignored escape such as a second \\N{} by inserting ERROR nodes. Bytes
// contain no expressions, so a complete lexical boundary check lets us treat
// the body as one token and validate its actual escape/ASCII rules separately.
// Never suppress errors for text beyond the first real closing delimiter.
func (a *adapter) bytesContent(n *sitter.Node) (start, end int, isBytes, complete bool) {
	if n.Kind() != "string" || n.ChildCount() < 2 {
		return 0, 0, false, false
	}
	first := n.Child(0)
	if first.Kind() != "string_start" {
		return 0, 0, false, false
	}
	opening := strings.ToLower(a.text(first))
	quote := strings.IndexAny(opening, "\"'")
	if quote < 0 || opening[:quote] != "b" && opening[:quote] != "br" && opening[:quote] != "rb" {
		return 0, 0, false, false
	}
	delimiter := opening[quote:]
	if delimiter != "'" && delimiter != `"` && delimiter != "'''" && delimiter != `"""` {
		return 0, 0, false, false
	}
	start, limit := int(first.EndByte()), int(n.EndByte())
	for i := start; i < limit; i++ {
		if a.source[i] == '\\' {
			i++
			if i+1 < limit && a.source[i] == '\r' && a.source[i+1] == '\n' {
				i++
			}
			continue
		}
		if len(delimiter) == 1 && (a.source[i] == '\n' || a.source[i] == '\r') {
			return 0, 0, true, false
		}
		if bytes.HasPrefix(a.source[i:limit], []byte(delimiter)) {
			return start, i, true, i+len(delimiter) == limit
		}
	}
	return 0, 0, true, false
}

func (a *adapter) checkStringContent(part *model.Node, raw bool) {
	bytesLiteral := part.A("type") == "bytes"
	if !raw {
		a.escapes(part, bytesLiteral)
	}
	if bytesLiteral {
		for _, r := range part.Text {
			if r > 127 {
				a.report("PP002", "bytes literals may contain only ASCII source characters", part.Span)
				break
			}
		}
	}
}

// The grammar deliberately accepts some lexical forms from older Python
// versions. Check escape widths and Unicode scalar ranges independently so an
// error-recovering grammar cannot turn invalid Python into accepted PurePy.
func (a *adapter) escapes(part *model.Node, bytesLiteral bool) {
	s := part.Text
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' {
			continue
		}
		start := i
		i++
		if i >= len(s) {
			a.report("PP002", "incomplete string escape", part.Span)
			return
		}
		n := 0
		switch s[i] {
		case 'x':
			n = 2
		case 'u':
			if !bytesLiteral {
				n = 4
			}
		case 'U':
			if !bytesLiteral {
				n = 8
			}
		case 'N':
			if !bytesLiteral {
				if i+1 >= len(s) || s[i+1] != '{' {
					a.report("PP002", "named Unicode escape requires a name in braces", a.span(part.Span.Start+start, part.Span.Start+i+1))
					continue
				}
				end := strings.IndexByte(s[i+2:], '}')
				if end < 0 {
					a.report("PP002", "unterminated named Unicode escape", a.span(part.Span.Start+start, part.Span.End))
					return
				}
				end += i + 2
				if !unicodenames.Valid(s[i+2 : end]) {
					a.report("PP002", "unknown Unicode character name", a.span(part.Span.Start+start, part.Span.Start+end+1))
				}
				i = end
			}
		}
		if n == 0 {
			continue
		}
		if i+n >= len(s) {
			a.report("PP002", "incomplete hexadecimal string escape", part.Span)
			return
		}
		value, err := strconv.ParseUint(s[i+1:i+n+1], 16, 32)
		if err != nil || value > 0x10ffff {
			a.report("PP002", "invalid hexadecimal or Unicode string escape", a.span(part.Span.Start+start, part.Span.Start+i+n+1))
		}
		i += n
	}
}
