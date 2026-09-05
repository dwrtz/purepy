// Package config loads the strict, non-executable PurePy project configuration.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dwrtz/purepy/internal/discovery"
	"github.com/pelletier/go-toml/v2"
)

type Config struct {
	Path         string
	ProjectRoot  string
	SourceRoot   string
	Language     string
	PythonSyntax string
	Entrypoints  []string
	Manifests    []string
}

type document struct {
	Tool struct {
		PurePy *settings `toml:"purepy"`
	} `toml:"tool"`
}

type settings struct {
	Language     string    `toml:"language"`
	PythonSyntax string    `toml:"python_syntax"`
	SourceRoot   string    `toml:"source_root"`
	Entrypoints  *[]string `toml:"entrypoints"`
	Manifests    *[]string `toml:"manifests"`
}

// Load accepts a purepy.toml path or its containing directory. Every configured
// path is resolved relative to that file and must stay inside its project root.
func Load(path string) (*Config, error) {
	if path == "" {
		path = "."
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("configuration path: %w", err)
	}
	info, err := os.Lstat(abs)
	if err != nil {
		return nil, fmt.Errorf("configuration %s: %w", abs, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("configuration path must not be a symlink: %s", abs)
	}
	if info.IsDir() {
		abs = filepath.Join(abs, "purepy.toml")
	}
	// Canonicalize the project directory first (e.g. /tmp on macOS), then
	// prohibit all symlinks within the project boundary.
	root, err := filepath.EvalSymlinks(filepath.Dir(abs))
	if err != nil {
		return nil, fmt.Errorf("configuration directory: %w", err)
	}
	abs = filepath.Join(root, filepath.Base(abs))
	if err := checkPath(root, abs, false); err != nil {
		return nil, fmt.Errorf("configuration: %w", err)
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return nil, fmt.Errorf("configuration %s: %w", abs, err)
	}
	var doc document
	if err := toml.NewDecoder(bytes.NewReader(data)).DisallowUnknownFields().Decode(&doc); err != nil {
		return nil, fmt.Errorf("configuration %s: %w", abs, tomlFailure(err))
	}
	p := doc.Tool.PurePy
	if p == nil {
		return nil, fmt.Errorf("configuration %s: missing [tool.purepy]", abs)
	}
	if p.Language != "0.1" {
		return nil, fmt.Errorf("configuration %s: language must be %q, got %q", abs, "0.1", p.Language)
	}
	if p.PythonSyntax != "3.14" {
		return nil, fmt.Errorf("configuration %s: python_syntax must be %q, got %q", abs, "3.14", p.PythonSyntax)
	}
	if p.Entrypoints == nil || p.Manifests == nil {
		return nil, fmt.Errorf("configuration %s: entrypoints and manifests are required (use [] for an empty list)", abs)
	}
	source, err := resolve(root, p.SourceRoot, true)
	if err != nil {
		return nil, fmt.Errorf("configuration %s: source_root: %w", abs, err)
	}
	cfg := &Config{Path: abs, ProjectRoot: root, SourceRoot: source, Language: p.Language, PythonSyntax: p.PythonSyntax, Entrypoints: append([]string{}, (*p.Entrypoints)...), Manifests: []string{}}
	seen := make(map[string]bool)
	for _, entry := range cfg.Entrypoints {
		if !strings.Contains(entry, ".") || !discovery.ValidModuleName(entry) {
			return nil, fmt.Errorf("configuration %s: invalid fully qualified entrypoint %q", abs, entry)
		}
		if seen[entry] {
			return nil, fmt.Errorf("configuration %s: duplicate entrypoint %q", abs, entry)
		}
		seen[entry] = true
	}
	seen = make(map[string]bool)
	for _, manifest := range *p.Manifests {
		resolved, err := resolve(root, manifest, false)
		if err != nil {
			return nil, fmt.Errorf("configuration %s: manifest %q: %w", abs, manifest, err)
		}
		if seen[resolved] {
			return nil, fmt.Errorf("configuration %s: duplicate manifest %q", abs, manifest)
		}
		seen[resolved] = true
		cfg.Manifests = append(cfg.Manifests, resolved)
	}
	return cfg, nil
}

func tomlFailure(err error) error {
	var unknown *toml.StrictMissingError
	if errors.As(err, &unknown) {
		fields := make([]string, 0, len(unknown.Errors))
		for _, field := range unknown.Errors {
			line, column := field.Position()
			fields = append(fields, fmt.Sprintf("%q (line %d, column %d)", strings.Join(field.Key(), "."), line, column))
		}
		return fmt.Errorf("unknown fields: %s", strings.Join(fields, "; "))
	}
	var syntax *toml.DecodeError
	if errors.As(err, &syntax) {
		line, column := syntax.Position()
		return fmt.Errorf("line %d, column %d: %w", line, column, err)
	}
	return err
}

func resolve(root, path string, directory bool) (string, error) {
	if path == "" {
		return "", fmt.Errorf("path is required")
	}
	if strings.Contains(path, "$") || strings.HasPrefix(path, "~") {
		return "", fmt.Errorf("environment and home-directory interpolation are prohibited: %q", path)
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	path = filepath.Clean(path)
	if err := checkPath(root, path, directory); err != nil {
		return "", err
	}
	return path, nil
}

func checkPath(root, path string, directory bool) error {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return fmt.Errorf("path escapes project root: %s", path)
	}
	current := root
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			return fmt.Errorf("%s: %w", current, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlinks are prohibited in configured paths: %s", current)
		}
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if directory && !info.IsDir() {
		return fmt.Errorf("expected a directory: %s", path)
	}
	if !directory && !info.Mode().IsRegular() {
		return fmt.Errorf("expected a regular file: %s", path)
	}
	return nil
}
