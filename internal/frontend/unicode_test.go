package frontend

import (
	"bytes"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestBytesParsingRetainsOriginalSource(t *testing.T) {
	for _, source := range []string{
		`x = b'#\N'`,
		`x = B'\x41\N{}'`,
		`x = f"{len(b'\N{}')}"`,
		`x = "b'\N{LATIN CAPITAL LETTER A}'"`,
		`x = '\xab'`,
	} {
		t.Run(source, func(t *testing.T) {
			input := []byte(source + "\n")
			original := bytes.Clone(input)
			tree, diagnostics := Parse("original.py", input)
			if len(diagnostics) != 0 {
				t.Fatal(diagnostics)
			}
			if !bytes.Equal(input, original) || tree.Text != string(original) {
				t.Fatal("grammar normalization changed original source or lowered text")
			}
			value := tree.Items("body")[0].Get("value")
			if value == nil || value.Text != strings.TrimPrefix(source, "x = ") {
				t.Fatalf("lost original literal text: %+v", value)
			}
		})
	}
}

func TestNamedUnicodeEscapes(t *testing.T) {
	for _, literal := range []string{
		`'\N{LATIN CAPITAL LETTER A}'`,
		`'\N{latin capital letter a}\N{GREEK SMALL LETTER LAMDA}'`,
		`'\N{NULL}\N{NUL}\N{LINE FEED}\N{BOM}'`,
		`'\N{HANGUL SYLLABLE GA}\N{hangul syllable hih}'`,
		`'\N{CJK UNIFIED IDEOGRAPH-4E00}\N{cjk unified ideograph-4e00}'`,
		`u'\N{GRINNING FACE}'`,
		`U'\N{SUN}'`,
		`'''\N{LATIN CAPITAL LETTER A}` + "\n" + `\N{LATIN CAPITAL LETTER B}'''`,
		`'\N{LATIN CAPITAL LETTER A}' '\N{LATIN CAPITAL LETTER B}'`,
		`f'\N{LATIN CAPITAL LETTER A}{1}'`,
		`f'{1}\N{LEFT CURLY BRACKET}\N{RIGHT CURLY BRACKET}'`,
		`f'{{\N{LATIN CAPITAL LETTER A}}}'`,
		`f"{'\N{LATIN CAPITAL LETTER A}'}"`,
		`rf"{'\N{LATIN CAPITAL LETTER A}'}"`,
		`r'\N{UNKNOWN}\N{}\N'`,
		`R'\N{UNKNOWN}'`,
		`b'\N{UNKNOWN}\N{}\N'`,
		`br'\N{UNKNOWN}'`,
		`rb'\N{UNKNOWN}'`,
		`fr'\N{{UNKNOWN}}'`,
		`'\\N{UNKNOWN}'`,
		`'\\\N{LATIN CAPITAL LETTER A}'`,
		`'\N{REPLACEMENT CHARACTER}\x41\u0042\U00000043'`,
	} {
		t.Run(literal, func(t *testing.T) {
			parseGood(t, "x = "+literal+"\n")
		})
	}
}

func TestInvalidNamedUnicodeEscapesRetainRanges(t *testing.T) {
	for _, tc := range []struct{ literal, selected string }{
		{`'\N'`, `\N`},
		{`'\Nmissing'`, `\N`},
		{`'\N{'`, `\N{`},
		{`'\N{LATIN CAPITAL LETTER A'`, `\N{LATIN CAPITAL LETTER A`},
		{`'\N{}'`, `\N{}`},
		{`'\N{UNKNOWN}'`, `\N{UNKNOWN}`},
		{`'\N{ LATIN CAPITAL LETTER A}'`, `\N{ LATIN CAPITAL LETTER A}`},
		{`'\N{LATIN CAPITAL LETTER A }'`, `\N{LATIN CAPITAL LETTER A }`},
		{`'\N{LATIN  CAPITAL LETTER A}'`, `\N{LATIN  CAPITAL LETTER A}`},
		{`'\N{KEYCAP DIGIT ONE}'`, `\N{KEYCAP DIGIT ONE}`},
		{`'\N{CJK UNIFIED IDEOGRAPH-04E00}'`, `\N{CJK UNIFIED IDEOGRAPH-04E00}`},
		{`'\N{CJK UNIFIED IDEOGRAPH-110000}'`, `\N{CJK UNIFIED IDEOGRAPH-110000}`},
		{`'\N{LATIN CAPITAL LETTER Å}'`, `\N{LATIN CAPITAL LETTER Å}`},
		{`'\N{LATIN CAPITAL LETTER A}\N{UNKNOWN}'`, `\N{UNKNOWN}`},
		{`'\N{LATIN CAPITAL LETTER A}\xGG'`, `\xGG`},
		{`'\\\N{UNKNOWN}'`, `\N{UNKNOWN}`},
		{`f'\N{UNKNOWN}{1}'`, `\N{UNKNOWN}`},
		{`rf"{'\N{UNKNOWN}'}"`, `\N{UNKNOWN}`},
		{"'''line one\nλ \\N{UNKNOWN}'''", `\N{UNKNOWN}`},
	} {
		t.Run(tc.literal, func(t *testing.T) {
			source := "# Unicode λ before the error\nx = 'é' " + tc.literal + "\n"
			_, diagnostics := Parse("unicode.py", []byte(source))
			if len(diagnostics) != 1 {
				t.Fatalf("want one lexical diagnostic, got %+v", diagnostics)
			}
			d := diagnostics[0]
			start := strings.Index(source, tc.selected)
			end := start + len(tc.selected)
			line := 1 + strings.Count(source[:start], "\n")
			column := 1 + utf8.RuneCountInString(source[strings.LastIndexByte(source[:start], '\n')+1:start])
			endLine := 1 + strings.Count(source[:end], "\n")
			endColumn := 1 + utf8.RuneCountInString(source[strings.LastIndexByte(source[:end], '\n')+1:end])
			if d.Code != "PP002" || d.Span.File != "unicode.py" || d.Span.Start != start || d.Span.End != end || d.Span.Line != line || d.Span.Column != column || d.Span.EndLine != endLine || d.Span.EndColumn != endColumn {
				t.Fatalf("incorrect lexical error range: %+v; want bytes [%d,%d), %d:%d-%d:%d", d, start, end, line, column, endLine, endColumn)
			}
		})
	}
}
