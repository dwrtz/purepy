package check

import (
	"github.com/dwrtz/purepy/internal/diag"
	"github.com/dwrtz/purepy/internal/model"
	"sort"
)

func writesIn(body []*model.Node, counts map[string]int) {
	for _, n := range body {
		if n.Kind == "Function" {
			counts[n.A("name")]++
			continue
		}
		if n.Kind == "Assign" || n.Kind == "For" {
			if target := n.Get("target"); target != nil && target.Kind == "Name" {
				counts[target.A("name")]++
				if n.Kind == "For" {
					counts[target.A("name")] += 2
				}
			}
		}
		if n.Kind == "If" || n.Kind == "While" || n.Kind == "For" {
			writesIn(n.Items("body"), counts)
			writesIn(n.Items("else"), counts)
		}
	}
}
func readNames(n *model.Node, names map[string]bool) {
	if n == nil {
		return
	}
	if n.Kind == "Name" {
		names[n.A("name")] = true
		return
	}
	// Annotations are type syntax, not captures; assignment targets aren't reads.
	for key, child := range n.Fields {
		if key != "annotation" && key != "returns" && key != "target" {
			readNames(child, names)
		} else if key == "target" && n.Kind == "Call" {
			readNames(child, names)
		}
	}
	for key, xs := range n.Lists {
		if key == "params" || key == "typeparams" {
			continue
		}
		for _, child := range xs {
			readNames(child, names)
		}
	}
}
func (c *checker) closure(n *model.Node) {
	name := n.A("name")
	f := c.p.Functions[c.f.Name+"."+name]
	if f == nil {
		return
	}
	if c.loop > 0 {
		c.error("PP503", "closures cannot be defined inside loops", n.Span)
	}
	if c.m.Bindings[name] != nil || c.m.TypeVars[name].Kind != "" || forbiddenName(name) {
		c.error("PP503", "nested function cannot shadow a declaration", n.Span)
	}
	counts := map[string]int{}
	writesIn(c.f.Body, counts)
	if counts[name] != 1 {
		c.error("PP503", "nested function bindings cannot be reassigned", n.Span)
	}
	locals := map[string]bool{}
	collectLocals(f.Body, locals)
	for _, p := range f.Parameters {
		locals[p.Name] = true
	}
	reads := map[string]bool{}
	for _, child := range f.Body {
		readNames(child, reads)
	}
	keys := []string{}
	for k := range reads {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	child := newChecker(c.p, c.p.Scopes[f.Name], f)
	for _, key := range keys {
		if locals[key] {
			continue
		}
		v, ok := c.vars[key]
		if !ok || key == name {
			continue
		}
		if !v.Assigned || !v.Current.FunctionalValue() || v.Parameter && counts[key] > 0 || !v.Parameter && counts[key] > 1 {
			c.error("PP503", "closure capture requires an assigned, stable immutable binding: "+key, n.Span).WithRelated(v.Declaration)
			continue
		}
		// Captured values are not original capability parameters of the child.
		v.Parameter = false
		child.vars[key] = v
	}
	self := functionType(f)
	child.vars[name] = variable{Type: self, Current: self, Assigned: true, Declaration: n.Span}
	r := child.function()
	c.result.Bindings = append(c.result.Bindings, r.Bindings...)
	c.result.Invocations = append(c.result.Invocations, r.Invocations...)
	c.result.Diagnostics = append(c.result.Diagnostics, r.Diagnostics...)
	c.result.Calls = append(c.result.Calls, r.Calls...)
	c.result.Facts = append(c.result.Facts, r.Facts...)
	if !f.Pure() {
		c.error("PP201", "closures require pure synchronous signatures", n.Span)
	}
	c.vars[name] = variable{Type: self, Current: self, Assigned: true, Declaration: n.Span}
	c.dependency(f.Name, "closure", n.Span)
}
func (c *checker) functionalEquality(t model.Type, seen map[string]bool) bool {
	if len(seen) > 128 || !t.WithinLimit() {
		return false
	}
	key := t.String()
	if seen[key] {
		return true
	}
	seen[key] = true
	switch t.Kind {
	case "None", "bool", "int", "float", "str", "bytes":
		return true
	case "optional", "tuple":
		return t.Elem != nil && c.functionalEquality(*t.Elem, seen)
	case "product":
		for _, a := range t.Args {
			if !c.functionalEquality(a, seen) {
				return false
			}
		}
		return true
	case "record":
		r := c.p.Records[t.Name]
		if r == nil {
			return false
		}
		for _, f := range recordFields(r, t) {
			if !c.functionalEquality(f.Type, seen) {
				return false
			}
		}
		return true
	}
	return false
}

func (p *Program) checkGenericCycles(result *Result) {
	adjacency := map[string][]string{}
	for _, e := range result.Calls {
		if p.Functions[e.Callee] != nil {
			adjacency[e.Caller] = append(adjacency[e.Caller], e.Callee)
		}
	}
	budget := 1000000
	for _, e := range result.Calls {
		if len(e.Instantiation) == 0 {
			continue
		}
		seen := map[string]bool{}
		queue := []string{e.Callee}
		cycle := false
		for len(queue) > 0 {
			last := len(queue) - 1
			name := queue[last]
			queue = queue[:last]
			if name == e.Caller {
				cycle = true
				break
			}
			if seen[name] {
				continue
			}
			seen[name] = true
			budget--
			if budget < 0 {
				result.Diagnostics = append(result.Diagnostics, diag.New("PP203", "generic dependency analysis budget exceeded", e.Span))
				return
			}
			queue = append(queue, adjacency[name]...)
		}
		if !cycle {
			continue
		}
		f := p.Functions[e.Caller]
		for f != nil && f.Parent != "" {
			f = p.Functions[f.Parent]
		}
		if f == nil {
			continue
		}
		valid := len(e.Instantiation) == len(f.TypeParams)
		for i, t := range e.Instantiation {
			if i >= len(f.TypeParams) || t.Kind != "typevar" || t.Name != f.TypeParams[i] {
				valid = false
			}
		}
		if !valid {
			result.Diagnostics = append(result.Diagnostics, diag.New("PP203", "recursive generic calls must preserve their type parameters", e.Span))
		}
	}
}
