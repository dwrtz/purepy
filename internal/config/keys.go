package config

import (
	"fmt"
	"strings"

	"github.com/pelletier/go-toml/v2/unstable"
)

// validateSourceKeys follows decoded TOML keys after successful typed decoding.
// DisallowUnknownFields still accepts case-insensitive struct-field matches;
// check every component explicitly, preserving the offending literal's span.
func validateSourceKeys(path string, data []byte) *Error {
	allowed := func(parts []string) bool {
		switch len(parts) {
		case 1:
			return parts[0] == "tool"
		case 2:
			return parts[0] == "tool" && parts[1] == "purepy"
		case 3:
			if parts[0] != "tool" || parts[1] != "purepy" {
				return false
			}
			switch parts[2] {
			case "language", "python_syntax", "source_root", "entrypoints", "manifests":
				return true
			}
		}
		return false
	}
	keys := func(node *unstable.Node, prefix []string) ([]string, *Error) {
		parts := append([]string{}, prefix...)
		iter := node.Key()
		for iter.Next() {
			key := iter.Node()
			parts = append(parts, string(key.Data))
			if !allowed(parts) {
				span := sourceSpan(path, data, int(key.Raw.Offset), int(key.Raw.Offset+key.Raw.Length))
				symbol := strings.Join(parts, ".")
				return nil, &Error{Err: fmt.Errorf("configuration %s: unknown fields: %q (line %d, column %d); keys are case-sensitive", path, symbol, span.Line, span.Column), Span: span, Symbol: symbol}
			}
		}
		return parts, nil
	}
	var visit func(*unstable.Node, []string) *Error
	visit = func(node *unstable.Node, prefix []string) *Error {
		parts, err := keys(node, prefix)
		if err != nil {
			return err
		}
		if value := node.Value(); value.Kind == unstable.InlineTable {
			iter := value.Children()
			for iter.Next() {
				if err := visit(iter.Node(), parts); err != nil {
					return err
				}
			}
		}
		return nil
	}
	var table []string
	var parser unstable.Parser
	parser.Reset(data)
	for parser.NextExpression() {
		node := parser.Expression()
		switch node.Kind {
		case unstable.Table, unstable.ArrayTable:
			var err *Error
			table, err = keys(node, nil)
			if err != nil {
				return err
			}
		case unstable.KeyValue:
			if err := visit(node, table); err != nil {
				return err
			}
		}
	}
	return nil
}
