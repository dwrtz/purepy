package config

import (
	"strings"
	"testing"
)

func TestBoundaryRequiredVersions(t *testing.T) {
	for _, tc := range []struct{ name, declaration, want string }{
		{"python_syntax_missing", "python_syntax = \"3.14\"\n", "python_syntax"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Load(project(t, strings.Replace(validConfig, tc.declaration, "", 1)))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("missing required version accepted or misidentified: %v", err)
			}
		})
	}
}

func TestStandardLanguage(t *testing.T) {
	for _, version := range []string{"", "0.2", "0.1", "future"} {
		text := strings.Replace(validConfig, "language = \"0.2\"\n", "", 1)
		if version != "" {
			text += "language = \"" + version + "\"\n"
		}
		cfg, err := Load(project(t, text))
		if version == "" || version == "0.2" {
			if err != nil || cfg.Language != "0.2" {
				t.Fatalf("standard %q: %+v %v", version, cfg, err)
			}
		} else if err == nil {
			t.Fatalf("unsupported language %q accepted", version)
		}
	}
}
