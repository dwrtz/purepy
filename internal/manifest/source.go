package manifest

import (
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/dwrtz/purepy/internal/model"
	"github.com/pelletier/go-toml/v2"
	"github.com/pelletier/go-toml/v2/unstable"
)

// Error carries the location of invalid declarative metadata to CLI reports.
// It never loads or executes the host implementation named by a manifest.
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

func declarationSpan(path string, data []byte, name string) model.Span {
	span := sourceSpan(path, data, 0, 0)
	var parser unstable.Parser
	parser.Reset(data)
	for parser.NextExpression() {
		node := parser.Expression()
		if node.Kind != unstable.KeyValue {
			continue
		}
		keys := node.Key()
		if !keys.Next() {
			continue
		}
		key := keys.Node()
		if name == "" && string(key.Data) == "schema" {
			return sourceSpan(path, data, int(key.Raw.Offset), int(key.Raw.Offset+key.Raw.Length))
		}
		value := node.Value()
		if name != "" && string(key.Data) == "name" && value.Kind == unstable.String && string(value.Data) == name {
			span = sourceSpan(path, data, int(value.Raw.Offset), int(value.Raw.Offset+value.Raw.Length))
		}
	}
	return span
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
