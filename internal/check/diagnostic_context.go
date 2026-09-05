package check

import (
	"sort"

	"github.com/dwrtz/purepy/internal/diag"
	"github.com/dwrtz/purepy/internal/model"
)

func (c *checker) localSymbol(name string) string {
	if c.f != nil {
		return c.f.Name + "." + name
	}
	return c.m.Name + "." + name
}

// nameContext only reads linked declarations and local metadata. Reporting a
// diagnostic must never re-evaluate an expression or add a call/authority edge.
func (c *checker) nameContext(d *diag.Diagnostic, name string) *diag.Diagnostic {
	if d == nil {
		return d
	}
	if v, ok := c.vars[name]; ok {
		d.WithSymbol(c.localSymbol(name)).WithRelated(v.Declaration)
		if _, exists := d.Types["actual"]; !exists {
			d.WithType("actual", v.Current)
		}
		d.WithType("declared", v.Type)
	} else if s := c.m.Bindings[name]; s != nil {
		d.WithSymbol(s.Name).WithRelated(c.m.ImportedAt[name], s.Declaration())
		if _, exists := d.Types["actual"]; !exists {
			d.WithType("actual", s.Type)
		}
	} else {
		d.WithSymbol(c.localSymbol(name))
	}
	return d
}

// expressionContext records dependencies in source order. IR child maps have
// no traversal order, and lexical ordering must not depend on worker scheduling.
func (c *checker) expressionContext(d *diag.Diagnostic, n *model.Node) *diag.Diagnostic {
	if d == nil || n == nil {
		return d
	}
	var references []*model.Node
	receiverTypes := map[model.Span]model.Type{}
	var visit func(*model.Node)
	visit = func(node *model.Node) {
		if node == nil {
			return
		}
		switch node.Kind {
		case "Name", "Attribute", "Call":
			references = append(references, node)
			if node.Kind == "Attribute" && node.Get("value") != nil {
				receiverTypes[node.Get("value").Span] = model.Invalid
			}
		}
		for _, child := range node.Fields {
			visit(child)
		}
		for _, children := range node.Lists {
			for _, child := range children {
				visit(child)
			}
		}
	}
	visit(n)
	// Facts already contain the receiver types checked at these exact locations,
	// including narrowing and nested field reads. Consult them without running
	// expressions again or extending the hot path for accepted programs.
	unresolved := len(receiverTypes)
	for i := len(c.result.Facts) - 1; i >= 0 && unresolved > 0; i-- {
		fact := c.result.Facts[i]
		if typ, ok := receiverTypes[fact.Span]; ok && typ.Kind == "invalid" && fact.Description == "expression" {
			receiverTypes[fact.Span] = fact.Type
			unresolved--
		}
	}
	sort.Slice(references, func(i, j int) bool {
		if references[i].Span.Start != references[j].Span.Start {
			return references[i].Span.Start < references[j].Span.Start
		}
		if references[i].Span.End != references[j].Span.End {
			return references[i].Span.End < references[j].Span.End
		}
		if references[i].Kind != references[j].Kind {
			return references[i].Kind < references[j].Kind
		}
		return references[i].A("name") < references[j].A("name")
	})
	for _, ref := range references {
		if ref.Kind == "Attribute" {
			if value := ref.Get("value"); value != nil {
				typ := receiverTypes[value.Span]
				if record := c.p.Records[typ.Name]; typ.Kind == "record" && record != nil {
					for _, field := range record.Fields {
						if field.Name == ref.A("name") {
							d.WithRelated(field.Span).WithNote(record.Name + "." + field.Name + " has type " + field.Type.String() + ".")
							break
						}
					}
				}
			}
			continue
		}
		if ref.Kind == "Call" {
			if target := ref.Get("target"); target != nil && target.Kind == "Name" {
				if s := c.m.Bindings[target.A("name")]; s != nil && s.Function != nil {
					d.WithRelated(s.Function.ReturnSpan).WithNote(s.Name + " returns " + s.Function.Returns.String() + ".")
				}
			}
			continue
		}
		name := ref.A("name")
		if v, ok := c.vars[name]; ok {
			d.WithRelated(v.Declaration)
			if v.Type.Kind != "invalid" && v.Type.Kind != "" {
				d.WithNote(c.localSymbol(name) + " is declared as " + v.Type.String() + ".")
			}
		} else if s := c.m.Bindings[name]; s != nil {
			d.WithRelated(c.m.ImportedAt[name], s.Declaration())
			d.WithNote("Referenced declaration: " + s.Name + ".")
		}
	}
	return d
}

func (c *checker) expectExpression(want, got model.Type, n *model.Node) *diag.Diagnostic {
	if n == nil {
		return nil
	}
	return c.expressionContext(c.expect(want, got, n.Span), n)
}

func (c *checker) pureExpression(t model.Type, n *model.Node) bool {
	if n == nil {
		return false
	}
	before := len(c.result.Diagnostics)
	ok := c.pure(t, n.Span)
	if len(c.result.Diagnostics) > before {
		d := &c.result.Diagnostics[before]
		if n.Kind == "Name" {
			c.nameContext(d, n.A("name"))
		} else {
			c.expressionContext(d, n)
		}
	}
	return ok
}
