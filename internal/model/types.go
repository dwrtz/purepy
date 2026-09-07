package model

import "strings"

type Type struct {
	Kind      string   `json:"kind"`
	Name      string   `json:"name,omitempty"`
	Elem      *Type    `json:"element,omitempty"`
	Args      []Type   `json:"arguments,omitempty"`
	Params    []Type   `json:"parameters,omitempty"`
	Returns   *Type    `json:"returns,omitempty"`
	Origins   []string `json:"-"`
	Variables []string `json:"-"`
}

var (
	Invalid = Type{Kind: "invalid"}
	None    = Type{Kind: "None"}
	Bool    = Type{Kind: "bool"}
	Int     = Type{Kind: "int"}
	Float   = Type{Kind: "float"}
	Str     = Type{Kind: "str"}
	Bytes   = Type{Kind: "bytes"}
)

// WithinLimit counts expanded type syntax, including repeated shared subtrees.
// This prevents compact generic source from producing exponential reports.
func (t Type) WithinLimit() bool {
	budget := 4096
	var visit func(Type, int) bool
	visit = func(t Type, depth int) bool {
		budget--
		if budget < 0 || depth > 128 {
			return false
		}
		if t.Elem != nil && !visit(*t.Elem, depth+1) {
			return false
		}
		if t.Returns != nil && !visit(*t.Returns, depth+1) {
			return false
		}
		for _, a := range t.Args {
			if !visit(a, depth+1) {
				return false
			}
		}
		for _, a := range t.Params {
			if !visit(a, depth+1) {
				return false
			}
		}
		return true
	}
	return visit(t, 0)
}

func (t Type) String() string {
	if !t.WithinLimit() {
		return "<type exceeds analysis limit>"
	}
	return t.spelling()
}
func (t Type) spelling() string {
	if t.Kind == "tuple" && t.Elem != nil {
		return "tuple[" + t.Elem.spelling() + ", ...]"
	}
	if t.Kind == "optional" && t.Elem != nil {
		return t.Elem.spelling() + " | None"
	}
	if t.Kind == "callable" {
		xs := []string{}
		for _, a := range t.Params {
			xs = append(xs, a.spelling())
		}
		result := "invalid"
		if t.Returns != nil {
			result = t.Returns.spelling()
		}
		return "Callable[[" + strings.Join(xs, ", ") + "], " + result + "]"
	}
	if len(t.Args) > 0 || t.Kind == "product" {
		xs := []string{}
		for _, a := range t.Args {
			xs = append(xs, a.spelling())
		}
		name := t.Name
		if t.Kind == "product" {
			name = "tuple"
		}
		return name + "[" + strings.Join(xs, ", ") + "]"
	}
	if t.Name != "" {
		return t.Name
	}
	return t.Kind
}
func (t Type) Equal(u Type) bool {
	return t.WithinLimit() && u.WithinLimit() && t.spelling() == u.spelling() && t.Kind == u.Kind
}
func (t Type) Accepts(u Type) bool {
	return t.Equal(u) || t.Kind == "optional" && (u.Kind == "None" || t.Elem != nil && t.Elem.Equal(u))
}
func (t Type) Pure() bool {
	switch t.Kind {
	case "None", "bool", "int", "float", "str", "bytes", "value", "typevar":
		return true
	case "record", "product":
		for _, a := range t.Args {
			if !a.Pure() {
				return false
			}
		}
		return true
	case "tuple", "optional":
		return t.Elem != nil && t.Elem.Pure()
	}
	return false
}
func (t Type) FunctionalValue() bool {
	if t.Pure() {
		return true
	}
	if t.Kind != "callable" || t.Returns == nil || !t.Returns.FunctionalValue() {
		return false
	}
	for _, p := range t.Params {
		if !p.FunctionalValue() {
			return false
		}
	}
	return true
}
func (t Type) Category() string {
	if t.Pure() {
		return "pure_value"
	}
	switch t.Kind {
	case "callable":
		return "pure_function"
	case "capability":
		return "capability"
	case "host_ref":
		return "host_reference"
	case "range":
		return "ephemeral_intrinsic"
	}
	return "unknown"
}
func Tuple(t Type) Type    { return Type{Kind: "tuple", Elem: &t} }
func Optional(t Type) Type { return Type{Kind: "optional", Elem: &t} }
func Primitive(s string) (Type, bool) {
	switch strings.TrimSpace(s) {
	case "None":
		return None, true
	case "bool":
		return Bool, true
	case "int":
		return Int, true
	case "float":
		return Float, true
	case "str":
		return Str, true
	case "bytes":
		return Bytes, true
	}
	return Invalid, false
}

type Parameter struct {
	Name   string   `json:"name"`
	Type   Type     `json:"type"`
	Span   Span     `json:"span"`
	Labels []string `json:"labels,omitempty"`
}
type Function struct {
	Name       string      `json:"name"`
	TypeParams []string    `json:"type_parameters,omitempty"`
	Parent     string      `json:"parent,omitempty"`
	Kind       string      `json:"kind"`
	Parameters []Parameter `json:"parameters"`
	Returns    Type        `json:"returns"`
	Origin     string      `json:"origin"`
	Trust      string      `json:"trust"`
	Source     string      `json:"source,omitempty"`
	Span       Span        `json:"span"`
	ReturnSpan Span        `json:"-"`
	Body       []*Node     `json:"-"`
	Module     string      `json:"-"`
}

func (f *Function) Pure() bool {
	for _, p := range f.Parameters {
		if !p.Type.FunctionalValue() {
			return false
		}
	}
	return f.Returns.FunctionalValue()
}
func (f *Function) Classification() string {
	prefix := "effectful"
	if f.Pure() {
		prefix = "pure"
	}
	return prefix + "_" + f.Kind
}

type Record struct {
	TypeParams []string
	Name       string
	Fields     []Parameter
	Span       Span
}
type CallEdge struct {
	Instantiation       []Type   `json:"-"`
	Caller              string   `json:"caller"`
	Callee              string   `json:"callee"`
	Kind                string   `json:"kind"`
	Span                Span     `json:"span"`
	CapabilityArguments []string `json:"capability_arguments"`
	HostRefArguments    []string `json:"host_ref_arguments"`
}
type Fact struct {
	Span        Span   `json:"span"`
	Symbol      string `json:"symbol,omitempty"`
	Type        Type   `json:"type"`
	Category    string `json:"category"`
	Description string `json:"description"`
}
