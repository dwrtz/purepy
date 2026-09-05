package config

import (
	"strings"
	"testing"
)

func TestBoundaryRequiredVersions(t *testing.T) {
	for _, tc := range []struct{ name, declaration, want string }{
		{"language_missing", "language = \"0.1\"\n", "language"},
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
