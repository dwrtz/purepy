package check

import (
	"fmt"

	"github.com/dwrtz/purepy/internal/model"
)

func (c *checker) call(n *model.Node, awaited bool) model.Type {
	if n == nil || n.Kind != "Call" {
		if n != nil {
			c.error("PP402", "await must directly contain a call", n.Span)
		} else {
			c.error("PP099", "malformed semantic call: missing call node", c.m.Tree.Span)
		}
		return model.Invalid
	}
	target := n.Get("target")
	if target == nil || target.Kind != "Name" {
		c.error("PP301", "call target must be a direct top-level function or record name; methods and dynamic calls are prohibited", n.Span)
		return model.Invalid
	}
	name := target.A("name")
	if forbiddenName(name) {
		c.error("PP301", "dunder calls are prohibited", n.Span)
		return model.Invalid
	}
	if _, ok := c.vars[name]; ok {
		c.error("PP301", "local values cannot be called: "+name, n.Span)
		return model.Invalid
	}
	s := c.m.Bindings[name]
	if s == nil {
		if c.constant {
			c.error("PP502", "ordinary calls cannot initialize module constants", n.Span)
			return model.Invalid
		}
		if awaited {
			c.error("PP403", "sealed synchronous intrinsics cannot be awaited", n.Span)
			return model.Invalid
		}
		return c.intrinsic(name, n)
	}
	var params []model.Parameter
	var ret model.Type
	var fn *model.Function
	if s.Record != nil {
		if c.constant {
			if at, imported := c.m.ImportedAt[name]; imported {
				if at.Start > n.Span.Start {
					c.error("PP502", "record constructor used before its import: "+name, n.Span)
				}
			} else if s.Node != nil && s.Node.Span.Start > n.Span.Start {
				c.error("PP502", "record constructor used before its declaration: "+name, n.Span)
			}
		}
		params = s.Record.Fields
		ret = s.Type
		if awaited {
			c.error("PP403", "record construction is synchronous", n.Span)
		}
	} else if s.Function != nil {
		fn = s.Function
		params = fn.Parameters
		ret = fn.Returns
		if c.constant {
			c.error("PP502", "ordinary calls cannot initialize module constants", n.Span)
			return model.Invalid
		}
		if fn.Kind == "async" && !awaited {
			c.error("PP401", "coroutine values are not first-class; directly await this async call", n.Span)
		}
		if fn.Kind == "sync" && awaited {
			c.error("PP403", "cannot await a synchronous function", n.Span)
		}
		if awaited && (c.f == nil || c.f.Kind != "async") {
			c.error("PP404", "await is permitted only inside async functions", n.Span)
		}
	} else {
		c.error("PP301", name+" does not name a callable declaration", n.Span)
		return model.Invalid
	}
	bound := make([]*model.Node, len(params))
	keyword := false
	position := 0
	for _, a := range n.Items("args") {
		if a.Kind != "Arg" || a.A("unpack") != "" || a.Get("value") == nil {
			c.error("PP302", "argument unpacking is prohibited", a.Span)
			continue
		}
		idx := -1
		key := a.A("name")
		if key == "" {
			if keyword {
				c.error("PP302", "positional arguments cannot follow keyword arguments", a.Span)
			}
			idx = position
			position++
		} else {
			keyword = true
			for i, p := range params {
				if p.Name == key {
					idx = i
					break
				}
			}
		}
		if idx < 0 || idx >= len(params) {
			c.error("PP302", "unexpected argument "+key, a.Span)
			c.expr(a.Get("value"), model.Invalid)
			continue
		}
		if bound[idx] != nil {
			c.error("PP302", "duplicate argument for "+params[idx].Name, a.Span)
			continue
		}
		bound[idx] = a.Get("value")
	}
	edge := model.CallEdge{Callee: s.Name, Span: n.Span, Kind: "sync", CapabilityArguments: []string{}, HostRefArguments: []string{}}
	if c.f != nil {
		edge.Caller = c.f.Name
	}
	if awaited {
		edge.Kind = "await"
	}
	for i, param := range params {
		a := bound[i]
		if a == nil {
			code := "PP302"
			if param.Type.Kind == "capability" {
				code = "PP312"
			}
			c.error(code, fmt.Sprintf("call to %s requires argument %s: %s", s.Name, param.Name, param.Type), n.Span)
			d := &c.result.Diagnostics[len(c.result.Diagnostics)-1]
			d.Related = append(d.Related, param.Span)
			if code == "PP312" {
				d.Notes = append(d.Notes, "Pass an original capability parameter of the exact declared type; capability labels do not grant subtyping.")
			}
			continue
		}
		if param.Type.Kind == "capability" || param.Type.Kind == "host_ref" {
			code := "PP312"
			if param.Type.Kind == "host_ref" {
				code = "PP334"
			}
			v, ok := c.vars[a.A("name")]
			if a.Kind != "Name" || !ok || !v.Parameter || !v.Assigned || !v.Type.Equal(param.Type) {
				c.error(code, "argument "+param.Name+" requires direct forwarding of an original parameter of exact type "+param.Type.String(), a.Span)
				d := &c.result.Diagnostics[len(c.result.Diagnostics)-1]
				d.Related = append(d.Related, param.Span)
				continue
			}
			if param.Type.Kind == "capability" {
				edge.CapabilityArguments = append(edge.CapabilityArguments, a.A("name"))
			} else {
				edge.HostRefArguments = append(edge.HostRefArguments, a.A("name"))
			}
			c.fact(a, param.Type, "", "direct parameter forwarding to "+s.Name+"."+param.Name)
		} else {
			actual := c.expr(a, param.Type)
			c.expect(param.Type, actual, a.Span)
		}
	}
	if fn != nil && c.f != nil {
		c.result.Calls = append(c.result.Calls, edge)
	}
	c.fact(n, ret, s.Name, "statically resolved "+edge.Kind+" call")
	return ret
}

// The intrinsic table is closed and versioned with the verifier. There is no
// fallback to Python callable lookup, protocol dispatch or a library import.
func (c *checker) intrinsic(name string, n *model.Node) model.Type {
	args := []model.Type{}
	for _, a := range n.Items("args") {
		if a.Kind != "Arg" || a.A("name") != "" || a.A("unpack") != "" {
			c.error("PP302", "intrinsics accept only explicit positional arguments", a.Span)
		}
		args = append(args, c.expr(a.Get("value"), model.Invalid))
	}
	fail := func() model.Type {
		c.error("PP303", "no sealed intrinsic signature matches "+name, n.Span)
		return model.Invalid
	}
	if len(args) == 1 {
		t := args[0]
		switch name {
		case "len":
			if sequence(t) {
				return model.Int
			}
		case "abs":
			if numeric(t) {
				return t
			}
		case "int":
			if t.Kind == "bool" || numeric(t) || t.Kind == "str" || t.Kind == "bytes" {
				return model.Int
			}
		case "float":
			if t.Kind == "bool" || numeric(t) || t.Kind == "str" || t.Kind == "bytes" {
				return model.Float
			}
		case "str":
			if t.Kind == "bool" || numeric(t) || t.Kind == "str" || t.Kind == "None" {
				return model.Str
			}
		case "bytes":
			if t.Kind == "bytes" || t.Kind == "tuple" && t.Elem != nil && t.Elem.Kind == "int" {
				return model.Bytes
			}
		case "all", "any":
			if t.Kind == "tuple" && t.Elem != nil && t.Elem.Kind == "bool" {
				return model.Bool
			}
		case "sum":
			if t.Kind == "tuple" && t.Elem != nil && t.Elem.Kind == "int" {
				return model.Int
			}
		case "min", "max":
			if t.Kind == "tuple" && t.Elem != nil && (numeric(*t.Elem) || t.Elem.Kind == "str" || t.Elem.Kind == "bytes") {
				return *t.Elem
			}
		}
	}
	if (name == "min" || name == "max") && len(args) >= 2 {
		t := args[0]
		if !numeric(t) && t.Kind != "str" && t.Kind != "bytes" {
			return fail()
		}
		for _, a := range args[1:] {
			if !t.Equal(a) {
				return fail()
			}
		}
		return t
	}
	if name == "sum" && len(args) == 2 && args[0].Kind == "tuple" && args[0].Elem != nil && numeric(args[1]) && args[0].Elem.Equal(args[1]) {
		return args[1]
	}
	if name == "range" {
		c.error("PP304", "range is ephemeral and can only be consumed directly by a for loop", n.Span)
		return model.Invalid
	}
	return fail()
}
func sequence(t model.Type) bool { return t.Kind == "tuple" || t.Kind == "str" || t.Kind == "bytes" }
func numeric(t model.Type) bool  { return t.Kind == "int" || t.Kind == "float" }
