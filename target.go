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

package main

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/klihub/go-and-maybe-cache-licenses/licenses"
	"k8s.io/klog/v2"
)

var (
	// targetPath is the --target_path template. Empty keeps the historical
	// behaviour of reporting the union of all patterns to one destination.
	targetPath string
	// mainOnly restricts per-target resolution to main packages.
	mainOnly bool
)

func init() {
	rootCmd.PersistentFlags().StringVar(&targetPath, "target_path", "",
		"Go template naming each target's output, enabling per-target results from one invocation. "+
			"Fields: .Name .Base .Path .Dir .Rel .Pattern .Module .ModuleBase. "+
			"Without it, several patterns are reported as a single union, as before.")
	rootCmd.PersistentFlags().BoolVar(&mainOnly, "main_only", false,
		"When expanding wildcard patterns, keep only main packages. Useful because a pattern "+
			"like ./cmd/... also matches library packages that are never shipped.")
}

// resolvePerTarget resolves each target separately and renders its output path.
//
// It returns results in argument order, with every path already validated, so a
// caller can write all of them knowing none will collide or escape.
func resolvePerTarget(opts licenses.Options, args []string) ([]targetOutput, error) {
	opts.MainOnly = mainOnly
	specs := parseTargetSpecs(args)
	results, err := licenses.FindLibrariesPerTarget(context.Background(), opts, targetPatterns(specs)...)
	if err != nil {
		return nil, err
	}

	// A pattern that matched nothing is already an error, so a pattern missing
	// from the results was filtered out by --main_only. Say so: producing no
	// output and exiting successfully is otherwise indistinguishable from the
	// pattern being wrong.
	if mainOnly {
		resolved := make(map[string]bool, len(results))
		for _, r := range results {
			resolved[r.Target.Pattern] = true
		}
		for _, s := range specs {
			if !resolved[s.Pattern] {
				klog.Warningf("--main_only filtered out every package matched by %q, "+
					"so no output is produced for it.", s.Pattern)
			}
		}
	}

	return resolveOutputs(targetPath, specs, results)
}

// targetSpec is one target argument: a package pattern and, optionally, the
// name to use for its output.
type targetSpec struct {
	// Name is the output name, or "" when the argument carried none.
	Name string
	// Pattern is the package pattern.
	Pattern string
}

// parseTargetSpec splits a positional argument of the form "name=pattern", or
// returns the whole argument as a pattern.
//
// An output name is needed because a name is not always derivable from the
// package path: two binaries built from ./cmd/plugins/x and ./cmd/plugins/y may
// ship under unrelated names.
//
// The split is on the first "=", so a name cannot contain one but a pattern
// can. An argument only counts as "name=pattern" when both halves are usable,
// which keeps a pattern that happens to contain "=" from being misread.
func parseTargetSpec(arg string) targetSpec {
	i := strings.Index(arg, "=")
	if i < 0 {
		return targetSpec{Pattern: arg}
	}
	name, pattern := arg[:i], arg[i+1:]
	if !validTargetName(name) || pattern == "" {
		return targetSpec{Pattern: arg}
	}
	return targetSpec{Name: name, Pattern: pattern}
}

// validTargetName reports whether s is usable as an output name. It must be a
// single path element that cannot escape the output root.
func validTargetName(s string) bool {
	if s == "" || s == "." || s == ".." {
		return false
	}
	if strings.ContainsAny(s, `/\`) || strings.Contains(s, "..") {
		return false
	}
	return true
}

// parseTargetSpecs parses every positional argument.
func parseTargetSpecs(args []string) []targetSpec {
	specs := make([]targetSpec, 0, len(args))
	for _, arg := range args {
		specs = append(specs, parseTargetSpec(arg))
	}
	return specs
}

// targetPatterns returns just the patterns, for passing to the licenses package.
func targetPatterns(specs []targetSpec) []string {
	patterns := make([]string, 0, len(specs))
	for _, s := range specs {
		patterns = append(patterns, s.Pattern)
	}
	return patterns
}

// targetOutput is one resolved target together with the output path its
// template rendered to.
type targetOutput struct {
	licenses.TargetLibraries
	// Name is the output name: the alias when one was given, else the
	// package's base name.
	Name string
	// Path is the rendered output path, relative and known not to escape.
	Path string
}

// targetTemplateData is what a --target_path template is evaluated against.
// Embedding Target exposes its fields directly, as in "{{.Base}}".
type targetTemplateData struct {
	Name string
	licenses.Target
}

// resolveOutputs renders the output path for each resolved target.
//
// Collisions and escaping paths are errors, and every target is checked before
// anything is written, so a run either produces all of its output or none.
func resolveOutputs(tmplText string, specs []targetSpec, results []licenses.TargetLibraries) ([]targetOutput, error) {
	if strings.TrimSpace(tmplText) == "" {
		return nil, fmt.Errorf("--target_path is empty")
	}
	// Option is a better name than Must here: report a bad template before
	// doing any work rather than after resolving a thousand packages.
	tmpl, err := template.New("target_path").Option("missingkey=error").Parse(tmplText)
	if err != nil {
		return nil, fmt.Errorf("parsing --target_path %q: %w", tmplText, err)
	}

	names := map[string]string{}
	for _, s := range specs {
		if s.Name != "" {
			names[s.Pattern] = s.Name
		}
	}

	// An alias names a single output, so it cannot cover a pattern that
	// expanded to several packages.
	matches := map[string]int{}
	for _, r := range results {
		matches[r.Target.Pattern]++
	}
	for pattern, name := range names {
		if matches[pattern] > 1 {
			return nil, fmt.Errorf("target name %q covers pattern %q, which matched %d packages; "+
				"name each package separately or drop the name", name, pattern, matches[pattern])
		}
	}

	outputs := make([]targetOutput, 0, len(results))
	byPath := map[string]targetOutput{}
	for _, r := range results {
		name := r.Target.Base
		if alias, ok := names[r.Target.Pattern]; ok {
			name = alias
		}

		var buf strings.Builder
		if err := tmpl.Execute(&buf, targetTemplateData{Name: name, Target: r.Target}); err != nil {
			return nil, fmt.Errorf("rendering --target_path for %s: %w", r.Target.Path, err)
		}
		rendered := buf.String()

		clean, err := cleanTargetPath(rendered)
		if err != nil {
			return nil, fmt.Errorf("--target_path for %s: %w", r.Target.Path, err)
		}

		out := targetOutput{TargetLibraries: r, Name: name, Path: clean}
		if prev, ok := byPath[clean]; ok {
			return nil, fmt.Errorf("targets %s and %s both render --target_path to %q; "+
				"use a template that distinguishes them, such as {{.Rel}}",
				prev.Target.Path, r.Target.Path, clean)
		}
		byPath[clean] = out
		outputs = append(outputs, out)
	}
	return outputs, nil
}

// cleanTargetPath validates a rendered path and returns it cleaned.
func cleanTargetPath(rendered string) (string, error) {
	trimmed := strings.TrimSpace(rendered)
	if trimmed == "" {
		return "", fmt.Errorf("rendered to an empty path")
	}
	return filepath.Clean(filepath.FromSlash(trimmed)), nil
}

// requireLocalPaths rejects output paths that would not stay inside a root
// directory.
//
// Only commands that join the rendered path under a root need this - save
// creates those directories and --force deletes them, so a template rendering
// "/etc" or "../.." must not be reachable. report writes where it is told and
// has no root, so an absolute path there is legitimate.
func requireLocalPaths(outputs []targetOutput) error {
	for _, out := range outputs {
		if !filepath.IsLocal(out.Path) {
			return fmt.Errorf("--target_path for %s rendered to %q, which is absolute or "+
				"leaves the output directory", out.Target.Path, out.Path)
		}
	}
	return nil
}
