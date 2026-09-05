package app

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestAuthorityAuditRejectsUnsafeImportAncestor(t *testing.T) {
	for _, tc := range []struct {
		name, ancestors string
		leafSafe        bool
		unsafe          string
	}{
		{"unsafe_parent", "[[module]]\nname = 'host'\nimport_safe = false\n", true, "host"},
		{"unsafe_grandparent", "[[module]]\nname = 'host'\nimport_safe = false\n[[module]]\nname = 'host.io'\nimport_safe = true\n", true, "host"},
		{"unsafe_immediate_parent", "[[module]]\nname = 'host'\nimport_safe = true\n[[module]]\nname = 'host.io'\nimport_safe = false\n", true, "host.io"},
		{"unsafe_leaf", "[[module]]\nname = 'host'\nimport_safe = true\n", false, "host.io.ops"},
		{"safe_parents", "[[module]]\nname = 'host'\nimport_safe = true\n[[module]]\nname = 'host.io'\nimport_safe = true\n", true, ""},
		{"implicit_parents", "", true, ""},
		{"unrelated_unsafe_module", "[[module]]\nname = 'hosting'\nimport_safe = false\n", true, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := cliProject(t, map[string]string{
				"manifest.toml": "schema = 1\n" + tc.ancestors + fmt.Sprintf(`
[[module]]
name = "host.io.ops"
import_safe = %t
[[function]]
name = "host.io.ops.one"
kind = "sync"
trust = "pure"
parameters = []
returns = "int"
`, tc.leafSafe),
				"src/main.py": "from host.io.ops import one\ndef run() -> int:\n    return one()\n",
			}, []string{"main.run"}, []string{"manifest.toml"})
			for _, opts := range []Options{{Path: root, Jobs: 1}, {Path: root, Jobs: 4}, {Path: root, NoCache: true}} {
				r := Check(opts)
				if tc.unsafe == "" {
					if !r.OK {
						t.Fatalf("safe or implicit ancestors rejected: %s", cliJSON(t, r))
					}
					continue
				}
				if r.Program == nil || r.Program.ExternalModuleDeclarations[tc.unsafe].ImportSafe {
					t.Fatal("unsafe ancestor was not retained by manifest linking")
				}
				if r.OK {
					authority, _ := r.Capabilities("")
					t.Fatalf("import accepted explicitly unsafe ancestor %q: %s; report: %+v", tc.unsafe, cliJSON(t, r), authority)
				}
				found := false
				for _, d := range r.Diagnostics {
					if d.Code == "PP604" && strings.Contains(d.Message, tc.unsafe) && strings.HasSuffix(d.Span.File, "/src/main.py") {
						found = true
						if len(d.Related) == 0 || d.Related[0] != r.Program.ExternalModuleDeclarations[tc.unsafe].Span {
							t.Fatalf("unsafe module declaration not identified: %+v", d)
						}
					}
				}
				if !found {
					t.Fatalf("missing located PP604 for %q: %s", tc.unsafe, cliJSON(t, r))
				}
				if _, err := r.Capabilities(""); err == nil {
					t.Fatal("rejected import produced an authority claim")
				}
			}
		})
	}
}

func TestAuthorityAuditManifestSafetyChangeInvalidatesCachedCaller(t *testing.T) {
	const manifest = `schema = 1
[[module]]
name = "host"
import_safe = true
[[module]]
name = "host.ops"
import_safe = true
[[function]]
name = "host.ops.noop"
kind = "sync"
trust = "pure"
parameters = []
returns = "None"
`
	root := cliProject(t, map[string]string{
		"manifest.toml": manifest,
		"src/main.py":   "from host.ops import noop\ndef run() -> None:\n    pass\n",
	}, []string{"main.run"}, []string{"manifest.toml"})
	if r := Check(Options{Path: root}); !r.OK {
		t.Fatal(cliJSON(t, r))
	}
	// The import still initializes host even though noop is never called.
	cliWrite(t, root, "manifest.toml", strings.Replace(manifest, "import_safe = true", "import_safe = false", 1))
	for _, warm := range []bool{false, true} {
		r := Check(Options{Path: root})
		if r.OK || !strings.Contains(cliJSON(t, r), "PP604") || !warm && r.CacheHits != 0 || warm && r.CacheHits != r.Files {
			t.Fatalf("unused import bypassed the changed ancestor contract or cache invalidation: warm=%t hits=%d/%d %s", warm, r.CacheHits, r.Files, cliJSON(t, r))
		}
	}
}

func TestAuthorityAuditForwardingProvenance(t *testing.T) {
	const manifest = cliManifest + `
[[type]]
name = "host.ops.Unused"
category = "capability"
labels = ["database.read", "audit.unused"]
[[function]]
name = "host.ops.combine"
kind = "async"
trust = "host"
parameters = [{name = "left", type = "host.ops.Read"}, {name = "right", type = "host.ops.Read"}, {name = "target", type = "host.ops.Connection"}]
returns = "int"
`
	root := cliProject(t, map[string]string{
		"manifest.toml": manifest,
		"src/helper.py": "from host.ops import Read, Connection, combine\nasync def forward(first: Read, second: Read, connection: Connection) -> int:\n    return await combine(right=first, target=connection, left=second)\n",
		"src/main.py":   "from host.ops import Read, Unused, Connection, load\nfrom helper import forward\nasync def run(first: Read, second: Read, unused: Unused, connection: Connection) -> int:\n    if False:\n        return await load(first, connection)\n    return await forward(connection=connection, second=first, first=second)\n",
	}, []string{"main.run"}, []string{"manifest.toml"})
	var baseline []byte
	for _, opts := range []Options{{Path: root, Jobs: 1}, {Path: root, Jobs: 4}, {Path: root, NoCache: true}} {
		r := Check(opts)
		if !r.OK {
			t.Fatal(cliJSON(t, r))
		}
		report, err := r.Capabilities("")
		if err != nil || len(report.Functions) != 1 {
			t.Fatalf("missing entrypoint authority: %+v, %v", report, err)
		}
		a := report.Functions[0]
		if !reflect.DeepEqual(a.UnusedCapabilities, []string{"unused"}) || len(a.Capabilities) != 3 || !reflect.DeepEqual(a.Capabilities[2].Labels, []string{"database.read", "audit.unused"}) {
			t.Fatalf("same reporting label conflated distinct parameter use: %+v", a)
		}
		if !reflect.DeepEqual(boundaryNames(a.ReachableTrustedExternal), []string{"host.ops.combine", "host.ops.load"}) {
			t.Fatalf("nested or statically dead call omitted from trust closure: %+v", a.ReachableTrustedExternal)
		}
		if len(a.DirectCalls) != 2 || a.DirectCalls[1].Callee != "helper.forward" || !reflect.DeepEqual(a.DirectCalls[1].CapabilityArguments, []string{"second", "first"}) || !reflect.DeepEqual(a.DirectCalls[1].HostRefArguments, []string{"connection"}) {
			t.Fatalf("caller parameter provenance does not follow callee binding order: %+v", a.DirectCalls)
		}
		helper, err := r.Capabilities("helper.forward")
		if err != nil || len(helper.Functions) != 1 || len(helper.Functions[0].DirectCalls) != 1 {
			t.Fatalf("helper authority missing: %+v, %v", helper, err)
		}
		edge := helper.Functions[0].DirectCalls[0]
		if edge.Callee != "host.ops.combine" || edge.Kind != "await" || !reflect.DeepEqual(edge.CapabilityArguments, []string{"second", "first"}) || !reflect.DeepEqual(edge.HostRefArguments, []string{"connection"}) {
			t.Fatalf("host call parameter provenance lost: %+v", edge)
		}
		encoded, err := json.Marshal(report)
		if err != nil {
			t.Fatal(err)
		}
		if baseline != nil && !reflect.DeepEqual(encoded, baseline) {
			t.Fatalf("authority evidence differs by cache/worker mode:\n%s\n%s", baseline, encoded)
		}
		baseline = encoded
	}
}
