// Copyright 2026 Google Inc. All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package licenses

import (
	"context"
	"fmt"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"golang.org/x/tools/go/packages"
)

// Target identifies one resolution target and carries the fields a caller can
// use to name its output.
type Target struct {
	// Pattern is the package pattern as given.
	Pattern string
	// Path is the resolved package import path.
	Path string
	// Base is the last element of Path.
	Base string
	// Dir is Path without its last element.
	Dir string
	// Rel is Path relative to Module, or "." when they are equal. It is the
	// only field guaranteed unique across the targets of one module, so it is
	// the collision-free choice for wildcard patterns.
	Rel string
	// Module is the module path containing the target.
	Module string
	// ModuleBase is the last element of Module.
	ModuleBase string
}

// TargetLibraries is one target's own library set.
type TargetLibraries struct {
	Target    Target
	Libraries []*Library
}

// FindLibrariesPerTarget resolves each target's library set from a single
// package load, returning results in argument order.
//
// This is not the same as passing several patterns to FindLibraries, which
// reports their union. A union would attribute every dependency of the largest
// target to the smallest one.
func FindLibrariesPerTarget(ctx context.Context, opts Options, patterns ...string) ([]TargetLibraries, error) {
	res, err := resolve(ctx, opts, patterns...)
	if err != nil {
		return nil, err
	}
	return res.perTarget(patterns)
}

// perTarget splits an already-resolved package graph into one library set per
// target.
func (r *resolution) perTarget(patterns []string) ([]TargetLibraries, error) {
	roots := primaryRoots(r.roots)

	byPath := make(map[string]pkgInfo, len(r.allPackages))
	for _, p := range r.allPackages {
		byPath[p.pkgPath] = p
	}

	var out []TargetLibraries
	for _, pattern := range patterns {
		// Match patterns to roots explicitly. go/packages exposes no
		// pattern-to-package mapping and does not return roots in pattern
		// order, so pairing them by position would silently attribute one
		// target's licenses to another.
		matched, err := matchRoots(pattern, roots)
		if err != nil {
			return nil, err
		}

		for _, root := range matched {
			if r.opts.MainOnly && root.Name != "main" {
				continue
			}

			// The graph is already in memory, so each target's own closure is
			// just a walk; only packages that survived resolution count.
			var pkgs []pkgInfo
			packages.Visit([]*packages.Package{root}, func(p *packages.Package) bool {
				if info, ok := byPath[p.PkgPath]; ok {
					pkgs = append(pkgs, info)
				}
				return true
			}, nil)

			out = append(out, TargetLibraries{
				Target:    targetInfo(pattern, root),
				Libraries: r.librariesFor(pkgs),
			})
		}
	}
	return out, nil
}

// matchRoots returns the loaded root packages a pattern refers to.
//
// A non-wildcard pattern must name exactly one package: it may carry an output
// name, and one name cannot address several outputs. A wildcard may match many.
func matchRoots(pattern string, roots []*packages.Package) ([]*packages.Package, error) {
	if pattern == "all" {
		return roots, nil
	}

	isWildcard := strings.Contains(pattern, "...")
	isFSPath := strings.HasPrefix(pattern, ".") || filepath.IsAbs(pattern)

	// For a filesystem pattern, compare resolved directories; for an import
	// path, compare package paths.
	prefix := strings.TrimSuffix(pattern, "...")
	if isFSPath {
		abs, err := filepath.Abs(prefix)
		if err != nil {
			return nil, err
		}
		prefix = abs
	} else {
		prefix = strings.TrimSuffix(prefix, "/")
	}

	key := func(p *packages.Package) string {
		if isFSPath {
			return packageDir(p)
		}
		return p.PkgPath
	}
	sep := "/"
	if isFSPath {
		sep = string(filepath.Separator)
	}

	var matched []*packages.Package
	for _, p := range roots {
		k := key(p)
		if k == "" {
			continue
		}
		switch {
		case isWildcard && (k == prefix || strings.HasPrefix(k, prefix+sep)):
			matched = append(matched, p)
		case !isWildcard && k == prefix:
			matched = append(matched, p)
		}
	}

	if len(matched) == 0 {
		return nil, fmt.Errorf("licenses: pattern %q matched none of the loaded packages", pattern)
	}
	if !isWildcard && len(matched) != 1 {
		return nil, fmt.Errorf("licenses: pattern %q matched %d packages, want exactly 1",
			pattern, len(matched))
	}
	return matched, nil
}

// primaryRoots drops the synthetic roots that packages.Load adds when Tests is
// enabled, so a target is not reported several times.
func primaryRoots(roots []*packages.Package) []*packages.Package {
	var out []*packages.Package
	for _, p := range roots {
		if strings.HasSuffix(p.PkgPath, ".test") || strings.Contains(p.PkgPath, " [") {
			continue
		}
		out = append(out, p)
	}
	return out
}

// packageDir returns the directory holding a package's source, or "" when it
// cannot be determined.
func packageDir(p *packages.Package) string {
	switch {
	case len(p.GoFiles) > 0:
		return filepath.Dir(p.GoFiles[0])
	case len(p.CompiledGoFiles) > 0:
		return filepath.Dir(p.CompiledGoFiles[0])
	case len(p.OtherFiles) > 0:
		return filepath.Dir(p.OtherFiles[0])
	}
	return ""
}

// majorVersionSuffix matches a module path's major version element, as in the
// "/v2" of "github.com/google/go-licenses/v2".
var majorVersionSuffix = regexp.MustCompile(`^v[0-9]+$`)

// moduleBase returns the last meaningful element of a module path, skipping a
// major version suffix. "github.com/google/go-licenses/v2" yields
// "go-licenses" rather than "v2", which is what a caller naming output after a
// module wants.
func moduleBase(modulePath string) string {
	base := path.Base(modulePath)
	if majorVersionSuffix.MatchString(base) {
		if parent := path.Dir(modulePath); parent != "." && parent != "/" {
			return path.Base(parent)
		}
	}
	return base
}

// targetInfo fills in the naming fields for one target.
func targetInfo(pattern string, root *packages.Package) Target {
	t := Target{
		Pattern: pattern,
		Path:    root.PkgPath,
		Base:    path.Base(root.PkgPath),
		Dir:     path.Dir(root.PkgPath),
	}
	if root.Module != nil {
		m := newModule(root.Module)
		t.Module = m.Path
		t.ModuleBase = moduleBase(m.Path)
	}

	t.Rel = "."
	if t.Module != "" {
		rel := strings.TrimPrefix(strings.TrimPrefix(t.Path, t.Module), "/")
		if rel != "" {
			t.Rel = rel
		}
	}
	return t
}
