package manifest

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/dwrtz/purepy/internal/model"
	"github.com/pelletier/go-toml/v2"
	"github.com/pelletier/go-toml/v2/unstable"
)

// Error carries the location of invalid declarative metadata to CLI reports.
// It never loads or executes the host implementation named by a manifest.
type Error struct {
	Err     error
	Span    model.Span
	Symbol  string
	Related []model.Span
}

func (e *Error) Error() string { return e.Err.Error() }
func (e *Error) Unwrap() error { return e.Err }

func sourceSpan(path string, data []byte, start, end int) model.Span {
	start = max(0, min(start, len(data)))
	end = max(start, min(end, len(data)))
	position := func(offset int) (int, int) {
		prefix := data[:offset]
		lineStart := strings.LastIndexByte(string(prefix), '\n') + 1
		return 1 + strings.Count(string(prefix), "\n"), 1 + utf8.RuneCount(prefix[lineStart:])
	}
	line, column := position(start)
	endLine, endColumn := position(end)
	return model.Span{File: path, Start: start, End: end, Line: line, Column: column, EndLine: endLine, EndColumn: endColumn}
}

// sourceValue is a source-only projection of the successfully decoded TOML.
// Following table arrays by their current element preserves declaration order
// and distinguishes repeated names, including inline and table parameters.
type sourceValue struct {
	span    model.Span
	keySpan model.Span
	array   bool
	fields  map[string]*sourceValue
	items   []*sourceValue
}

func (v *sourceValue) field(name string) *sourceValue {
	if found := v.fields[name]; found != nil {
		return found
	}
	return &sourceValue{span: v.span, keySpan: v.span}
}

func (v *sourceValue) item(index int) *sourceValue {
	if index < len(v.items) {
		return v.items[index]
	}
	return &sourceValue{span: v.span}
}

func containerSpan(value *sourceValue) model.Span {
	if value.keySpan.File != "" {
		return value.keySpan
	}
	return value.span
}

type sourcePoint struct {
	offset int
	runes  int
}

// sourceIndex bounds column lookup work even for large single-line inline
// arrays. Checkpoints store one prefix count per 256 bytes, at rune boundaries,
// instead of a column for every byte. Each lookup scans at most 259 bytes.
type sourceIndex struct {
	path        string
	data        []byte
	lines       []sourcePoint
	checkpoints []sourcePoint
}

func indexSource(path string, data []byte) sourceIndex {
	index := sourceIndex{path: path, data: data, lines: []sourcePoint{{}}, checkpoints: []sourcePoint{{}}}
	runes, nextCheckpoint := 0, 256
	for offset := 0; offset < len(data); {
		if offset >= nextCheckpoint {
			index.checkpoints = append(index.checkpoints, sourcePoint{offset, runes})
			nextCheckpoint = offset + 256
		}
		width := 1
		if data[offset] >= utf8.RuneSelf {
			_, width = utf8.DecodeRune(data[offset:])
		}
		runes++
		if data[offset] == '\n' {
			index.lines = append(index.lines, sourcePoint{offset + 1, runes})
		}
		offset += width
	}
	return index
}

func (index sourceIndex) position(offset int) (line, column int) {
	line = sort.Search(len(index.lines), func(i int) bool { return index.lines[i].offset > offset })
	checkpoint := sort.Search(len(index.checkpoints), func(i int) bool { return index.checkpoints[i].offset > offset }) - 1
	point := index.checkpoints[checkpoint]
	column = 1 + point.runes + utf8.RuneCount(index.data[point.offset:offset]) - index.lines[line-1].runes
	return
}

func (index sourceIndex) span(start, end int) model.Span {
	start = max(0, min(start, len(index.data)))
	end = max(start, min(end, len(index.data)))
	line, column := index.position(start)
	endLine, endColumn := index.position(end)
	return model.Span{File: index.path, Start: start, End: end, Line: line, Column: column, EndLine: endLine, EndColumn: endColumn}
}

func declarationSources(path string, data []byte) *sourceValue {
	index := indexSource(path, data)
	spanAt := index.span
	root := &sourceValue{span: spanAt(0, 0), fields: map[string]*sourceValue{}}
	section := root
	type sourceKey struct {
		name string
		span model.Span
	}
	keysOf := func(node *unstable.Node) []sourceKey {
		var keys []sourceKey
		it := node.Key()
		for it.Next() {
			key := it.Node()
			keys = append(keys, sourceKey{string(key.Data), spanAt(int(key.Raw.Offset), int(key.Raw.Offset+key.Raw.Length))})
		}
		return keys
	}
	// The TOML decoder has validated syntax and value types. Exact schema keys
	// and array shapes are checked using this projection because the decoder
	// also accepts case-folded field names and singleton tables for slices.
	// Intermediate table-array nodes refer to their most recent declaration.
	locate := func(start *sourceValue, keys []sourceKey) *sourceValue {
		current := start
		for _, key := range keys {
			if current.fields == nil {
				current.fields = map[string]*sourceValue{}
			}
			next := current.fields[key.name]
			if next == nil {
				next = &sourceValue{span: current.span, keySpan: key.span, fields: map[string]*sourceValue{}}
				current.fields[key.name] = next
			}
			if len(next.items) > 0 {
				next = next.items[len(next.items)-1]
			}
			current = next
		}
		return current
	}
	var valueOf func(*unstable.Node) *sourceValue
	valueOf = func(node *unstable.Node) *sourceValue {
		value := &sourceValue{span: spanAt(int(node.Raw.Offset), int(node.Raw.Offset+node.Raw.Length))}
		switch node.Kind {
		case unstable.Array:
			value.array = true
			it := node.Children()
			for it.Next() {
				value.items = append(value.items, valueOf(it.Node()))
			}
		case unstable.InlineTable:
			value.fields = map[string]*sourceValue{}
			it := node.Children()
			for it.Next() {
				entry := it.Node()
				keys := keysOf(entry)
				parent := locate(value, keys[:len(keys)-1])
				if parent.fields == nil {
					parent.fields = map[string]*sourceValue{}
				}
				field := valueOf(entry.Value())
				key := keys[len(keys)-1]
				field.keySpan = key.span
				parent.fields[key.name] = field
			}
		}
		return value
	}
	var parser unstable.Parser
	parser.Reset(data)
	for parser.NextExpression() {
		node := parser.Expression()
		switch node.Kind {
		case unstable.KeyValue:
			keys := keysOf(node)
			parent := locate(section, keys[:len(keys)-1])
			if parent.fields == nil {
				parent.fields = map[string]*sourceValue{}
			}
			value := valueOf(node.Value())
			key := keys[len(keys)-1]
			value.keySpan = key.span
			parent.fields[key.name] = value
		case unstable.Table:
			section = locate(root, keysOf(node))
		case unstable.ArrayTable:
			keys := keysOf(node)
			parent := locate(root, keys[:len(keys)-1])
			key := keys[len(keys)-1]
			if parent.fields == nil {
				parent.fields = map[string]*sourceValue{}
			}
			array := parent.fields[key.name]
			if array == nil {
				array = &sourceValue{span: parent.span, keySpan: key.span, array: true}
				parent.fields[key.name] = array
			}
			section = &sourceValue{span: keys[0].span, fields: map[string]*sourceValue{}}
			array.items = append(array.items, section)
		}
	}
	return root
}

// validateSourceKeys closes the typed decoder's case-insensitive field lookup.
// It uses decoded TOML key names, so quoted/escaped canonical keys remain valid,
// and selects the first offending key by source offset rather than map order.
func validateSourceKeys(root *sourceValue) error {
	fields := map[string]map[string]string{
		"root":       {"schema": "", "module": "module", "type": "type", "function": "function"},
		"module":     {"name": "", "import_safe": ""},
		"type":       {"name": "", "category": "", "labels": "", "immutable": ""},
		"function":   {"name": "", "kind": "", "trust": "", "returns": "", "parameters": "parameters"},
		"parameters": {"name": "", "type": ""},
	}
	var first *Error
	var visit func(*sourceValue, string, string)
	visit = func(value *sourceValue, scope, prefix string) {
		for key, field := range value.fields {
			path := key
			if prefix != "" {
				path = prefix + "." + key
			}
			childScope, allowed := fields[scope][key]
			if !allowed {
				span := containerSpan(field)
				if first == nil || span.Start < first.Span.Start {
					first = &Error{Err: fmt.Errorf("manifest %s: unknown fields: %s (line %d, column %d); keys are case-sensitive", span.File, path, span.Line, span.Column), Span: span}
				}
				continue
			}
			if childScope != "" {
				visit(field, childScope, path)
				for _, item := range field.items {
					visit(item, childScope, path)
				}
			}
		}
	}
	visit(root, "root", "")
	if first != nil {
		return first
	}
	return nil
}

func decodingSpan(path string, data []byte, err error) model.Span {
	line, column := 1, 1
	var unknown *toml.StrictMissingError
	var syntax *toml.DecodeError
	if errors.As(err, &unknown) && len(unknown.Errors) > 0 {
		line, column = unknown.Errors[0].Position()
	} else if errors.As(err, &syntax) {
		line, column = syntax.Position()
	}
	start := 0
	for i := 1; i < line; i++ {
		next := strings.IndexByte(string(data[start:]), '\n')
		if next < 0 {
			return sourceSpan(path, data, len(data), len(data))
		}
		start += next + 1
	}
	// TOML reports byte columns; public PurePy spans use Unicode columns.
	start = min(len(data), start+column-1)
	end := start
	if end < len(data) {
		_, width := utf8.DecodeRune(data[end:])
		end += width
	}
	return sourceSpan(path, data, start, end)
}
