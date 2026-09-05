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
	Span     model.Span
}

// Declaration returns the source location even for manifest and synthetic
// symbols, which do not necessarily have a syntax node.
func (s *Symbol) Declaration() model.Span {
	if s == nil {
		return model.Span{}
	}
	if s.Span.File != "" {
		return s.Span
	}
	if s.Node != nil {
		return s.Node.Span
	}
	if s.Function != nil {
		return s.Function.Span
	}
	if s.Record != nil {
		return s.Record.Span
	}
	return manifestSpan(model.Span{}, s.Source)
}

func manifestSpan(at model.Span, source string) model.Span {
	if at.File != "" || source == "" {
		return at
	}
	return model.Span{File: source, Line: 1, Column: 1, EndLine: 1, EndColumn: 1}
}

type Program struct {
	Modules                    map[string]*Module
	Order                      []string
	Symbols                    map[string]*Symbol
	Functions                  map[string]*model.Function
	Records                    map[string]*model.Record
	Diagnostics                []diag.Diagnostic
	ExternalModules            map[string]bool
	ExternalModuleDeclarations map[string]manifest.Module
}

func Link(modules []*Module, external *manifest.Set, entrypoints []string) *Program {
	p := &Program{Modules: map[string]*Module{}, Symbols: map[string]*Symbol{}, Functions: map[string]*model.Function{}, Records: map[string]*model.Record{}, ExternalModules: map[string]bool{}, ExternalModuleDeclarations: map[string]manifest.Module{}, Diagnostics: []diag.Diagnostic{}}
	for _, m := range modules {
		m.Bindings = map[string]*Symbol{}
		m.ImportedAt = map[string]model.Span{}
		p.Modules[m.Name] = m
		p.Order = append(p.Order, m.Name)
		if m.Name == "purepy" || strings.HasPrefix(m.Name, "purepy.") || m.Name == "typing" || strings.HasPrefix(m.Name, "typing.") {
			p.error("PP106", "verified modules cannot shadow sealed support packages: "+m.Name, m.Tree.Span).WithSymbol(m.Name)
		}
	}
	sort.Strings(p.Order)
	for _, m := range external.Modules {
		if m.Name == "purepy" || strings.HasPrefix(m.Name, "purepy.") || m.Name == "typing" || strings.HasPrefix(m.Name, "typing.") {
			p.error("PP601", "manifests cannot replace sealed support packages: "+m.Name, manifestSpan(m.Span, m.Source)).WithSymbol(m.Name)
		}
		p.ExternalModules[m.Name] = m.ImportSafe
		p.ExternalModuleDeclarations[m.Name] = m
		if existing := p.Modules[m.Name]; existing != nil {
			p.error("PP601", "manifest module conflicts with verified module "+m.Name, manifestSpan(m.Span, m.Source)).WithSymbol(m.Name).WithRelated(existing.Tree.Span)
		}
	}
	for _, t := range external.Types {
		s := &Symbol{Name: t.Name, Kind: "type", Type: model.Type{Kind: t.Category, Name: t.Name}, Labels: t.Labels, Source: t.Source, Span: manifestSpan(t.Span, t.Source)}
		p.add(s, s.Span)
	}
	for _, f := range external.Functions {
		fn := &model.Function{Name: f.Name, Kind: f.Kind, Origin: "manifest", Trust: f.Trust, Source: f.Source, Span: manifestSpan(f.Span, f.Source), ReturnSpan: manifestSpan(f.ReturnSpan, f.Source), Parameters: []model.Parameter{}}
		p.add(&Symbol{Name: f.Name, Kind: "function", Function: fn, Source: f.Source, Span: fn.Span}, fn.Span)
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
			start := len(p.Diagnostics)
			t := p.typeText(nil, a.Type, manifestSpan(a.TypeSpan, f.Source))
			p.ownerContext(start, f.Name+"."+a.Name)
			fn.Parameters = append(fn.Parameters, model.Parameter{Name: a.Name, Type: t, Span: manifestSpan(a.Span, f.Source), Labels: p.labels(t)})
		}
		start := len(p.Diagnostics)
		fn.Returns = p.typeText(nil, f.Returns, fn.ReturnSpan)
		p.ownerContext(start, fn.Name)
		if !fn.Returns.Pure() {
			p.error("PP602", "external function must return a Pure Value: "+fn.Name, fn.ReturnSpan).WithSymbol(fn.Name).WithType("return", fn.Returns).WithRelated(p.Symbols[fn.Returns.Name].Declaration())
		}
		caps := 0
		for _, a := range fn.Parameters {
			if a.Type.Kind == "capability" {
				caps++
			}
			if fn.Trust == "pure" && !a.Type.Pure() {
				p.error("PP602", "trusted pure function accepts only Pure Values: "+fn.Name, a.Span).WithSymbol(fn.Name+"."+a.Name).WithType("parameter", a.Type).WithRelated(p.Symbols[a.Type.Name].Declaration())
			}
		}
		if fn.Trust == "host" && caps == 0 {
			d := p.error("PP603", "host function requires at least one explicit capability parameter: "+fn.Name, fn.Span).WithSymbol(fn.Name).WithType("return", fn.Returns)
			for _, param := range fn.Parameters {
				d.WithType("parameter."+param.Name, param.Type).WithRelated(param.Span)
			}
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
			p.error("PP701", "duplicate entrypoint "+name, model.Span{}).WithSymbol(name).WithRelated(p.Symbols[name].Declaration())
			continue
		}
		seen[name] = true
		f := p.Functions[name]
		if f == nil || f.Origin != "project" {
			d := p.error("PP701", "entrypoint must name a verified top-level function: "+name, model.Span{}).WithSymbol(name).WithRelated(p.Symbols[name].Declaration())
			if s := p.Symbols[name]; s != nil {
				d.WithType("actual", s.Type).WithNote(name + " is a " + s.Kind + " declaration.")
				if s.Function != nil {
					d.WithType("return", s.Function.Returns).WithNote("The function is declared by a manifest, outside the verified source boundary.")
				}
			}
		}
	}
	return p
}

func (p *Program) error(code, message string, span model.Span) *diag.Diagnostic {
	p.Diagnostics = append(p.Diagnostics, diag.New(code, message, span))
	return &p.Diagnostics[len(p.Diagnostics)-1]
}
func forbiddenName(s string) bool { return strings.HasPrefix(s, "__") && strings.HasSuffix(s, "__") }
func (p *Program) add(s *Symbol, span model.Span) bool {
	if existing := p.Symbols[s.Name]; existing != nil {
		p.error("PP103", "duplicate declaration "+s.Name, span).WithSymbol(s.Name).WithType("declared", s.Type).WithType("previous", existing.Type).WithRelated(existing.Declaration())
		return false
	}
	if s.Span.File == "" {
		s.Span = span
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
			p.error("PP101", "package initializers may contain only a docstring", n.Span).WithSymbol(m.Name)
			continue
		}
		s := &Symbol{Node: n, Module: m.Name, Span: n.Span}
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
				p.error("PP501", "module assignment requires a single Final constant name", n.Span).WithSymbol(m.Name)
				continue
			}
		default:
			p.error("PP003", "unsupported module statement: "+n.Kind, n.Span).WithSymbol(m.Name)
			continue
		}
		if forbiddenName(name) {
			p.error("PP104", "dunder names are not accessible in verified code", n.Span).WithSymbol(m.Name + "." + name)
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
				p.error("PP102", "use absolute from imports without aliases or stars", item.Span).WithSymbol(from + "." + name)
				continue
			}
			if existing := m.Bindings[name]; existing != nil {
				p.error("PP103", "import conflicts with existing name "+name, item.Span).WithSymbol(m.Name+"."+name).WithType("previous", existing.Type).WithRelated(m.ImportedAt[name], existing.Declaration())
				continue
			}
			var s *Symbol
			if from == "purepy" && name == "value" || from == "typing" && name == "Final" {
				s = &Symbol{Name: from + "." + name, Kind: "support", Span: item.Span}
			} else {
				s = p.Symbols[from+"."+name]
			}
			if s == nil {
				p.error("PP102", "unresolved direct import "+from+"."+name, item.Span).WithSymbol(from + "." + name)
				continue
			}
			if s.Source != "" && !p.ExternalModules[from] {
				declaration := p.ExternalModuleDeclarations[from]
				p.error("PP604", "external module is not declared import_safe: "+from, item.Span).WithSymbol(from+"."+name).WithRelated(manifestSpan(declaration.Span, declaration.Source), s.Declaration())
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
			first := 0
			for path[first] != name {
				first++
			}
			cycle := append(append([]string{}, path[first:]...), name)
			locations := make([]model.Span, 0, len(cycle)-1)
			for i := 1; i < len(cycle); i++ {
				locations = append(locations, p.importLocation(cycle[i-1], cycle[i]))
			}
			p.error("PP105", "import cycle: "+strings.Join(cycle, " -> "), locations[len(locations)-1]).WithSymbol(name).WithRelated(locations[:len(locations)-1]...).WithNote("import path: " + strings.Join(cycle, " -> "))
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

func (p *Program) importLocation(from, to string) model.Span {
	m := p.Modules[from]
	for _, n := range m.Tree.Items("body") {
		if n.Kind == "Import" && n.A("module") == to {
			return n.Span
		}
	}
	return m.Tree.Span
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
				d := p.error("PP003", "functions require fixed annotated signatures and no decorators", n.Span).WithSymbol(f.Name)
				for i, decorator := range n.Items("decorators") {
					p.annotationContext(d, m, decorator, fmt.Sprintf("decorator.%d", i+1))
				}
			}
			seen := map[string]model.Parameter{}
			for _, a := range n.Items("params") {
				name := a.A("name")
				t := p.annotationFor(m, a.Get("annotation"), a.Span, f.Name+"."+name)
				previous, duplicate := seen[name]
				if duplicate || forbiddenName(name) {
					p.error("PP103", "invalid or duplicate parameter "+name, a.Span).WithSymbol(f.Name+"."+name).WithType("declared", t).WithType("previous", previous.Type).WithRelated(previous.Span)
				}
				if !duplicate {
					seen[name] = model.Parameter{Name: name, Type: t, Span: a.Span}
				}
				if a.Kind != "Param" || a.A("unsupported") != "" {
					p.error("PP003", "parameter defaults, variadics and signature markers are prohibited", a.Span).WithSymbol(f.Name+"."+name).WithType("parameter", t)
				}
				f.Parameters = append(f.Parameters, model.Parameter{Name: name, Type: t, Span: a.Span, Labels: p.labels(t)})
			}
			f.ReturnSpan = n.Span
			if returns := n.Get("returns"); returns != nil {
				f.ReturnSpan = returns.Span
			}
			f.Returns = p.annotationFor(m, n.Get("returns"), n.Span, f.Name)
			if !f.Returns.Pure() {
				p.error("PP201", "function return type must be a Pure Value", f.ReturnSpan).WithSymbol(f.Name).WithType("return", f.Returns).WithRelated(p.Symbols[f.Returns.Name].Declaration())
			}
		} else if n.Kind == "Record" {
			decs := n.Items("decorators")
			valid := len(decs) == 1 && decs[0].Kind == "Name" && m.Bindings[decs[0].A("name")] != nil && m.Bindings[decs[0].A("name")].Name == "purepy.value"
			if valid && m.ImportedAt[decs[0].A("name")].Start > n.Span.Start {
				valid = false
			}
			if !valid || len(n.Items("bases")) > 0 || n.A("unsupported") != "" {
				d := p.error("PP202", "records require the exact @value decorator, no bases or class options", n.Span).WithSymbol(s.Name).WithType("record", s.Type)
				for i, decorator := range decs {
					p.annotationContext(d, m, decorator, fmt.Sprintf("decorator.%d", i+1))
				}
				for i, base := range n.Items("bases") {
					p.annotationContext(d, m, base, fmt.Sprintf("base.%d", i+1))
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
					d := p.error("PP202", "record bodies allow only annotated fields without defaults", a.Span).WithSymbol(owner).WithType("record", s.Type)
					p.annotationContext(d, m, a.Get("annotation"), "annotation")
					continue
				}
				name := target.A("name")
				t := p.annotationFor(m, a.Get("annotation"), a.Span, s.Name+"."+name)
				previous, duplicate := seen[name]
				if duplicate || forbiddenName(name) {
					p.error("PP202", "invalid or duplicate record field "+name, a.Span).WithSymbol(s.Name+"."+name).WithType("declared", t).WithType("previous", previous.Type).WithRelated(previous.Span)
				}
				if !duplicate {
					seen[name] = model.Parameter{Name: name, Type: t, Span: a.Span}
				}
				if !t.Pure() {
					p.error("PP201", "record fields must contain only Pure Values", a.Span).WithSymbol(s.Name+"."+name).WithType("field", t).WithRelated(p.Symbols[t.Name].Declaration())
				}
				s.Record.Fields = append(s.Record.Fields, model.Parameter{Name: name, Type: t, Span: a.Span})
			}
		}
	}
}

func (p *Program) ownerContext(start int, name string) {
	for i := start; i < len(p.Diagnostics); i++ {
		if p.Diagnostics[i].Symbol == "" {
			p.Diagnostics[i].WithSymbol(name)
		}
	}
}

func (p *Program) annotationFor(m *Module, n *model.Node, at model.Span, owner string) model.Type {
	start := len(p.Diagnostics)
	t := p.annotation(m, n, at)
	p.ownerContext(start, owner)
	return t
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
			if s := m.Bindings[name]; s != nil {
				if s.Kind == "type" {
					return s.Type
				}
				p.error("PP203", "unsupported type expression: "+n.Text, n.Span).WithSymbol(s.Name).WithType("actual", s.Type).WithRelated(m.ImportedAt[name], s.Declaration()).WithNote("the name resolves to a " + s.Kind + ", which cannot be used as a type")
				return model.Invalid
			}
		}
		p.error("PP203", "unsupported type expression: "+n.Text, n.Span).WithSymbol(name)
		return model.Invalid
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
			p.error("PP203", "unsupported type expression: "+n.Text, n.Span).WithType("element", t).WithRelated(p.Symbols[t.Name].Declaration())
			return model.Invalid
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
				p.error("PP203", "unsupported type expression: "+n.Text, n.Span).WithType("element", t).WithRelated(p.Symbols[t.Name].Declaration())
				return model.Invalid
			}
		}
	}
	p.annotationContext(p.error("PP203", "unsupported type expression: "+n.Text, n.Span), m, n, "annotation")
	return model.Invalid
}

// Rejected annotation shapes can still contain known types and names. Collect
// their context without resolving the expression again or adding diagnostics.
func (p *Program) annotationContext(d *diag.Diagnostic, m *Module, n *model.Node, role string) {
	if n == nil {
		return
	}
	if n.Kind == "Name" {
		name := n.A("name")
		if m != nil && m.Bindings[name] != nil {
			s := m.Bindings[name]
			if d.Symbol == "" {
				d.WithSymbol(s.Name)
			}
			d.WithType(role, s.Type).WithRelated(m.ImportedAt[name], s.Declaration())
		} else if t, ok := model.Primitive(name); ok {
			d.WithType(role, t)
		} else if d.Symbol == "" && name != "tuple" {
			d.WithSymbol(name)
		}
	}
	if n.Kind == "Literal" && n.A("type") == "None" {
		d.WithType(role, model.None)
	}
	keys := make([]string, 0, len(n.Fields))
	for key := range n.Fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		p.annotationContext(d, m, n.Fields[key], role+"."+key)
	}
	keys = keys[:0]
	for key := range n.Lists {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		for i, child := range n.Lists[key] {
			p.annotationContext(d, m, child, fmt.Sprintf("%s.%s.%d", role, key, i))
		}
	}
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
		p.error("PP602", "unknown or unsupported manifest type "+s, at).WithType("element", t).WithRelated(p.Symbols[t.Name].Declaration())
		return model.Invalid
	}
	if strings.HasPrefix(s, "tuple[") && strings.HasSuffix(s, ",...]") {
		t := p.typeText(m, s[6:len(s)-5], at)
		if t.Pure() {
			return model.Tuple(t)
		}
		p.error("PP602", "unknown or unsupported manifest type "+s, at).WithType("element", t).WithRelated(p.Symbols[t.Name].Declaration())
		return model.Invalid
	}
	if sym := p.Symbols[s]; sym != nil {
		if sym.Kind == "type" {
			return sym.Type
		}
		p.error("PP602", "unknown or unsupported manifest type "+s, at).WithSymbol(s).WithType("actual", sym.Type).WithRelated(sym.Declaration()).WithNote("the name resolves to a " + sym.Kind + ", which cannot be used as a type")
		return model.Invalid
	}
	p.error("PP602", "unknown or unsupported manifest type "+s, at).WithSymbol(s)
	return model.Invalid
}
func (p *Program) recordCycles() {
	type fieldEdge struct {
		symbol string
		field  model.Parameter
	}
	state := map[string]int{}
	var visit func(string, []string, []fieldEdge)
	visit = func(name string, path []string, edges []fieldEdge) {
		r := p.Records[name]
		if r == nil {
			return
		}
		if state[name] == 2 {
			return
		}
		if state[name] == 1 {
			first := 0
			for path[first] != name {
				first++
			}
			cycle := edges[first:]
			last := cycle[len(cycle)-1]
			d := p.error("PP204", "recursive value-record type "+name, last.field.Span).WithSymbol(last.symbol).WithType("field", last.field.Type)
			for _, edge := range cycle {
				d.WithNote(edge.symbol + " has type " + edge.field.Type.String())
			}
			for _, edge := range cycle[:len(cycle)-1] {
				d.WithRelated(edge.field.Span)
			}
			d.WithRelated(r.Span)
			return
		}
		state[name] = 1
		for _, f := range r.Fields {
			t := f.Type
			for t.Elem != nil {
				t = *t.Elem
			}
			if t.Kind == "record" {
				visit(t.Name, append(path, name), append(edges, fieldEdge{symbol: name + "." + f.Name, field: f}))
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
		visit(name, nil, nil)
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
		c.contextSymbol = s.Name
		a := n.Get("annotation")
		var t model.Type
		if a != nil && a.Kind == "Index" && a.Get("value") != nil && a.Get("value").Kind == "Name" && m.Bindings[a.Get("value").A("name")] != nil && m.Bindings[a.Get("value").A("name")].Name == "typing.Final" {
			t = p.annotationFor(m, a.Get("index"), n.Span, s.Name)
		} else {
			p.annotationContext(p.error("PP501", "module constants require Final[T] annotations", n.Span).WithSymbol(s.Name), m, a, "annotation")
			t = model.Invalid
		}
		if !t.Pure() {
			p.error("PP201", "module constants must have Pure Value types", n.Span).WithSymbol(s.Name).WithType("declared", t).WithRelated(p.Symbols[t.Name].Declaration())
		}
		if n.Get("value") == nil {
			p.error("PP501", "module constants require initializers", n.Span).WithSymbol(s.Name).WithType("declared", t)
			continue
		}
		c.constant = true
		actual := c.expr(n.Get("value"), t)
		declaration := n.Span
		if a != nil {
			declaration = a.Span
		}
		c.expect(t, actual, n.Get("value").Span).WithSymbol(s.Name).WithRelated(declaration)
		s.Type = t
	}
	p.Diagnostics = append(p.Diagnostics, c.result.Diagnostics...)
}

func (p *Program) String() string {
	return fmt.Sprintf("%d modules, %d functions", len(p.Modules), len(p.Functions))
}
