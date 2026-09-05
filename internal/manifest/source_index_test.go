package manifest

import (
	"fmt"
	"strings"
	"testing"
)

func TestSourceIndexPreservesUnicodeCoordinates(t *testing.T) {
	data := []byte(strings.Repeat("a", 255) + "β😀\n" + "x\r\n" + strings.Repeat("界", 1000) + "\n")
	index := indexSource("host.toml", data)
	if len(index.checkpoints) > 1+len(data)/256 {
		t.Fatalf("source index grew beyond sparse checkpoint budget: %d for %d bytes", len(index.checkpoints), len(data))
	}
	// Include interior UTF-8 byte offsets as well as token boundaries and EOF;
	// clamping and Unicode columns must agree with the existing span contract.
	for start := -1; start <= len(data)+1; start++ {
		end := min(len(data)+1, start+317)
		if got, want := index.span(start, end), sourceSpan("host.toml", data, start, end); got != want {
			t.Fatalf("indexed span disagrees at %d:%d:\n%+v\n%+v", start, end, got, want)
		}
	}
}

func inlineParameterSource(count int, prefix string) string {
	var source strings.Builder
	source.WriteString("schema = 1\nmodule = [{name = 'host.ops', import_safe = true}]\nfunction = [{name = 'host.ops.run', kind = 'sync', trust = 'pure', parameters = [")
	for i := 0; i < count; i++ {
		if i > 0 {
			source.WriteByte(',')
		}
		fmt.Fprintf(&source, "{name = '%s%d', type = 'int'}", prefix, i)
	}
	source.WriteString("], returns = 'int'}]\n")
	return source.String()
}

func TestManifestLargeInlineParameterSourceProjection(t *testing.T) {
	const count = 10000
	for _, prefix := range []string{"p", "β"} {
		t.Run(prefix, func(t *testing.T) {
			text := inlineParameterSource(count, prefix)
			path := writeManifest(t, text)
			set, err := Load([]string{path})
			if err != nil {
				t.Fatal(err)
			}
			f := set.Functions[0]
			if len(f.Parameters) != count {
				t.Fatalf("large inline list lost parameters: %d", len(f.Parameters))
			}
			for _, position := range []int{0, count / 2, count - 1} {
				name := fmt.Sprintf("%s%d", prefix, position)
				if f.Parameters[position].Name != name {
					t.Fatalf("large inline list changed parameter order at %d", position)
				}
				assertManifestSpan(t, path, text, f.Parameters[position].Span, "'"+name+"'", 0)
			}
			start := strings.LastIndex(text, "'int'")
			if got, want := f.ReturnSpan, sourceSpan(path, []byte(text), start, start+len("'int'")); got != want {
				t.Fatalf("return location after the large inline list is incorrect:\n%+v\n%+v", got, want)
			}
		})
	}
}

func BenchmarkManifestInlineSourceProjection(b *testing.B) {
	for _, prefix := range []string{"p", "β"} {
		for _, count := range []int{100, 1000, 10000} {
			b.Run(fmt.Sprintf("%s/%d", prefix, count), func(b *testing.B) {
				data := []byte(inlineParameterSource(count, prefix))
				b.SetBytes(int64(len(data)))
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					source := declarationSources("host.toml", data)
					if got := len(source.field("function").item(0).field("parameters").items); got != count {
						b.Fatalf("source projection lost parameters: %d", got)
					}
				}
			})
		}
	}
}
