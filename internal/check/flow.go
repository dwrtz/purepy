package check

import (
	"sort"
	"strings"
	"sync"

	"github.com/dwrtz/purepy/internal/model"
)

func clone(in map[string]variable) map[string]variable {
	out := make(map[string]variable, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// CheckFunctions bounds concurrency at the function level. Linked declarations
// are immutable; each worker owns its environments, facts and diagnostics.
func (p *Program) CheckFunctions(jobs int) Result {
	if jobs < 1 {
		jobs = 1
	}
	names := []string{}
	for name, f := range p.Functions {
		if f.Origin == "project" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	results := make([]Result, len(names))
	work := make(chan int)
	var wg sync.WaitGroup
	if jobs > len(names) {
		jobs = len(names)
	}
	for i := 0; i < jobs; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range work {
				f := p.Functions[names[i]]
				c := newChecker(p, p.Modules[f.Module], f)
				results[i] = c.function()
			}
		}()
	}
	for i := range names {
		work <- i
	}
	close(work)
	wg.Wait()
	out := Result{Diagnostics: p.Diagnostics, Calls: []model.CallEdge{}, Facts: []model.Fact{}}
	for _, r := range results {
		out.Diagnostics = append(out.Diagnostics, r.Diagnostics...)
		out.Calls = append(out.Calls, r.Calls...)
		out.Facts = append(out.Facts, r.Facts...)
	}
	return out
}

func collectLocals(body []*model.Node, names map[string]bool) {
	for _, n := range body {
		if n.Kind == "Assign" || n.Kind == "For" {
			if t := n.Get("target"); t != nil && t.Kind == "Name" {
				names[t.A("name")] = true
			}
		}
		if n.Kind == "If" || n.Kind == "While" || n.Kind == "For" {
			collectLocals(n.Items("body"), names)
			collectLocals(n.Items("else"), names)
		}
	}
}
func (c *checker) function() Result {
	names := map[string]bool{}
	collectLocals(c.f.Body, names)
	for name := range names {
		c.vars[name] = variable{Type: model.Invalid, Current: model.Invalid}
	}
	for _, p := range c.f.Parameters {
		c.vars[p.Name] = variable{Type: p.Type, Current: p.Type, Assigned: true, Parameter: true}
		if s := c.m.Bindings[p.Name]; s != nil {
			c.error("PP503", "parameter cannot shadow module or imported declaration "+p.Name, p.Span)
		}
	}
	if c.block(c.f.Body, true) && c.f.Returns.Kind != "None" {
		c.error("PP213", "function may fall through without returning "+c.f.Returns.String(), c.f.Span)
	}
	c.result.Facts = append(c.result.Facts, model.Fact{Span: c.f.Span, Symbol: c.f.Name, Type: c.f.Returns, Category: c.f.Returns.Category(), Description: c.f.Classification() + " function"})
	return c.result
}
func (c *checker) localAnnotation(n *model.Node, at model.Span) model.Type {
	// A shallow copy shares only read-only linked declarations, so parsing local
	// annotations cannot mutate diagnostics belonging to another worker.
	p := *c.p
	p.Diagnostics = nil
	t := p.annotation(c.m, n, at)
	c.result.Diagnostics = append(c.result.Diagnostics, p.Diagnostics...)
	return t
}
func (c *checker) bind(target *model.Node, t model.Type, annotation *model.Node, assigned bool, at model.Span) {
	if target == nil || target.Kind != "Name" {
		c.error("PP503", "assignment may only rebind one local name", at)
		return
	}
	name := target.A("name")
	if forbiddenName(name) {
		c.error("PP104", "dunder locals are prohibited", at)
	}
	if s := c.m.Bindings[name]; s != nil {
		c.error("PP503", "module and imported names cannot be rebound: "+name, at)
		return
	}
	v := c.vars[name]
	if v.Parameter && !v.Type.Pure() {
		code := "PP313"
		if v.Type.Kind == "host_ref" {
			code = "PP334"
		}
		c.error(code, "capability and host-reference parameters cannot be rebound", at)
		return
	}
	declared := model.Invalid
	if annotation != nil {
		declared = c.localAnnotation(annotation, at)
		if !declared.Pure() {
			c.error("PP201", "local annotations must be Pure Value types", at)
		}
	}
	if declared.Kind != "invalid" {
		if v.Type.Kind != "" && v.Type.Kind != "invalid" && !v.Type.Equal(declared) {
			c.error("PP205", "local annotation changes the declared type of "+name, at)
		} else {
			v.Type = declared
		}
	}
	if v.Type.Kind == "" || v.Type.Kind == "invalid" {
		v.Type = t
	}
	if assigned {
		c.pure(t, at)
		c.expect(v.Type, t, at)
		v.Current = v.Type
		v.Assigned = true
	} else {
		v.Current = v.Type
	}
	c.vars[name] = v
	c.fact(target, v.Type, name, "local declaration or assignment")
}
func (c *checker) block(body []*model.Node, docstring bool) bool {
	alive := true
	for i, n := range body {
		if i == 0 && docstring && doc(n) {
			continue
		}
		next := c.statement(n)
		alive = alive && next
	}
	return alive
}
func (c *checker) statement(n *model.Node) bool {
	switch n.Kind {
	case "Assign":
		target := n.Get("target")
		want := model.Invalid
		if target != nil && target.Kind == "Name" {
			if v, ok := c.vars[target.A("name")]; ok {
				want = v.Type
			}
		}
		if a := n.Get("annotation"); a != nil {
			want = c.localAnnotation(a, n.Span)
		}
		value := n.Get("value")
		t := want
		if value != nil {
			t = c.expr(value, want)
		}
		c.bind(target, t, n.Get("annotation"), value != nil, n.Span)
	case "Return":
		t := model.None
		if n.Get("value") != nil {
			t = c.expr(n.Get("value"), c.f.Returns)
		}
		c.pure(t, n.Span)
		c.expect(c.f.Returns, t, n.Span)
		return false
	case "ExprStmt":
		x := n.Get("value")
		if x == nil || (x.Kind != "Call" && x.Kind != "AwaitCall") {
			c.error("PP003", "expression statements must be direct calls returning None", n.Span)
			if x != nil {
				c.expr(x, model.Invalid)
			}
		} else {
			c.expect(model.None, c.expr(x, model.None), n.Span)
		}
	case "Pass":
	case "If":
		c.expect(model.Bool, c.expr(n.Get("test"), model.Bool), n.Span)
		before := clone(c.vars)
		c.narrow(n.Get("test"), true)
		leftAlive := c.block(n.Items("body"), false)
		left := clone(c.vars)
		c.vars = clone(before)
		c.narrow(n.Get("test"), false)
		rightAlive := c.block(n.Items("else"), false)
		right := clone(c.vars)
		c.vars = c.join(left, right, leftAlive, rightAlive, n.Span)
		return leftAlive || rightAlive
	case "While", "For":
		if len(n.Items("else")) > 0 {
			c.error("PP003", "loop else clauses are prohibited", n.Span)
		}
		before := clone(c.vars)
		writes := map[string]bool{}
		collectLocals(n.Items("body"), writes)
		for name := range writes {
			v := c.vars[name]
			v.Current = v.Type
			c.vars[name] = v
		}
		if n.Kind == "While" {
			c.expect(model.Bool, c.expr(n.Get("test"), model.Bool), n.Span)
			c.narrow(n.Get("test"), true)
		} else {
			iter := n.Get("iter")
			t := model.Invalid
			if iter != nil && iter.Kind == "Call" && iter.Get("target") != nil && iter.Get("target").Kind == "Name" && iter.Get("target").A("name") == "range" && c.m.Bindings["range"] == nil {
				if _, local := c.vars["range"]; local {
					c.error("PP301", "local values cannot be called: range", iter.Span)
				}
				args := iter.Items("args")
				if len(args) < 1 || len(args) > 3 {
					c.error("PP303", "range requires one to three int arguments", iter.Span)
				}
				for _, a := range args {
					if a.Kind != "Arg" || a.A("name") != "" || a.A("unpack") != "" {
						c.error("PP302", "range requires explicit positional arguments", a.Span)
					}
					c.expect(model.Int, c.expr(a.Get("value"), model.Int), a.Span)
				}
				t = model.Int
			} else {
				seq := c.expr(iter, model.Invalid)
				switch seq.Kind {
				case "str":
					t = model.Str
				case "bytes":
					t = model.Int
				case "tuple":
					if seq.Elem != nil {
						t = *seq.Elem
					}
				default:
					if seq.Kind != "invalid" {
						c.error("PP214", "for requires direct range, tuple, str or bytes", n.Span)
					}
				}
			}
			c.bind(n.Get("target"), t, nil, true, n.Span)
		}
		c.loop++
		c.block(n.Items("body"), false)
		c.loop--
		after := clone(c.vars)
		c.vars = c.join(before, after, true, true, n.Span)
		// Loops may execute zero times. Refinements of values written by any
		// iteration cannot survive a break, continue, or subsequent iteration.
		for name := range writes {
			v := c.vars[name]
			v.Current = v.Type
			c.vars[name] = v
		}
		if n.Kind == "While" && literalTrue(n.Get("test")) && !containsBreak(n.Items("body")) {
			return false
		}
	case "Break", "Continue":
		if c.loop == 0 {
			c.error("PP215", strings.ToLower(n.Kind)+" outside a loop", n.Span)
		}
		return false
	default:
		c.error("PP003", "unsupported statement: "+n.Kind, n.Span)
	}
	return true
}
func literalTrue(n *model.Node) bool {
	return n != nil && n.Kind == "Literal" && n.A("type") == "bool" && n.Text == "True"
}
func containsBreak(body []*model.Node) bool {
	for _, n := range body {
		if n.Kind == "Break" {
			return true
		}
		if n.Kind == "If" && (containsBreak(n.Items("body")) || containsBreak(n.Items("else"))) {
			return true
		}
	}
	return false
}
func (c *checker) join(a, b map[string]variable, aliveA, aliveB bool, at model.Span) map[string]variable {
	out := clone(a)
	for name, y := range b {
		x, exists := out[name]
		if !exists {
			x = variable{Type: model.Invalid, Current: model.Invalid}
		}
		if x.Type.Kind == "invalid" || x.Type.Kind == "" {
			x.Type = y.Type
		}
		if y.Type.Kind != "invalid" && y.Type.Kind != "" && !x.Type.Equal(y.Type) {
			c.error("PP205", "branch or loop assignments disagree on exact type of "+name, at)
		}
		if aliveA && !aliveB {
			out[name] = x
			continue
		}
		if !aliveA && aliveB {
			// Declared local types are function-wide even when the assignment
			// occurred on a branch that returned. Only assignment/refinement
			// facts come from the surviving path.
			if y.Type.Kind == "invalid" || y.Type.Kind == "" {
				y.Type = x.Type
			}
			out[name] = y
			continue
		}
		x.Assigned = x.Assigned && y.Assigned
		x.Parameter = x.Parameter || y.Parameter
		if !x.Current.Equal(y.Current) {
			x.Current = x.Type
		}
		out[name] = x
	}
	return out
}
func (c *checker) narrow(test *model.Node, truth bool) {
	if test == nil {
		return
	}
	if test.Kind == "Unary" && test.A("op") == "not" {
		c.narrow(test.Get("operand"), !truth)
		return
	}
	if test.Kind != "Compare" {
		return
	}
	xs := test.Items("operands")
	op := test.A("ops")
	if len(xs) != 2 || (op != "is" && op != "is not") {
		return
	}
	name, value := xs[0], xs[1]
	if name.Kind == "Literal" {
		name, value = value, name
	}
	if name.Kind != "Name" || value.Kind != "Literal" || value.A("type") != "None" {
		return
	}
	v, ok := c.vars[name.A("name")]
	if !ok || !v.Assigned || v.Type.Kind != "optional" || v.Type.Elem == nil {
		return
	}
	if (op == "is" && truth) || (op == "is not" && !truth) {
		v.Current = model.None
	} else {
		v.Current = *v.Type.Elem
	}
	c.vars[name.A("name")] = v
}
