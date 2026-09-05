package config

import (
	"strings"
	"unicode/utf8"

	"github.com/dwrtz/purepy/internal/model"
	"github.com/pelletier/go-toml/v2/unstable"
)

// Error preserves a configuration rejection's source location for CLI reports.
// Unreadable files use a zero-width position at the beginning of the file.
type Error struct {
	Err  error
	Span model.Span
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

// declarationSpans reads only declarative TOML syntax. The returned model spans
// are detached from the parser, including escaped and multiline entrypoint names.
func declarationSpans(path string, data []byte) (map[string]model.Span, map[string]model.Span) {
	fields := map[string]model.Span{}
	entries := map[string]model.Span{}
	var parser unstable.Parser
	parser.Reset(data)
	key := func(node *unstable.Node) []string {
		var parts []string
		iter := node.Key()
		for iter.Next() {
			parts = append(parts, string(iter.Node().Data))
		}
		return parts
	}
	var visit func(*unstable.Node, []string)
	visit = func(node *unstable.Node, prefix []string) {
		parts := append(append([]string{}, prefix...), key(node)...)
		value := node.Value()
		if value.Kind == unstable.InlineTable {
			iter := value.Children()
			for iter.Next() {
				visit(iter.Node(), parts)
			}
		}
		if len(parts) != 3 || parts[0] != "tool" || parts[1] != "purepy" {
			return
		}
		iter := node.Key()
		iter.Next()
		raw := iter.Node().Raw
		fields[parts[2]] = sourceSpan(path, data, int(raw.Offset), int(raw.Offset+raw.Length))
		if parts[2] == "entrypoints" && value.Kind == unstable.Array {
			iter := value.Children()
			for iter.Next() {
				entry := iter.Node()
				if entry.Kind == unstable.String {
					raw := entry.Raw
					entries[string(entry.Data)] = sourceSpan(path, data, int(raw.Offset), int(raw.Offset+raw.Length))
				}
			}
		}
	}
	var table []string
	for parser.NextExpression() {
		expression := parser.Expression()
		switch expression.Kind {
		case unstable.Table, unstable.ArrayTable:
			table = key(expression)
		case unstable.KeyValue:
			visit(expression, table)
		}
	}
	return fields, entries
}

func errorPosition(path string, data []byte, line, column int) model.Span {
	start := 0
	for i := 1; i < line; i++ {
		next := strings.IndexByte(string(data[start:]), '\n')
		if next < 0 {
			return sourceSpan(path, data, len(data), len(data))
		}
		start += next + 1
	}
	start = max(0, min(len(data), start+column-1))
	end := start
	if end < len(data) {
		_, size := utf8.DecodeRune(data[end:])
		end += size
	}
	return sourceSpan(path, data, start, end)
}
