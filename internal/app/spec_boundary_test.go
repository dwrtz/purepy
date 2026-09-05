package app

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/dwrtz/purepy/internal/model"
)

const boundaryManifest = cliManifest + `
[[type]]
name = "host.ops.Write"
category = "capability"
labels = ["network.write", "database.read"]
[[type]]
name = "host.ops.Row"
category = "value"
immutable = true
[[type]]
name = "host.ops.Token"
category = "value"
immutable = true
[[type]]
name = "host.ops.Unreachable"
category = "value"
immutable = true
[[function]]
name = "host.ops.decode"
kind = "sync"
trust = "pure"
parameters = [{name = "value", type = "int"}]
returns = "host.ops.Row"
[[function]]
name = "host.ops.encode"
kind = "sync"
trust = "pure"
parameters = [{name = "value", type = "host.ops.Row"}]
returns = "str"
[[function]]
name = "host.ops.hidden"
kind = "sync"
trust = "pure"
parameters = []
returns = "host.ops.Unreachable"
`

const boundarySource = `from purepy import value
from host.ops import Read, Write, Connection, Row, Token, load, decode, encode, hidden

@value
class Wrapped:
    row: Row

def format_row(number: int) -> str:
    wrapped = Wrapped(decode(number))
    token: Token | None = None
    return encode(wrapped.row)

async def recurse(read: Read, connection: Connection, count: int) -> str:
    if count > 0:
        return await recurse(read, connection, count - 1)
    return format_row(await load(read, connection))

async def run(read: Read, write: Write, connection: Connection, count: int) -> str:
    return await recurse(read, connection, count)

def pure(number: int) -> str:
    return format_row(number)

def unreachable() -> None:
    ignored = hidden()
`

func boundaryNames(functions []model.Function) []string {
	names := []string{}
	for _, f := range functions {
		names = append(names, f.Name)
	}
	return names
}

func TestBoundaryAuthorityAndEntrypointReports(t *testing.T) {
	root := cliProject(t, map[string]string{"manifest.toml": boundaryManifest, "src/main.py": boundarySource}, []string{"main.run", "main.pure"}, []string{"manifest.toml"})
	r := Check(Options{Path: root, NoCache: true, Jobs: 4})
	if !r.OK {
		t.Fatal(cliJSON(t, r))
	}
	report, err := r.Capabilities("")
	if err != nil || len(report.Functions) != 2 {
		t.Fatalf("entrypoint report failed: %+v, %v", report, err)
	}
	if report.Functions[0].Name != "main.pure" || report.Functions[1].Name != "main.run" {
		t.Fatalf("entrypoints not sorted deterministically: %+v", report.Functions)
	}
	pure, effectful := report.Functions[0], report.Functions[1]
	if pure.Kind != "sync" || pure.Classification != "pure_sync" || len(pure.Capabilities) != 0 || len(pure.HostReferences) != 0 || len(pure.Parameters) != 1 || !pure.Parameters[0].Type.Equal(model.Int) || !pure.Returns.Equal(model.Str) {
		t.Fatalf("pure entrypoint signature changed: %+v", pure)
	}
	if effectful.Kind != "async" || effectful.Classification != "effectful_async" || len(effectful.Capabilities) != 2 || len(effectful.HostReferences) != 1 || len(effectful.Parameters) != 4 || !effectful.Returns.Equal(model.Str) {
		t.Fatalf("effectful entrypoint signature changed: %+v", effectful)
	}
	if !reflect.DeepEqual(effectful.Capabilities[0].Labels, []string{"database.read"}) || !reflect.DeepEqual(effectful.Capabilities[1].Labels, []string{"network.write", "database.read"}) || effectful.HostReferences[0].Type.Name != "host.ops.Connection" {
		t.Fatalf("exact labels or host reference type lost: %+v", effectful)
	}
	if !reflect.DeepEqual(effectful.UnusedCapabilities, []string{"write"}) {
		t.Fatalf("unused capability must remain visible and nonfatal: %+v", effectful.UnusedCapabilities)
	}
	// Section 17.7 applies to every function, including helpers that are not
	// configured entrypoints. Summaries reflect only their own signatures.
	for _, function := range r.Functions {
		authority, err := r.Capabilities(function.Name)
		if err != nil || len(authority.Functions) != 1 {
			t.Fatalf("missing helper report for %s: %+v, %v", function.Name, authority, err)
		}
		want, got := map[string]bool{}, map[string]bool{}
		for _, p := range function.Parameters {
			if p.Type.Kind == "capability" {
				for _, label := range p.Labels {
					want[label] = true
				}
			}
		}
		for _, p := range authority.Functions[0].Capabilities {
			for _, label := range p.Labels {
				got[label] = true
			}
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("signature authority for %s = %v, want %v", function.Name, got, want)
		}
	}
}

func TestBoundaryTransitiveTrustReports(t *testing.T) {
	root := cliProject(t, map[string]string{"manifest.toml": boundaryManifest, "src/main.py": boundarySource}, []string{"main.run", "main.pure"}, []string{"manifest.toml"})
	var baseline []byte
	for _, opts := range []Options{{Path: root, Jobs: 1}, {Path: root, Jobs: 4}, {Path: root, Jobs: 2, NoCache: true}} {
		r := Check(opts)
		if !r.OK {
			t.Fatal(cliJSON(t, r))
		}
		report, err := r.Capabilities("main.run")
		if err != nil {
			t.Fatal(err)
		}
		a := report.Functions[0]
		if len(a.TrustedExternal) != 0 || !reflect.DeepEqual(boundaryNames(a.ReachableTrustedExternal), []string{"host.ops.decode", "host.ops.encode", "host.ops.load"}) {
			t.Fatalf("trust traversal must follow helpers/recursion and exclude unreachable declarations: %+v", a)
		}
		if len(a.DirectCalls) != 1 || a.DirectCalls[0].Callee != "main.recurse" || a.DirectCalls[0].Kind != "await" || !reflect.DeepEqual(a.DirectCalls[0].CapabilityArguments, []string{"read"}) || !reflect.DeepEqual(a.DirectCalls[0].HostRefArguments, []string{"connection"}) {
			t.Fatalf("direct authority evidence lost: %+v", a.DirectCalls)
		}
		typeNames := []string{}
		for _, typ := range a.TrustedTypes {
			typeNames = append(typeNames, typ.Name)
			if !strings.HasSuffix(typ.Source, "/manifest.toml") {
				t.Fatalf("trusted type lacks manifest provenance: %+v", typ)
			}
		}
		if !reflect.DeepEqual(typeNames, []string{"host.ops.Connection", "host.ops.Read", "host.ops.Row", "host.ops.Token", "host.ops.Write"}) {
			t.Fatalf("signature, record field, expression, or local annotation contract missing: %v", typeNames)
		}
		for _, function := range a.ReachableTrustedExternal {
			if function.Origin != "manifest" || function.Trust != "host" && function.Trust != "pure" || !strings.HasSuffix(function.Source, "/manifest.toml") {
				t.Fatalf("trusted external lacks declared contract/provenance: %+v", function)
			}
		}
		data, err := json.Marshal(report)
		if err != nil {
			t.Fatal(err)
		}
		if baseline != nil && !reflect.DeepEqual(data, baseline) {
			t.Fatalf("trust report changed with workers/cache:\n%s\n%s", baseline, data)
		}
		baseline = data
	}
}

func TestBoundaryEntrypointRejections(t *testing.T) {
	for _, name := range []string{"main.absent", "main.Wrapped", "main.decode", "host.ops.load"} {
		t.Run(name, func(t *testing.T) {
			root := cliProject(t, map[string]string{"manifest.toml": boundaryManifest, "src/main.py": boundarySource}, []string{name}, []string{"manifest.toml"})
			r := Check(Options{Path: root, NoCache: true})
			if r.OK || !strings.Contains(cliJSON(t, r), "PP701") {
				t.Fatalf("non-verified or non-function entrypoint accepted: %s", cliJSON(t, r))
			}
			if _, err := r.Capabilities(""); err == nil {
				t.Fatal("failed verification produced an authority claim")
			}
		})
	}
}

func TestBoundaryReportsRejectUnknownOrExternalFunction(t *testing.T) {
	root := cliProject(t, map[string]string{"manifest.toml": boundaryManifest, "src/main.py": boundarySource}, nil, []string{"manifest.toml"})
	r := Check(Options{Path: root, NoCache: true})
	if !r.OK {
		t.Fatal(cliJSON(t, r))
	}
	for _, name := range []string{"main.absent", "main.Wrapped", "host.ops.load"} {
		if _, err := r.Capabilities(name); err == nil {
			t.Fatalf("reported non-verified function %s", name)
		}
	}
	if report, err := r.Capabilities(""); err != nil || len(report.Functions) != 0 {
		t.Fatalf("explicit empty entrypoint list changed meaning: %+v, %v", report, err)
	}
}

func TestBoundaryManifestOrderAndConflict(t *testing.T) {
	module := "schema = 1\n[[module]]\nname = \"host.ops\"\nimport_safe = true\n"
	declarations := "schema = 1\n" + strings.SplitN(cliManifest, "[[type]]", 2)[1]
	declarations = strings.Replace(declarations, "schema = 1\n", "schema = 1\n[[type]]", 1)
	for _, reversed := range []bool{false, true} {
		t.Run(map[bool]string{false: "declared_order", true: "reversed_order"}[reversed], func(t *testing.T) {
			paths := []string{"module.toml", "declarations.toml"}
			if reversed {
				paths[0], paths[1] = paths[1], paths[0]
			}
			root := cliProject(t, map[string]string{"module.toml": module, "declarations.toml": declarations, "src/main.py": "from host.ops import Read, Connection, load\nasync def run(cap: Read, conn: Connection) -> int:\n    return await load(cap, conn)\n"}, []string{"main.run"}, paths)
			r := Check(Options{Path: root, NoCache: true})
			if !r.OK {
				t.Fatal(cliJSON(t, r))
			}
			if !strings.HasSuffix(r.Config.Manifests[0], paths[0]) || !strings.HasSuffix(r.Config.Manifests[1], paths[1]) {
				t.Fatalf("configuration order changed: %v", r.Config.Manifests)
			}
			authority, err := r.Capabilities("")
			if err != nil || len(authority.Functions) != 1 || len(authority.Functions[0].TrustedModules) != 1 {
				t.Fatalf("module trust report missing: %+v, %v", authority, err)
			}
			module := authority.Functions[0].TrustedModules[0]
			if module.Name != "host.ops" || !module.ImportSafe || !strings.HasSuffix(module.Source, "/module.toml") {
				t.Fatalf("module provenance confused with declaration provenance: %+v", module)
			}
			cliWrite(t, root, "declarations.toml", cliManifest)
			if r := Check(Options{Path: root, NoCache: true}); r.OK || !strings.Contains(cliJSON(t, r), "duplicate declaration") {
				t.Fatalf("later declaration silently replaced first: %s", cliJSON(t, r))
			}
		})
	}
}

func TestBoundaryTransitiveImportSafetyReports(t *testing.T) {
	modules := `schema = 1
[[module]]
name = "host"
import_safe = true
[[module]]
name = "host.math"
import_safe = true
[[module]]
name = "host.idle"
import_safe = true
[[module]]
name = "host.unreachable"
import_safe = true
`
	declarations := `schema = 1
[[function]]
name = "host.math.one"
kind = "sync"
trust = "pure"
parameters = []
returns = "int"
[[function]]
name = "host.idle.noop"
kind = "sync"
trust = "pure"
parameters = []
returns = "None"
[[function]]
name = "host.unreachable.noop"
kind = "sync"
trust = "pure"
parameters = []
returns = "None"
`
	root := cliProject(t, map[string]string{
		"modules.toml":        modules,
		"declarations.toml":   declarations,
		"src/helper.py":       "from host.math import one\nfrom host.idle import noop\ndef integer() -> int:\n    return one()\n",
		"src/main.py":         "from helper import integer\ndef run() -> int:\n    return integer()\n",
		"src/unreferenced.py": "from host.unreachable import noop\ndef ignored() -> None:\n    noop()\n",
	}, []string{"main.run"}, []string{"modules.toml", "declarations.toml"})
	r := Check(Options{Path: root, NoCache: true})
	if !r.OK {
		t.Fatal(cliJSON(t, r))
	}
	report, err := r.Capabilities("")
	if err != nil {
		t.Fatal(err)
	}
	a := report.Functions[0]
	names := []string{}
	for _, module := range a.TrustedModules {
		names = append(names, module.Name)
		if !module.ImportSafe || !strings.HasSuffix(module.Source, "/modules.toml") {
			t.Fatalf("incorrect module contract: %+v", module)
		}
	}
	if !reflect.DeepEqual(names, []string{"host", "host.idle", "host.math"}) || !reflect.DeepEqual(boundaryNames(a.ReachableTrustedExternal), []string{"host.math.one"}) {
		t.Fatalf("module imports and callable reachability conflated: %+v", a)
	}
	status, out, errOut := cliRun("capabilities", "--config", root+"/purepy.toml", "--no-cache")
	if status != 0 || errOut != "" {
		t.Fatalf("text trust report failed: %d, %q, %q", status, out, errOut)
	}
	for _, module := range a.TrustedModules {
		want := "trusted module " + module.Name + " import_safe=true (" + module.Source + ")"
		if !strings.Contains(out, want) {
			t.Fatalf("text report omitted module contract %q: %s", want, out)
		}
	}
	if strings.Contains(out, "host.unreachable") || strings.Contains(out, "operation host.idle.noop") {
		t.Fatalf("text report included unreachable declarations: %s", out)
	}
}
