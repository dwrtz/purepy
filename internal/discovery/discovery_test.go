package discovery

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func sources(t *testing.T, names ...string) string {
	t.Helper()
	root := t.TempDir()
	for _, name := range names {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("# never executed\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestDiscoverDeterministicMapping(t *testing.T) {
	root := sources(t, "z.py", "app/sub/β.py", "app/__init__.py", "app/sub/__init__.py", "data.txt", "app/helpers.py")
	files, err := Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	want := []File{
		{Path: filepath.Join(root, "app/__init__.py"), RelativePath: "app/__init__.py", Module: "app", IsPackage: true},
		{Path: filepath.Join(root, "app/helpers.py"), RelativePath: "app/helpers.py", Module: "app.helpers"},
		{Path: filepath.Join(root, "app/sub/__init__.py"), RelativePath: "app/sub/__init__.py", Module: "app.sub", IsPackage: true},
		{Path: filepath.Join(root, "app/sub/β.py"), RelativePath: "app/sub/β.py", Module: "app.sub.β"},
		{Path: filepath.Join(root, "z.py"), RelativePath: "z.py", Module: "z"},
	}
	if !reflect.DeepEqual(files, want) {
		t.Fatalf("got %#v, want %#v", files, want)
	}
	for i := 0; i < 3; i++ {
		other, err := Discover(root)
		if err != nil || !reflect.DeepEqual(files, other) {
			t.Fatalf("nondeterministic discovery: %#v, %v", other, err)
		}
	}
}

func TestDiscoverRejectsUnresolvableModules(t *testing.T) {
	tests := []struct {
		name, want string
		files      []string
	}{
		{"namespace", "namespace", []string{"app/main.py"}},
		{"nested namespace", "namespace", []string{"app/__init__.py", "app/sub/main.py"}},
		{"ambiguous", "ambiguous module", []string{"app.py", "app/__init__.py"}},
		{"invalid file", "module name", []string{"a.b.py"}},
		{"keyword file", "module name", []string{"while.py"}},
		{"invalid package", "package name", []string{"bad-name/__init__.py"}},
		{"root package", "no module name", []string{"__init__.py"}},
		{"normalization", "noncanonical", []string{"ﬁle.py"}},
		{"build dir is not ignored", "namespace", []string{"build/generated.py"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Discover(sources(t, tt.files...)); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected %q error, got %v", tt.want, err)
			}
		})
	}
}

func TestDiscoverRejectsSymlinks(t *testing.T) {
	for _, name := range []string{"alias.py", "alias", "data.txt"} {
		t.Run(name, func(t *testing.T) {
			root := sources(t, "main.py")
			if err := os.Symlink(filepath.Join(root, "main.py"), filepath.Join(root, name)); err != nil {
				t.Fatal(err)
			}
			if _, err := Discover(root); err == nil || !strings.Contains(err.Error(), "symlink") {
				t.Fatalf("expected symlink rejection, got %v", err)
			}
		})
	}
}

func TestCanonicalIdentifiers(t *testing.T) {
	for _, name := range []string{"a", "_", "café", "β2", "match", "case", "type"} {
		if !ValidIdentifier(name) {
			t.Errorf("rejected valid identifier %q", name)
		}
	}
	for _, name := range []string{"", "9a", "a-b", "a.b", "class", "None", "ﬁle", "cafe\u0301", "a\x00"} {
		if ValidIdentifier(name) {
			t.Errorf("accepted invalid/noncanonical identifier %q", name)
		}
	}
}
