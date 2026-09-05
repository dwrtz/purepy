package app

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// BenchmarkCheckWarm isolates the repeated verification path for CPU and heap
// profiles. Process startup and JSON output are measured by tools/benchmark.py.
// Every iteration still relinks and rechecks the cached syntax against the image.
func BenchmarkCheckWarm(b *testing.B) {
	for _, count := range []int{100, 1000} {
		b.Run(fmt.Sprintf("modules_%d", count), func(b *testing.B) {
			root := b.TempDir()
			source := filepath.Join(root, "src")
			if err := os.Mkdir(source, 0o755); err != nil {
				b.Fatal(err)
			}
			config := "[tool.purepy]\nlanguage = '0.1'\npython_syntax = '3.14'\nsource_root = 'src'\nentrypoints = []\nmanifests = []\n"
			if err := os.WriteFile(filepath.Join(root, "purepy.toml"), []byte(config), 0o644); err != nil {
				b.Fatal(err)
			}
			for i := 0; i < count; i++ {
				var body string
				for j := 0; j < 10; j++ {
					body += fmt.Sprintf("def operation%d(value: int) -> int:\n    return value + %d\n", j, j)
				}
				if err := os.WriteFile(filepath.Join(source, fmt.Sprintf("m%05d.py", i)), []byte(body), 0o644); err != nil {
					b.Fatal(err)
				}
			}
			opts := Options{Path: root, Jobs: 4}
			if first := Check(opts); !first.OK {
				b.Fatal(first.Diagnostics)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				report := Check(opts)
				if !report.OK || report.CacheHits != count || len(report.Functions) != count*10 {
					b.Fatalf("warm check changed outcome or cache hits: ok=%v hits=%d functions=%d", report.OK, report.CacheHits, len(report.Functions))
				}
			}
		})
	}
}
