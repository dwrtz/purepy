package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEntrypointSpansFollowTOMLDeclarations(t *testing.T) {
	for _, tc := range []struct{ name, source, literal string }{
		{"table", "# app.main is only a comment\n" + validConfig, `"app.main"`},
		{"dotted", `tool.purepy.language = "0.1"
tool.purepy.python_syntax = "3.14"
tool.purepy.source_root = "src"
tool.purepy.entrypoints = ["app.ma\u0069n"]
tool.purepy.manifests = []
`, `"app.ma\u0069n"`},
		{"inline", `tool = { purepy = { language = "0.1", python_syntax = "3.14", source_root = "src", entrypoints = ['app.main'], manifests = [] } }`, "'app.main'"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := project(t, tc.source)
			cfg, err := Load(root)
			if err != nil {
				t.Fatal(err)
			}
			span, ok := cfg.EntrypointSpans["app.main"]
			if !ok || span.File != cfg.Path || tc.source[span.Start:span.End] != tc.literal || span.Line < 1 || span.Column < 1 || span.EndLine < span.Line || span.EndColumn < 1 {
				t.Fatalf("entrypoint location lost literal spelling: %+v", span)
			}
		})
	}
}

func TestConfigurationErrorsHaveLocatedRanges(t *testing.T) {
	for _, tc := range []struct {
		name, source, selected string
		line                   int
	}{
		{"language", strings.Replace(validConfig, `"0.1"`, `"0.2"`, 1), "language", 2},
		{"syntax version", strings.Replace(validConfig, `"3.14"`, `"3.13"`, 1), "python_syntax", 3},
		{"source root", strings.Replace(validConfig, `"src"`, `"absent"`, 1), "source_root", 4},
		{"entrypoint", strings.Replace(validConfig, `"app.main"`, `"notqualified"`, 1), `"notqualified"`, 5},
		{"unknown key", validConfig + "exclude = []\n", "e", 7},
		{"invalid TOML", validConfig + "broken = @\n", "@", 7},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := project(t, tc.source)
			_, err := Load(root)
			var located *Error
			if !errors.As(err, &located) {
				t.Fatalf("expected typed source error, got %v", err)
			}
			span := located.Span
			if span.Line != tc.line || span.EndLine != tc.line || span.Column < 1 || span.EndColumn <= span.Column || tc.source[span.Start:span.End] != tc.selected {
				t.Fatalf("incorrect error range: %+v (%v)", span, err)
			}
		})
	}
	t.Run("missing file", func(t *testing.T) {
		root := project(t, validConfig)
		if err := os.Remove(filepath.Join(root, "purepy.toml")); err != nil {
			t.Fatal(err)
		}
		_, err := Load(root)
		var located *Error
		if !errors.As(err, &located) || filepath.Base(located.Span.File) != "purepy.toml" || located.Span.Line != 1 || located.Span.EndLine != 1 || located.Span.Start != located.Span.End {
			t.Fatalf("unreadable configuration must use file with zero-width fallback: %v", err)
		}
	})
}

func TestMalformedConfigurationAlwaysReturnsLocatedError(t *testing.T) {
	for _, source := range []string{
		"[tool.purepy\n", "tool = { purepy = { entrypoints = [ }\n", "[tool.purepy]\nentrypoints = [\n", "tool.purepy =\n", "[tool.purepy]\nentrypoints = ['unterminated\n", "\x00", "\xff",
	} {
		t.Run(source, func(t *testing.T) {
			_, err := Load(project(t, source))
			var located *Error
			if !errors.As(err, &located) {
				t.Fatalf("malformed TOML must fail with a located error: %v", err)
			}
			span := located.Span
			if span.Start < 0 || span.End < span.Start || span.End > len(source) || span.Line < 1 || span.Column < 1 || span.EndLine < span.Line || span.EndColumn < 1 {
				t.Fatalf("malformed TOML produced out-of-bounds range: %+v", span)
			}
		})
	}
}

func TestConfigurationDuplicateDeclarationPaths(t *testing.T) {
	for _, tc := range []struct{ name, source, symbol, first, second string }{
		{"entrypoint", strings.Replace(validConfig, `["app.main"]`, `["app.main", 'app.main']`, 1), "app.main", `"app.main"`, `'app.main'`},
		{"escaped_entrypoint", strings.Replace(validConfig, `["app.main"]`, `["app.ma\u0069n", 'app.main']`, 1), "app.main", `"app.ma\u0069n"`, `'app.main'`},
		{"resolved_manifest", strings.Replace(validConfig, `["manifests/host.toml"]`, `["manifests/host.toml", './manifests/host.toml']`, 1), "./manifests/host.toml", `"manifests/host.toml"`, `'./manifests/host.toml'`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Load(project(t, tc.source))
			var located *Error
			if !errors.As(err, &located) || located.Symbol != tc.symbol || len(located.Related) != 1 {
				t.Fatalf("duplicate must identify its symbol and first declaration: %+v", err)
			}
			primary, previous := located.Span, located.Related[0]
			if primary.Start <= previous.Start || primary.File != previous.File || tc.source[primary.Start:primary.End] != tc.second || tc.source[previous.Start:previous.End] != tc.first {
				t.Fatalf("duplicate locations lost their literal occurrences: %+v -> %+v", primary, previous)
			}
		})
	}
}
