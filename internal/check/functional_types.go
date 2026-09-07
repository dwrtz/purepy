package check

import (
	"fmt"
	"sort"
	"strings"

	"github.com/dwrtz/purepy/internal/model"
)

// The 0.2 profile recognizes these standard-library declarations directly.
// No support module is imported or executed by verification.
func sealedPackage(name string) bool {
	for _, root := range []string{"typing", "collections", "copy"} {
		if name == root || strings.HasPrefix(name, root+".") {
			return true
		}
	}
	return false
}
func scope(m *Module) *Module {
	out := *m
	out.TypeVars = map[string]model.Type{}
	out.Bindings = map[string]*Symbol{}
	for k, v := range m.TypeVars {
		out.TypeVars[k] = v
	}
	for k, v := range m.Bindings {
		out.Bindings[k] = v
	}
	return &out
}
func (p *Program) parameters(m *Module, owner string, nodes []*model.Node) []string {
	params := []string{}
	for _, n := range nodes {
		name := n.A("name")
		if n.Kind != "Name" || forbiddenName(name) || m.TypeVars[name].Kind != "" || m.Bindings[name] != nil {
			p.error("PP203", "type parameters must be distinct unbounded data names", n.Span)
			continue
		}
		if _, ok := model.Primitive(name); ok || strings.Contains(" tuple len ord chr range abs min max sum all any int float str bytes ", " "+name+" ") {
			p.error("PP203", "type parameter shadows a builtin type", n.Span)
			continue
		}
		key := owner + "." + name
		params = append(params, key)
		m.TypeVars[name] = model.Type{Kind: "typevar", Name: key}
	}
	return params
}
func typeNodes(n *model.Node) []*model.Node {
	if n == nil {
		return nil
	}
	if n.Kind == "Tuple" {
		return n.Items("elements")
	}
	return []*model.Node{n}
}
func (p *Program) signatures(m *Module) {
	// Make generic arities available before resolving any forward annotation.
	for _, n := range m.Tree.Items("body") {
		s := p.Symbols[m.Name+"."+n.A("name")]
		if s == nil || s.Node != n {
			continue
		}
		local := scope(m)
		params := p.parameters(local, s.Name, n.Items("typeparams"))
		p.Scopes[s.Name] = local
		if s.Record != nil {
			s.Record.TypeParams = params
		}
		if s.Function != nil {
			s.Function.TypeParams = params
		}
	}
	// Other modules' generic arities can be needed before their signature pass.
	for _, other := range p.Order {
		for _, n := range p.Modules[other].Tree.Items("body") {
			if n.Kind == "Record" {
				s := p.Symbols[other+"."+n.A("name")]
				if s != nil && s.Record != nil && len(s.Record.TypeParams) == 0 {
					for _, tp := range n.Items("typeparams") {
						if tp.Kind == "Name" {
							s.Record.TypeParams = append(s.Record.TypeParams, s.Name+"."+tp.A("name"))
						}
					}
				}
			}
		}
	}
	for _, n := range m.Tree.Items("body") {
		if n.Kind == "Alias" { // Validate even unused aliases.
			left := n.Get("left")
			if left == nil {
				continue
			}
			name := left.A("name")
			if left.Kind == "Index" {
				name = left.Get("value").A("name")
			}
			s := m.Bindings[name]
			if s != nil && s.Node == n {
				p.alias(m, s, nil, n.Span, true)
			}
			continue
		}
		s := p.Symbols[m.Name+"."+n.A("name")]
		if s == nil || s.Node != n {
			continue
		}
		local := p.Scopes[s.Name]
		if s.Function != nil {
			p.functionalFunction(local, s.Function, n)
			continue
		}
		if s.Record == nil {
			continue
		}
		bases := n.Items("bases")
		valid := len(bases) == 1 && bases[0].Kind == "Name"
		if valid {
			b := m.Bindings[bases[0].A("name")]
			valid = b != nil && b.Name == "typing.NamedTuple" && m.ImportedAt[bases[0].A("name")].Start < n.Span.Start
		}
		if !valid || len(n.Items("decorators")) != 0 || n.A("unsupported") != "" {
			d := p.error("PP202", "records require exactly typing.NamedTuple as their base and no decorators", n.Span).WithSymbol(s.Name).WithType("record", s.Type)
			for i, base := range bases {
				p.annotationContext(d, m, base, fmt.Sprintf("base.%d", i+1))
			}
			for i, dec := range n.Items("decorators") {
				p.annotationContext(d, m, dec, fmt.Sprintf("decorator.%d", i+1))
			}
		}
		seen := map[string]model.Parameter{}
		for i, a := range n.Items("body") {
			if i == 0 && doc(a) {
				continue
			}
			target := a.Get("target")
			if a.Kind != "Assign" || target == nil || target.Kind != "Name" || a.Get("value") != nil {
				owner := s.Name
				if target != nil && target.Kind == "Name" {
					owner += "." + target.A("name")
				} else if a.Kind == "Function" {
					owner += "." + a.A("name")
				}
				d := p.error("PP202", "NamedTuple bodies contain only annotated fields without defaults", a.Span).WithSymbol(owner).WithType("record", s.Type)
				p.annotationContext(d, m, a.Get("annotation"), "annotation")
				continue
			}
			name := target.A("name")
			t := p.annotationFor(local, a.Get("annotation"), a.Span, s.Name+"."+name)
			previous, duplicate := seen[name]
			if duplicate || strings.HasPrefix(name, "_") {
				p.error("PP202", "invalid or duplicate NamedTuple field "+name, a.Span).WithSymbol(s.Name+"."+name).WithType("declared", t).WithType("previous", previous.Type).WithRelated(previous.Span)
			}
			if !duplicate {
				seen[name] = model.Parameter{Name: name, Type: t, Span: a.Span}
			}
			if !t.Pure() {
				p.error("PP201", "record fields require immutable data", a.Span)
			}
			s.Record.Fields = append(s.Record.Fields, model.Parameter{Name: name, Type: t, Span: a.Span})
		}
	}
}
func (p *Program) functionalFunction(m *Module, f *model.Function, n *model.Node) {
	if n.A("unsupported") != "" || len(n.Items("decorators")) > 0 {
		d := p.error("PP003", "functions require fixed annotated signatures without decorators", n.Span).WithSymbol(f.Name)
		for i, dec := range n.Items("decorators") {
			p.annotationContext(d, m, dec, fmt.Sprintf("decorator.%d", i+1))
		}
	}
	seen := map[string]model.Parameter{}
	for _, a := range n.Items("params") {
		name := a.A("name")
		if m.TypeVars[name].Kind != "" {
			p.error("PP203", "parameters cannot shadow type parameters", a.Span)
		}
		t := p.annotationFor(m, a.Get("annotation"), a.Span, f.Name+"."+name)
		previous, duplicate := seen[name]
		if duplicate || forbiddenName(name) {
			p.error("PP103", "invalid or duplicate parameter "+name, a.Span).WithSymbol(f.Name+"."+name).WithType("declared", t).WithType("previous", previous.Type).WithRelated(previous.Span)
		}
		if a.Kind != "Param" || a.A("unsupported") != "" {
			p.error("PP003", "parameter defaults, variadics and signature markers are prohibited", a.Span).WithSymbol(f.Name+"."+name).WithType("parameter", t)
		}
		if !duplicate {
			seen[name] = model.Parameter{Name: name, Type: t, Span: a.Span}
		}
		if t.Kind == "callable" && f.Kind != "sync" {
			p.error("PP201", "higher-order async functions are outside this profile", a.Span)
		}
		f.Parameters = append(f.Parameters, model.Parameter{Name: name, Type: t, Span: a.Span, Labels: p.labels(t)})
	}
	f.ReturnSpan = n.Span
	if n.Get("returns") != nil {
		f.ReturnSpan = n.Get("returns").Span
	}
	f.Returns = p.annotationFor(m, n.Get("returns"), n.Span, f.Name)
	if !f.Returns.FunctionalValue() || f.Kind != "sync" && f.Returns.Kind == "callable" {
		p.error("PP201", "function results require immutable data or pure synchronous functions", f.ReturnSpan)
	}
	if !f.Pure() {
		for _, a := range f.Parameters {
			if a.Type.Kind == "callable" {
				p.error("PP201", "higher-order functions require pure synchronous signatures", a.Span)
			}
		}
		if f.Returns.Kind == "callable" {
			p.error("PP201", "effectful functions cannot return functions", f.ReturnSpan)
		}
	}
	if len(f.TypeParams) > 0 && (!f.Pure() || f.Kind != "sync") {
		p.error("PP203", "generic functions require pure synchronous signatures", f.Span)
	}
	// Prelink nested declarations; workers later check their captures at creation.
	var nested func([]*model.Node)
	nested = func(body []*model.Node) {
		for _, child := range body {
			if child.Kind == "Function" {
				name := child.A("name")
				full := f.Name + "." + name
				local := scope(m)
				kind := "sync"
				if child.A("async") == "true" {
					kind = "async"
					p.error("PP003", "nested async functions are prohibited", child.Span)
				}
				nf := &model.Function{Name: full, Parent: f.Name, Module: f.Module, Kind: kind, Origin: "project", Trust: "verified", Span: child.Span, Body: child.Items("body"), Parameters: []model.Parameter{}}
				nf.TypeParams = p.parameters(local, full, child.Items("typeparams"))
				if len(nf.TypeParams) > 0 {
					p.error("PP203", "nested functions must be monomorphic", child.Span)
				}
				if p.add(&Symbol{Name: full, Kind: "function", Function: nf, Node: child, Module: f.Module}, child.Span) {
					p.Functions[full] = nf
					p.Scopes[full] = local
					p.functionalFunction(local, nf, child)
				}
			} else if child.Kind == "If" || child.Kind == "For" || child.Kind == "While" {
				nested(child.Items("body"))
				nested(child.Items("else"))
			}
		}
	}
	nested(f.Body)
}
func (p *Program) alias(m *Module, s *Symbol, args []model.Type, at model.Span, validate bool) model.Type {
	if p.AliasActive[s.Name] {
		p.error("PP204", "recursive aliases must pass through a named record", at)
		return model.Invalid
	}
	local := scope(p.Modules[s.Module])
	left := s.Node.Get("left")
	var nodes []*model.Node
	if left.Kind == "Index" {
		nodes = typeNodes(left.Get("index"))
	}
	params := p.parameters(local, s.Name, nodes)
	if !validate && len(params) != len(args) {
		p.error("PP203", "wrong number of alias type arguments", at)
		return model.Invalid
	}
	p.AliasActive[s.Name] = true
	t := p.annotation(local, s.Node.Get("right"), at)
	delete(p.AliasActive, s.Name)
	if validate {
		return t
	}
	subs := map[string]model.Type{}
	for i, k := range params {
		subs[k] = args[i]
	}
	return substitute(t, subs)
}
func (p *Program) functionalAnnotation(m *Module, n *model.Node, at model.Span) model.Type {
	p.TypeSteps++
	if p.TypeSteps > 100000 {
		p.error("PP203", "type expansion budget exceeded", at)
		return model.Invalid
	}
	if n == nil {
		p.error("PP203", "explicit type annotation required", at)
		return model.Invalid
	}
	if n.Kind == "Name" && m != nil {
		if t, ok := m.TypeVars[n.A("name")]; ok {
			return t
		}
		if s := m.Bindings[n.A("name")]; s != nil {
			if s.Kind == "alias" {
				return p.alias(m, s, nil, at, false)
			}
			if s.Record != nil && len(s.Record.TypeParams) > 0 {
				p.error("PP203", "generic records require explicit type arguments", at)
				return model.Invalid
			}
		}
	}
	if n.Kind == "Index" && n.Get("value") != nil && n.Get("value").Kind == "Name" {
		name := n.Get("value").A("name")
		nodes := typeNodes(n.Get("index"))
		var s *Symbol
		if m != nil {
			s = m.Bindings[name]
		}
		if s != nil && s.Name == "collections.abc.Callable" {
			if len(nodes) != 2 || nodes[0].Kind != "TypeList" {
				p.error("PP203", "Callable requires an explicit parameter list and result", at)
				return model.Invalid
			}
			t := model.Type{Kind: "callable", Params: []model.Type{}}
			for _, a := range nodes[0].Items("elements") {
				t.Params = append(t.Params, p.annotation(m, a, at))
			}
			ret := p.annotation(m, nodes[1], at)
			t.Returns = &ret
			if !t.FunctionalValue() {
				p.error("PP201", "Callable signatures cannot contain capabilities or host references", at)
				return model.Invalid
			}
			return t
		}
		if name == "tuple" && s == nil && !(len(nodes) == 2 && nodes[1].Kind == "Literal" && nodes[1].A("type") == "ellipsis") {
			t := model.Type{Kind: "product", Args: []model.Type{}}
			for _, a := range nodes {
				t.Args = append(t.Args, p.annotation(m, a, at))
			}
			if !t.Pure() {
				p.error("PP201", "tuple fields require immutable data", at)
			}
			return t
		}
		if s != nil && (s.Record != nil || s.Kind == "alias") {
			args := []model.Type{}
			for _, a := range nodes {
				t := p.annotation(m, a, at)
				if !t.Pure() {
					p.error("PP201", "type arguments require immutable data", a.Span)
				}
				args = append(args, t)
			}
			if s.Kind == "alias" {
				return p.alias(m, s, args, at, false)
			}
			if len(args) != len(s.Record.TypeParams) {
				p.error("PP203", "wrong number of record type arguments", at)
				return model.Invalid
			}
			return model.Type{Kind: "record", Name: s.Name, Args: args}
		}
	}
	return p.dataAnnotation(m, n, at)
}

// Types are finite syntax trees containing nominal references, never expanded
// recursive layouts. Substitution is single-pass, preventing polymorphic growth.
func substitute(t model.Type, subs map[string]model.Type) model.Type {
	if t.Kind == "typevar" {
		if v, ok := subs[t.Name]; ok {
			return v
		}
		return t
	}
	if t.Elem != nil {
		v := substitute(*t.Elem, subs)
		t.Elem = &v
	}
	if t.Returns != nil {
		v := substitute(*t.Returns, subs)
		t.Returns = &v
	}
	if t.Args != nil {
		xs := make([]model.Type, len(t.Args))
		for i, a := range t.Args {
			xs[i] = substitute(a, subs)
		}
		t.Args = xs
	}
	if t.Params != nil {
		xs := make([]model.Type, len(t.Params))
		for i, a := range t.Params {
			xs[i] = substitute(a, subs)
		}
		t.Params = xs
	}
	return t
}
func recordFields(r *model.Record, t model.Type) []model.Parameter {
	subs := map[string]model.Type{}
	for i, k := range r.TypeParams {
		if i < len(t.Args) {
			subs[k] = t.Args[i]
		}
	}
	fields := append([]model.Parameter{}, r.Fields...)
	for i := range fields {
		fields[i].Type = substitute(fields[i].Type, subs)
	}
	return fields
}
func (p *Program) functionalRecords() {
	// On a cycle, parameters must be passed through in their original positions.
	// This admits regular recursive records while bounding instantiated layouts.
	budget := 100000
	for _, name := range sortedRecords(p.Records) {
		root := p.Records[name]
		active := map[string]bool{}
		seen := map[string]bool{}
		var visit func(model.Type, int)
		visit = func(t model.Type, depth int) {
			budget--
			if budget == 0 {
				p.error("PP204", "record type analysis budget exceeded", root.Span)
			}
			if budget <= 0 {
				return
			}
			if !t.WithinLimit() {
				p.error("PP204", "record instance exceeds analysis limit", root.Span)
				return
			}
			key := t.String()
			if seen[key] {
				return
			}
			seen[key] = true
			if depth > 128 {
				p.error("PP204", "recursive type graph exceeds depth limit", root.Span)
				return
			}
			if t.Elem != nil {
				visit(*t.Elem, depth+1)
			}
			for _, a := range t.Args {
				visit(a, depth+1)
			}
			if t.Kind != "record" {
				return
			}
			r := p.Records[t.Name]
			if r == nil {
				return
			}
			if active[t.Name] {
				for i, a := range t.Args {
					if i >= len(root.TypeParams) || a.Kind != "typevar" || a.Name != root.TypeParams[i] {
						p.error("PP204", "recursive records must preserve their type parameters", root.Span)
						break
					}
				}
				return
			}
			active[t.Name] = true
			for _, f := range recordFields(r, t) {
				visit(f.Type, depth+1)
			}
			delete(active, t.Name)
		}
		args := []model.Type{}
		for _, k := range root.TypeParams {
			args = append(args, model.Type{Kind: "typevar", Name: k})
		}
		visit(model.Type{Kind: "record", Name: name, Args: args}, 0)
	}
}

func sortedRecords(rs map[string]*model.Record) []string {
	xs := []string{}
	for k := range rs {
		xs = append(xs, k)
	}
	sort.Strings(xs)
	return xs
}
