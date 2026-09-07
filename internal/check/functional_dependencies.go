package check

import (
	"github.com/dwrtz/purepy/internal/diag"
	"github.com/dwrtz/purepy/internal/model"
	"sort"
	"strings"
)

type callableInvocation struct {
	Targets   []string
	Arguments map[int][]string
}

type callableBinding struct {
	Slot    string
	Sources []string
}

// A finite, context-insensitive provenance graph tracks callable parameters,
// captures and returned closures. Every origin is a linked declaration; an
// annotation alone can never introduce a callback from the host.
func (p *Program) resolveCallbacks(r *Result) {
	values := map[string]map[string]bool{}
	budget := 1000000
	var origins func(string, int) map[string]bool
	origins = func(source string, depth int) map[string]bool {
		budget--
		if depth > 128 {
			budget = -1
		}
		if budget < 0 {
			return nil
		}
		if strings.HasPrefix(source, "$result:") {
			result := map[string]bool{}
			for fn := range origins(strings.TrimPrefix(source, "$result:"), depth+1) {
				for returned := range values["$return:"+fn] {
					budget--
					if budget < 0 {
						return nil
					}
					result[returned] = true
				}
			}
			return result
		}
		if strings.HasPrefix(source, "$") {
			return values[source]
		}
		if p.Functions[source] != nil {
			return map[string]bool{source: true}
		}
		return nil
	}
	changed := true
	for changed && budget >= 0 {
		changed = false
		for _, b := range r.Bindings {
			if values[b.Slot] == nil {
				values[b.Slot] = map[string]bool{}
			}
			for _, source := range b.Sources {
				for origin := range origins(source, 0) {
					budget--
					if budget < 0 {
						break
					}
					if !values[b.Slot][origin] {
						values[b.Slot][origin] = true
						changed = true
					}
				}
			}
		}
		for _, inv := range r.Invocations {
			for _, target := range inv.Targets {
				for fn := range origins(target, 0) {
					budget--
					if budget < 0 {
						break
					}
					f := p.Functions[fn]
					if f == nil {
						continue
					}
					for i, param := range f.Parameters {
						budget--
						if budget < 0 {
							break
						}
						slot := "$param:" + fn + "." + param.Name
						if values[slot] == nil {
							values[slot] = map[string]bool{}
						}
						for _, source := range inv.Arguments[i] {
							for origin := range origins(source, 0) {
								budget--
								if budget < 0 {
									break
								}
								if !values[slot][origin] {
									values[slot][origin] = true
									changed = true
								}
							}
						}
					}
				}
			}
		}
	}
	if budget < 0 {
		r.Diagnostics = append(r.Diagnostics, diag.New("PP203", "callback provenance analysis budget exceeded", model.Span{}))
		return
	}
	edges := []model.CallEdge{}
	for _, e := range r.Calls {
		if !strings.HasPrefix(e.Callee, "$") {
			edges = append(edges, e)
			continue
		}
		names := []string{}
		for name := range origins(e.Callee, 0) {
			names = append(names, name)
		}
		sort.Strings(names)
		// Keep a symbolic edge for uninstantiated internal abstractions. Configured
		// entrypoints cannot receive functions, so no host-origin call is hidden.
		if len(names) == 0 {
			edges = append(edges, e)
		} else {
			for _, name := range names {
				budget--
				if budget < 0 {
					r.Diagnostics = append(r.Diagnostics, diag.New("PP203", "callback provenance analysis budget exceeded", e.Span))
					return
				}
				copy := e
				copy.Callee = name
				edges = append(edges, copy)
			}
		}
	}
	if budget < 0 {
		r.Diagnostics = append(r.Diagnostics, diag.New("PP203", "callback provenance analysis budget exceeded", model.Span{}))
	}
	r.Calls = edges
}

func mergeOrigins(a, b []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, xs := range [][]string{a, b} {
		for _, name := range xs {
			if !seen[name] {
				seen[name] = true
				out = append(out, name)
			}
		}
	}
	return out
}
