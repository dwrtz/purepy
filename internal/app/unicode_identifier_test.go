package app

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestPython314UnicodeIdentifiersAcrossBoundaries(t *testing.T) {
	// Unicode 16.0 added TJE; Python 3.14 admits it in identifiers, while
	// Go 1.24's Unicode 15.0 character predicates do not.
	const name = "\u1c89"
	// Keep language/configuration/manifest coverage independent of the host
	// filesystem's support for newly assigned characters in filenames.
	root := cliProject(t, map[string]string{
		"src/main.py": "from host." + name + " import " + name + ", Value" + name + ", echo" + name + "\n\ndef run" + name + "(x: int) -> int:\n    return " + name + "(" + name + "=x)\n\ndef forward" + name + "(x: Value" + name + ") -> Value" + name + ":\n    return echo" + name + "(" + name + "=x)\n",
		"host.toml": "schema = 1\n[[module]]\nname = 'host." + name + "'\nimport_safe = true\n" +
			"[[type]]\nname = 'host." + name + ".Value" + name + "'\ncategory = 'value'\nimmutable = true\n" +
			"[[function]]\nname = 'host." + name + "." + name + "'\nkind = 'sync'\ntrust = 'pure'\nparameters = [{name = '" + name + "', type = 'int'}]\nreturns = 'int'\n" +
			"[[function]]\nname = 'host." + name + ".echo" + name + "'\nkind = 'sync'\ntrust = 'pure'\nparameters = [{name = '" + name + "', type = 'host." + name + ".Value" + name + "'}]\nreturns = 'host." + name + ".Value" + name + "'\n",
	}, []string{"main.run" + name, "main.forward" + name}, []string{"host.toml"})
	baseline := Check(Options{Path: root, NoCache: true, Jobs: 1})
	if !baseline.OK || len(baseline.Functions) != 2 || baseline.Functions[0].Name != "main.forward"+name || baseline.Functions[0].Returns.Name != "host."+name+".Value"+name || baseline.Functions[1].Name != "main.run"+name {
		t.Fatal(cliJSON(t, baseline))
	}
	for _, jobs := range []int{1, 4} {
		got := Check(Options{Path: root, Jobs: jobs})
		if cliJSON(t, got) != cliJSON(t, baseline) {
			t.Fatalf("Unicode identifier result changed with caching/workers: %s", cliJSON(t, got))
		}
	}
}

func TestPython314UnicodeModuleFilename(t *testing.T) {
	const name = "\u1c89"
	root := cliProject(t, map[string]string{
		"src/main.py": "from " + name + " import run\ndef forward(x: int) -> int:\n    return run(x)\n",
	}, []string{name + ".run", "main.forward"}, nil)
	path := filepath.Join(root, "src", name+".py")
	err := os.WriteFile(path, []byte("def run(x: int) -> int:\n    return x\n"), 0o644)
	if errors.Is(err, syscall.EILSEQ) {
		// macOS 14's filesystem can reject U+1C89 before the verifier runs.
		// Only this real-filename case is conditional; the Unicode 16.0
		// source, annotation, manifest, and entrypoint checks above always run.
		t.Skipf("filesystem cannot create the Unicode 16.0 module filename U+1C89: %v", err)
	}
	if err != nil {
		t.Fatal(err)
	}
	baseline := Check(Options{Path: root, NoCache: true, Jobs: 1})
	if !baseline.OK || len(baseline.Functions) != 2 || baseline.Functions[1].Name != name+".run" {
		t.Fatal(cliJSON(t, baseline))
	}
	for _, jobs := range []int{1, 4} {
		got := Check(Options{Path: root, Jobs: jobs})
		if cliJSON(t, got) != cliJSON(t, baseline) {
			t.Fatalf("Unicode module discovery/linking changed with caching/workers: %s", cliJSON(t, got))
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
