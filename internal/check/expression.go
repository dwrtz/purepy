package check

import (
	"fmt"
	"strings"

	"github.com/dwrtz/purepy/internal/diag"
	"github.com/dwrtz/purepy/internal/model"
)

type Result struct {
	Diagnostics []diag.Diagnostic
	Calls       []model.CallEdge
	Facts       []model.Fact
}
type variable struct {
	Type, Current       model.Type
	Assigned, Parameter bool
}
type checker struct {
	p        *Program
	m        *Module
	f        *model.Function
	vars     map[string]variable
	result   Result
	constant bool
	loop     int
}

func newChecker(p *Program, m *Module, f *model.Function) *checker {
	return &checker{p: p, m: m, f: f, vars: map[string]variable{}, result: Result{Diagnostics: []diag.Diagnostic{}, Calls: []model.CallEdge{}, Facts: []model.Fact{}}}
}
func (c *checker) error(code, msg string, at model.Span) {
	d := diag.New(code, msg, at)
	if c.f != nil {
		d.Symbol = c.f.Name
	}
	c.result.Diagnostics = append(c.result.Diagnostics, d)
}
func (c *checker) expect(want, got model.Type, at model.Span) {
	if want.Kind != "invalid" && got.Kind != "invalid" && !want.Accepts(got) {
		c.error("PP205", fmt.Sprintf("expected %s, got %s", want, got), at)
	}
}
func (c *checker) fact(n *model.Node, t model.Type, symbol, desc string) {
	c.result.Facts = append(c.result.Facts, model.Fact{Span: n.Span, Symbol: symbol, Type: t, Category: t.Category(), Description: desc})
}
func (c *checker) pure(t model.Type, at model.Span) bool {
	if t.Kind == "invalid" {
		return false
	}
	if !t.Pure() {
		code := "PP201"
		if t.Kind == "host_ref" {
			code = "PP334"
		}
		if t.Kind == "capability" {
			code = "PP313"
		}
		c.error(code, "only direct forwarding from an original parameter is permitted for "+t.String(), at)
		return false
	}
	return true
}
func (c *checker) expr(n *model.Node, want model.Type) (t model.Type) {
	if n == nil {
		c.error("PP099", "malformed semantic expression: missing node", c.m.Tree.Span)
		return model.Invalid
	}
	t = model.Invalid
	defer func() { c.fact(n, t, "", "expression") }()
	if c.constant {
		switch n.Kind {
		case "Literal", "Tuple", "Name", "Call":
		case "Unary":
			if n.A("op") != "-" && n.A("op") != "+" {
				c.error("PP502", "only immutable constant expressions may initialize modules", n.Span)
				return
			}
		default:
			c.error("PP502", "only literals, earlier constants, tuples and value constructors may initialize modules", n.Span)
			return
		}
	}
	switch n.Kind {
	case "Literal":
		if primitive, ok := model.Primitive(n.A("type")); ok {
			return primitive
		}
		c.error("PP203", "unsupported literal "+n.Text, n.Span)
	case "Name":
		name := n.A("name")
		if forbiddenName(name) {
			c.error("PP104", "dunder names are not accessible", n.Span)
			return
		}
		if v, ok := c.vars[name]; ok {
			if !v.Assigned {
				c.error("PP206", "local "+name+" is not definitely assigned", n.Span)
				return
			}
			if !c.pure(v.Current, n.Span) {
				return
			}
			return v.Current
		}
		if s := c.m.Bindings[name]; s != nil {
			if c.constant {
				if at, imported := c.m.ImportedAt[name]; imported && at.Start > n.Span.Start {
					c.error("PP502", "constant initializer uses "+name+" before its import", n.Span)
					return
				}
			}
			if s.Kind == "constant" {
				if !s.Type.Pure() {
					c.error("PP502", "constant must be defined before use: "+name, n.Span)
					return
				}
				return s.Type
			}
			c.error("PP301", name+" is a declaration, not a first-class value", n.Span)
			return
		}
		c.error("PP104", "unknown name "+name, n.Span)
	case "Tuple":
		xs := n.Items("elements")
		element := model.Invalid
		if want.Kind == "tuple" && want.Elem != nil {
			element = *want.Elem
		}
		if len(xs) == 0 {
			if element.Kind == "invalid" {
				c.error("PP207", "empty tuple needs an explicit contextual tuple[T, ...] type", n.Span)
				return
			}
			return model.Tuple(element)
		}
		for i, x := range xs {
			actual := c.expr(x, element)
			c.pure(actual, x.Span)
			if i == 0 && element.Kind == "invalid" {
				element = actual
			} else if !element.Equal(actual) && actual.Kind != "invalid" {
				c.error("PP207", "tuple elements must have one exact type", x.Span)
			}
		}
		if element.Pure() {
			return model.Tuple(element)
		}
	case "Attribute":
		base := c.expr(n.Get("value"), model.Invalid)
		name := n.A("name")
		if forbiddenName(name) {
			c.error("PP104", "dunder attributes are not accessible", n.Span)
			return
		}
		if base.Kind == "record" {
			if r := c.p.Records[base.Name]; r != nil {
				for _, f := range r.Fields {
					if f.Name == name {
						return f.Type
					}
				}
			}
		}
		if base.Kind != "invalid" {
			c.error("PP208", "attribute reads require a declared field of an exact @value record", n.Span)
		}
	case "Unary":
		v := c.expr(n.Get("operand"), model.Invalid)
		op := n.A("op")
		if c.constant && n.Get("operand") != nil && n.Get("operand").Kind != "Literal" {
			c.error("PP502", "constant unary signs must apply directly to numeric literals", n.Span)
			return
		}
		if op == "not" {
			c.expect(model.Bool, v, n.Span)
			return model.Bool
		}
		if (op == "+" || op == "-") && (v.Kind == "int" || v.Kind == "float") || op == "~" && v.Kind == "int" {
			return v
		}
		if v.Kind != "invalid" {
			c.error("PP209", "unsupported unary operator for "+v.String(), n.Span)
		}
	case "Binary":
		l := c.expr(n.Get("left"), model.Invalid)
		r := c.expr(n.Get("right"), model.Invalid)
		return c.binary(n.A("op"), l, r, n.Span)
	case "Bool":
		if n.A("op") != "and" && n.A("op") != "or" {
			c.error("PP099", "malformed semantic boolean operator", n.Span)
			return
		}
		l := c.expr(n.Get("left"), model.Bool)
		c.expect(model.Bool, l, n.Span)
		before := clone(c.vars)
		c.narrow(n.Get("left"), n.A("op") == "and")
		r := c.expr(n.Get("right"), model.Bool)
		c.expect(model.Bool, r, n.Span)
		c.vars = before
		return model.Bool
	case "Compare":
		xs := n.Items("operands")
		ops := strings.Split(n.A("ops"), "|")
		if len(xs) != len(ops)+1 {
			c.error("PP003", "malformed comparison", n.Span)
			return
		}
		types := make([]model.Type, len(xs))
		for i, x := range xs {
			types[i] = c.expr(x, model.Invalid)
		}
		for i, op := range ops {
			c.compare(op, types[i], types[i+1], n.Span)
		}
		return model.Bool
	case "Conditional":
		c.expect(model.Bool, c.expr(n.Get("test"), model.Bool), n.Span)
		before := clone(c.vars)
		c.narrow(n.Get("test"), true)
		a := c.expr(n.Get("body"), want)
		c.vars = clone(before)
		c.narrow(n.Get("test"), false)
		b := c.expr(n.Get("else"), want)
		c.vars = before
		if !a.Equal(b) {
			c.error("PP205", "conditional expression branches must have the same exact type", n.Span)
			return
		}
		return a
	case "Index", "Slice":
		base := c.expr(n.Get("value"), model.Invalid)
		if base.Kind != "str" && base.Kind != "bytes" && base.Kind != "tuple" {
			if base.Kind != "invalid" {
				c.error("PP210", "only str, bytes and homogeneous tuples support subscription", n.Span)
			}
			return
		}
		if n.Kind == "Slice" {
			if n.Get("step") != nil {
				c.error("PP210", "extended slicing is prohibited", n.Span)
			}
			for _, key := range []string{"start", "stop"} {
				if bound := n.Get(key); bound != nil {
					c.expect(model.Optional(model.Int), c.expr(bound, model.Optional(model.Int)), bound.Span)
				}
			}
			return base
		}
		c.expect(model.Int, c.expr(n.Get("index"), model.Int), n.Span)
		if base.Kind == "bytes" {
			return model.Int
		}
		if base.Kind == "tuple" && base.Elem != nil {
			return *base.Elem
		}
		return model.Str
	case "Call":
		return c.call(n, false)
	case "AwaitCall":
		if c.constant {
			c.error("PP502", "module initialization cannot await", n.Span)
			return
		}
		return c.call(n.Get("call"), true)
	case "Await":
		c.error("PP402", "await must directly contain one statically known async call", n.Span)
	case "FString":
		for _, part := range n.Items("parts") {
			if part.Kind == "Literal" {
				continue
			}
			if part.Kind != "Format" {
				c.error("PP211", "unsupported f-string component", part.Span)
				continue
			}
			value := c.expr(part.Get("value"), model.Invalid)
			if value.Kind != "bool" && value.Kind != "int" && value.Kind != "float" && value.Kind != "str" && value.Kind != "invalid" {
				c.error("PP211", "formatting requires bool, int, float or str", part.Span)
			}
			if part.A("conversion") != "" || part.A("format") != "" {
				c.error("PP211", "format conversions and specifications are not in the sealed formatting table", part.Span)
			}
		}
		return model.Str
	default:
		c.error("PP003", "unsupported expression: "+n.Kind, n.Span)
	}
	return
}
func (c *checker) binary(op string, l, r model.Type, at model.Span) model.Type {
	if l.Kind == "invalid" || r.Kind == "invalid" {
		return model.Invalid
	}
	if l.Equal(r) {
		if l.Kind == "int" {
			switch op {
			case "+", "-", "*", "//", "%", "&", "|", "^", "<<", ">>":
				return l
			case "/":
				return model.Float
			}
		}
		if l.Kind == "float" {
			switch op {
			case "+", "-", "*", "/", "//", "%":
				return l
			}
		}
		if op == "+" && (l.Kind == "str" || l.Kind == "bytes" || l.Kind == "tuple") {
			return l
		}
	}
	// Power is intentionally absent: int**int can return int or float, and
	// float**float can return complex. No single exact return type is sound.
	c.error("PP209", fmt.Sprintf("operator %s has no exact rule for %s and %s", op, l, r), at)
	return model.Invalid
}
func (c *checker) equality(t model.Type, active map[string]bool) bool {
	switch t.Kind {
	case "None", "bool", "int", "float", "str", "bytes":
		return true
	case "tuple", "optional":
		return t.Elem != nil && c.equality(*t.Elem, active)
	case "record":
		if active[t.Name] {
			return false
		}
		r := c.p.Records[t.Name]
		if r == nil {
			return false
		}
		active[t.Name] = true
		defer delete(active, t.Name)
		for _, f := range r.Fields {
			if !c.equality(f.Type, active) {
				return false
			}
		}
		return true
	}
	return false
}
func (c *checker) compare(op string, l, r model.Type, at model.Span) {
	if l.Kind == "invalid" || r.Kind == "invalid" {
		return
	}
	ok := false
	switch op {
	case "is", "is not":
		ok = l.Kind == "None" && (r.Kind == "optional" || r.Kind == "None") || r.Kind == "None" && (l.Kind == "optional" || l.Kind == "None")
	case "==", "!=":
		ok = l.Equal(r) && c.equality(l, map[string]bool{}) || l.Kind == "optional" && r.Kind == "None" && c.equality(l, map[string]bool{}) || r.Kind == "optional" && l.Kind == "None" && c.equality(r, map[string]bool{})
	case "<", "<=", ">", ">=":
		ok = l.Equal(r) && (l.Kind == "int" || l.Kind == "float" || l.Kind == "str" || l.Kind == "bytes")
	case "in", "not in":
		ok = r.Kind == "tuple" && r.Elem != nil && r.Elem.Equal(l) && c.equality(l, map[string]bool{}) || l.Kind == "str" && r.Kind == "str" || l.Kind == "int" && r.Kind == "bytes"
	}
	if !ok {
		c.error("PP212", fmt.Sprintf("comparison %s is not defined for %s and %s", op, l, r), at)
	}
}
