// Package app assembles the hermetic verification pipeline and public reports.
package app

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"sync"
	"time"

	"github.com/dwrtz/purepy/internal/cache"
	"github.com/dwrtz/purepy/internal/check"
	"github.com/dwrtz/purepy/internal/config"
	"github.com/dwrtz/purepy/internal/diag"
	"github.com/dwrtz/purepy/internal/discovery"
	"github.com/dwrtz/purepy/internal/frontend"
	"github.com/dwrtz/purepy/internal/manifest"
	"github.com/dwrtz/purepy/internal/model"
)

const Version = "0.1.0-dev.1"
const SpecificationVersion = "0.3-draft"
const LanguageVersion = "0.1"
const PythonSyntaxVersion = "3.14"
const JSONSchema = 1

type Options struct {
	Path, Config string
	Jobs         int
	NoCache      bool
	Timings      bool
}
type Report struct {
	Schema        int                `json:"schema"`
	Version       string             `json:"verifier_version"`
	Specification string             `json:"specification_version"`
	Language      string             `json:"language"`
	PythonSyntax  string             `json:"python_syntax"`
	OK            bool               `json:"ok"`
	Files         int                `json:"files"`
	Diagnostics   []diag.Diagnostic  `json:"diagnostics"`
	Functions     []FunctionReport   `json:"functions"`
	Program       *check.Program     `json:"-"`
	Config        *config.Config     `json:"-"`
	Calls         []model.CallEdge   `json:"-"`
	Facts         []model.Fact       `json:"-"`
	Timings       map[string]float64 `json:"-"`
	CacheHits     int                `json:"-"`
}
type FunctionReport struct {
	Name           string            `json:"name"`
	Kind           string            `json:"kind"`
	Classification string            `json:"classification"`
	Parameters     []model.Parameter `json:"parameters"`
	Returns        model.Type        `json:"returns"`
}

func Check(opts Options) *Report {
	r := &Report{Schema: JSONSchema, Version: Version, Specification: SpecificationVersion, Language: LanguageVersion, PythonSyntax: PythonSyntaxVersion, Diagnostics: []diag.Diagnostic{}, Functions: []FunctionReport{}, Calls: []model.CallEdge{}, Facts: []model.Fact{}, Timings: map[string]float64{}}
	// Wall stages partition the pipeline, including early failure paths. Work
	// measurements below are worker elapsed sums and can overlap one another.
	var stage string
	var mark time.Time
	begin := func(next string) {
		if !opts.Timings {
			return
		}
		now := time.Now()
		if stage != "" {
			r.Timings[stage] += now.Sub(mark).Seconds()
		}
		stage, mark = next, now
	}
	if opts.Timings {
		for _, name := range timingStages {
			r.Timings[name] = 0
		}
	}
	begin("wall_configuration")
	defer func() { begin("") }()
	path := opts.Path
	if opts.Config != "" {
		path = opts.Config
	}
	if path == "" {
		path = "."
	}
	cfg, err := config.Load(path)
	begin("wall_report")
	if err != nil {
		var located *config.Error
		if errors.As(err, &located) {
			d := diag.New("PP001", err.Error(), located.Span)
			d.WithSymbol(located.Symbol).WithRelated(located.Related...)
			r.Diagnostics = append(r.Diagnostics, d)
		} else {
			r.failure("PP001", err.Error(), path)
		}
		return r
	}
	r.Config = cfg
	begin("wall_discovery")
	files, err := discovery.Discover(cfg.SourceRoot)
	begin("wall_report")
	if err != nil {
		d := diag.New("PP101", err.Error(), cfg.FieldSpans["source_root"])
		d.WithSymbol("tool.purepy.source_root")
		r.Diagnostics = append(r.Diagnostics, d)
		return r
	}
	r.Files = len(files)
	moduleNames := make(map[string]string, len(files))
	for _, f := range files {
		moduleNames[f.Path] = f.Module
	}
	begin("wall_manifests")
	ext, err := manifest.Load(cfg.Manifests)
	begin("wall_report")
	if err != nil {
		var located *manifest.Error
		if errors.As(err, &located) {
			d := diag.New("PP601", err.Error(), located.Span)
			d.WithSymbol(located.Symbol).WithRelated(located.Related...)
			r.Diagnostics = append(r.Diagnostics, d)
		} else {
			r.failure("PP601", err.Error(), cfg.Path)
		}
		return r
	}
	begin("wall_image_inputs")
	semantic, _ := json.Marshal(struct {
		Config   *config.Config
		External *manifest.Set
	}{cfg, ext})
	imageParts := []string{Version, frontend.Version, "intrinsics-" + check.IntrinsicVersion, cache.Digest(semantic)}
	// Manifest bytes are hashed even if a whitespace-only edit leaves the
	// decoded schema unchanged. No semantic input silently escapes the key.
	for _, path := range cfg.Manifests {
		data, e := os.ReadFile(path)
		if e != nil {
			begin("wall_report")
			r.failure("PP601", e.Error(), path)
			return r
		}
		imageParts = append(imageParts, path, cache.Digest(data))
	}
	configBytes, e := os.ReadFile(cfg.Path)
	if e != nil {
		begin("wall_report")
		r.failure("PP001", e.Error(), cfg.Path)
		return r
	}
	imageParts = append(imageParts, cache.Digest(configBytes))
	imageKey := cache.Key(imageParts...)
	begin("wall_frontend")
	type parsed struct {
		tree *model.Node
		ds   []diag.Diagnostic
		hit  bool
	}
	parsedFiles := make([]parsed, len(files))
	jobs := opts.Jobs
	if jobs < 1 {
		jobs = runtime.GOMAXPROCS(0)
	}
	if jobs > len(files) {
		jobs = len(files)
	}
	work := make(chan int)
	var wg sync.WaitGroup
	dir := filepath.Join(cfg.ProjectRoot, ".purepy-cache")
	var workerTimings [][len(workStages)]time.Duration
	if opts.Timings {
		workerTimings = make([][len(workStages)]time.Duration, jobs)
	}
	for i := 0; i < jobs; i++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			var workStart time.Time
			startWork := func() {
				if opts.Timings {
					workStart = time.Now()
				}
			}
			endWork := func(index int) {
				if opts.Timings {
					workerTimings[worker][index] += time.Since(workStart)
				}
			}
			for i := range work {
				f := files[i]
				startWork()
				data, err := os.ReadFile(f.Path)
				endWork(0)
				if err != nil {
					d := diag.New("PP101", err.Error(), model.Span{File: f.Path, Line: 1, Column: 1})
					d.WithSymbol(f.Module).WithRelated(cfg.FieldSpans["source_root"])
					parsedFiles[i].ds = []diag.Diagnostic{d}
					continue
				}
				startWork()
				key := cache.Key(imageKey, f.Module, f.Path, cache.Digest(data))
				endWork(1)
				if !opts.NoCache {
					startWork()
					s, ok := cache.Read(dir, key)
					endWork(2)
					if ok {
						parsedFiles[i] = parsed{tree: s.Tree, ds: s.Diagnostics, hit: true}
						continue
					}
				}
				var tree *model.Node
				var ds []diag.Diagnostic
				if opts.Timings {
					var measured frontend.ParseTimings
					tree, ds, measured = frontend.ParseTimed(f.Path, data)
					workerTimings[worker][3] += measured.Parse
					workerTimings[worker][4] += measured.Lower
				} else {
					tree, ds = frontend.Parse(f.Path, data)
				}
				parsedFiles[i] = parsed{tree: tree, ds: ds}
				if !opts.NoCache {
					startWork()
					_ = cache.Write(dir, key, cache.Summary{Tree: tree, Diagnostics: ds})
					endWork(5)
				}
			}
		}(i)
	}
	for i := range files {
		work <- i
	}
	close(work)
	wg.Wait()
	begin("wall_report")
	for _, worker := range workerTimings {
		for i, elapsed := range worker {
			r.Timings[workStages[i]] += elapsed.Seconds()
		}
	}
	modules := make([]*check.Module, 0, len(files))
	for i, f := range files {
		item := parsedFiles[i]
		r.Diagnostics = append(r.Diagnostics, item.ds...)
		if item.hit {
			r.CacheHits++
		}
		modules = append(modules, &check.Module{Name: f.Module, Path: f.Path, Package: f.IsPackage, Tree: item.tree})
	}
	if len(r.Diagnostics) > 0 {
		diag.SortModules(r.Diagnostics, moduleNames)
		return r
	}
	begin("wall_link")
	p := check.Link(modules, ext, cfg.Entrypoints)
	for i := range p.Diagnostics {
		d := &p.Diagnostics[i]
		if d.Code == "PP701" {
			declaration := cfg.EntrypointSpans[d.Symbol]
			if declaration.File == "" {
				declaration = cfg.FieldSpans["entrypoints"]
			}
			if d.Span.File == "" {
				d.Span = declaration
			} else {
				d.WithRelated(declaration)
			}
		}
	}
	r.Program = p
	begin("wall_check")
	checkJobs := opts.Jobs
	if checkJobs < 1 {
		checkJobs = runtime.GOMAXPROCS(0)
	}
	result := p.CheckFunctions(checkJobs)
	begin("wall_report")
	r.Diagnostics = append(r.Diagnostics, result.Diagnostics...)
	r.Calls = result.Calls
	r.Facts = result.Facts
	for _, f := range p.Functions {
		if f.Origin == "project" {
			r.Functions = append(r.Functions, FunctionReport{Name: f.Name, Kind: f.Kind, Classification: f.Classification(), Parameters: f.Parameters, Returns: f.Returns})
		}
	}
	sort.Slice(r.Functions, func(i, j int) bool { return r.Functions[i].Name < r.Functions[j].Name })
	diag.SortModules(r.Diagnostics, moduleNames)
	r.OK = len(r.Diagnostics) == 0
	return r
}

const TimingsSchema = 2

var workStages = [...]string{"work_read", "work_hash", "work_cache_read", "work_parse", "work_lower", "work_cache_write"}

// All categories are emitted, including zero durations for skipped stages.
// wall_render is filled by Run after report construction and output complete.
var timingStages = [...]string{
	"wall_configuration", "wall_discovery", "wall_manifests", "wall_image_inputs",
	"wall_frontend", "wall_link", "wall_check", "wall_report", "wall_render",
	"work_read", "work_hash", "work_cache_read", "work_parse", "work_lower", "work_cache_write",
}

func (r *Report) failure(code, message, file string) {
	r.Diagnostics = append(r.Diagnostics, diag.New(code, message, model.Span{File: file, Line: 1, Column: 1}))
}
