package manifest

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/dwrtz/purepy/internal/discovery"
	"github.com/dwrtz/purepy/internal/model"
	"github.com/pelletier/go-toml/v2"
)

const (
	manifestFuzzMaxRaw       = 16 << 10
	manifestFuzzMaxControls  = 64
	manifestFuzzMaxGenerated = 8 << 10
	manifestFuzzMutations    = 15
)

// FuzzManifestLoad reaches the complete file reader, strict TOML decoder, source
// projection, declaration validation, and ordered multi-manifest merge. Mode 0
// uses arbitrary bytes; mode 1 constructs a valid image and a targeted invalid
// near miss with an independent acceptance/rejection oracle. No host code runs.
// Input limits bound decoder recursion/source-index work as well as fuzz memory.
func FuzzManifestLoad(f *testing.F) {
	for _, pair := range manifestRawSeeds() {
		f.Add(byte(0), []byte(pair[0]), []byte(pair[1]))
	}
	for layout := byte(0); layout < 4; layout++ {
		for mutation := byte(0); mutation < manifestFuzzMutations; mutation++ {
			f.Add(byte(1), []byte{layout, mutation, layout, mutation, 3, 5, 170, mutation}, []byte{})
		}
	}
	f.Fuzz(func(t *testing.T, mode byte, first, second []byte) {
		if mode&1 == 0 {
			if len(first)+len(second) > manifestFuzzMaxRaw {
				t.Skip()
			}
			texts := [][]byte{first}
			if len(second) != 0 {
				texts = append(texts, second)
			}
			checkManifestLoad(t, texts)
			return
		}
		if len(first)+len(second) > manifestFuzzMaxControls {
			t.Skip()
		}
		controls := append(append([]byte{}, first...), second...)
		checkManifestConstruction(t, controls)
	})
}

func manifestRawSeeds() [][2]string {
	const module = "schema = 1\n[[module]]\nname = 'host.ops'\nimport_safe = true\n"
	const function = "schema = 1\n[[function]]\nname = 'host.ops.run'\nkind = 'sync'\ntrust = 'pure'\nparameters = []\nreturns = 'int'\n"
	return [][2]string{
		{"", ""}, {"schema = 1\n", ""}, {validManifest, ""},
		{module, function}, {function, module}, {module, module},
		{inlineParameterSource(8, "β"), "schema = 1\n"},
		{"schema = 1\nmodule = [{name = 'host.Ᲊ', import_safe = false}]\n", ""},
		{"schema = 1\n[[module]]\nname = 'host.ops'\n", ""},
		{"schema = 1\nmodule = [{name = 'host.ops', import_safe = true, execute = 'never.py'}]\n", ""},
		{"schema = 1\n[[module]]\nname = '\xff'\nimport_safe = true\n", ""},
		{"\x00", ""}, {"schema = [", ""}, {"schema = 1\nschema = 1\n", ""},
		{"schema = 1\nfunction = [{parameters = [{name = 'p', type = 'int'}]}]\n", ""},
		{"schema = 1\nmodule.name = 'host.ops'\nmodule.import_safe = true\n", ""},
		{"schema = 1\nunknown = " + strings.Repeat("[", 64) + "0" + strings.Repeat("]", 64) + "\n", ""},
	}
}

// The seed regressions run without a mutation campaign in ordinary/race tests.
func TestManifestLoadFuzzSeeds(t *testing.T) {
	for index, pair := range manifestRawSeeds() {
		t.Run(fmt.Sprintf("raw_%d", index), func(t *testing.T) {
			texts := [][]byte{[]byte(pair[0])}
			if pair[1] != "" {
				texts = append(texts, []byte(pair[1]))
			}
			checkManifestLoad(t, texts)
		})
	}
	for layout := byte(0); layout < 4; layout++ {
		for mutation := byte(0); mutation < manifestFuzzMutations; mutation++ {
			t.Run(fmt.Sprintf("construction_%d_%d", layout, mutation), func(t *testing.T) {
				checkManifestConstruction(t, []byte{layout, mutation, layout, mutation, 3, 5, 170, mutation})
			})
		}
	}
}

func checkManifestSpan(t *testing.T, sources map[string][]byte, span model.Span) {
	t.Helper()
	data, ok := sources[span.File]
	if !ok || span.Start < 0 || span.End < span.Start || span.End > len(data) {
		t.Fatalf("manifest span escaped source: %+v", span)
	}
	// Independent direct coordinates avoid reusing the source-index algorithm.
	position := func(offset int) (int, int) {
		prefix := data[:offset]
		lineStart := bytes.LastIndexByte(prefix, '\n') + 1
		return bytes.Count(prefix, []byte{'\n'}) + 1, utf8.RuneCount(prefix[lineStart:]) + 1
	}
	line, column := position(span.Start)
	endLine, endColumn := position(span.End)
	if span.Line != line || span.Column != column || span.EndLine != endLine || span.EndColumn != endColumn {
		t.Fatalf("manifest span coordinates disagree with bytes: %+v; want %d:%d-%d:%d", span, line, column, endLine, endColumn)
	}
}

func checkManifestStringSpan(t *testing.T, sources map[string][]byte, span model.Span, value string) {
	t.Helper()
	checkManifestSpan(t, sources, span)
	// The source projection must select the actual string token, including
	// inline/table, escaped, multiline, and quoted-key spellings.
	raw := sources[span.File][span.Start:span.End]
	var decoded struct{ Value string }
	if err := toml.Unmarshal(append([]byte("value = "), raw...), &decoded); err != nil || decoded.Value != value {
		t.Fatalf("span %+v does not select declaration value %q: token=%q error=%v", span, value, raw, err)
	}
}

func checkManifestAccepted(t *testing.T, set *Set, sources map[string][]byte) {
	t.Helper()
	if set == nil || set.Modules == nil || set.Types == nil || set.Functions == nil {
		t.Fatal("successful manifest load returned nil declarations")
	}
	seen := map[string]bool{}
	modules := map[string]bool{}
	declaration := func(name, source string, span model.Span) {
		if seen[name] || !discovery.ValidModuleName(name) || source != span.File {
			t.Fatalf("invalid, duplicate, or misattributed accepted declaration %q: %+v", name, span)
		}
		seen[name] = true
		checkManifestStringSpan(t, sources, span, name)
	}
	for _, module := range set.Modules {
		declaration(module.Name, module.Source, module.Span)
		modules[module.Name] = true
	}
	ownerDeclared := func(name string) {
		at := strings.LastIndexByte(name, '.')
		if at < 1 || !modules[name[:at]] {
			t.Fatalf("accepted declaration %q has no owning module", name)
		}
	}
	for _, typ := range set.Types {
		declaration(typ.Name, typ.Source, typ.Span)
		ownerDeclared(typ.Name)
		switch typ.Category {
		case "value":
			if !typ.Immutable || len(typ.Labels) != 0 {
				t.Fatalf("accepted external value lacks deep immutability: %+v", typ)
			}
		case "capability":
			if typ.Immutable || len(typ.Labels) == 0 {
				t.Fatalf("accepted capability lacks labels: %+v", typ)
			}
			labels := map[string]bool{}
			for _, label := range typ.Labels {
				if label == "" || labels[label] || strings.ContainsAny(label, "*?") || strings.IndexFunc(label, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
					t.Fatalf("accepted invalid capability label %q", label)
				}
				labels[label] = true
			}
		case "host_ref":
			if typ.Immutable || len(typ.Labels) != 0 {
				t.Fatalf("accepted host reference has invalid metadata: %+v", typ)
			}
		default:
			t.Fatalf("accepted unknown category %q", typ.Category)
		}
	}
	for _, function := range set.Functions {
		declaration(function.Name, function.Source, function.Span)
		ownerDeclared(function.Name)
		if function.Kind != "sync" && function.Kind != "async" || function.Trust != "pure" && function.Trust != "host" || function.Parameters == nil {
			t.Fatalf("accepted invalid fixed function declaration: %+v", function)
		}
		checkManifestStringSpan(t, sources, function.ReturnSpan, function.Returns)
		parameters := map[string]bool{}
		for _, parameter := range function.Parameters {
			if parameters[parameter.Name] || !discovery.ValidIdentifier(parameter.Name) || parameter.Span.File != function.Source || parameter.TypeSpan.File != function.Source {
				t.Fatalf("accepted invalid or duplicate parameter: %+v", parameter)
			}
			parameters[parameter.Name] = true
			checkManifestStringSpan(t, sources, parameter.Span, parameter.Name)
			checkManifestStringSpan(t, sources, parameter.TypeSpan, parameter.Type)
		}
	}
	// Nominal type resolution, deep parameter categories, and host capability
	// authorization belong to the linker and are intentionally not asserted here.
}

func checkManifestLoad(t *testing.T, texts [][]byte) (*Set, error) {
	t.Helper()
	root := t.TempDir()
	paths := make([]string, len(texts))
	sources := make(map[string][]byte, len(texts))
	for index, data := range texts {
		path := filepath.Join(root, fmt.Sprintf("manifest_%d.toml", index))
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		paths[index], sources[path] = path, data
	}
	snapshot := func(set *Set, err error) []byte {
		if err == nil {
			checkManifestAccepted(t, set, sources)
			encoded, marshalErr := json.Marshal(set)
			if marshalErr != nil {
				t.Fatal(marshalErr)
			}
			return encoded
		}
		if set != nil {
			t.Fatal("failed manifest load leaked partially accepted declarations")
		}
		var located *Error
		if !errors.As(err, &located) || located.Err == nil || located.Error() == "" {
			t.Fatalf("manifest error lost actionable source metadata: %v", err)
		}
		checkManifestSpan(t, sources, located.Span)
		for _, related := range located.Related {
			checkManifestSpan(t, sources, related)
		}
		encoded, marshalErr := json.Marshal(struct {
			Message string
			Span    model.Span
			Symbol  string
			Related []model.Span
		}{located.Error(), located.Span, located.Symbol, located.Related})
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		return encoded
	}
	set, err := Load(paths)
	want := snapshot(set, err)
	again, repeatErr := Load(paths)
	if got := snapshot(again, repeatErr); !bytes.Equal(got, want) {
		t.Fatalf("manifest loading is nondeterministic:\n%s\n%s", want, got)
	}
	return set, err
}

type manifestFuzzField struct{ name, value string }

type manifestFuzzConstruction struct {
	layout                                        int
	quoted, escaped, crlf, reverse                bool
	module, parameterPrefix, kind, trust, returns string
	parameterCount, padding                       int
	importSafe                                    bool
}

func manifestControl(data []byte, at, modulus int) int {
	if at >= len(data) {
		return 0
	}
	return int(data[at]) % modulus
}

func manifestConstruction(data []byte) manifestFuzzConstruction {
	stems := []string{"ops", "β", "Ᲊ", "café"}
	module := "host." + stems[manifestControl(data, 2, len(stems))]
	returns := []string{"int", "str", "float", "bool", "bytes", module + ".Row"}[manifestControl(data, 3, 6)]
	for depth := manifestControl(data, 4, 4); depth > 0; depth-- {
		returns = "tuple[" + returns + ", ...]"
	}
	if manifestControl(data, 7, 2) == 1 {
		returns += " | None"
	}
	return manifestFuzzConstruction{
		layout: manifestControl(data, 0, 4), quoted: manifestControl(data, 7, 4) >= 2,
		escaped: manifestControl(data, 3, 2) == 1, crlf: manifestControl(data, 6, 2) == 1,
		reverse: manifestControl(data, 7, 2) == 1,
		module:  module, parameterPrefix: stems[manifestControl(data, 2, len(stems))],
		kind:  []string{"sync", "async"}[manifestControl(data, 3, 2)],
		trust: []string{"pure", "host"}[manifestControl(data, 5, 2)], returns: returns,
		parameterCount: 2 + manifestControl(data, 5, 7), padding: manifestControl(data, 6, 256),
		importSafe: manifestControl(data, 7, 2) == 0,
	}
}

func (c manifestFuzzConstruction) q(value string) string {
	if c.escaped {
		return strconv.QuoteToASCII(value)
	}
	return "'" + value + "'"
}

func (c manifestFuzzConstruction) key(value string) string {
	if c.quoted {
		return strconv.Quote(value)
	}
	return value
}

func (c manifestFuzzConstruction) inline(fields []manifestFuzzField) string {
	parts := make([]string, 0, len(fields))
	for _, field := range fields {
		parts = append(parts, c.key(field.name)+" = "+field.value)
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

func (c manifestFuzzConstruction) table(kind string, fields []manifestFuzzField) string {
	var text strings.Builder
	fmt.Fprintf(&text, "[[%s]]\n", c.key(kind))
	for _, field := range fields {
		fmt.Fprintf(&text, "%s = %s\n", c.key(field.name), field.value)
	}
	return text.String()
}

func (c manifestFuzzConstruction) documents(mutation int) [][]byte {
	header := "# " + strings.Repeat("β", c.padding) + "\n" + c.key("schema") + " = 1\n"
	moduleFields := []manifestFuzzField{{"name", c.q(c.module)}}
	if mutation != 5 {
		moduleFields = append(moduleFields, manifestFuzzField{"import_safe", strconv.FormatBool(c.importSafe)})
	}
	moduleText, declarations := header, header
	if c.layout == 1 {
		moduleText += c.key("module") + " = [" + c.inline(moduleFields) + "]\n"
	} else {
		moduleText += c.table("module", moduleFields)
	}
	labels, immutable := "['test.read']", "true"
	if mutation == 6 {
		labels = "[]"
	}
	if mutation == 7 {
		immutable = "false"
	}
	if mutation == 13 {
		labels = "['test.*']"
	}
	types := [][]manifestFuzzField{
		{{"name", c.q(c.module + ".Read")}, {"category", c.q("capability")}, {"labels", labels}},
		{{"name", c.q(c.module + ".Context")}, {"category", c.q("host_ref")}},
		{{"name", c.q(c.module + ".Row")}, {"category", c.q("value")}, {"immutable", immutable}},
	}
	if c.layout == 1 {
		var items []string
		for _, fields := range types {
			items = append(items, c.inline(fields))
		}
		declarations += c.key("type") + " = [" + strings.Join(items, ", ") + "]\n"
	} else {
		for _, fields := range types {
			declarations += c.table("type", fields)
		}
	}
	name, kind := c.module+".run", c.kind
	if mutation == 3 {
		name = c.module + ".Row"
	}
	if mutation == 8 {
		kind = "generator"
	}
	if mutation == 11 {
		name = "missing.ops.run"
	}
	fields := []manifestFuzzField{{"name", c.q(name)}, {"kind", c.q(kind)}, {"trust", c.q(c.trust)}, {"returns", c.q(c.returns)}}
	var parameters [][]manifestFuzzField
	for index := 0; index < c.parameterCount; index++ {
		parameterName := fmt.Sprintf("%s%d", c.parameterPrefix, index)
		if mutation == 4 && index == 1 {
			parameterName = c.parameterPrefix + "0"
		}
		annotation := "int"
		if c.trust == "host" && index < 2 {
			annotation = c.module + []string{".Read", ".Context"}[index]
		}
		if mutation == 10 && index == 0 {
			annotation = "list[int]"
		}
		if mutation == 14 && index == 0 {
			annotation = strings.Repeat("tuple[", 66) + "int" + strings.Repeat(", ...]", 66)
		}
		parameter := []manifestFuzzField{{"name", c.q(parameterName)}, {"type", c.q(annotation)}}
		if mutation == 12 && index == 0 {
			parameter = append(parameter, manifestFuzzField{"execute", c.q("never.py")})
		}
		parameters = append(parameters, parameter)
	}
	if c.layout != 2 && mutation != 9 {
		var items []string
		for _, parameter := range parameters {
			items = append(items, c.inline(parameter))
		}
		separator := ", "
		if c.layout == 3 {
			separator = ",\n"
		}
		fields = append(fields, manifestFuzzField{"parameters", "[" + strings.Join(items, separator) + "]"})
	}
	if c.layout == 1 {
		declarations += c.key("function") + " = [" + c.inline(fields) + "]\n"
	} else {
		declarations += c.table("function", fields)
		if c.layout == 2 && mutation != 9 {
			for _, parameter := range parameters {
				// Quote the dotted components independently, preserving table nesting.
				declarations += "[[" + c.key("function") + "." + c.key("parameters") + "]]\n"
				for _, field := range parameter {
					declarations += c.key(field.name) + " = " + field.value + "\n"
				}
			}
		}
	}
	switch mutation {
	case 0:
		moduleText = "execute = 'never.py'\n" + moduleText
	case 1:
		moduleText = strings.Replace(moduleText, c.key("schema")+" = 1", c.key("schema")+" = 2", 1)
	case 2:
		// Duplicate across documents, including reverse configured search order.
		declarations += c.table("module", moduleFields)
	}
	texts := []string{moduleText, declarations}
	if c.reverse {
		texts[0], texts[1] = texts[1], texts[0]
	}
	result := make([][]byte, len(texts))
	for index, text := range texts {
		if c.crlf {
			text = strings.ReplaceAll(text, "\n", "\r\n")
		}
		result[index] = []byte(text)
	}
	return result
}

func checkManifestConstruction(t *testing.T, controls []byte) {
	t.Helper()
	c := manifestConstruction(controls)
	check := func(mutation int) (*Set, error) {
		texts := c.documents(mutation)
		if len(texts[0])+len(texts[1]) > manifestFuzzMaxGenerated {
			t.Fatal("manifest constructor exceeded source budget")
		}
		return checkManifestLoad(t, texts)
	}
	set, err := check(-1)
	if err != nil {
		t.Fatalf("independently valid manifest construction rejected: %v\n%s\n%s", err, c.documents(-1)[0], c.documents(-1)[1])
	}
	want := &Set{
		Modules: []Module{{Name: c.module, ImportSafe: c.importSafe}},
		Types: []Type{
			{Name: c.module + ".Read", Category: "capability", Labels: []string{"test.read"}},
			{Name: c.module + ".Context", Category: "host_ref", Labels: []string{}},
			{Name: c.module + ".Row", Category: "value", Immutable: true, Labels: []string{}},
		},
		Functions: []Function{{Name: c.module + ".run", Kind: c.kind, Trust: c.trust, Returns: c.returns, Parameters: []Parameter{}}},
	}
	for index := 0; index < c.parameterCount; index++ {
		annotation := "int"
		if c.trust == "host" && index < 2 {
			annotation = c.module + []string{".Read", ".Context"}[index]
		}
		want.Functions[0].Parameters = append(want.Functions[0].Parameters, Parameter{Name: fmt.Sprintf("%s%d", c.parameterPrefix, index), Type: annotation})
	}
	// Source spans were checked above; compare all semantic fields independently
	// to ensure accepting everything or silently dropping entries cannot pass.
	for index := range set.Modules {
		set.Modules[index].Source, set.Modules[index].Span = "", model.Span{}
	}
	for index := range set.Types {
		set.Types[index].Source, set.Types[index].Span = "", model.Span{}
	}
	for index := range set.Functions {
		function := &set.Functions[index]
		function.Source, function.Span, function.ReturnSpan = "", model.Span{}, model.Span{}
		for parameter := range function.Parameters {
			function.Parameters[parameter].Span, function.Parameters[parameter].TypeSpan = model.Span{}, model.Span{}
		}
	}
	if !reflect.DeepEqual(set, want) {
		t.Fatalf("manifest construction lost or changed declarations:\ngot %+v\nwant %+v", set, want)
	}
	mutation := manifestControl(controls, 1, manifestFuzzMutations)
	_, err = check(mutation)
	wantError := []string{"unknown fields", "unsupported schema", "duplicate declaration", "duplicate declaration", "duplicate parameter", "import_safe is required", "capability requires", "immutable = true", "kind must", "parameters is required", "invalid type", "containing module", "unknown fields", "without whitespace, controls or wildcards", "invalid type"}[mutation]
	if err == nil || !strings.Contains(err.Error(), wantError) {
		t.Fatalf("manifest near miss %d requires %q, got %v", mutation, wantError, err)
	}
}
