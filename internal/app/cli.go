package app

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/dwrtz/purepy/internal/cache"
	"github.com/dwrtz/purepy/internal/check"
	"github.com/dwrtz/purepy/internal/config"
	"github.com/dwrtz/purepy/internal/diag"
)

const usage = `Usage:
  purepy check [path] [--config PATH] [--format text|json] [--no-cache] [--jobs N]
  purepy explain FILE:LINE[:COLUMN] [--config PATH] [--format text|json]
  purepy capabilities [QUALIFIED_FUNCTION] [--config PATH] [--format text|json]
  purepy cache clean [--config PATH]
  purepy version

--timings writes stage timings and cache hits to stderr.
Verification never imports or executes analyzed project code.
`

func Run(args []string, out, errOut io.Writer) (status int) {
	defer func() {
		if recovered := recover(); recovered != nil {
			id := cache.Digest([]byte(Version + "\n" + fmt.Sprint(recovered)))[:16]
			fmt.Fprintf(errOut, "PP099 internal verifier error %s: %v\n", id, recovered)
			status = 2
		}
	}()
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		fmt.Fprint(out, usage)
		return 0
	}
	if args[0] == "version" || args[0] == "--version" {
		fmt.Fprintf(out, "purepy %s (specification %s, language %s, Python syntax %s, intrinsics %s)\n", Version, SpecificationVersion, LanguageVersion, PythonSyntaxVersion, check.IntrinsicVersion)
		return 0
	}
	command := args[0]
	rest := args[1:]
	if command == "cache" {
		if len(rest) == 0 || rest[0] != "clean" {
			fmt.Fprint(errOut, usage)
			return 2
		}
		rest = rest[1:]
	}
	if command != "check" && command != "explain" && command != "capabilities" && command != "cache" {
		fmt.Fprintf(errOut, "unknown command %q\n%s", command, usage)
		return 2
	}
	fs := flag.NewFlagSet(command, flag.ContinueOnError)
	fs.SetOutput(errOut)
	var opts Options
	var format string
	var timings bool
	fs.StringVar(&opts.Config, "config", "", "project configuration path")
	fs.StringVar(&format, "format", "text", "text or json")
	fs.BoolVar(&opts.NoCache, "no-cache", false, "disable cache reads and writes")
	fs.IntVar(&opts.Jobs, "jobs", runtime.GOMAXPROCS(0), "maximum worker count")
	fs.BoolVar(&timings, "timings", false, "write stage timings to stderr")
	ordered, e := orderArgs(rest)
	if e != nil {
		fmt.Fprintln(errOut, e)
		return 2
	}
	if err := fs.Parse(ordered); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	if opts.Jobs < 1 {
		fmt.Fprintln(errOut, "--jobs must be positive")
		return 2
	}
	if format != "text" && format != "json" {
		fmt.Fprintln(errOut, "--format must be text or json")
		return 2
	}
	pos := fs.Args()
	if len(pos) > 1 {
		fmt.Fprintln(errOut, "unexpected positional arguments")
		return 2
	}
	if command == "cache" {
		if len(pos) > 0 {
			opts.Path = pos[0]
		}
		path := opts.Config
		if path == "" {
			path = opts.Path
		}
		cfg, err := config.Load(path)
		if err != nil {
			fmt.Fprintln(errOut, err)
			return 2
		}
		n, err := cache.Clean(filepath.Join(cfg.ProjectRoot, ".purepy-cache"))
		if err != nil {
			fmt.Fprintln(errOut, err)
			return 2
		}
		fmt.Fprintf(out, "Removed %d cached module summaries.\n", n)
		return 0
	}
	if command == "check" && len(pos) == 1 {
		opts.Path = pos[0]
	}
	opts.Timings = timings
	r := Check(opts)
	if timings {
		start := time.Now()
		defer func() {
			r.Timings["wall_render"] = time.Since(start).Seconds()
			fmt.Fprintf(errOut, "timings_schema %d\n", TimingsSchema)
			keys := make([]string, 0, len(r.Timings))
			for k := range r.Timings {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				fmt.Fprintf(errOut, "%s %.9fs\n", k, r.Timings[k])
			}
			fmt.Fprintf(errOut, "cache_hits %d/%d\n", r.CacheHits, r.Files)
		}()
	}
	encode := func(v any) int {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		if err := enc.Encode(v); err != nil {
			fmt.Fprintln(errOut, err)
			return 2
		}
		return 0
	}
	if command == "check" || !r.OK && command != "explain" {
		if format == "json" {
			if status := encode(r); status != 0 {
				return status
			}
		} else {
			diag.Text(out, r.Diagnostics)
			if r.OK {
				fmt.Fprintf(out, "Verified %d modules and %d functions (PurePy 0.1).\n", r.Files, len(r.Functions))
			}
		}
		if r.OK {
			return 0
		}
		for _, d := range r.Diagnostics {
			if d.Code == "PP001" || d.Code == "PP601" {
				return 2
			}
		}
		return 1
	}
	if command == "capabilities" {
		name := ""
		if len(pos) == 1 {
			name = pos[0]
		}
		report, err := r.Capabilities(name)
		if err != nil {
			fmt.Fprintln(errOut, err)
			return 2
		}
		if format == "json" {
			return encode(report)
		}
		for _, a := range report.Functions {
			fmt.Fprintf(out, "%s (%s) -> %s\n", a.Name, a.Classification, a.Returns)
			for _, p := range a.Capabilities {
				fmt.Fprintf(out, "  capability %s: %s [%s]\n", p.Name, p.Type, strings.Join(p.Labels, ", "))
			}
			for _, p := range a.HostReferences {
				fmt.Fprintf(out, "  host reference %s: %s\n", p.Name, p.Type)
			}
			for _, f := range a.TrustedExternal {
				fmt.Fprintf(out, "  direct trusted %s call %s (%s)\n", f.Trust, f.Name, f.Source)
			}
			for _, f := range a.ReachableTrustedExternal {
				fmt.Fprintf(out, "  reachable trusted %s operation %s (%s)\n", f.Trust, f.Name, f.Source)
			}
			for _, typ := range a.TrustedTypes {
				fmt.Fprintf(out, "  trusted %s type %s (%s)\n", typ.Category, typ.Name, typ.Source)
			}
			for _, module := range a.TrustedModules {
				fmt.Fprintf(out, "  trusted module %s import_safe=%t (%s)\n", module.Name, module.ImportSafe, module.Source)
			}
			for _, name := range a.UnusedCapabilities {
				fmt.Fprintf(out, "  unused capability %s\n", name)
			}
		}
		return 0
	}
	if len(pos) != 1 {
		fmt.Fprintln(errOut, "explain requires FILE:LINE[:COLUMN]")
		return 2
	}
	file, line, column, err := location(pos[0])
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 2
	}
	report := r.Explain(file, line, column)
	if format == "json" {
		return encode(report)
	}
	diag.Text(out, report.Diagnostics)
	for _, f := range report.Facts {
		fmt.Fprintf(out, "%s:%d:%d %s: %s (%s) %s\n", f.Span.File, f.Span.Line, f.Span.Column, f.Symbol, f.Type, f.Category, f.Description)
	}
	if len(report.Diagnostics)+len(report.Facts) == 0 {
		fmt.Fprintln(out, "No semantic fact found at this location.")
		if !r.OK {
			diag.Text(out, r.Diagnostics)
			return 1
		}
	}
	return 0
}
func orderArgs(args []string) ([]string, error) {
	flags := []string{}
	pos := []string{}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			pos = append(pos, args[i+1:]...)
			break
		}
		if strings.HasPrefix(a, "-") {
			flags = append(flags, a)
			key := strings.TrimLeft(strings.SplitN(a, "=", 2)[0], "-")
			if !strings.Contains(a, "=") && (key == "config" || key == "format" || key == "jobs") {
				i++
				if i == len(args) {
					return nil, fmt.Errorf("%s requires a value", a)
				}
				flags = append(flags, args[i])
			}
		} else {
			pos = append(pos, a)
		}
	}
	return append(flags, pos...), nil
}
func location(s string) (string, int, int, error) {
	parts := strings.Split(s, ":")
	if len(parts) < 2 {
		return "", 0, 0, fmt.Errorf("expected FILE:LINE[:COLUMN]")
	}
	n, err := strconv.Atoi(parts[len(parts)-1])
	if err != nil || n < 1 {
		return "", 0, 0, fmt.Errorf("location requires positive line/column")
	}
	column := 0
	line := n
	parts = parts[:len(parts)-1]
	if len(parts) > 1 {
		if other, e := strconv.Atoi(parts[len(parts)-1]); e == nil {
			line = other
			column = n
			parts = parts[:len(parts)-1]
		}
	}
	if line < 1 || len(parts) == 0 {
		return "", 0, 0, fmt.Errorf("invalid location")
	}
	return strings.Join(parts, ":"), line, column, nil
}
