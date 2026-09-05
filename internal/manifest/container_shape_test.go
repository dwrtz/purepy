package manifest

import (
	"errors"
	"strings"
	"testing"
)

func TestRejectSingletonDeclarationContainers(t *testing.T) {
	const module = "module = [{name = 'host.ops', import_safe = true}]\n"
	for _, declaration := range []struct{ kind, before, fields string }{
		{"module", "", "name = 'host.ops'\nimport_safe = true\n"},
		{"type", module, "name = 'host.ops.Row'\ncategory = 'value'\nimmutable = true\n"},
		{"function", module, "name = 'host.ops.run'\nkind = 'sync'\ntrust = 'pure'\nreturns = 'int'\nparameters = []\n"},
	} {
		for _, form := range []string{"table", "dotted", "inline"} {
			t.Run(declaration.kind+"/"+form, func(t *testing.T) {
				text := "schema = 1\n" + declaration.before
				switch form {
				case "table":
					text += "[" + declaration.kind + "]\n" + declaration.fields
				case "dotted":
					for _, field := range strings.Split(strings.TrimSpace(declaration.fields), "\n") {
						text += declaration.kind + "." + field + "\n"
					}
				case "inline":
					text += declaration.kind + " = {" + strings.ReplaceAll(strings.TrimSpace(declaration.fields), "\n", ", ") + "}\n"
				}
				path := writeManifest(t, text)
				set, err := Load([]string{path})
				var located *Error
				if set != nil || !errors.As(err, &located) {
					t.Fatalf("singleton declaration escaped array schema: %+v, %v", set, err)
				}
				if form == "inline" {
					// The decoder already rejects inline-object-to-slice conversion;
					// it is plain and dotted tables that need the shape guard.
					if !strings.Contains(err.Error(), "cannot decode TOML inline table") || located.Span.File != path || text[located.Span.Start:located.Span.End] != "{" {
						t.Fatalf("inline declaration rejection lost source context: %v, %+v", err, located)
					}
					return
				}
				if !strings.Contains(err.Error(), declaration.kind+" must be an array of tables") || located.Symbol != declaration.kind {
					t.Fatalf("singleton declaration escaped array schema: %+v, %v", set, err)
				}
				span := located.Span
				if span.File != path || span.Start == 0 || text[span.Start:span.End] != declaration.kind {
					t.Fatalf("container diagnostic must select its actual key: %+v", span)
				}
			})
		}
	}
}

func TestRejectSingletonParameterContainers(t *testing.T) {
	const module = "schema = 1\nmodule = [{name = 'host.ops', import_safe = true}]\n"
	const function = "[[function]]\nname = 'host.ops.run'\nkind = 'sync'\ntrust = 'pure'\nreturns = 'int'\n"
	for _, parameters := range []string{
		"parameters = {name = 'x', type = 'int'}\n",
		"[function.parameters]\nname = 'x'\ntype = 'int'\n",
		"parameters.name = 'x'\nparameters.type = 'int'\n",
	} {
		text := module + function + parameters
		path := writeManifest(t, text)
		set, err := Load([]string{path})
		var located *Error
		if set != nil || !errors.As(err, &located) {
			t.Fatalf("singleton parameter table escaped array schema: %+v, %v", set, err)
		}
		if strings.HasPrefix(parameters, "parameters =") {
			if !strings.Contains(err.Error(), "cannot decode TOML inline table") || located.Span.File != path || text[located.Span.Start:located.Span.End] != "{" {
				t.Fatalf("inline parameter rejection lost source context: %v, %+v", err, located)
			}
			continue
		}
		if !strings.Contains(err.Error(), "parameters must be an array of tables") || located.Symbol != "host.ops.run" {
			t.Fatalf("singleton parameter table escaped array schema: %+v, %v", set, err)
		}
		if text[located.Span.Start:located.Span.End] != "parameters" || located.Span.File != path || len(located.Related) != 1 {
			t.Fatalf("parameter diagnostic lost key/function context: %+v", located)
		}
	}
	text := module + "function = [{name = 'host.ops.run', kind = 'sync', trust = 'pure', returns = 'int', parameters = {name = 'x', type = 'int'}}]\n"
	path := writeManifest(t, text)
	_, err := Load([]string{path})
	var located *Error
	if !errors.As(err, &located) || !strings.Contains(err.Error(), "cannot decode TOML inline table") || located.Span.File != path || text[located.Span.Start:located.Span.End] != "{" {
		t.Fatalf("nested inline parameter table must identify its source: %v, %+v", err, located)
	}
}

func TestManifestArrayShapeAllowsExplicitEmptyArrays(t *testing.T) {
	for _, text := range []string{
		"schema = 1\nmodule = []\ntype = []\nfunction = []\n",
		"schema = 1\nmodule = [{name = 'host.ops', import_safe = true}]\nfunction = [{name = 'host.ops.run', kind = 'sync', trust = 'pure', returns = 'int', parameters = []}]\n",
	} {
		if _, err := Load([]string{writeManifest(t, text)}); err != nil {
			t.Fatalf("explicit empty arrays must remain valid: %v", err)
		}
	}
}

func TestManifestDecoderRejectsScalarCoercions(t *testing.T) {
	for _, schema := range []string{"true", "1.0", "'1'"} {
		if _, err := Load([]string{writeManifest(t, "schema = "+schema+"\n")}); err == nil {
			t.Fatalf("schema coerced from non-integer %s", schema)
		}
	}
	for _, labels := range []string{"'test.read'", "1", "true", "{name = 'test.read'}"} {
		text := "schema = 1\nmodule = [{name = 'host.ops', import_safe = true}]\ntype = [{name = 'host.ops.Read', category = 'capability', labels = " + labels + "}]\n"
		if _, err := Load([]string{writeManifest(t, text)}); err == nil {
			t.Fatalf("capability labels coerced from non-array %s", labels)
		}
	}
}
