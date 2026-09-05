package manifest

import (
	"errors"
	"strings"
	"testing"
)

func TestManifestKeysAreCaseSensitive(t *testing.T) {
	for _, tc := range []struct{ name, from, to, key string }{
		{"schema", "schema =", "sChemA =", "sChemA"},
		{"module", "[[module]]", "[[Module]]", "Module"},
		{"type", "[[type]]", "[[Type]]", "Type"},
		{"function", "[[function]]", "[[Function]]", "Function"},
		{"module_name", `name = "host.database"`, `nAme = "host.database"`, "nAme"},
		{"import_safe", "import_safe =", "import_sAfe =", "import_sAfe"},
		{"type_name", `name = "host.database.Read"`, `Name = "host.database.Read"`, "Name"},
		{"category", "category =", "Category =", "Category"},
		{"labels", "labels =", "Labels =", "Labels"},
		{"immutable", "immutable =", "Immutable =", "Immutable"},
		{"function_name", `name = "host.database.load"`, `Name = "host.database.load"`, "Name"},
		{"kind", "kind =", "Kind =", "Kind"},
		{"trust", "trust =", "Trust =", "Trust"},
		{"returns", "returns =", "Returns =", "Returns"},
		{"parameters", "parameters =", "Parameters =", "Parameters"},
		{"parameter_name", `name = "read"`, `Name = "read"`, "Name"},
		{"parameter_type", `type = "host.database.Read"`, `Type = "host.database.Read"`, "Type"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			text := strings.Replace(validManifest, tc.from, tc.to, 1)
			assertManifestUnknownKey(t, text, tc.key)
		})
	}
}

func assertManifestUnknownKey(t *testing.T, text, key string) {
	t.Helper()
	path := writeManifest(t, text)
	for range 3 {
		set, err := Load([]string{path})
		var located *Error
		if set != nil || !errors.As(err, &located) || !strings.Contains(err.Error(), "unknown fields") {
			t.Fatalf("non-schema key %q accepted or misdiagnosed: %+v, %v", key, set, err)
		}
		assertManifestSpan(t, path, text, located.Span, key, 0)
	}
}

func TestManifestCaseAliasesAcrossTOMLForms(t *testing.T) {
	const prefix = "schema = 1\nmodule = [{name = 'host.ops', import_safe = true}]\n"
	const function = "[[function]]\nname = 'host.ops.run'\nkind = 'sync'\ntrust = 'pure'\nreturns = 'int'\n"
	for _, tc := range []struct{ text, key string }{
		{"sChemA=1\n[[module]]\nnAme='A.A'\nimport_sAfe=true", "sChemA"},
		{"schema=1\nmodule=[{nAme='host.ops',import_safe=true}]", "nAme"},
		{"schema=1\n[[module]]\nname='host.ops'\nName='host.other'\nimport_safe=true", "Name"},
		{"schema=1\n[Module]\nname='host.ops'\nimport_safe=true", "Module"},
		{"schema=1\nModule.name='host.ops'\nModule.import_safe=true", "Module"},
		{prefix + function + "[[function.Parameters]]\nname='x'\ntype='int'", "Parameters"},
		{prefix + function + "[[function.parameters]]\nName='x'\ntype='int'", "Name"},
		{prefix + function + "parameters.Name='x'\nparameters.type='int'", "Name"},
		{prefix + function + "parameters=[{name='x', \"Type\"='int'}]", `"Type"`},
		{prefix + "function=[{name='host.ops.run', kind='sync', trust='pure', returns='int', parameters=[{name='x', Type='int'}]}]", "Type"},
		{"schema=1\nmodule=[{name='host.ops', Import_safe=true, Name='host.other'}]", "Import_safe"},
	} {
		assertManifestUnknownKey(t, tc.text, tc.key)
	}
}

func TestManifestQuotedCanonicalKeysRemainValid(t *testing.T) {
	text := "\"\\u0073chema\"=1\n[[\"module\"]]\n'name'='host.ops'\n\"import_safe\"=true\n"
	if _, err := Load([]string{writeManifest(t, text)}); err != nil {
		t.Fatalf("quoted and escaped exact schema keys must remain valid: %v", err)
	}
}
