// Package cache stores disposable parser-independent module summaries. Cached
// bodies are always relinked and rechecked against the current program image.
package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
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
	f, err := os.Open(path)
	if err != nil {
		return Summary{}, false
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) || opened.Size() > 64<<20 {
		return Summary{}, false
	}
	// Keep the byte limit effective if an ordinary cache artifact grows after
	// either metadata check. The cache remains disposable on any read failure.
	data, err := io.ReadAll(io.LimitReader(f, (64<<20)+1))
	if err != nil || len(data) > 64<<20 {
		return Summary{}, false
	}
	var a artifact
	if !withinJSONBudget(data, maxCacheJSONValues) || json.Unmarshal(data, &a) != nil || a.Schema != Schema || a.Key != key || Digest(a.Payload) != a.Checksum {
		return Summary{}, false
	}
	// Inspect raw members before allocating recursive Nodes or Diagnostics.
	// Decode those final members separately so duplicate JSON keys cannot hide
	// an earlier unbounded array from the projection's budget check.
	var raw struct {
		Tree        json.RawMessage `json:"tree"`
		Diagnostics json.RawMessage `json:"diagnostics"`
	}
	if json.Unmarshal(a.Payload, &raw) != nil || !withinJSONBudget(raw.Diagnostics, maxDiagnosticJSONValues) {
		return Summary{}, false
	}
	var s Summary
	if json.Unmarshal(raw.Tree, &s.Tree) != nil || len(raw.Diagnostics) != 0 && json.Unmarshal(raw.Diagnostics, &s.Diagnostics) != nil || s.Tree == nil || s.Tree.Kind != "Module" || !validSummary(s) {
		return Summary{}, false
	}
	return s, true
}

// Checksums detect byte corruption, not deliberate tampering. The structural
// boundary below rejects malformed IR before it reaches linker/checker code;
// it does not authenticate a summary as the parse of a particular source file.
const (
	maxNodeDepth = 1024
	maxNodes     = 100_000
)

type nodeRole uint8

const (
	roleModule nodeRole = 1 << iota
	roleStatement
	roleExpression
	roleParam
	roleArg
	roleFormat
	roleRaw
)

type nodeShape struct {
	role     nodeRole
	attrs    string
	required string
	fields   map[string]nodeRole
	lists    map[string]nodeRole
}

// This table describes parser-independent syntax shapes, not PurePy typing
// rules. Structurally valid but prohibited Python still goes through ordinary
// diagnostics. Unknown keys cannot smuggle unchecked descendants into a hit.
var nodeShapes = map[string]nodeShape{
	"Module":      {role: roleModule, lists: map[string]nodeRole{"body": roleStatement}},
	"Function":    {role: roleStatement, attrs: "name async", required: "returns", fields: map[string]nodeRole{"returns": roleExpression}, lists: map[string]nodeRole{"body": roleStatement, "params": roleParam, "typeparams": roleExpression, "decorators": roleExpression}},
	"Record":      {role: roleStatement, attrs: "name", lists: map[string]nodeRole{"body": roleStatement, "bases": roleExpression, "typeparams": roleExpression, "decorators": roleExpression}},
	"Param":       {role: roleParam, attrs: "name", required: "annotation", fields: map[string]nodeRole{"annotation": roleExpression, "default": roleExpression}},
	"Import":      {role: roleStatement, attrs: "module", lists: map[string]nodeRole{"names": roleExpression}},
	"Name":        {role: roleExpression, attrs: "name alias"},
	"Literal":     {role: roleExpression, attrs: "type", lists: map[string]nodeRole{"parts": roleExpression}},
	"Assign":      {role: roleStatement, required: "target", fields: map[string]nodeRole{"target": roleExpression, "annotation": roleExpression, "value": roleExpression}},
	"If":          {role: roleStatement, required: "test", fields: map[string]nodeRole{"test": roleExpression}, lists: map[string]nodeRole{"body": roleStatement, "else": roleStatement}},
	"While":       {role: roleStatement, required: "test", fields: map[string]nodeRole{"test": roleExpression}, lists: map[string]nodeRole{"body": roleStatement, "else": roleStatement}},
	"For":         {role: roleStatement, required: "target iter", fields: map[string]nodeRole{"target": roleExpression, "iter": roleExpression}, lists: map[string]nodeRole{"body": roleStatement, "else": roleStatement}},
	"ExprStmt":    {role: roleStatement, required: "value", fields: map[string]nodeRole{"value": roleExpression}},
	"Return":      {role: roleStatement, fields: map[string]nodeRole{"value": roleExpression}},
	"Pass":        {role: roleStatement},
	"Break":       {role: roleStatement},
	"Continue":    {role: roleStatement},
	"Alias":       {role: roleStatement, required: "left right", fields: map[string]nodeRole{"left": roleExpression, "right": roleExpression}},
	"TypeList":    {role: roleExpression, lists: map[string]nodeRole{"elements": roleExpression}},
	"Tuple":       {role: roleExpression, lists: map[string]nodeRole{"elements": roleExpression}},
	"Attribute":   {role: roleExpression, attrs: "name", required: "value", fields: map[string]nodeRole{"value": roleExpression}},
	"Unary":       {role: roleExpression, attrs: "op", required: "operand", fields: map[string]nodeRole{"operand": roleExpression}},
	"Binary":      {role: roleExpression, attrs: "op", required: "left right", fields: map[string]nodeRole{"left": roleExpression, "right": roleExpression}},
	"Bool":        {role: roleExpression, attrs: "op", required: "left right", fields: map[string]nodeRole{"left": roleExpression, "right": roleExpression}},
	"Compare":     {role: roleExpression, attrs: "ops", lists: map[string]nodeRole{"operands": roleExpression}},
	"Conditional": {role: roleExpression, required: "test body else", fields: map[string]nodeRole{"test": roleExpression, "body": roleExpression, "else": roleExpression}},
	"Index":       {role: roleExpression, required: "value index", fields: map[string]nodeRole{"value": roleExpression, "index": roleExpression}},
	"Slice":       {role: roleExpression, required: "value", fields: map[string]nodeRole{"value": roleExpression, "start": roleExpression, "stop": roleExpression, "step": roleExpression}},
	"Call":        {role: roleExpression, required: "target", fields: map[string]nodeRole{"target": roleExpression}, lists: map[string]nodeRole{"args": roleArg | roleRaw}},
	"Arg":         {role: roleArg, attrs: "name", required: "value", fields: map[string]nodeRole{"value": roleExpression}},
	"AwaitCall":   {role: roleExpression, required: "call", fields: map[string]nodeRole{"call": roleExpression}},
	"Await":       {role: roleExpression, required: "value", fields: map[string]nodeRole{"value": roleExpression}},
	"FString":     {role: roleExpression, attrs: "type", lists: map[string]nodeRole{"parts": roleExpression | roleFormat}},
	"Format":      {role: roleFormat, attrs: "conversion format debug", required: "value", fields: map[string]nodeRole{"value": roleExpression}},
	"Unsupported": {role: roleStatement | roleExpression | roleRaw, attrs: "syntax", lists: map[string]nodeRole{"children": roleRaw}},
}

func wordIn(words, word string) bool {
	for candidate := range strings.FieldsSeq(words) {
		if word == candidate {
			return true
		}
	}
	return false
}

func validSpan(s, root model.Span) bool {
	if s.Start < root.Start || s.End < s.Start || s.End > root.End || s.File != root.File || s.Line < 0 || s.Column < 0 || s.EndLine < 0 || s.EndColumn < 0 {
		return false
	}
	if s.Line == 0 || s.Column == 0 || s.EndLine == 0 || s.EndColumn == 0 {
		// Preserve the empty synthetic Module used by callers/tests; a span
		// with real source coordinates must have all four coordinates.
		return s.Line == 0 && s.Column == 0 && s.EndLine == 0 && s.EndColumn == 0
	}
	return s.EndLine > s.Line || s.EndLine == s.Line && s.EndColumn >= s.Column
}

func validSummary(s Summary) bool {
	root := s.Tree
	if root == nil || root.Span.Start < 0 || root.Span.End < root.Span.Start {
		return false
	}
	remaining := maxNodes
	if !validNode(root, 0, root.Span, roleModule, &remaining, len(s.Diagnostics) != 0) {
		return false
	}
	for _, d := range s.Diagnostics {
		// Cached diagnostics come only from parsing; semantic type context is
		// recomputed against linked declarations on every verification run.
		if (d.Code != "PP002" && d.Code != "PP003") || d.Severity != "error" || d.Message == "" || len(d.Types) != 0 || !validSpan(d.Span, root.Span) {
			return false
		}
		for _, at := range d.Related {
			if !validSpan(at, root.Span) {
				return false
			}
		}
	}
	return true
}

func validNode(n *model.Node, depth int, root model.Span, role nodeRole, remaining *int, hasDiagnostics bool) bool {
	if n == nil || depth > maxNodeDepth || *remaining <= 0 || !validSpan(n.Span, root) {
		return false
	}
	*remaining--
	if !hasDiagnostics && (n.Kind == "Unsupported" || n.A("unsupported") != "") {
		return false
	}
	shape, ok := nodeShapes[n.Kind]
	if !ok || shape.role&role == 0 {
		return false
	}
	for key := range n.Attr {
		if key != "unsupported" && !wordIn(shape.attrs, key) {
			return false
		}
	}
	if wordIn("Function Record If For While", n.Kind) && len(n.Items("body")) == 0 {
		return false
	}
	switch n.Kind {
	case "Function":
		if !wordIn("true false", n.A("async")) || n.A("name") == "" {
			return false
		}
	case "Record", "Name", "Param", "Attribute":
		if n.A("name") == "" {
			return false
		}
	case "Import":
		if n.A("module") == "" || len(n.Items("names")) == 0 {
			return false
		}
		for _, name := range n.Items("names") {
			if name == nil || name.Kind != "Name" {
				return false
			}
		}
	case "Literal":
		if !wordIn("None bool int float str bytes ellipsis", n.A("type")) {
			return false
		}
		for _, part := range n.Items("parts") {
			if part == nil || part.Kind != "Literal" {
				return false
			}
		}
	case "Assign":
		if n.Get("annotation") == nil && n.Get("value") == nil {
			return false
		}
	case "Unary":
		if !wordIn("+ - ~ not", n.A("op")) {
			return false
		}
	case "Binary":
		if !wordIn("+ - * / // % ** << >> & | ^ @", n.A("op")) {
			return false
		}
	case "Bool":
		if !wordIn("and or", n.A("op")) {
			return false
		}
	case "Compare":
		ops := strings.Split(n.A("ops"), "|")
		if len(n.Items("operands")) != len(ops)+1 {
			return false
		}
		for _, op := range ops {
			switch op {
			case "==", "!=", "<", "<=", ">", ">=", "is", "is not", "in", "not in":
			default:
				return false
			}
		}
	case "AwaitCall":
		if n.Get("call") == nil || n.Get("call").Kind != "Call" {
			return false
		}
	case "FString":
		if n.A("type") != "str" {
			return false
		}
		for _, part := range n.Items("parts") {
			if part == nil || part.Kind != "Literal" && part.Kind != "Format" {
				return false
			}
		}
	}
	for key := range strings.FieldsSeq(shape.required) {
		if n.Get(key) == nil {
			return false
		}
	}
	for key, child := range n.Fields {
		childRole, ok := shape.fields[key]
		if !ok || child != nil && !validNode(child, depth+1, root, childRole, remaining, hasDiagnostics) {
			return false
		}
	}
	for key, children := range n.Lists {
		childRole, ok := shape.lists[key]
		if !ok {
			return false
		}
		for _, child := range children {
			if !validNode(child, depth+1, root, childRole, remaining, hasDiagnostics) {
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
