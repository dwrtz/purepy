// Package check verifies the closed PurePy language without executing source.
package check

import (
	"fmt"
	"sort"
	"strings"

	"github.com/dwrtz/purepy/internal/diag"
	"github.com/dwrtz/purepy/internal/manifest"
	"github.com/dwrtz/purepy/internal/model"
)

type Module struct {
	Name       string
	Path       string
	Package    bool
	Tree       *model.Node
	Bindings   map[string]*Symbol
	ImportedAt map[string]model.Span
	Imports    []string
}
type Symbol struct {
	Name     string
	Kind     string
	Type     model.Type
	Node     *model.Node
	Module   string
	Function *model.Function
	Record   *model.Record
	Labels   []string
	Source   string
}
type Program struct {
	Modules         map[string]*Module
	Order           []string
	Symbols         map[string]*Symbol
	Functions       map[string]*model.Function
	Records         map[string]*model.Record
	Diagnostics     []diag.Diagnostic
	ExternalModules map[string]bool
}

func Link(modules []*Module, external *manifest.Set, entrypoints []string) *Program {
	p := &Program{Modules: map[string]*Module{}, Symbols: map[string]*Symbol{}, Functions: map[string]*model.Function{}, Records: map[string]*model.Record{}, ExternalModules: map[string]bool{}, Diagnostics: []diag.Diagnostic{}}
	for _, m := range modules {
		m.Bindings = map[string]*Symbol{}
		m.ImportedAt = map[string]model.Span{}
		p.Modules[m.Name] = m
		p.Order = append(p.Order, m.Name)
		if m.Name == "purepy" || strings.HasPrefix(m.Name, "purepy.") || m.Name == "typing" || strings.HasPrefix(m.Name, "typing.") {
			p.error("PP106", "verified modules cannot shadow sealed support packages: "+m.Name, m.Tree.Span)
		}
	}
	sort.Strings(p.Order)
	for _, m := range external.Modules {
		if m.Name == "purepy" || strings.HasPrefix(m.Name, "purepy.") || m.Name == "typing" || strings.HasPrefix(m.Name, "typing.") {
			p.error("PP601", "manifests cannot replace sealed support packages: "+m.Name, model.Span{File: m.Source, Line: 1, Column: 1})
		}
		p.ExternalModules[m.Name] = m.ImportSafe
		if _, ok := p.Modules[m.Name]; ok {
			p.error("PP601", "manifest module conflicts with verified module "+m.Name, model.Span{File: m.Source, Line: 1, Column: 1})
		}
	}
	for _, t := range external.Types {
		s := &Symbol{Name: t.Name, Kind: "type", Type: model.Type{Kind: t.Category, Name: t.Name}, Labels: t.Labels, Source: t.Source}
		p.add(s, model.Span{File: t.Source, Line: 1, Column: 1})
	}
	for _, f := range external.Functions {
		fn := &model.Function{Name: f.Name, Kind: f.Kind, Origin: "manifest", Trust: f.Trust, Source: f.Source, Span: model.Span{File: f.Source, Line: 1, Column: 1}, Parameters: []model.Parameter{}}
		p.add(&Symbol{Name: f.Name, Kind: "function", Function: fn, Source: f.Source}, fn.Span)
		p.Functions[f.Name] = fn
	}
	for _, name := range p.Order {
		p.index(p.Modules[name])
	}
	for _, name := range p.Order {
		p.imports(p.Modules[name])
	}
	p.cycles()
	for _, name := range p.Order {
		p.signatures(p.Modules[name])
	}
	for _, f := range external.Functions {
		fn := p.Functions[f.Name]
		if fn == nil || fn.Origin != "manifest" {
			continue
		}
		for _, a := range f.Parameters {
			t := p.typeText(nil, a.Type, fn.Span)
			fn.Parameters = append(fn.Parameters, model.Parameter{Name: a.Name, Type: t, Span: fn.Span, Labels: p.labels(t)})
		}
		fn.Returns = p.typeText(nil, f.Returns, fn.Span)
		if !fn.Returns.Pure() {
			p.error("PP602", "external function must return a Pure Value: "+fn.Name, fn.Span)
		}
		caps := 0
		for _, a := range fn.Parameters {
			if a.Type.Kind == "capability" {
				caps++
			}
			if fn.Trust == "pure" && !a.Type.Pure() {
				p.error("PP602", "trusted pure function accepts only Pure Values: "+fn.Name, fn.Span)
			}
		}
		if fn.Trust == "host" && caps == 0 {
			p.error("PP603", "host function requires at least one explicit capability parameter: "+fn.Name, fn.Span)
		}
	}
	p.recordCycles()
	// Constants are checked in import order, then source order within each module.
	done := map[string]bool{}
	visiting := map[string]bool{}
	var constants func(string)
	constants = func(name string) {
		if done[name] || visiting[name] {
			return
		}
		visiting[name] = true
		m := p.Modules[name]
		for _, dep := range m.Imports {
			constants(dep)
		}
		p.constants(m)
		done[name] = true
	}
	for _, name := range p.Order {
		constants(name)
	}
	seen := map[string]bool{}
	for _, name := range entrypoints {
		if seen[name] {
			p.error("PP701", "duplicate entrypoint "+name, model.Span{})
			continue
		}
		seen[name] = true
		f := p.Functions[name]
		if f == nil || f.Origin != "project" {
			p.error("PP701", "entrypoint must name a verified top-level function: "+name, model.Span{})
		}
	}
	return p
}

func (p *Program) error(code, message string, span model.Span) {
	p.Diagnostics = append(p.Diagnostics, diag.New(code, message, span))
}
func forbiddenName(s string) bool { return strings.HasPrefix(s, "__") && strings.HasSuffix(s, "__") }
func (p *Program) add(s *Symbol, span model.Span) bool {
	if _, ok := p.Symbols[s.Name]; ok {
		p.error("PP103", "duplicate declaration "+s.Name, span)
		return false
	}
	p.Symbols[s.Name] = s
	return true
}
func doc(n *model.Node) bool {
	return n != nil && n.Kind == "ExprStmt" && n.Get("value") != nil && n.Get("value").Kind == "Literal" && n.Get("value").A("type") == "str"
}
func (p *Program) index(m *Module) {
	if m.Tree == nil {
		return
	}
	for i, n := range m.Tree.Items("body") {
		if i == 0 && doc(n) {
			continue
		}
		if m.Package {
			p.error("PP101", "package initializers may contain only a docstring", n.Span)
			continue
		}
		s := &Symbol{Node: n, Module: m.Name}
		name := ""
		switch n.Kind {
		case "Import":
			continue
		case "Function":
			name = n.A("name")
			s.Kind = "function"
			kind := "sync"
			if n.A("async") == "true" {
				kind = "async"
			}
			s.Function = &model.Function{Name: m.Name + "." + name, Kind: kind, Origin: "project", Trust: "verified", Span: n.Span, Module: m.Name, Body: n.Items("body"), Parameters: []model.Parameter{}}
		case "Record":
			name = n.A("name")
			s.Kind = "type"
			s.Type = model.Type{Kind: "record", Name: m.Name + "." + name}
			s.Record = &model.Record{Name: s.Type.Name, Span: n.Span}
		case "Assign":
			if target := n.Get("target"); target != nil && target.Kind == "Name" {
				name = target.A("name")
				s.Kind = "constant"
			} else {
				p.error("PP501", "module assignment requires a single Final constant name", n.Span)
				continue
			}
		default:
			p.error("PP003", "unsupported module statement: "+n.Kind, n.Span)
			continue
		}
		if forbiddenName(name) {
			p.error("PP104", "dunder names are not accessible in verified code", n.Span)
		}
		s.Name = m.Name + "." + name
		if p.add(s, n.Span) {
			m.Bindings[name] = s
			if s.Function != nil {
				p.Functions[s.Name] = s.Function
			}
			if s.Record != nil {
				p.Records[s.Name] = s.Record
			}
		}
	}
}
func (p *Program) imports(m *Module) {
	if m.Tree == nil {
		return
	}
	for _, n := range m.Tree.Items("body") {
		if n.Kind != "Import" || m.Package {
			continue
		}
		from := n.A("module")
		if dep := p.Modules[from]; dep != nil {
			m.Imports = append(m.Imports, from)
		}
		for _, item := range n.Items("names") {
			name := item.A("name")
			if item.A("alias") != "" || name == "*" || strings.HasPrefix(from, ".") {
				p.error("PP102", "use absolute from imports without aliases or stars", item.Span)
				continue
			}
			if _, ok := m.Bindings[name]; ok {
				p.error("PP103", "import conflicts with existing name "+name, item.Span)
				continue
			}
			var s *Symbol
			if from == "purepy" && name == "value" || from == "typing" && name == "Final" {
				s = &Symbol{Name: from + "." + name, Kind: "support"}
			} else {
				s = p.Symbols[from+"."+name]
			}
			if s == nil {
				p.error("PP102", "unresolved direct import "+from+"."+name, item.Span)
				continue
			}
			if s.Source != "" && !p.ExternalModules[from] {
				p.error("PP604", "external module is not declared import_safe: "+from, item.Span)
				continue
			}
			m.Bindings[name] = s
			m.ImportedAt[name] = item.Span
		}
	}
}
func (p *Program) cycles() {
	state := map[string]int{}
	var visit func(string, []string)
	visit = func(name string, path []string) {
		if state[name] == 2 {
			return
		}
		if state[name] == 1 {
			p.error("PP105", "import cycle: "+strings.Join(append(path, name), " -> "), p.Modules[name].Tree.Span)
			return
		}
		state[name] = 1
		for _, d := range p.Modules[name].Imports {
			visit(d, append(path, name))
		}
		state[name] = 2
	}
	for _, n := range p.Order {
		visit(n, nil)
	}
}
func (p *Program) labels(t model.Type) []string {
	if s := p.Symbols[t.Name]; s != nil {
		return s.Labels
	}
	return nil
}
func (p *Program) signatures(m *Module) {
	for _, n := range m.Tree.Items("body") {
		s := p.Symbols[m.Name+"."+n.A("name")]
		if s == nil || s.Node != n {
			continue
		}
		if n.Kind == "Function" {
			f := s.Function
			if len(n.Items("decorators")) != 0 || n.A("unsupported") != "" {
				p.error("PP003", "functions require fixed annotated signatures and no decorators", n.Span)
			}
			seen := map[string]bool{}
			for _, a := range n.Items("params") {
				name := a.A("name")
				if seen[name] || forbiddenName(name) {
					p.error("PP103", "invalid or duplicate parameter "+name, a.Span)
				}
				seen[name] = true
				if a.Kind != "Param" || a.A("unsupported") != "" {
					p.error("PP003", "parameter defaults, variadics and signature markers are prohibited", a.Span)
				}
				t := p.annotation(m, a.Get("annotation"), a.Span)
				f.Parameters = append(f.Parameters, model.Parameter{Name: name, Type: t, Span: a.Span, Labels: p.labels(t)})
			}
			f.Returns = p.annotation(m, n.Get("returns"), n.Span)
			if !f.Returns.Pure() {
				p.error("PP201", "function return type must be a Pure Value", n.Span)
			}
		} else if n.Kind == "Record" {
			decs := n.Items("decorators")
			valid := len(decs) == 1 && decs[0].Kind == "Name" && m.Bindings[decs[0].A("name")] != nil && m.Bindings[decs[0].A("name")].Name == "purepy.value"
			if valid && m.ImportedAt[decs[0].A("name")].Start > n.Span.Start {
				valid = false
			}
			if !valid || len(n.Items("bases")) > 0 || n.A("unsupported") != "" {
				p.error("PP202", "records require the exact @value decorator, no bases or class options", n.Span)
			}
			seen := map[string]bool{}
			for i, a := range n.Items("body") {
				if i == 0 && doc(a) {
					continue
				}
				target := a.Get("target")
				if a.Kind != "Assign" || target == nil || target.Kind != "Name" || a.Get("value") != nil {
					p.error("PP202", "record bodies allow only annotated fields without defaults", a.Span)
					continue
				}
				name := target.A("name")
				if seen[name] || forbiddenName(name) {
					p.error("PP202", "invalid or duplicate record field "+name, a.Span)
				}
				seen[name] = true
				t := p.annotation(m, a.Get("annotation"), a.Span)
				if !t.Pure() {
					p.error("PP201", "record fields must contain only Pure Values", a.Span)
				}
				s.Record.Fields = append(s.Record.Fields, model.Parameter{Name: name, Type: t, Span: a.Span})
			}
		}
	}
}
func (p *Program) annotation(m *Module, n *model.Node, at model.Span) model.Type {
	if n == nil {
		p.error("PP203", "explicit type annotation required", at)
		return model.Invalid
	}
	if n.Kind == "Name" {
		name := n.A("name")
		if t, ok := model.Primitive(name); ok {
			if m == nil || m.Bindings[name] == nil {
				return t
			}
		}
		if m != nil {
			if s := m.Bindings[name]; s != nil && s.Kind == "type" {
				return s.Type
			}
		}
	}
	if n.Kind == "Literal" && n.A("type") == "None" {
		return model.None
	}
	if n.Kind == "Binary" && n.A("op") == "|" {
		l, r := n.Get("left"), n.Get("right")
		if r != nil && r.Kind == "Literal" && r.A("type") == "None" {
			t := p.annotation(m, l, n.Span)
			if t.Pure() && t.Kind != "optional" && t.Kind != "None" {
				return model.Optional(t)
			}
		}
	}
	if n.Kind == "Index" && n.Get("value") != nil && n.Get("value").Kind == "Name" && n.Get("value").A("name") == "tuple" && (m == nil || m.Bindings["tuple"] == nil) {
		idx := n.Get("index")
		if idx != nil && idx.Kind == "Tuple" {
			xs := idx.Items("elements")
			if len(xs) == 2 && xs[1].Kind == "Literal" && xs[1].A("type") == "ellipsis" {
				t := p.annotation(m, xs[0], n.Span)
				if t.Pure() {
					return model.Tuple(t)
				}
			}
		}
	}
	p.error("PP203", "unsupported type expression: "+n.Text, n.Span)
	return model.Invalid
}
func (p *Program) typeText(m *Module, text string, at model.Span) model.Type {
	s := strings.NewReplacer(" ", "", "\t", "").Replace(strings.TrimSpace(text))
	if t, ok := model.Primitive(s); ok {
		return t
	}
	if strings.HasSuffix(s, "|None") {
		t := p.typeText(m, strings.TrimSuffix(s, "|None"), at)
		if t.Pure() && t.Kind != "optional" && t.Kind != "None" {
			return model.Optional(t)
		}
	}
	if strings.HasPrefix(s, "tuple[") && strings.HasSuffix(s, ",...]") {
		t := p.typeText(m, s[6:len(s)-5], at)
		if t.Pure() {
			return model.Tuple(t)
		}
	}
	if sym := p.Symbols[s]; sym != nil && sym.Kind == "type" {
		return sym.Type
	}
	p.error("PP602", "unknown or unsupported manifest type "+s, at)
	return model.Invalid
}
func (p *Program) recordCycles() {
	state := map[string]int{}
	var visit func(string)
	visit = func(name string) {
		r := p.Records[name]
		if r == nil {
			return
		}
		if state[name] == 2 {
			return
		}
		if state[name] == 1 {
			p.error("PP204", "recursive value-record type "+name, r.Span)
			return
		}
		state[name] = 1
		for _, f := range r.Fields {
			t := f.Type
			for t.Elem != nil {
				t = *t.Elem
			}
			if t.Kind == "record" {
				visit(t.Name)
			}
		}
		state[name] = 2
	}
	names := make([]string, 0, len(p.Records))
	for name := range p.Records {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		visit(name)
	}
}
func (p *Program) constants(m *Module) {
	c := newChecker(p, m, nil)
	for _, n := range m.Tree.Items("body") {
		if n.Kind != "Assign" || m.Package {
			continue
		}
		target := n.Get("target")
		if target == nil || target.Kind != "Name" {
			continue
		}
		s := m.Bindings[target.A("name")]
		if s == nil || s.Node != n {
			continue
		}
		a := n.Get("annotation")
		var t model.Type
		if a != nil && a.Kind == "Index" && a.Get("value") != nil && a.Get("value").Kind == "Name" && m.Bindings[a.Get("value").A("name")] != nil && m.Bindings[a.Get("value").A("name")].Name == "typing.Final" {
			t = p.annotation(m, a.Get("index"), n.Span)
		} else {
			p.error("PP501", "module constants require Final[T] annotations", n.Span)
			t = model.Invalid
		}
		if !t.Pure() {
			p.error("PP201", "module constants must have Pure Value types", n.Span)
		}
		if n.Get("value") == nil {
			p.error("PP501", "module constants require initializers", n.Span)
			continue
		}
		c.constant = true
		actual := c.expr(n.Get("value"), t)
		c.expect(t, actual, n.Span)
		s.Type = t
	}
	p.Diagnostics = append(p.Diagnostics, c.result.Diagnostics...)
}

func (p *Program) String() string {
	return fmt.Sprintf("%d modules, %d functions", len(p.Modules), len(p.Functions))
}
