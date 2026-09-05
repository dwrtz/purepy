package app

import "testing"

func TestPython314UnicodeIdentifiersAcrossBoundaries(t *testing.T) {
	// Unicode 16.0 added TJE; Python 3.14 admits it in identifiers, while
	// Go 1.24's Unicode 15.0 character predicates do not.
	const name = "\u1c89"
	root := cliProject(t, map[string]string{
		"src/" + name + ".py": "from host." + name + " import " + name + "\n\ndef run(x: int) -> int:\n    return " + name + "(x)\n",
		"host.toml":           "schema = 1\n[[module]]\nname = 'host." + name + "'\nimport_safe = true\n[[function]]\nname = 'host." + name + "." + name + "'\nkind = 'sync'\ntrust = 'pure'\nparameters = [{name = '" + name + "', type = 'int'}]\nreturns = 'int'\n",
	}, []string{name + ".run"}, []string{"host.toml"})
	baseline := Check(Options{Path: root, NoCache: true, Jobs: 1})
	if !baseline.OK {
		t.Fatal(cliJSON(t, baseline))
	}
	for _, jobs := range []int{1, 4} {
		got := Check(Options{Path: root, Jobs: jobs})
		if cliJSON(t, got) != cliJSON(t, baseline) {
			t.Fatalf("Unicode identifier result changed with caching/workers: %s", cliJSON(t, got))
		}
	}
}

func TestPython314UnicodeSourceNormalization(t *testing.T) {
	// Unicode 16.0 added canonical composition for Kirat Rai vowel signs.
	// Python binds this decomposed spelling to the configured canonical name.
	const canonical = "x\U00016D6A"
	const decomposed = "x\U00016D63\U00016D67\U00016D67"
	root := cliProject(t, map[string]string{
		"src/main.py": "def " + decomposed + "(n: int) -> int:\n    return n\n",
	}, []string{"main." + canonical}, nil)
	baseline := Check(Options{Path: root, NoCache: true, Jobs: 1})
	if !baseline.OK || len(baseline.Functions) != 1 || baseline.Functions[0].Name != "main."+canonical {
		t.Fatal(cliJSON(t, baseline))
	}
	for _, jobs := range []int{1, 4} {
		got := Check(Options{Path: root, Jobs: jobs})
		if cliJSON(t, got) != cliJSON(t, baseline) {
			t.Fatalf("Unicode normalization changed with caching/workers: %s", cliJSON(t, got))
		}
	}
}
