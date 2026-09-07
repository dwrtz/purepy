package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type conformanceCase struct {
	Name     string            `json:"name"`
	Valid    bool              `json:"valid"`
	Codes    []string          `json:"codes"`
	Source   string            `json:"source"`
	Files    map[string]string `json:"files,omitempty"`
	Manifest string            `json:"manifest,omitempty"`
}

func TestConformance(t *testing.T) {
	data, err := os.ReadFile("../../fixtures/conformance/cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []conformanceCase
	if err = json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, tc := range cases {
		if seen[tc.Name] {
			t.Fatal("duplicate fixture", tc.Name)
		}
		seen[tc.Name] = true
		t.Run(tc.Name, func(t *testing.T) {
			root := t.TempDir()
			write := func(path, body string) {
				path = filepath.Join(root, path)
				if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(body), 0644); err != nil {
					t.Fatal(err)
				}
			}
			manifests := "[]"
			if tc.Manifest != "" {
				write("host.toml", tc.Manifest)
				manifests = `["host.toml"]`
			}
			write("purepy.toml", "[tool.purepy]\nlanguage = \"0.2\"\npython_syntax = \"3.14\"\nsource_root = \"src\"\nentrypoints = []\nmanifests = "+manifests+"\n")
			write("src/app.py", tc.Source)
			for path, body := range tc.Files {
				write("src/"+path, body)
			}
			r := Check(Options{Path: root, NoCache: true, Jobs: 3})
			if r.OK != tc.Valid {
				data, _ := json.MarshalIndent(r.Diagnostics, "", "  ")
				t.Fatalf("valid=%v want %v: %s", r.OK, tc.Valid, data)
			}
			codes := map[string]bool{}
			for _, d := range r.Diagnostics {
				codes[d.Code] = true
				if d.Span.Line < 1 || d.Span.Column < 1 || d.Severity != "error" || !strings.HasPrefix(d.Code, "PP") {
					t.Errorf("invalid diagnostic: %+v", d)
				}
			}
			for _, want := range tc.Codes {
				if !codes[want] {
					t.Errorf("missing %s in %+v", want, r.Diagnostics)
				}
			}
		})
	}
}

func TestReferenceServiceConforms(t *testing.T) {
	r := Check(Options{Path: "../../examples/reference_service", NoCache: true, Jobs: 4})
	if !r.OK {
		t.Fatalf("reference service failed: %+v", r.Diagnostics)
	}
	a, err := r.Capabilities("")
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Functions) != 1 || len(a.Functions[0].ReachableTrustedExternal) < 5 {
		t.Fatalf("incomplete trust report: %+v", a)
	}
	if len(a.Functions[0].TrustedTypes) != 6 {
		t.Fatalf("incomplete host type provenance: %+v", a.Functions[0].TrustedTypes)
	}
}

func BenchmarkReferenceService(b *testing.B) {
	for i := 0; i < b.N; i++ {
		r := Check(Options{Path: "../../examples/reference_service", NoCache: true, Jobs: 1})
		if !r.OK {
			b.Fatal(r.Diagnostics)
		}
	}
}
