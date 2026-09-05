// Package cache stores disposable parser-independent module summaries. Cached
// bodies are always relinked and rechecked against the current program image.
package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dwrtz/purepy/internal/diag"
	"github.com/dwrtz/purepy/internal/model"
)

const Schema = 1

type Summary struct {
	Tree        *model.Node       `json:"tree"`
	Diagnostics []diag.Diagnostic `json:"diagnostics"`
}
type artifact struct {
	Schema   int             `json:"schema"`
	Key      string          `json:"key"`
	Checksum string          `json:"checksum"`
	Payload  json.RawMessage `json:"payload"`
}

func Digest(data []byte) string  { h := sha256.Sum256(data); return hex.EncodeToString(h[:]) }
func Key(parts ...string) string { data, _ := json.Marshal(parts); return Digest(data) }
func safeDir(dir string) bool {
	info, err := os.Lstat(dir)
	return err == nil && info.IsDir() && info.Mode()&os.ModeSymlink == 0
}
func validKey(key string) bool {
	if len(key) != 64 {
		return false
	}
	_, err := hex.DecodeString(key)
	return err == nil
}
func Read(dir, key string) (Summary, bool) {
	if !validKey(key) || !safeDir(dir) {
		return Summary{}, false
	}
	path := filepath.Join(dir, key+".json")
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 64<<20 {
		return Summary{}, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Summary{}, false
	}
	var a artifact
	if json.Unmarshal(data, &a) != nil || a.Schema != Schema || a.Key != key || Digest(a.Payload) != a.Checksum {
		return Summary{}, false
	}
	var s Summary
	if json.Unmarshal(a.Payload, &s) != nil || s.Tree == nil || s.Tree.Kind != "Module" || !validNode(s.Tree, 0, s.Tree.Span) {
		return Summary{}, false
	}
	return s, true
}

// Checksums detect byte corruption; structural checks also reject malformed
// summaries that happen to be valid JSON. Nil optional fields are allowed, but
// an ordered syntax list can never contain a nil node.
func validNode(n *model.Node, depth int, root model.Span) bool {
	if n == nil || depth > 1024 || n.Kind == "" || n.Span.Start < 0 || n.Span.End < n.Span.Start || n.Span.File != root.File || n.Span.Start < root.Start || n.Span.End > root.End {
		return false
	}
	var required []string
	if n.Kind == "Function" || n.Kind == "Record" || n.Kind == "If" || n.Kind == "For" || n.Kind == "While" {
		if len(n.Items("body")) == 0 {
			return false
		}
	}
	switch n.Kind {
	case "Module", "Tuple", "FString", "Pass", "Break", "Continue", "Return", "Unsupported":
	case "Function":
		if n.A("name") == "" || (n.A("async") != "true" && n.A("async") != "false") {
			return false
		}
		required = []string{"returns"}
	case "Record", "Name", "ImportName":
		if n.A("name") == "" {
			return false
		}
	case "Param":
		if n.A("name") == "" {
			return false
		}
		required = []string{"annotation"}
	case "Import":
		if n.A("module") == "" {
			return false
		}
	case "Literal":
		if n.A("type") == "" {
			return false
		}
	case "Assign":
		required = []string{"target"}
		if n.Get("annotation") == nil && n.Get("value") == nil {
			return false
		}
	case "If", "While":
		required = []string{"test"}
	case "For":
		required = []string{"target", "iter"}
	case "ExprStmt", "Arg", "Format", "Await":
		required = []string{"value"}
	case "Attribute":
		required = []string{"value"}
		if n.A("name") == "" {
			return false
		}
	case "Unary":
		required = []string{"operand"}
		if n.A("op") == "" {
			return false
		}
	case "Binary", "Bool":
		required = []string{"left", "right"}
		if n.A("op") == "" {
			return false
		}
		if n.Kind == "Bool" && n.A("op") != "and" && n.A("op") != "or" {
			return false
		}
	case "Compare":
		if len(n.Items("operands")) < 2 || n.A("ops") == "" {
			return false
		}
	case "Conditional":
		required = []string{"test", "body", "else"}
	case "Index":
		required = []string{"value", "index"}
	case "Slice":
		required = []string{"value"}
	case "Call":
		required = []string{"target"}
	case "AwaitCall":
		required = []string{"call"}
		if n.Get("call") == nil || n.Get("call").Kind != "Call" {
			return false
		}
	default:
		return false
	}
	for _, key := range required {
		if n.Get(key) == nil {
			return false
		}
	}
	for _, child := range n.Fields {
		if child != nil && !validNode(child, depth+1, root) {
			return false
		}
	}
	for _, children := range n.Lists {
		for _, child := range children {
			if !validNode(child, depth+1, root) {
				return false
			}
		}
	}
	return true
}
func Write(dir, key string, s Summary) error {
	if !validKey(key) {
		return fmt.Errorf("invalid cache key")
	}
	if err := os.Mkdir(dir, 0755); err != nil && !os.IsExist(err) {
		return err
	}
	if !safeDir(dir) {
		return fmt.Errorf("cache directory is not an ordinary directory")
	}
	payload, err := json.Marshal(s)
	if err != nil {
		return err
	}
	data, err := json.Marshal(artifact{Schema: Schema, Key: key, Checksum: Digest(payload), Payload: payload})
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".purepy-")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, key+".json"))
}

// Clean removes only cache artifacts bearing the owned naming convention. It
// never recursively deletes a user-provided path or follows a cache symlink.
func Clean(dir string) (int, error) {
	if _, err := os.Lstat(dir); os.IsNotExist(err) {
		return 0, nil
	}
	if !safeDir(dir) {
		return 0, fmt.Errorf("refusing to clean non-directory or symlink cache %s", dir)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, e := range entries {
		if e.Type().IsRegular() && strings.HasSuffix(e.Name(), ".json") && validKey(strings.TrimSuffix(e.Name(), ".json")) {
			if err := os.Remove(filepath.Join(dir, e.Name())); err != nil {
				return count, err
			}
			count++
		}
	}
	return count, nil
}
