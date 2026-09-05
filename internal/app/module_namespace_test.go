package app

import (
	"fmt"
	"strings"
	"testing"
)

// A trusted child cannot turn a verified ordinary module into a Python package.
// Exercise the complete image so imports of both declarations must resolve.
func TestManifestModuleAncestors(t *testing.T) {
	for _, tc := range []struct {
		name, projectModule, externalModule string
		packages                            []string
		conflict                            bool
	}{
		{"direct_child", "host", "host.ops", nil, true},
		{"deep_child", "host", "host.ops.more", nil, true},
		{"nested_module", "pkg.host", "pkg.host.ops", []string{"pkg"}, true},
		{"distinct_prefix", "host", "hosting.ops", nil, false},
		{"disjoint_modules", "app", "host.ops", nil, false},
		{"verified_package_ancestor", "app", "host.ops", []string{"host"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files := map[string]string{
				"src/" + strings.ReplaceAll(tc.projectModule, ".", "/") + ".py": "def local() -> int:\n    return 1\n",
				"src/main.py": fmt.Sprintf("from %s import local\nfrom %s import read\ndef run() -> int:\n    return local() + read()\n", tc.projectModule, tc.externalModule),
				"host.toml":   fmt.Sprintf("schema = 1\n[[module]]\nname = %q\nimport_safe = true\n[[function]]\nname = %q\nkind = 'sync'\ntrust = 'pure'\nparameters = []\nreturns = 'int'\n", tc.externalModule, tc.externalModule+".read"),
			}
			for _, pkg := range tc.packages {
				files["src/"+strings.ReplaceAll(pkg, ".", "/")+"/__init__.py"] = ""
			}
			root := cliProject(t, files, []string{"main.run"}, []string{"host.toml"})
			var want string
			for i, opts := range []Options{{Path: root, Jobs: 1}, {Path: root, Jobs: 4}, {Path: root, Jobs: 1, NoCache: true}} {
				r := Check(opts)
				if r.OK == tc.conflict {
					t.Fatalf("conflict=%t, report=%s", tc.conflict, cliJSON(t, r))
				}
				if tc.conflict {
					if len(r.Diagnostics) != 1 {
						t.Fatalf("expected one namespace conflict: %s", cliJSON(t, r))
					}
					d := r.Diagnostics[0]
					if d.Code != "PP601" || d.Symbol != tc.externalModule || !strings.Contains(d.Message, tc.projectModule) || !strings.HasSuffix(d.Span.File, "/host.toml") || len(d.Related) != 1 || d.Related[0] != r.Program.Modules[tc.projectModule].Tree.Span {
						t.Fatalf("conflict must identify both module declarations: %+v", d)
					}
				}
				got := cliJSON(t, r)
				if i == 0 {
					want = got
				} else if got != want {
					t.Fatalf("namespace result depends on cache or workers:\n%s\n%s", want, got)
				}
				if i == 1 && r.CacheHits != r.Files {
					t.Fatalf("warm check missed cached modules: %d/%d", r.CacheHits, r.Files)
				}
			}
		})
	}
}
