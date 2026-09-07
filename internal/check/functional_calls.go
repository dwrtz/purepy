package check

import (
	"fmt"
	"github.com/dwrtz/purepy/internal/model"
	"strconv"
	"strings"
)

func functionType(f *model.Function) model.Type {
	t := model.Type{Kind: "callable", Returns: &f.Returns, Variables: f.TypeParams, Origins: []string{f.Name}, Params: []model.Type{}}
	for _, p := range f.Parameters {
		t.Params = append(t.Params, p.Type)
	}
	return t
}
func (c *checker) dependency(name, kind string, at model.Span) {
	if c.f != nil {
		c.result.Calls = append(c.result.Calls, model.CallEdge{Caller: c.f.Name, Callee: name, Kind: kind, Span: at, CapabilityArguments: []string{}, HostRefArguments: []string{}})
	}
}
func (c *checker) functionValue(f *model.Function, want model.Type, at model.Span) model.Type {
	if c.constant || f.Kind != "sync" || !f.Pure() {
		c.error("PP301", "only verified pure synchronous functions can be used as values", at)
		return model.Invalid
	}
	t := functionType(f)
	if !t.WithinLimit() {
		c.error("PP203", "function type exceeds analysis limit", at)
		return model.Invalid
	}
	if len(f.TypeParams) > 0 {
		subs := map[string]model.Type{}
		vars := map[string]bool{}
		for _, k := range f.TypeParams {
			vars[k] = true
		}
		if want.Kind == "callable" {
			c.infer(t, want, subs, vars, at, false)
		}
		for _, k := range f.TypeParams {
			if _, ok := subs[k]; !ok {
				c.error("PP203", "generic function values require a concrete Callable context", at)
				return model.Invalid
			}
		}
		t = substitute(t, subs)
		t.Variables = nil
	}
	c.dependency(f.Name, "function_value", at)
	if len(f.TypeParams) > 0 && c.f != nil {
		subs := map[string]model.Type{}
		vars := map[string]bool{}
		for _, k := range f.TypeParams {
			vars[k] = true
		}
		c.infer(functionType(f), t, subs, vars, at, false)
		for _, k := range f.TypeParams {
			c.result.Calls[len(c.result.Calls)-1].Instantiation = append(c.result.Calls[len(c.result.Calls)-1].Instantiation, subs[k])
		}
	}
	return t
}

// Rank-one inference solves only the called declaration's variables. All other
// type variables are rigid. Substitutions contain immutable data, never functions.
func (c *checker) infer(pattern, actual model.Type, subs map[string]model.Type, vars map[string]bool, at model.Span, allowOptional bool) {
	if pattern.Kind == "invalid" || actual.Kind == "invalid" {
		return
	}
	if pattern.Kind == "typevar" && vars[pattern.Name] {
		if !actual.Pure() {
			c.error("PP203", "generic type arguments must be immutable data", at)
			return
		}
		if old, ok := subs[pattern.Name]; ok {
			if !old.Equal(actual) {
				c.expect(old, actual, at)
			}
		} else {
			subs[pattern.Name] = actual
		}
		return
	}
	if allowOptional && pattern.Kind == "optional" && pattern.Elem != nil {
		if actual.Kind == "None" {
			return
		}
		if actual.Kind == "optional" && actual.Elem != nil {
			c.infer(*pattern.Elem, *actual.Elem, subs, vars, at, false)
		} else {
			c.infer(*pattern.Elem, actual, subs, vars, at, false)
		}
		return
	}
	if pattern.Kind != actual.Kind || pattern.Name != actual.Name {
		c.expect(substitute(pattern, subs), actual, at)
		return
	}
	if pattern.Elem != nil && actual.Elem != nil {
		c.infer(*pattern.Elem, *actual.Elem, subs, vars, at, false)
	}
	if len(pattern.Args) != len(actual.Args) || len(pattern.Params) != len(actual.Params) {
		c.error("PP205", "type arity mismatch", at)
		return
	}
	for i, a := range pattern.Args {
		c.infer(a, actual.Args[i], subs, vars, at, false)
	}
	for i, a := range pattern.Params {
		c.infer(a, actual.Params[i], subs, vars, at, false)
	}
	if pattern.Returns != nil && actual.Returns != nil {
		c.infer(*pattern.Returns, *actual.Returns, subs, vars, at, false)
	}
}
func (c *checker) functionalCall(n *model.Node, want model.Type) model.Type {
	target := n.Get("target")
	if target == nil || target.Kind != "Name" {
		return c.call(n, false)
	}
	name := target.A("name")
	if forbiddenName(name) {
		c.error("PP104", "dunder names cannot be called", n.Span)
		return model.Invalid
	}
	s := c.m.Bindings[name]
	v, local := c.vars[name]
	if !local && s != nil && s.Name == "copy.replace" {
		return c.replace(n)
	}
	if !local && (s == nil || s.Function == nil && s.Record == nil || s.Record != nil && len(s.Record.TypeParams) == 0 || s.Function != nil && len(s.Function.TypeParams) == 0 && !hasCallbacks(s.Function)) {
		return c.call(n, false)
	}
	var params []model.Parameter
	var ret model.Type
	var variables []string
	callee := ""
	kind := "sync"
	if local {
		if !v.Assigned || v.Current.Kind != "callable" || v.Current.Returns == nil {
			c.nameContext(c.error("PP301", "local call requires an assigned pure Callable", target.Span), name)
			return model.Invalid
		}
		if c.constant {
			c.error("PP502", "module initializers cannot call functions", n.Span)
			return model.Invalid
		}
		ret = *v.Current.Returns
		kind = "callback"
		callee = ""
		for i, t := range v.Current.Params {
			params = append(params, model.Parameter{Name: fmt.Sprint(i), Type: t, Span: target.Span})
		}
		slot := fmt.Sprintf("$local:%s:%d", c.f.Name, n.Span.Start)
		c.result.Bindings = append(c.result.Bindings, callableBinding{Slot: slot, Sources: v.Current.Origins})
		c.dependency(slot, "callback", n.Span)
	} else if s != nil && s.Record != nil {
		if c.constant { // Module constants must follow their constructors.
			if at, imported := c.m.ImportedAt[name]; imported && at.Start > n.Span.Start || !imported && s.Node != nil && s.Node.Span.Start > n.Span.Start {
				c.error("PP502", "record used before declaration or import", n.Span)
			}
		}
		params = s.Record.Fields
		variables = s.Record.TypeParams
		ret = s.Type
		for _, k := range variables {
			ret.Args = append(ret.Args, model.Type{Kind: "typevar", Name: k})
		}
	} else if s != nil && s.Function != nil {
		if c.constant {
			c.error("PP502", "module initializers cannot call functions", n.Span)
			return model.Invalid
		}
		params = s.Function.Parameters
		variables = s.Function.TypeParams
		ret = s.Function.Returns
		callee = s.Name
	} else {
		c.error("PP301", "name does not identify a callable declaration", n.Span)
		return model.Invalid
	}
	bound := make([]*model.Node, len(params))
	position := 0
	keyword := false
	for _, a := range n.Items("args") {
		if a.Kind != "Arg" || a.A("unpack") != "" || a.Get("value") == nil {
			c.error("PP302", "argument unpacking is prohibited", a.Span)
			continue
		}
		idx := -1
		key := a.A("name")
		if key == "" {
			if keyword {
				c.error("PP302", "positional argument follows keyword", a.Span)
			}
			idx = position
			position++
		} else {
			keyword = true
			if local {
				c.error("PP302", "Callable invocation uses positional arguments", a.Span)
			} else {
				for i, p := range params {
					if p.Name == key {
						idx = i
						break
					}
				}
			}
		}
		if idx < 0 || idx >= len(params) {
			c.error("PP302", "unexpected argument "+key, a.Span)
			c.expr(a.Get("value"), model.Invalid)
			continue
		}
		if bound[idx] != nil {
			c.error("PP302", "duplicate argument", a.Span)
			continue
		}
		bound[idx] = a.Get("value")
	}
	vars := map[string]bool{}
	subs := map[string]model.Type{}
	for _, k := range variables {
		vars[k] = true
	}
	// Recursive calls are monomorphic, including calls from nested closures.
	if s != nil && s.Function != nil && c.f != nil && (c.f.Name == s.Name || strings.HasPrefix(c.f.Name, s.Name+".")) {
		for _, k := range variables {
			subs[k] = model.Type{Kind: "typevar", Name: k}
		}
	}
	if len(vars) > 0 && want.Kind != "invalid" && want.Kind != "" {
		context := want
		if context.Kind == "optional" && ret.Kind != "optional" && ret.Kind != "typevar" && context.Elem != nil {
			context = *context.Elem
		}
		c.infer(ret, context, subs, vars, n.Span, false)
	}
	actuals := make([]model.Type, len(params))
	// Data arguments determine callback contexts, regardless of parameter order.
	for pass := 0; pass < 2; pass++ {
		for i, p := range params {
			if (p.Type.Kind == "callable") != (pass == 1) {
				continue
			}
			a := bound[i]
			if a == nil {
				c.error("PP302", "missing argument "+p.Name, n.Span)
				continue
			}
			expected := substitute(p.Type, subs)
			// A monomorphic named callback can supply previously unknown type variables.
			if pass == 1 && a.Kind == "Name" {
				if f := c.m.Bindings[a.A("name")]; f != nil && f.Function != nil && len(f.Function.TypeParams) == 0 {
					actuals[i] = c.functionValue(f.Function, model.Invalid, a.Span)
				} else {
					actuals[i] = c.expr(a, expected)
				}
			} else {
				actuals[i] = c.expr(a, expected)
			}
			if p.Type.Kind == "callable" && !local && s != nil && s.Function != nil {
				c.result.Bindings = append(c.result.Bindings, callableBinding{Slot: "$param:" + s.Name + "." + p.Name, Sources: actuals[i].Origins})
			}
			if len(vars) > 0 {
				c.infer(p.Type, actuals[i], subs, vars, a.Span, true)
			}
		}
	}
	for _, k := range variables {
		if _, ok := subs[k]; !ok {
			c.error("PP203", "cannot infer type argument "+k+"; add an explicit contextual annotation", n.Span)
			return model.Invalid
		}
	}
	for i, p := range params {
		if bound[i] != nil {
			c.expect(substitute(p.Type, subs), actuals[i], bound[i].Span)
		}
	}
	if local {
		invocation := callableInvocation{Targets: v.Current.Origins, Arguments: map[int][]string{}}
		for i, a := range actuals {
			if a.Kind == "callable" {
				invocation.Arguments[i] = a.Origins
			}
		}
		c.result.Invocations = append(c.result.Invocations, invocation)
	}
	ret = substitute(ret, subs)
	if callee != "" {
		c.dependency(callee, kind, n.Span)
		if c.f != nil {
			for _, k := range variables {
				c.result.Calls[len(c.result.Calls)-1].Instantiation = append(c.result.Calls[len(c.result.Calls)-1].Instantiation, subs[k])
			}
		}
		if ret.Kind == "callable" {
			ret.Origins = []string{"$return:" + callee}
		}
	}
	if local && ret.Kind == "callable" {
		for _, origin := range v.Current.Origins {
			ret.Origins = append(ret.Origins, "$result:"+origin)
		}
	}
	return ret
}
func (c *checker) replace(n *model.Node) model.Type {
	args := n.Items("args")
	if c.constant || len(args) == 0 || args[0].Kind != "Arg" || args[0].A("name") != "" {
		c.error("PP302", "replace requires a record positional argument followed by explicit field keywords", n.Span)
		return model.Invalid
	}
	t := c.expr(args[0].Get("value"), model.Invalid)
	r := c.p.Records[t.Name]
	if t.Kind != "record" || r == nil {
		c.error("PP303", "replace requires an exact verified NamedTuple", n.Span)
		return model.Invalid
	}
	fields := recordFields(r, t)
	seen := map[string]bool{}
	for _, a := range args[1:] {
		name := a.A("name")
		expected := model.Invalid
		for _, f := range fields {
			if f.Name == name {
				expected = f.Type
			}
		}
		if a.Kind != "Arg" || a.A("unpack") != "" || name == "" || seen[name] || expected.Kind == "invalid" {
			c.error("PP302", "replace requires distinct declared field keywords", a.Span)
		}
		seen[name] = true
		actual := c.expr(a.Get("value"), expected)
		c.expect(expected, actual, a.Span)
	}
	return t
}
func (c *checker) product(n *model.Node, want model.Type) model.Type {
	xs := n.Items("elements")
	t := model.Type{Kind: "product", Args: []model.Type{}}
	if len(xs) != len(want.Args) {
		c.error("PP207", "product tuple length does not match its annotation", n.Span)
	}
	for i, x := range xs {
		expected := model.Invalid
		if i < len(want.Args) {
			expected = want.Args[i]
		}
		actual := c.expr(x, expected)
		c.expect(expected, actual, x.Span)
		if !actual.Pure() {
			c.error("PP201", "tuple elements require immutable data", x.Span)
		}
		if expected.Kind != "invalid" {
			actual = expected
		}
		t.Args = append(t.Args, actual)
	}
	return t
}
func (c *checker) productIndex(n *model.Node, t model.Type) model.Type {
	index := n.Get("index")
	negative := false
	if index != nil && index.Kind == "Unary" && index.A("op") == "-" {
		negative = true
		index = index.Get("operand")
	}
	if n.Kind != "Index" || index == nil || index.Kind != "Literal" || index.A("type") != "int" {
		c.error("PP210", "product tuples require a constant integer index", n.Span)
		return model.Invalid
	}
	i, err := strconv.ParseInt(strings.ReplaceAll(index.Text, "_", ""), 0, 64)
	if negative {
		i = -i
	}
	if i < 0 {
		i += int64(len(t.Args))
	}
	if err != nil || i < 0 || i >= int64(len(t.Args)) {
		c.error("PP210", "product tuple index is out of bounds", n.Span)
		return model.Invalid
	}
	return t.Args[i]
}

// Concrete signatures use the same argument diagnostics as host/async calls.
func hasCallbacks(f *model.Function) bool {
	if f.Returns.Kind == "callable" {
		return true
	}
	for _, p := range f.Parameters {
		if p.Type.Kind == "callable" {
			return true
		}
	}
	return false
}
