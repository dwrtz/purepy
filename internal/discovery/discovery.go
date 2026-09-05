// Package discovery maps source files to the closed PurePy module namespace.
package discovery

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// File is one Python source file. RelativePath always uses forward slashes.
type File struct {
	Path         string
	RelativePath string
	Module       string
	IsPackage    bool
}

// ValidIdentifier accepts Python identifiers in their canonical NFKC spelling.
// Soft keywords (match, case and type) remain valid identifiers.
func ValidIdentifier(name string) bool {
	if name == "" || !norm.NFKC.IsNormalString(name) {
		return false
	}
	switch name {
	case "False", "None", "True", "and", "as", "assert", "async", "await", "break", "class", "continue", "def", "del", "elif", "else", "except", "finally", "for", "from", "global", "if", "import", "in", "is", "lambda", "nonlocal", "not", "or", "pass", "raise", "return", "try", "while", "with", "yield":
		return false
	}
	for i, r := range name {
		start := r == '_' || unicode.IsLetter(r) || unicode.Is(unicode.Nl, r) || unicode.Is(unicode.Other_ID_Start, r)
		if !start && (i == 0 || !(unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Mc, r) || unicode.Is(unicode.Nd, r) || unicode.Is(unicode.Pc, r) || unicode.Is(unicode.Other_ID_Continue, r))) {
			return false
		}
	}
	return true
}

// ValidModuleName validates a dotted sequence of canonical identifiers.
func ValidModuleName(name string) bool {
	for _, part := range strings.Split(name, ".") {
		if !ValidIdentifier(part) {
			return false
		}
	}
	return true
}

// Discover recursively discovers every .py file, without importing any code.
// Symlinks are rejected, including symlink directories and non-Python symlinks.
func Discover(sourceRoot string) ([]File, error) {
	root, err := filepath.Abs(sourceRoot)
	if err != nil {
		return nil, fmt.Errorf("source root: %w", err)
	}
	info, err := os.Lstat(root)
	if err != nil {
		return nil, fmt.Errorf("source root %s: %w", root, err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("source root %s must be a real directory, not a symlink", root)
	}
	files := []File{}
	modules := make(map[string]string)
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink is prohibited inside source root: %s", path)
		}
		if d.IsDir() || filepath.Ext(path) != ".py" {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("Python source must be a regular file: %s", path)
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		parts := strings.Split(filepath.ToSlash(rel), "/")
		stem := strings.TrimSuffix(parts[len(parts)-1], ".py")
		isPackage := stem == "__init__"
		if isPackage && len(parts) == 1 {
			return fmt.Errorf("%s: source-root __init__.py has no module name; configure the package's parent as source_root", path)
		}
		for i, part := range parts[:len(parts)-1] {
			if !ValidIdentifier(part) {
				return fmt.Errorf("%s: invalid or noncanonical package name %q", path, part)
			}
			packagePath := filepath.Join(root, filepath.FromSlash(strings.Join(parts[:i+1], "/")))
			initPath := filepath.Join(packagePath, "__init__.py")
			initInfo, err := os.Lstat(initPath)
			if err != nil || !initInfo.Mode().IsRegular() {
				return fmt.Errorf("%s: namespace packages are prohibited; package %q requires a regular __init__.py", path, strings.Join(parts[:i+1], "."))
			}
		}
		if !ValidIdentifier(stem) {
			return fmt.Errorf("%s: invalid or noncanonical module name %q", path, stem)
		}
		parts[len(parts)-1] = stem
		if isPackage {
			parts = parts[:len(parts)-1]
		}
		module := strings.Join(parts, ".")
		if previous, exists := modules[module]; exists {
			return fmt.Errorf("ambiguous module %q: %s and %s", module, previous, path)
		}
		modules[module] = path
		files = append(files, File{Path: path, RelativePath: filepath.ToSlash(rel), Module: module, IsPackage: isPackage})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].RelativePath < files[j].RelativePath })
	return files, nil
}
