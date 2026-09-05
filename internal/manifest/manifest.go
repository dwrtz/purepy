// Package manifest reads declarative external signatures, never executable code.
package manifest

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"github.com/dwrtz/purepy/internal/discovery"
	"github.com/dwrtz/purepy/internal/model"
	"github.com/pelletier/go-toml/v2"
)

const SchemaVersion = 1

type Set struct {
	Modules   []Module
	Types     []Type
	Functions []Function
}

type Module struct {
	Span       model.Span
	Name       string
	ImportSafe bool
	Source     string
}

type Type struct {
	Span      model.Span
	Name      string
	Category  string
	Labels    []string
	Source    string
	Immutable bool
}

type Function struct {
	Span       model.Span
	ReturnSpan model.Span
	Name       string
	Kind       string
	Trust      string
	Returns    string
	Parameters []Parameter
	Source     string
}

type Parameter struct {
	Name     string
	Type     string
	Span     model.Span
	TypeSpan model.Span
}

type rawParameter struct {
	Name string `toml:"name"`
	Type string `toml:"type"`
}

type document struct {
	Schema    int           `toml:"schema"`
	Modules   []rawModule   `toml:"module"`
	Types     []rawType     `toml:"type"`
	Functions []rawFunction `toml:"function"`
}

type rawModule struct {
	Name       string `toml:"name"`
	ImportSafe *bool  `toml:"import_safe"`
}

type rawType struct {
	Name      string    `toml:"name"`
	Category  string    `toml:"category"`
	Labels    *[]string `toml:"labels"`
	Immutable *bool     `toml:"immutable"`
}

type rawFunction struct {
	Name       string          `toml:"name"`
	Kind       string          `toml:"kind"`
	Trust      string          `toml:"trust"`
	Returns    string          `toml:"returns"`
	Parameters *[]rawParameter `toml:"parameters"`
}

// Load preserves configuration order and declaration order. Duplicate names
// are errors even when their declarations are identical. Type resolution,
// deep category checking and host capability authorization happen after the
// project and manifest declarations have been linked into one program image.
func Load(paths []string) (result *Set, loadErr error) {
	currentPath := ""
	defer func() {
		var located *Error
		if loadErr != nil && !errors.As(loadErr, &located) {
			loadErr = &Error{Err: loadErr, Span: sourceSpan(currentPath, nil, 0, 0)}
		}
	}()
	set := &Set{Modules: []Module{}, Types: []Type{}, Functions: []Function{}}
	seen := make(map[string]model.Span)
	for _, path := range paths {
		currentPath = path
		path, err := filepath.Abs(path)
		if err != nil {
			return nil, fmt.Errorf("manifest path: %w", err)
		}
		currentPath = path
		info, err := os.Lstat(path)
		if err != nil {
			return nil, fmt.Errorf("manifest %s: %w", path, err)
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("manifest %s: expected a regular file, not a symlink", path)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("manifest %s: %w", path, err)
		}
		var doc document
		if err := toml.NewDecoder(bytes.NewReader(data)).DisallowUnknownFields().Decode(&doc); err != nil {
			return nil, &Error{Err: fmt.Errorf("manifest %s: %w", path, tomlFailure(err)), Span: decodingSpan(path, data, err)}
		}
		sources := declarationSources(path, data)
		if doc.Schema != SchemaVersion {
			return nil, &Error{Err: fmt.Errorf("manifest %s: unsupported schema %d; expected %d", path, doc.Schema, SchemaVersion), Span: sources.field("schema").keySpan}
		}
		// Check all declaration kinds together in source order so a conflict
		// always identifies the first definition and its first redefinition.
		type declaration struct {
			name string
			span model.Span
		}
		declarations := make([]declaration, 0, len(doc.Modules)+len(doc.Types)+len(doc.Functions))
		for index, m := range doc.Modules {
			declarations = append(declarations, declaration{m.Name, sources.field("module").item(index).field("name").span})
		}
		for index, t := range doc.Types {
			declarations = append(declarations, declaration{t.Name, sources.field("type").item(index).field("name").span})
		}
		for index, f := range doc.Functions {
			declarations = append(declarations, declaration{f.Name, sources.field("function").item(index).field("name").span})
		}
		sort.SliceStable(declarations, func(i, j int) bool { return declarations[i].span.Start < declarations[j].span.Start })
		for _, declaration := range declarations {
			// Missing names get their category-specific diagnostic below.
			if declaration.name == "" {
				continue
			}
			if err := register(seen, declaration.name, declaration.span); err != nil {
				return nil, err
			}
		}
		for index, m := range doc.Modules {
			span := sources.field("module").item(index).field("name").span
			if !discovery.ValidModuleName(m.Name) {
				return nil, invalid(span, m.Name, "invalid or noncanonical module name")
			}
			if m.ImportSafe == nil {
				return nil, invalid(span, m.Name, "import_safe is required")
			}
			set.Modules = append(set.Modules, Module{Name: m.Name, ImportSafe: *m.ImportSafe, Source: path, Span: span})
		}
		for index, t := range doc.Types {
			span := sources.field("type").item(index).field("name").span
			if !qualified(t.Name) {
				return nil, invalid(span, t.Name, "type name must be a canonical fully qualified name")
			}
			labels := []string{}
			immutable := false
			switch t.Category {
			case "value":
				if t.Immutable == nil || !*t.Immutable {
					return nil, invalid(span, t.Name, "external value requires immutable = true (the deep immutability contract)")
				}
				immutable = true
			case "capability":
				if t.Labels == nil || len(*t.Labels) == 0 {
					return nil, invalid(span, t.Name, "capability requires at least one exact reporting label")
				}
				labelSeen := make(map[string]bool)
				for _, label := range *t.Labels {
					if label == "" || strings.ContainsAny(label, "*?") || strings.IndexFunc(label, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
						return nil, invalid(span, t.Name, "capability labels must be nonempty exact strings without whitespace, controls or wildcards")
					}
					if labelSeen[label] {
						return nil, invalid(span, t.Name, fmt.Sprintf("duplicate capability label %q", label))
					}
					labelSeen[label] = true
					labels = append(labels, label)
				}
			case "host_ref":
			default:
				return nil, invalid(span, t.Name, fmt.Sprintf("unknown type category %q", t.Category))
			}
			if t.Category != "capability" && t.Labels != nil {
				return nil, invalid(span, t.Name, "labels are permitted only on capability types")
			}
			if t.Category != "value" && t.Immutable != nil {
				return nil, invalid(span, t.Name, "immutable is permitted only on value types")
			}
			set.Types = append(set.Types, Type{Name: t.Name, Category: t.Category, Labels: labels, Immutable: immutable, Source: path, Span: span})
		}
		for index, f := range doc.Functions {
			source := sources.field("function").item(index)
			span := source.field("name").span
			if !qualified(f.Name) {
				return nil, invalid(span, f.Name, "function name must be a canonical fully qualified name")
			}
			if f.Kind != "sync" && f.Kind != "async" {
				return nil, invalid(span, f.Name, "kind must be sync or async")
			}
			if f.Trust != "pure" && f.Trust != "host" {
				return nil, invalid(span, f.Name, "trust must be pure or host")
			}
			if f.Parameters == nil {
				return nil, invalid(span, f.Name, "parameters is required (use [] for no parameters)")
			}
			if !validTypeSyntax(f.Returns) {
				return nil, invalid(source.field("returns").span, f.Name, fmt.Sprintf("invalid return type %q", f.Returns), span)
			}
			parameterSeen := make(map[string]model.Span)
			parameters := make([]Parameter, 0, len(*f.Parameters))
			for index, p := range *f.Parameters {
				parameterSource := source.field("parameters").item(index)
				parameterSpan := parameterSource.field("name").span
				typeSpan := parameterSource.field("type").span
				symbol := f.Name + "." + p.Name
				if !discovery.ValidIdentifier(p.Name) {
					return nil, invalid(parameterSpan, symbol, fmt.Sprintf("invalid parameter name %q", p.Name), span)
				}
				if previous, exists := parameterSeen[p.Name]; exists {
					return nil, invalid(parameterSpan, symbol, fmt.Sprintf("duplicate parameter %q", p.Name), previous)
				}
				parameterSeen[p.Name] = parameterSpan
				if !validTypeSyntax(p.Type) {
					return nil, invalid(typeSpan, symbol, fmt.Sprintf("invalid type %q for parameter %q", p.Type, p.Name), parameterSpan, span)
				}
				parameters = append(parameters, Parameter{Name: p.Name, Type: p.Type, Span: parameterSpan, TypeSpan: typeSpan})
			}
			set.Functions = append(set.Functions, Function{Name: f.Name, Kind: f.Kind, Trust: f.Trust, Parameters: parameters, Returns: f.Returns, Source: path, Span: span, ReturnSpan: source.field("returns").span})
		}
	}
	modules := make(map[string]bool)
	for _, m := range set.Modules {
		modules[m.Name] = true
	}
	for _, t := range set.Types {
		if !modules[owner(t.Name)] {
			return nil, invalid(t.Span, t.Name, fmt.Sprintf("containing module %q is not declared", owner(t.Name)))
		}
	}
	for _, f := range set.Functions {
		if !modules[owner(f.Name)] {
			return nil, invalid(f.Span, f.Name, fmt.Sprintf("containing module %q is not declared; methods are prohibited", owner(f.Name)))
		}
	}
	return set, nil
}

func tomlFailure(err error) error {
	var unknown *toml.StrictMissingError
	if errors.As(err, &unknown) {
		fields := make([]string, 0, len(unknown.Errors))
		for _, field := range unknown.Errors {
			line, column := field.Position()
			fields = append(fields, fmt.Sprintf("%q (line %d, column %d)", strings.Join(field.Key(), "."), line, column))
		}
		return fmt.Errorf("unknown fields: %s", strings.Join(fields, "; "))
	}
	var syntax *toml.DecodeError
	if errors.As(err, &syntax) {
		line, column := syntax.Position()
		return fmt.Errorf("line %d, column %d: %w", line, column, err)
	}
	return err
}

func invalid(span model.Span, name, message string, related ...model.Span) error {
	return &Error{Err: fmt.Errorf("manifest %s, declaration %q: %s", span.File, name, message), Span: span, Symbol: name, Related: related}
}

func register(seen map[string]model.Span, name string, span model.Span) error {
	if previous, exists := seen[name]; exists {
		return invalid(span, name, fmt.Sprintf("duplicate declaration (first declared in %s)", previous.File), previous)
	}
	seen[name] = span
	return nil
}

func qualified(name string) bool {
	return strings.Contains(name, ".") && discovery.ValidModuleName(name)
}

func owner(name string) string {
	return name[:strings.LastIndexByte(name, '.')]
}

// The grammar is deliberately lexical: nominal names (including project
// records) are resolved later. There is no evaluation of embedded Python.
func validTypeSyntax(text string) bool {
	p := typeParser{text: text}
	if !p.typ(0) {
		return false
	}
	p.space()
	return p.pos == len(p.text)
}

type typeParser struct {
	text string
	pos  int
}

func (p *typeParser) space() {
	for p.pos < len(p.text) && (p.text[p.pos] == ' ' || p.text[p.pos] == '\t') {
		p.pos++
	}
}

func (p *typeParser) take(token string) bool {
	p.space()
	if strings.HasPrefix(p.text[p.pos:], token) {
		p.pos += len(token)
		return true
	}
	return false
}

func (p *typeParser) typ(depth int) bool {
	if depth > 64 {
		return false
	}
	p.space()
	start := p.pos
	for p.pos < len(p.text) && !strings.ContainsRune(" \t\r\n[],|", rune(p.text[p.pos])) {
		p.pos++
	}
	name := p.text[start:p.pos]
	if name == "tuple" {
		if !p.take("[") || !p.typ(depth+1) || !p.take(",") || !p.take("...") || !p.take("]") {
			return false
		}
	} else if name != "None" && name != "bool" && name != "int" && name != "float" && name != "str" && name != "bytes" && !qualified(name) {
		return false
	}
	if p.take("|") {
		// Optional-of-None is not a meaningful type, and arbitrary unions
		// and chained optional operators are intentionally not admitted.
		return name != "None" && p.take("None")
	}
	return true
}
