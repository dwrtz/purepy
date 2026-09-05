// Package manifest reads declarative external signatures, never executable code.
package manifest

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/dwrtz/purepy/internal/discovery"
	"github.com/pelletier/go-toml/v2"
)

const SchemaVersion = 1

type Set struct {
	Modules   []Module
	Types     []Type
	Functions []Function
}

type Module struct {
	Name       string
	ImportSafe bool
	Source     string
}

type Type struct {
	Name      string
	Category  string
	Labels    []string
	Source    string
	Immutable bool
}

type Function struct {
	Name       string
	Kind       string
	Trust      string
	Returns    string
	Parameters []Parameter
	Source     string
}

type Parameter struct {
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
	Name       string       `toml:"name"`
	Kind       string       `toml:"kind"`
	Trust      string       `toml:"trust"`
	Returns    string       `toml:"returns"`
	Parameters *[]Parameter `toml:"parameters"`
}

// Load preserves configuration order and declaration order. Duplicate names
// are errors even when their declarations are identical. Type resolution,
// deep category checking and host capability authorization happen after the
// project and manifest declarations have been linked into one program image.
func Load(paths []string) (*Set, error) {
	set := &Set{Modules: []Module{}, Types: []Type{}, Functions: []Function{}}
	seen := make(map[string]string)
	for _, path := range paths {
		path, err := filepath.Abs(path)
		if err != nil {
			return nil, fmt.Errorf("manifest path: %w", err)
		}
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
			return nil, fmt.Errorf("manifest %s: %w", path, tomlFailure(err))
		}
		if doc.Schema != SchemaVersion {
			return nil, fmt.Errorf("manifest %s: unsupported schema %d; expected %d", path, doc.Schema, SchemaVersion)
		}
		for _, m := range doc.Modules {
			if !discovery.ValidModuleName(m.Name) {
				return nil, invalid(path, m.Name, "invalid or noncanonical module name")
			}
			if m.ImportSafe == nil {
				return nil, invalid(path, m.Name, "import_safe is required")
			}
			if err := register(seen, m.Name, path); err != nil {
				return nil, err
			}
			set.Modules = append(set.Modules, Module{Name: m.Name, ImportSafe: *m.ImportSafe, Source: path})
		}
		for _, t := range doc.Types {
			if !qualified(t.Name) {
				return nil, invalid(path, t.Name, "type name must be a canonical fully qualified name")
			}
			if err := register(seen, t.Name, path); err != nil {
				return nil, err
			}
			labels := []string{}
			immutable := false
			switch t.Category {
			case "value":
				if t.Immutable == nil || !*t.Immutable {
					return nil, invalid(path, t.Name, "external value requires immutable = true (the deep immutability contract)")
				}
				immutable = true
			case "capability":
				if t.Labels == nil || len(*t.Labels) == 0 {
					return nil, invalid(path, t.Name, "capability requires at least one exact reporting label")
				}
				labelSeen := make(map[string]bool)
				for _, label := range *t.Labels {
					if label == "" || strings.ContainsAny(label, "*?") || strings.IndexFunc(label, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
						return nil, invalid(path, t.Name, "capability labels must be nonempty exact strings without whitespace, controls or wildcards")
					}
					if labelSeen[label] {
						return nil, invalid(path, t.Name, fmt.Sprintf("duplicate capability label %q", label))
					}
					labelSeen[label] = true
					labels = append(labels, label)
				}
			case "host_ref":
			default:
				return nil, invalid(path, t.Name, fmt.Sprintf("unknown type category %q", t.Category))
			}
			if t.Category != "capability" && t.Labels != nil {
				return nil, invalid(path, t.Name, "labels are permitted only on capability types")
			}
			if t.Category != "value" && t.Immutable != nil {
				return nil, invalid(path, t.Name, "immutable is permitted only on value types")
			}
			set.Types = append(set.Types, Type{Name: t.Name, Category: t.Category, Labels: labels, Immutable: immutable, Source: path})
		}
		for _, f := range doc.Functions {
			if !qualified(f.Name) {
				return nil, invalid(path, f.Name, "function name must be a canonical fully qualified name")
			}
			if err := register(seen, f.Name, path); err != nil {
				return nil, err
			}
			if f.Kind != "sync" && f.Kind != "async" {
				return nil, invalid(path, f.Name, "kind must be sync or async")
			}
			if f.Trust != "pure" && f.Trust != "host" {
				return nil, invalid(path, f.Name, "trust must be pure or host")
			}
			if f.Parameters == nil {
				return nil, invalid(path, f.Name, "parameters is required (use [] for no parameters)")
			}
			if !validTypeSyntax(f.Returns) {
				return nil, invalid(path, f.Name, fmt.Sprintf("invalid return type %q", f.Returns))
			}
			parameterSeen := make(map[string]bool)
			for _, p := range *f.Parameters {
				if !discovery.ValidIdentifier(p.Name) {
					return nil, invalid(path, f.Name, fmt.Sprintf("invalid parameter name %q", p.Name))
				}
				if parameterSeen[p.Name] {
					return nil, invalid(path, f.Name, fmt.Sprintf("duplicate parameter %q", p.Name))
				}
				parameterSeen[p.Name] = true
				if !validTypeSyntax(p.Type) {
					return nil, invalid(path, f.Name, fmt.Sprintf("invalid type %q for parameter %q", p.Type, p.Name))
				}
			}
			set.Functions = append(set.Functions, Function{Name: f.Name, Kind: f.Kind, Trust: f.Trust, Parameters: append([]Parameter{}, (*f.Parameters)...), Returns: f.Returns, Source: path})
		}
	}
	modules := make(map[string]bool)
	for _, m := range set.Modules {
		modules[m.Name] = true
	}
	for _, t := range set.Types {
		if !modules[owner(t.Name)] {
			return nil, invalid(t.Source, t.Name, fmt.Sprintf("containing module %q is not declared", owner(t.Name)))
		}
	}
	for _, f := range set.Functions {
		if !modules[owner(f.Name)] {
			return nil, invalid(f.Source, f.Name, fmt.Sprintf("containing module %q is not declared; methods are prohibited", owner(f.Name)))
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

func invalid(path, name, message string) error {
	return fmt.Errorf("manifest %s, declaration %q: %s", path, name, message)
}

func register(seen map[string]string, name, source string) error {
	if previous, exists := seen[name]; exists {
		return invalid(source, name, fmt.Sprintf("duplicate declaration (first declared in %s)", previous))
	}
	seen[name] = source
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
