package app

import (
	"fmt"
	"strings"
	"testing"
)

func TestSpecCompletionEntrypointCategories(t *testing.T) {
	// All eight combinations of the three admitted parameter categories occur
	// in both synchronous and asynchronous configured entrypoints.
	for _, kind := range []string{"def", "async def"} {
		for categories := 0; categories < 8; categories++ {
			t.Run(fmt.Sprintf("%s/categories=%d", kind, categories), func(t *testing.T) {
				params := []string{}
				for i, p := range []string{"number: int", "capability: Read", "connection: Connection"} {
					if categories&(1<<i) != 0 {
						params = append(params, p)
					}
				}
				source := "from host.ops import Read, Connection\n" + kind + " run(" + strings.Join(params, ", ") + ") -> int:\n    return 1\n"
				root := cliProject(t, map[string]string{"manifest.toml": cliManifest, "src/main.py": source}, []string{"main.run"}, []string{"manifest.toml"})
				for _, opts := range []Options{{Path: root, Jobs: 1}, {Path: root, Jobs: 4}, {Path: root, NoCache: true}} {
					r := Check(opts)
					if !r.OK {
						t.Fatalf("valid entrypoint rejected: %s", cliJSON(t, r))
					}
					report, err := r.Capabilities("")
					if err != nil || len(report.Functions) != 1 || report.Functions[0].Name != "main.run" {
						t.Fatalf("entrypoint was not resolved: %+v %v", report, err)
					}
					wantCapabilities, wantReferences := 0, 0
					if categories&2 != 0 {
						wantCapabilities = 1
					}
					if categories&4 != 0 {
						wantReferences = 1
					}
					if len(report.Functions[0].Capabilities) != wantCapabilities || len(report.Functions[0].HostReferences) != wantReferences {
						t.Fatalf("entrypoint category summary differs from signature: %+v", report.Functions[0])
					}
				}
			})
		}
	}
	for _, kind := range []string{"def", "async def"} {
		for _, category := range []string{"Read", "Connection"} {
			t.Run(kind+"/forbidden_return/"+category, func(t *testing.T) {
				source := "from host.ops import Read, Connection\n" + kind + " run(item: " + category + ") -> " + category + ":\n    return item\n"
				root := cliProject(t, map[string]string{"manifest.toml": cliManifest, "src/main.py": source}, []string{"main.run"}, []string{"manifest.toml"})
				r := Check(Options{Path: root, NoCache: true})
				if r.OK || !strings.Contains(cliJSON(t, r), "PP201") {
					t.Fatalf("forbidden entrypoint result category was accepted: %s", cliJSON(t, r))
				}
				if _, err := r.Capabilities(""); err == nil {
					t.Fatal("invalid entrypoint produced an authority report")
				}
			})
		}
	}
}
