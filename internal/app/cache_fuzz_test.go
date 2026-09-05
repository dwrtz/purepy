package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dwrtz/purepy/internal/cache"
	"github.com/dwrtz/purepy/internal/diag"
	"github.com/dwrtz/purepy/internal/model"
)

// These inputs describe corruption, not an authenticated-cache threat model.
// A syntactically valid semantic rewrite with a recomputed checksum is outside
// the oracle: a disposable local cache cannot authenticate its source contents.
var cacheFallbackModes = []string{
	"truncated envelope", "unknown schema", "wrong key", "wrong checksum",
	"nonobject payload", "missing tree", "unknown node kind", "nil statement",
	"invalid byte range", "missing return annotation", "comparison arity",
	"literal type", "call argument role", "statement role", "unknown child key",
	"diagnostic range", "diagnostic severity", "diagnostic code",
}

func TestMalformedCacheFallbackMatchesUncachedReport(t *testing.T) {
	for project := byte(0); project < 3; project++ {
		for mode, name := range cacheFallbackModes {
			t.Run(fmt.Sprintf("project_%d/%s", project, name), func(t *testing.T) {
				checkCacheFallback(t, []byte{project, byte(mode), 17, 93, 8})
			})
		}
	}
}

// FuzzCacheFallback exercises the complete application path, including parser
// regeneration, linking, function checks, JSON reports, and worker scheduling.
// Inputs are capped at 8 KiB and choose one of three two-module projects. No
// project source is executed, and no tree can grow recursively from the input.
func FuzzCacheFallback(f *testing.F) {
	for mode := range cacheFallbackModes {
		for project := byte(0); project < 3; project++ {
			f.Add([]byte{project, byte(mode), 17, 93, 8})
		}
	}
	f.Add([]byte{})
	f.Add([]byte{1})
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 8<<10 {
			t.Skip()
		}
		checkCacheFallback(t, data)
	})
}

func cacheFallbackByte(data []byte, index int) byte {
	if index >= len(data) {
		return 0
	}
	return data[index]
}

func checkCacheFallback(t *testing.T, data []byte) {
	t.Helper()
	project := cacheFallbackByte(data, 0) % 3
	mode := int(cacheFallbackByte(data, 1)) % len(cacheFallbackModes)
	value := int(cacheFallbackByte(data, 2)) - 128
	main := fmt.Sprintf("from helper import twice\ndef run(x: int) -> int:\n    return twice(x) + len((x, %d))\n", value)
	switch project {
	case 1:
		main = fmt.Sprintf("from helper import twice\ndef run(x: int) -> int:\n    return twice(\"bad\") + len((x, %d))\n", value)
	case 2:
		main = fmt.Sprintf("from helper import twice\ndef run(x: int) -> int:\n    value: int = %d\n    if x > 0:\n        return twice(value)\n    value = \"bad\"\n    return value\n", value)
	}
	root := cliProject(t, map[string]string{
		"src/helper.py": "def twice(x: int) -> int:\n    return x + x\n",
		"src/main.py":   main,
	}, []string{"main.run"}, nil)
	baseline := Check(Options{Path: root, NoCache: true, Jobs: 1})
	if baseline.Files != 2 || baseline.OK != (project == 0) || baseline.CacheHits != 0 {
		t.Fatalf("invalid seed project %d: %s, cache hits %d", project, cliJSON(t, baseline), baseline.CacheHits)
	}
	want := cliJSON(t, baseline)
	assertReport := func(stage string, options Options, hits int) {
		t.Helper()
		r := Check(options)
		if got := cliJSON(t, r); got != want || r.CacheHits != hits {
			t.Fatalf("%s (%s): cache hits %d, want %d; reports:\n%s\n%s", stage, cacheFallbackModes[mode], r.CacheHits, hits, want, got)
		}
	}
	assertReport("cold", Options{Path: root, Jobs: 1}, 0)
	assertReport("warm workers", Options{Path: root, Jobs: 2}, 2)
	artifacts, err := filepath.Glob(filepath.Join(root, ".purepy-cache", "*.json"))
	if err != nil || len(artifacts) != 2 {
		t.Fatalf("expected two artifacts: %v, %v", artifacts, err)
	}
	for _, path := range artifacts {
		original, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		corrupted := corruptCacheFallback(t, original, mode, data)
		if err := os.WriteFile(path, corrupted, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Every mutation is definitely malformed, so both files must be reparsed.
	assertReport("corruption fallback", Options{Path: root, Jobs: 2}, 0)
	assertReport("repaired warm", Options{Path: root, Jobs: 1}, 2)
	assertReport("uncached workers", Options{Path: root, NoCache: true, Jobs: 2}, 0)
}

type fallbackArtifact struct {
	Schema   int             `json:"schema"`
	Key      string          `json:"key"`
	Checksum string          `json:"checksum"`
	Payload  json.RawMessage `json:"payload"`
}

func corruptCacheFallback(t *testing.T, original []byte, mode int, data []byte) []byte {
	t.Helper()
	marshal := func(value any) []byte {
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		return encoded
	}
	if mode == 0 {
		// The generated envelope has no trailing whitespace, so a strict
		// prefix can never accidentally remain a valid complete artifact.
		cut := int(cacheFallbackByte(data, 3)) * len(original) / 256
		return original[:cut]
	}
	var envelope fallbackArtifact
	if err := json.Unmarshal(original, &envelope); err != nil {
		t.Fatal(err)
	}
	switch mode {
	case 1:
		envelope.Schema = -1 - int(cacheFallbackByte(data, 3))
	case 2:
		envelope.Key = "invalid-" + cache.Digest(data)
	case 3:
		at := int(cacheFallbackByte(data, 3)) % len(envelope.Checksum)
		changed := []byte(envelope.Checksum)
		if changed[at] == '0' {
			changed[at] = '1'
		} else {
			changed[at] = '0'
		}
		envelope.Checksum = string(changed)
	case 4:
		envelope.Payload = marshal(string(data))
		envelope.Checksum = cache.Digest(envelope.Payload)
	default:
		var summary cache.Summary
		if err := json.Unmarshal(envelope.Payload, &summary); err != nil {
			t.Fatal(err)
		}
		if summary.Tree == nil || summary.Tree.Kind != "Module" {
			t.Fatal("expected a parsed module in the original cache")
		}
		span := summary.Tree.Span
		node := func(kind string) *model.Node {
			return &model.Node{Kind: kind, Span: span, Fields: map[string]*model.Node{}, Lists: map[string][]*model.Node{}, Attr: map[string]string{}}
		}
		name := func(value string) *model.Node {
			n := node("Name")
			n.Attr["name"] = value
			return n
		}
		appendExpression := func(expression *model.Node) {
			statement := node("ExprStmt")
			statement.Fields["value"] = expression
			summary.Tree.Lists["body"] = append(summary.Tree.Items("body"), statement)
		}
		switch mode {
		case 5:
			summary.Tree = nil
		case 6:
			summary.Tree.Kind = "Unknown_" + cache.Digest(data)
		case 7:
			summary.Tree.Lists["body"] = append(summary.Tree.Items("body"), nil)
		case 8:
			summary.Tree.Span.End = -1 - int(cacheFallbackByte(data, 3))
		case 9:
			found := false
			for _, statement := range summary.Tree.Items("body") {
				if statement.Kind == "Function" {
					delete(statement.Fields, "returns")
					found = true
					break
				}
			}
			if !found {
				t.Fatal("expected a function to corrupt")
			}
		case 10:
			comparison := node("Compare")
			comparison.Lists["operands"] = []*model.Node{name("x"), name("x")}
			comparison.Attr["ops"] = strings.Repeat("< ", 2+int(cacheFallbackByte(data, 3)%16))
			appendExpression(comparison)
		case 11:
			literal := node("Literal")
			literal.Attr["type"] = "unknown-" + cache.Digest(data)
			appendExpression(literal)
		case 12:
			call := node("Call")
			call.Fields["target"] = name("len")
			// Call arguments must be Arg wrappers, never naked expressions.
			call.Lists["args"] = []*model.Node{name("x")}
			appendExpression(call)
		case 13:
			summary.Tree.Lists["body"] = append(summary.Tree.Items("body"), name("x"))
		case 14:
			if summary.Tree.Fields == nil {
				summary.Tree.Fields = map[string]*model.Node{}
			}
			summary.Tree.Fields["unexpected-"+cache.Digest(data)] = name("x")
		case 15, 16, 17:
			diagnostic := diag.New("PP002", "invalid cached diagnostic", span)
			switch mode {
			case 15:
				diagnostic.Span.Start = span.End + 1 + int(cacheFallbackByte(data, 3))
				diagnostic.Span.End = diagnostic.Span.Start
			case 16:
				diagnostic.Severity = "invalid-" + cache.Digest(data)
			case 17:
				diagnostic.Code = "INVALID" + cache.Digest(data)
			}
			summary.Diagnostics = append(summary.Diagnostics, diagnostic)
		default:
			t.Fatalf("unknown corruption mode %d", mode)
		}
		envelope.Payload = marshal(summary)
		envelope.Checksum = cache.Digest(envelope.Payload)
	}
	return marshal(envelope)
}
