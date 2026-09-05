package app

import (
	"bytes"
	"errors"
	"math"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func requireTimings(t *testing.T, measured map[string]float64) {
	t.Helper()
	if len(measured) != len(timingStages) {
		t.Fatalf("incomplete timing categories: %+v", measured)
	}
	for _, name := range timingStages {
		value, present := measured[name]
		if !present || value < 0 || math.IsNaN(value) || math.IsInf(value, 0) {
			t.Fatalf("missing or invalid %s timing: %v", name, value)
		}
	}
}

func timingFooter(t *testing.T, stderr string) map[string]float64 {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(stderr), "\n")
	if len(lines) != len(timingStages)+2 || lines[0] != "timings_schema 2" || !strings.HasPrefix(lines[len(lines)-1], "cache_hits ") {
		t.Fatalf("invalid timing footer: %q", stderr)
	}
	measured := make(map[string]float64)
	previous := ""
	for _, line := range lines[1 : len(lines)-1] {
		parts := strings.Fields(line)
		if len(parts) != 2 || !strings.HasSuffix(parts[1], "s") || parts[0] <= previous {
			t.Fatalf("invalid or unordered duration: %q", line)
		}
		previous = parts[0]
		number := strings.TrimSuffix(parts[1], "s")
		if dot := strings.IndexByte(number, '.'); dot < 0 || len(number)-dot-1 != 9 {
			t.Fatalf("duration must preserve nanosecond precision: %q", line)
		}
		value, err := strconv.ParseFloat(number, 64)
		if err != nil {
			t.Fatal(err)
		}
		measured[parts[0]] = value
	}
	requireTimings(t, measured)
	return measured
}

func TestCheckTimingsAcrossCacheAndWorkers(t *testing.T) {
	root := cliProject(t, map[string]string{
		"src/helper.py": "def twice(x: int) -> int:\n    return x * 2\n",
		"src/main.py":   "from helper import twice\ndef run(x: int) -> int:\n    return twice(x)\n",
	}, []string{"main.run"}, nil)
	baseline := Check(Options{Path: root, Jobs: 1, NoCache: true})
	if !baseline.OK || len(baseline.Timings) != 0 {
		t.Fatalf("ordinary verification unexpectedly measured work: %+v", baseline)
	}
	for _, test := range []struct {
		name    string
		options Options
		hits    int
	}{
		{"cold", Options{Path: root, Jobs: 1, Timings: true}, 0},
		{"warm parallel", Options{Path: root, Jobs: 8, Timings: true}, 2},
		{"warm serial", Options{Path: root, Jobs: 1, Timings: true}, 2},
		{"uncached parallel", Options{Path: root, Jobs: 8, NoCache: true, Timings: true}, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			start := time.Now()
			report := Check(test.options)
			elapsed := time.Since(start).Seconds()
			requireContextReportEqual(t, baseline, report)
			requireTimings(t, report.Timings)
			var wall float64
			for name, duration := range report.Timings {
				if strings.HasPrefix(name, "wall_") {
					wall += duration
				}
			}
			if wall > elapsed || report.Timings["wall_render"] != 0 {
				t.Fatalf("Check wall stages overlap or include CLI rendering: wall=%v elapsed=%v", wall, elapsed)
			}
			if report.CacheHits != test.hits {
				t.Fatalf("unexpected cache hits: %d", report.CacheHits)
			}
			if report.Timings["work_read"] <= 0 || report.Timings["work_hash"] <= 0 {
				t.Fatal("source read/hash work was not measured")
			}
			if test.hits > 0 {
				if report.Timings["work_parse"] != 0 || report.Timings["work_lower"] != 0 || report.Timings["work_cache_write"] != 0 || report.Timings["work_cache_read"] <= 0 {
					t.Fatalf("warm cache reports work it skipped: %+v", report.Timings)
				}
			} else if report.Timings["work_parse"] <= 0 || report.Timings["work_lower"] <= 0 {
				t.Fatal("cold parser/lowering work was not measured")
			}
			if test.options.NoCache && (report.Timings["work_cache_read"] != 0 || report.Timings["work_cache_write"] != 0) {
				t.Fatal("disabled cache reports cache work")
			}
		})
	}
}

func TestCheckTimingsEarlyFailures(t *testing.T) {
	invalidSource := cliProject(t, map[string]string{"src/main.py": "def f(:\n"}, nil, nil)
	invalidManifest := cliProject(t, nil, nil, []string{"host.toml"})
	cliWrite(t, invalidManifest, "host.toml", "schema = 999\n")
	for _, test := range []struct {
		name, path string
		skipped    []string
	}{
		{"configuration", filepath.Join(t.TempDir(), "absent"), []string{"wall_discovery", "wall_manifests", "wall_image_inputs", "wall_frontend", "wall_link", "wall_check", "work_parse", "work_lower"}},
		{"manifest", invalidManifest, []string{"wall_image_inputs", "wall_frontend", "wall_link", "wall_check", "work_parse", "work_lower"}},
		{"syntax", invalidSource, []string{"wall_link", "wall_check", "work_lower"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			report := Check(Options{Path: test.path, Jobs: 4, NoCache: true, Timings: true})
			if report.OK || len(report.Diagnostics) == 0 {
				t.Fatal("fixture did not reject")
			}
			requireTimings(t, report.Timings)
			for _, name := range test.skipped {
				if report.Timings[name] != 0 {
					t.Fatalf("skipped stage %s has a duration", name)
				}
			}
			if report.Timings["wall_configuration"] <= 0 || report.Timings["wall_report"] <= 0 {
				t.Fatal("early failure lost configuration/report work")
			}
		})
	}
}

func TestCLITimingsPreserveOutput(t *testing.T) {
	valid := cliProject(t, map[string]string{"src/main.py": "def run(x: int) -> int:\n    return x + 1\n"}, []string{"main.run"}, nil)
	invalid := cliProject(t, map[string]string{"src/main.py": "def run() -> int:\n    return True\n"}, nil, nil)
	for _, args := range [][]string{
		{"check", valid},
		{"check", invalid},
		{"check", filepath.Join(valid, "absent")},
		{"explain", filepath.Join(valid, "src/main.py") + ":2:12", "--config", valid},
		{"capabilities", "main.run", "--config", valid},
	} {
		for _, format := range []string{"text", "json"} {
			t.Run(strings.Join(args, " ")+" "+format, func(t *testing.T) {
				commandArgs := append(args[:len(args):len(args)], "--format", format, "--no-cache")
				wantStatus, wantOut, wantErr := cliRun(commandArgs...)
				status, output, stderr := cliRun(append(commandArgs, "--timings")...)
				if status != wantStatus || output != wantOut || wantErr != "" {
					t.Fatalf("timings changed public output: %d vs %d, %q vs %q, %q", status, wantStatus, output, wantOut, wantErr)
				}
				measured := timingFooter(t, stderr)
				if measured["wall_render"] <= 0 {
					t.Fatal("CLI output was not timed")
				}
			})
		}
	}
}

type timedOutput struct {
	bytes.Buffer
	elapsed time.Duration
	errOut  *bytes.Buffer
	early   bool
	fail    bool
}

func (w *timedOutput) Write(p []byte) (int, error) {
	w.early = w.early || w.errOut.Len() != 0
	start := time.Now()
	time.Sleep(time.Millisecond)
	w.elapsed += time.Since(start)
	if w.fail {
		return 0, errors.New("test output failure")
	}
	return w.Buffer.Write(p)
}

func TestCLITimingsIncludeOutputAndFollowEncodingFailure(t *testing.T) {
	root := cliProject(t, map[string]string{"src/main.py": "def run() -> None:\n    pass\n"}, []string{"main.run"}, nil)
	for _, fail := range []bool{false, true} {
		var stderr bytes.Buffer
		out := &timedOutput{errOut: &stderr, fail: fail}
		status := Run([]string{"check", root, "--no-cache", "--format", "json", "--timings"}, out, &stderr)
		footer := stderr.String()
		if fail {
			if status != 2 || !strings.HasPrefix(footer, "test output failure\n") {
				t.Fatalf("encoding failure changed: %d %q", status, footer)
			}
			footer = strings.TrimPrefix(footer, "test output failure\n")
		} else if status != 0 {
			t.Fatalf("unexpected status: %d %q", status, footer)
		}
		measured := timingFooter(t, footer)
		if out.early || measured["wall_render"] < out.elapsed.Seconds() {
			t.Fatalf("timing footer preceded output or omitted writer time: early=%t render=%v writer=%v", out.early, measured["wall_render"], out.elapsed.Seconds())
		}
	}
}
