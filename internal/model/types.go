package model

import "strings"

type Type struct {
	Kind string `json:"kind"`
	Name string `json:"name,omitempty"`
	Elem *Type  `json:"element,omitempty"`
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

func (t Type) String() string {
	if t.Kind == "tuple" && t.Elem != nil {
		return "tuple[" + t.Elem.String() + ", ...]"
	}
	if t.Kind == "optional" && t.Elem != nil {
		return t.Elem.String() + " | None"
	}
	if t.Name != "" {
		return t.Name
	}
	return t.Kind
}
func (t Type) Equal(u Type) bool { return t.String() == u.String() && t.Kind == u.Kind }
func (t Type) Accepts(u Type) bool {
	return t.Equal(u) || t.Kind == "optional" && (u.Kind == "None" || t.Elem != nil && t.Elem.Equal(u))
}
func (t Type) Pure() bool {
	switch t.Kind {
	case "None", "bool", "int", "float", "str", "bytes", "record", "value":
		return true
	case "tuple", "optional":
		return t.Elem != nil && t.Elem.Pure()
	}
	return false
}
func (t Type) Category() string {
	if t.Pure() {
		return "pure_value"
	}
	switch t.Kind {
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
		if !p.Type.Pure() {
			return false
		}
	}
	return f.Returns.Pure()
}
func (f *Function) Classification() string {
	prefix := "effectful"
	if f.Pure() {
		prefix = "pure"
	}
	return prefix + "_" + f.Kind
}

type Record struct {
	Name   string
	Fields []Parameter
	Span   Span
}
type CallEdge struct {
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
