// Package app assembles the hermetic verification pipeline and public reports.
package app

import (
	"encoding/json"
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

const Version = "0.1.0-dev"
const JSONSchema = 1

type Options struct {
	Path, Config string
	Jobs         int
	NoCache      bool
}
type Report struct {
	Schema      int                `json:"schema"`
	Version     string             `json:"verifier_version"`
	Language    string             `json:"language"`
	OK          bool               `json:"ok"`
	Files       int                `json:"files"`
	Diagnostics []diag.Diagnostic  `json:"diagnostics"`
	Functions   []FunctionReport   `json:"functions"`
	Program     *check.Program     `json:"-"`
	Config      *config.Config     `json:"-"`
	Calls       []model.CallEdge   `json:"-"`
	Facts       []model.Fact       `json:"-"`
	Timings     map[string]float64 `json:"-"`
	CacheHits   int                `json:"-"`
}
type FunctionReport struct {
	Name           string            `json:"name"`
	Kind           string            `json:"kind"`
	Classification string            `json:"classification"`
	Parameters     []model.Parameter `json:"parameters"`
	Returns        model.Type        `json:"returns"`
}

func Check(opts Options) *Report {
	r := &Report{Schema: JSONSchema, Version: Version, Language: "0.1", Diagnostics: []diag.Diagnostic{}, Functions: []FunctionReport{}, Calls: []model.CallEdge{}, Facts: []model.Fact{}, Timings: map[string]float64{}}
	mark := time.Now()
	stamp := func(stage string) { r.Timings[stage] = time.Since(mark).Seconds(); mark = time.Now() }
	path := opts.Path
	if opts.Config != "" {
		path = opts.Config
	}
	if path == "" {
		path = "."
	}
	cfg, err := config.Load(path)
	stamp("configuration")
	if err != nil {
		r.failure("PP001", err.Error(), path)
		return r
	}
	r.Config = cfg
	files, err := discovery.Discover(cfg.SourceRoot)
	stamp("discovery")
	if err != nil {
		r.failure("PP101", err.Error(), cfg.SourceRoot)
		return r
	}
	r.Files = len(files)
	ext, err := manifest.Load(cfg.Manifests)
	stamp("manifests")
	if err != nil {
		r.failure("PP601", err.Error(), cfg.Path)
		return r
	}
	semantic, _ := json.Marshal(struct {
		Config   *config.Config
		External *manifest.Set
	}{cfg, ext})
	imageParts := []string{Version, frontend.Version, "intrinsics-1", cache.Digest(semantic)}
	// Manifest bytes are hashed even if a whitespace-only edit leaves the
	// decoded schema unchanged. No semantic input silently escapes the key.
	for _, path := range cfg.Manifests {
		data, e := os.ReadFile(path)
		if e != nil {
			r.failure("PP601", e.Error(), path)
			return r
		}
		imageParts = append(imageParts, path, cache.Digest(data))
	}
	configBytes, e := os.ReadFile(cfg.Path)
	if e != nil {
		r.failure("PP001", e.Error(), cfg.Path)
		return r
	}
	imageParts = append(imageParts, cache.Digest(configBytes))
	imageKey := cache.Key(imageParts...)
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
	for i := 0; i < jobs; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range work {
				f := files[i]
				data, err := os.ReadFile(f.Path)
				if err != nil {
					parsedFiles[i].ds = []diag.Diagnostic{diag.New("PP101", err.Error(), model.Span{File: f.Path, Line: 1, Column: 1})}
					continue
				}
				key := cache.Key(imageKey, f.Module, f.Path, cache.Digest(data))
				if !opts.NoCache {
					if s, ok := cache.Read(dir, key); ok {
						parsedFiles[i] = parsed{tree: s.Tree, ds: s.Diagnostics, hit: true}
						continue
					}
				}
				tree, ds := frontend.Parse(f.Path, data)
				parsedFiles[i] = parsed{tree: tree, ds: ds}
				if !opts.NoCache {
					_ = cache.Write(dir, key, cache.Summary{Tree: tree, Diagnostics: ds})
				}
			}
		}()
	}
	for i := range files {
		work <- i
	}
	close(work)
	wg.Wait()
	stamp("read_hash_parse_lower_cache")
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
		diag.Sort(r.Diagnostics)
		return r
	}
	p := check.Link(modules, ext, cfg.Entrypoints)
	r.Program = p
	stamp("link")
	checkJobs := opts.Jobs
	if checkJobs < 1 {
		checkJobs = runtime.GOMAXPROCS(0)
	}
	result := p.CheckFunctions(checkJobs)
	r.Diagnostics = append(r.Diagnostics, result.Diagnostics...)
	r.Calls = result.Calls
	r.Facts = result.Facts
	stamp("check")
	for _, f := range p.Functions {
		if f.Origin == "project" {
			r.Functions = append(r.Functions, FunctionReport{Name: f.Name, Kind: f.Kind, Classification: f.Classification(), Parameters: f.Parameters, Returns: f.Returns})
		}
	}
	sort.Slice(r.Functions, func(i, j int) bool { return r.Functions[i].Name < r.Functions[j].Name })
	diag.Sort(r.Diagnostics)
	r.OK = len(r.Diagnostics) == 0
	return r
}
func (r *Report) failure(code, message, file string) {
	r.Diagnostics = append(r.Diagnostics, diag.New(code, message, model.Span{File: file, Line: 1, Column: 1}))
}
