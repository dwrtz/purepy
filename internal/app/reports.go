package app

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dwrtz/purepy/internal/diag"
	"github.com/dwrtz/purepy/internal/model"
)

type AuthorityReport struct {
	Schema    int         `json:"schema"`
	Functions []Authority `json:"functions"`
}
type Authority struct {
	FunctionReport
	Capabilities             []model.Parameter `json:"capabilities"`
	HostReferences           []model.Parameter `json:"host_references"`
	UnusedCapabilities       []string          `json:"unused_capabilities"`
	DirectCalls              []model.CallEdge  `json:"direct_calls"`
	TrustedExternal          []model.Function  `json:"trusted_external"`
	ReachableTrustedExternal []model.Function  `json:"reachable_trusted_external"`
	TrustedTypes             []TrustedType     `json:"trusted_types"`
	TrustedModules           []TrustedModule   `json:"trusted_modules"`
}

type TrustedType struct {
	Name     string   `json:"name"`
	Category string   `json:"category"`
	Labels   []string `json:"labels"`
	Source   string   `json:"source"`
}

type TrustedModule struct {
	Name       string `json:"name"`
	ImportSafe bool   `json:"import_safe"`
	Source     string `json:"source"`
}

func (r *Report) Capabilities(name string) (AuthorityReport, error) {
	out := AuthorityReport{Schema: r.Schema, Functions: []Authority{}}
	if r.Program == nil || !r.OK {
		return out, fmt.Errorf("capability reports require successful verification")
	}
	names := append([]string{}, r.Config.Entrypoints...)
	if name != "" {
		names = []string{name}
	}
	sort.Strings(names)
	for _, name := range names {
		f := r.Program.Functions[name]
		if f == nil || f.Origin != "project" {
			return out, fmt.Errorf("unknown verified function %s", name)
		}
		a := Authority{FunctionReport: FunctionReport{TypeParams: f.TypeParams, Parent: f.Parent, Name: f.Name, Kind: f.Kind, Classification: f.Classification(), Parameters: f.Parameters, Returns: f.Returns}, Capabilities: []model.Parameter{}, HostReferences: []model.Parameter{}, UnusedCapabilities: []string{}, DirectCalls: []model.CallEdge{}, TrustedExternal: []model.Function{}, ReachableTrustedExternal: []model.Function{}, TrustedTypes: []TrustedType{}, TrustedModules: []TrustedModule{}}
		for _, p := range f.Parameters {
			if p.Type.Kind == "capability" {
				a.Capabilities = append(a.Capabilities, p)
			}
			if p.Type.Kind == "host_ref" {
				a.HostReferences = append(a.HostReferences, p)
			}
		}
		used := map[string]bool{}
		direct := map[string]bool{}
		for _, edge := range r.Calls {
			if edge.Caller == name {
				a.DirectCalls = append(a.DirectCalls, edge)
				for _, arg := range edge.CapabilityArguments {
					used[arg] = true
				}
				if target := r.Program.Functions[edge.Callee]; target != nil && target.Origin == "manifest" {
					direct[target.Name] = true
				}
			}
		}
		for _, p := range a.Capabilities {
			if !used[p.Name] {
				a.UnusedCapabilities = append(a.UnusedCapabilities, p.Name)
			}
		}
		for _, fn := range sortedKeys(direct) {
			a.TrustedExternal = append(a.TrustedExternal, *r.Program.Functions[fn])
		}
		visited := map[string]bool{}
		trusted := map[string]bool{}
		var walk func(string)
		walk = func(caller string) {
			if visited[caller] {
				return
			}
			visited[caller] = true
			for _, edge := range r.Calls {
				if edge.Caller != caller {
					continue
				}
				fn := r.Program.Functions[edge.Callee]
				if fn == nil {
					continue
				}
				if fn.Origin == "manifest" {
					trusted[fn.Name] = true
				} else {
					walk(fn.Name)
				}
			}
		}
		walk(name)
		for _, fn := range sortedKeys(trusted) {
			a.ReachableTrustedExternal = append(a.ReachableTrustedExternal, *r.Program.Functions[fn])
		}
		// Include nominal contracts from reachable signatures, record layouts,
		// and retained expression/annotation facts, with their manifest provenance.
		types := map[string]bool{}
		var collectType func(model.Type)
		collectType = func(t model.Type) {
			for _, a := range t.Args {
				collectType(a)
			}
			for _, a := range t.Params {
				collectType(a)
			}
			if t.Returns != nil {
				collectType(*t.Returns)
			}
			if t.Elem != nil {
				collectType(*t.Elem)
				return
			}
			if t.Name == "" || types[t.Name] {
				return
			}
			types[t.Name] = true
			if rec := r.Program.Records[t.Name]; rec != nil {
				for _, field := range rec.Fields {
					collectType(field.Type)
				}
			}
		}
		for fn := range trusted {
			visited[fn] = true
		}
		for fn := range visited {
			decl := r.Program.Functions[fn]
			if decl == nil {
				continue
			}
			for _, param := range decl.Parameters {
				collectType(param.Type)
			}
			collectType(decl.Returns)
			for _, fact := range r.Facts {
				if fact.Span.File == decl.Span.File && fact.Span.Start >= decl.Span.Start && fact.Span.End <= decl.Span.End {
					collectType(fact.Type)
				}
			}
		}
		for _, name := range sortedKeys(types) {
			decl := r.Program.Symbols[name]
			if decl != nil && decl.Kind == "type" && decl.Source != "" {
				a.TrustedTypes = append(a.TrustedTypes, TrustedType{Name: name, Category: decl.Type.Category(), Labels: append([]string{}, decl.Labels...), Source: decl.Source})
			}
		}
		// Import safety is a separate trusted declaration: its manifest may
		// differ from the files declaring functions and types. Module imports
		// execute even when the imported callable is never invoked.
		modules := map[string]bool{}
		var collectModule func(string)
		collectModule = func(name string) {
			if name == "" || modules[name] {
				return
			}
			modules[name] = true
			if at := strings.LastIndexByte(name, '.'); at >= 0 {
				collectModule(name[:at])
			}
			if module := r.Program.Modules[name]; module != nil {
				for _, dependency := range module.Imports {
					collectModule(dependency)
				}
				for _, item := range module.Tree.Items("body") {
					if item.Kind == "Import" {
						collectModule(item.A("module"))
					}
				}
			}
		}
		for fn := range visited {
			if decl := r.Program.Functions[fn]; decl != nil {
				if at := strings.LastIndexByte(decl.Name, '.'); at >= 0 {
					collectModule(decl.Name[:at])
				}
			}
		}
		for _, typ := range a.TrustedTypes {
			if at := strings.LastIndexByte(typ.Name, '.'); at >= 0 {
				collectModule(typ.Name[:at])
			}
		}
		for _, name := range sortedKeys(modules) {
			if decl, ok := r.Program.ExternalModuleDeclarations[name]; ok {
				a.TrustedModules = append(a.TrustedModules, TrustedModule{Name: decl.Name, ImportSafe: decl.ImportSafe, Source: decl.Source})
			}
		}
		out.Functions = append(out.Functions, a)
	}
	return out, nil
}
func sortedKeys(m map[string]bool) []string {
	keys := []string{}
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

type Explanation struct {
	Schema      int               `json:"schema"`
	Diagnostics []diag.Diagnostic `json:"diagnostics"`
	Facts       []model.Fact      `json:"facts"`
}

func (r *Report) Explain(file string, line, column int) Explanation {
	out := Explanation{Schema: r.Schema, Diagnostics: []diag.Diagnostic{}, Facts: []model.Fact{}}
	abs, _ := filepath.Abs(file)
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	match := func(s model.Span) bool {
		if s.File != abs {
			return false
		}
		if line < s.Line || line > s.EndLine {
			return false
		}
		if column > 0 && (line == s.Line && column < s.Column || line == s.EndLine && column >= s.EndColumn) {
			return false
		}
		return true
	}
	for _, d := range r.Diagnostics {
		if match(d.Span) {
			out.Diagnostics = append(out.Diagnostics, d)
		}
	}
	for _, f := range r.Facts {
		if match(f.Span) {
			out.Facts = append(out.Facts, f)
		}
	}
	sort.SliceStable(out.Facts, func(i, j int) bool {
		return out.Facts[i].Span.End-out.Facts[i].Span.Start < out.Facts[j].Span.End-out.Facts[j].Span.Start
	})
	return out
}
