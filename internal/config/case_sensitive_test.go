package config

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadRejectsCaseInsensitiveConfigAliases(t *testing.T) {
	for _, tc := range []struct{ name, source, selected, symbol string }{
		{"root table", strings.Replace(validConfig, "[tool.purepy]", "[Tool.PurePy]", 1), "Tool", "Tool"},
		{"settings table", strings.Replace(validConfig, "[tool.purepy]", "[tool.PurePy]", 1), "PurePy", "tool.PurePy"},
		{"language field", strings.Replace(validConfig, "language =", "Language =", 1), "Language", "tool.purepy.Language"},
		{"syntax field", strings.Replace(validConfig, "python_syntax =", "Python_Syntax =", 1), "Python_Syntax", "tool.purepy.Python_Syntax"},
		{"source field", strings.Replace(validConfig, "source_root =", "Source_Root =", 1), "Source_Root", "tool.purepy.Source_Root"},
		{"entrypoints field", strings.Replace(validConfig, "entrypoints =", "Entrypoints =", 1), "Entrypoints", "tool.purepy.Entrypoints"},
		{"manifests field", strings.Replace(validConfig, "manifests =", "Manifests =", 1), "Manifests", "tool.purepy.Manifests"},
		{"overwrite language", strings.Replace(validConfig, `language = "0.2"`, "language = \"0.2\"\nLanguage = \"0.2\"", 1), "Language", "tool.purepy.Language"},
		{"overwrite source root", validConfig + "Source_Root = \"manifests\"\n", "Source_Root", "tool.purepy.Source_Root"},
		{"dotted", "tool.purepy.Language = \"0.2\"\ntool.purepy.python_syntax = \"3.14\"\ntool.purepy.source_root = \"src\"\ntool.purepy.entrypoints = []\ntool.purepy.manifests = []\n", "Language", "tool.purepy.Language"},
		{"inline", "tool = { purepy = { Language = \"0.2\", python_syntax = \"3.14\", source_root = \"src\", entrypoints = [], manifests = [] } }", "Language", "tool.purepy.Language"},
		{"quoted escaped", strings.Replace(validConfig, "language =", `"Langu\u0061ge" =`, 1), `"Langu\u0061ge"`, "tool.purepy.Language"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := project(t, tc.source)
			cfg, err := Load(root)
			var located *Error
			if !errors.As(err, &located) || !strings.Contains(err.Error(), "unknown fields") {
				t.Fatalf("case-sensitive config alias accepted or misidentified: config=%+v error=%v", cfg, err)
			}
			span := located.Span
			start := strings.Index(tc.source, tc.selected)
			want := sourceSpan(span.File, []byte(tc.source), start, start+len(tc.selected))
			if span != want || located.Symbol != tc.symbol || filepath.Base(span.File) != "purepy.toml" {
				t.Fatalf("alias location: got %+v symbol=%q; want %+v symbol=%q", span, located.Symbol, want, tc.symbol)
			}
		})
	}
}

func TestLoadAcceptsQuotedCanonicalConfigKeys(t *testing.T) {
	for _, source := range []string{
		strings.ReplaceAll(strings.Replace(validConfig, "[tool.purepy]", `["to\u006fl".'purepy']`, 1), "language =", `"langu\u0061ge" =`),
		`"tool".'purepy'."language" = "0.2"
tool.purepy.python_syntax = "3.14"
tool.purepy.source_root = "src"
tool.purepy.entrypoints = []
tool.purepy.manifests = []
`,
		`"tool" = { 'purepy' = { "langu\u0061ge" = "0.2", python_syntax = "3.14", source_root = "src", entrypoints = [], manifests = [] } }`,
	} {
		if _, err := Load(project(t, source)); err != nil {
			t.Fatalf("canonical quoted/escaped config keys rejected: %v", err)
		}
	}
}
