package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/dwrtz/purepy/internal/model"
)

const validConfig = `[tool.purepy]
language = "0.1"
python_syntax = "3.14"
source_root = "src"
entrypoints = ["app.main"]
manifests = ["manifests/host.toml"]
`

func project(t *testing.T, config string) string {
	t.Helper()
	root := t.TempDir()
	for _, directory := range []string{"src", "manifests"} {
		if err := os.Mkdir(filepath.Join(root, directory), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for name, content := range map[string]string{"purepy.toml": config, "manifests/host.toml": "schema = 1\n"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestLoadResolvesPathsAndPreservesOrder(t *testing.T) {
	root := project(t, strings.Replace(validConfig, `["manifests/host.toml"]`, `["manifests/host.toml", "second.toml"]`, 1))
	if err := os.WriteFile(filepath.Join(root, "second.toml"), []byte("schema = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fromDirectory, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	fromFile, err := Load(filepath.Join(root, "purepy.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(fromDirectory, fromFile) {
		t.Fatalf("file/directory load differs: %#v, %#v", fromDirectory, fromFile)
	}
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	want := &Config{Path: filepath.Join(canonical, "purepy.toml"), ProjectRoot: canonical, SourceRoot: filepath.Join(canonical, "src"), Language: "0.1", PythonSyntax: "3.14", Entrypoints: []string{"app.main"}, Manifests: []string{filepath.Join(canonical, "manifests/host.toml"), filepath.Join(canonical, "second.toml")}}
	want.FieldSpans, _ = declarationSpans(want.Path, []byte(strings.Replace(validConfig, `["manifests/host.toml"]`, `["manifests/host.toml", "second.toml"]`, 1)))
	want.EntrypointSpans = map[string]model.Span{"app.main": {File: want.Path, Start: 89, End: 99, Line: 5, Column: 16, EndLine: 5, EndColumn: 26}}
	if !reflect.DeepEqual(fromFile, want) {
		t.Fatalf("got %#v, want %#v", fromFile, want)
	}
}

func TestLoadRejectsInvalidConfiguration(t *testing.T) {
	tests := []struct{ name, content, want string }{
		{"unknown", validConfig + "exclude = []\n", "fields"},
		{"duplicate key", validConfig + "language = \"0.1\"\n", "already"},
		{"missing table", "language = \"0.1\"\n", "fields"},
		{"empty", "", "missing [tool.purepy]"},
		{"language", strings.Replace(validConfig, `"0.1"`, `"0.2"`, 1), "language"},
		{"syntax", strings.Replace(validConfig, `"3.14"`, `"3.13"`, 1), "python_syntax"},
		{"source missing", strings.Replace(validConfig, "source_root = \"src\"\n", "", 1), "path is required"},
		{"entrypoints missing", strings.Replace(validConfig, "entrypoints = [\"app.main\"]\n", "", 1), "required"},
		{"manifests missing", strings.Replace(validConfig, "manifests = [\"manifests/host.toml\"]\n", "", 1), "required"},
		{"escape", strings.Replace(validConfig, `"src"`, `"../outside"`, 1), "escapes"},
		{"interpolation", strings.Replace(validConfig, `"src"`, `"${SOURCE}"`, 1), "interpolation"},
		{"home", strings.Replace(validConfig, `"src"`, `"~/src"`, 1), "interpolation"},
		{"invalid entrypoint", strings.Replace(validConfig, `"app.main"`, `"main"`, 1), "entrypoint"},
		{"duplicate entrypoint", strings.Replace(validConfig, `["app.main"]`, `["app.main", "app.main"]`, 1), "duplicate entrypoint"},
		{"duplicate manifest", strings.Replace(validConfig, `["manifests/host.toml"]`, `["manifests/host.toml", "manifests/./host.toml"]`, 1), "duplicate manifest"},
		{"manifest escape", strings.Replace(validConfig, `"manifests/host.toml"`, `"../host.toml"`, 1), "escapes"},
		{"manifest missing", strings.Replace(validConfig, `"manifests/host.toml"`, `"missing.toml"`, 1), "missing.toml"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load(project(t, tt.content))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("got %v, want error containing %q", err, tt.want)
			}
		})
	}
}

func TestLoadAllowsExplicitEmptyLists(t *testing.T) {
	content := strings.ReplaceAll(validConfig, `["app.main"]`, `[]`)
	content = strings.ReplaceAll(content, `["manifests/host.toml"]`, `[]`)
	if _, err := Load(project(t, content)); err != nil {
		t.Fatal(err)
	}
}

func TestUnknownFieldsIdentifyKeysAndPositions(t *testing.T) {
	_, err := Load(project(t, validConfig+"exclude = []\nunsafe = true\n"))
	if err == nil {
		t.Fatal("unknown keys accepted")
	}
	for _, want := range []string{"tool.purepy.exclude", "tool.purepy.unsafe", "line 7, column 1", "line 8, column 1"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("missing %q in error: %v", want, err)
		}
	}
}

func TestLoadRejectsSymlinks(t *testing.T) {
	for _, kind := range []string{"source", "manifest", "config", "directory"} {
		t.Run(kind, func(t *testing.T) {
			root := project(t, validConfig)
			var path string
			switch kind {
			case "source":
				path = filepath.Join(root, "src")
			case "manifest":
				path = filepath.Join(root, "manifests", "host.toml")
			case "config":
				path = filepath.Join(root, "purepy.toml")
			case "directory":
				path = filepath.Join(root, "manifests")
			}
			if err := os.Rename(path, path+"-real"); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(path+"-real", path); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(root); err == nil || !strings.Contains(err.Error(), "symlink") {
				t.Fatalf("expected symlink rejection, got %v", err)
			}
		})
	}
}
