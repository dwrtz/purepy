package frontend

import (
	"strings"
	"testing"
)

func TestRejectUnjoinedHeaderNewlines(t *testing.T) {
	for name, source := range map[string]string{
		"function name":       "def\n f() -> int:\n    return 1\n",
		"function parameters": "def f\n() -> int:\n    return 1\n",
		"function return":     "def f()\n -> int:\n    return 1\n",
		"function annotation": "def f() ->\n int:\n    return 1\n",
		"function colon":      "def f() -> int\n:\n    return 1\n",
		"async function":      "async def\n f() -> int:\n    return 1\n",
		"class name":          "class\n R:\n    x: int\n",
		"class colon":         "class R\n:\n    x: int\n",
		"import module":       "from\n purepy import value\n",
		"import keyword":      "from purepy\n import value\n",
		"import name":         "from purepy import\n value\n",
		"decorator":           "@\nvalue\nclass R:\n    x: int\n",
		"comment":             "def f() # a comment does not join lines\n -> int:\n    return 1\n",
		"comment backslash":   "def f() # a comment ending in \\\n -> int:\n    return 1\n",
	} {
		for label, newline := range map[string]string{"LF": "\n", "CRLF": "\r\n"} {
			t.Run(name+"/"+label, func(t *testing.T) {
				requirePythonSyntaxError(t, strings.ReplaceAll(source, "\n", newline))
			})
		}
	}
}

func TestRejectMisalignedConditionalClauses(t *testing.T) {
	for _, clause := range []string{"else", "elif False"} {
		for _, indent := range []string{"     ", "      ", "       "} {
			source := "def f() -> int:\n    if True:\n        return 1\n" + indent + clause + ":\n        return 2\n    return 3\n"
			requirePythonSyntaxError(t, source)
			requirePythonSyntaxError(t, strings.ReplaceAll(source, "\n", "\r\n"))
		}
		// Tab expansion agrees, but Python's alternate indentation count does not.
		requirePythonSyntaxError(t, "def f() -> int:\n\tif True:\n\t\treturn 1\n        "+clause+":\n                return 2\n\treturn 3\n")
	}
}

func TestPreserveJoinedHeadersAndAlignedClauses(t *testing.T) {
	for name, source := range map[string]string{
		"explicit function":   "def \\\nf\\\n(\n    x: int,\n) \\\n-> \\\nint\\\n:\n    return x\n",
		"explicit import":     "from \\\npurepy \\\nimport \\\nvalue\n",
		"implicit import":     "from purepy import (\n    # an import comment\n    value,\n)\n",
		"explicit decorator":  "@\\\nvalue\nclass R:\n    x: int\n",
		"implicit decorator":  "@(\n    value\n)\nclass R:\n    x: int\n",
		"joined class":        "class \\\nR\\\n:\n    x: int\n",
		"implicit annotation": "def f(\n    x: tuple[\n        int, ...\n    ],\n) -> tuple[\n    int, ...\n]:\n    return x\n",
		"conditional clauses": "def f(x: bool) -> int:\n    if (\n        x\n    ):\n        return 1\n    elif (\n        not x\n    ):\n        return 2\n    else:\n        return 3\n",
		"tab clauses":         "def f(x: bool) -> int:\n\tif x:\n\t\treturn 1\n\telif not x:\n\t\treturn 2\n\telse:\n\t\treturn 3\n",
		"multiline literals":  "def f() -> str:\n    return '''first\nsecond'''\n",
		"multiline fstring":   "def f() -> str:\n    return f\"{(1 # expression comment\n)}\"\n",
		"header fstring":      "def f() -> bool:\n    if f\"{(1 # expression comment\n)}\" == \"1\":\n        return True\n    return False\n",
		"header triple quote": "def f() -> int:\n    for x in '''first\nsecond''':\n        return 1\n    return 0\n",
		"decorator comments":  "@value\n# between decorator and class\nclass R:\n    x: int\n",
		"simple suites":       "def f(x: bool) -> int:\n    if x: return 1\n    elif not x: return 2\n    else: return 3\n",
	} {
		for label, newline := range map[string]string{"LF": "\n", "CRLF": "\r\n"} {
			t.Run(name+"/"+label, func(t *testing.T) {
				parseGood(t, strings.ReplaceAll(source, "\n", newline))
			})
		}
	}
}

func requirePythonSyntaxError(t *testing.T, source string) {
	t.Helper()
	_, diagnostics := Parse("layout.py", []byte(source))
	for _, diagnostic := range diagnostics {
		if diagnostic.Code == "PP002" {
			return
		}
	}
	t.Fatalf("expected Python syntax diagnostic, got %+v for %q", diagnostics, source)
}
