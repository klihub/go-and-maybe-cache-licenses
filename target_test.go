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
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-licenses/v2/licenses"
)

func TestParseTargetSpec(t *testing.T) {
	for _, test := range []struct {
		desc string
		arg  string
		want targetSpec
	}{
		{
			desc: "bare import path",
			arg:  "k8s.io/klog/v2",
			want: targetSpec{Pattern: "k8s.io/klog/v2"},
		},
		{
			desc: "bare relative path",
			arg:  "./cmd/plugins/topology-aware",
			want: targetSpec{Pattern: "./cmd/plugins/topology-aware"},
		},
		{
			desc: "named target",
			arg:  "sample-policy-topology-aware=./cmd/plugins/topology-aware",
			want: targetSpec{
				Name:    "sample-policy-topology-aware",
				Pattern: "./cmd/plugins/topology-aware",
			},
		},
		{
			desc: "splits on the first = only, so a pattern may contain one",
			arg:  "name=a=b",
			want: targetSpec{Name: "name", Pattern: "a=b"},
		},
		{
			desc: "a name with a path separator is not a name",
			arg:  "a/b=c",
			want: targetSpec{Pattern: "a/b=c"},
		},
		{
			desc: "an empty name is not a name",
			arg:  "=foo",
			want: targetSpec{Pattern: "=foo"},
		},
		{
			desc: "a name may not escape upwards",
			arg:  "../evil=./cmd/x",
			want: targetSpec{Pattern: "../evil=./cmd/x"},
		},
		{
			desc: "an empty pattern is not an alias",
			arg:  "name=",
			want: targetSpec{Pattern: "name="},
		},
	} {
		t.Run(test.desc, func(t *testing.T) {
			if diff := cmp.Diff(test.want, parseTargetSpec(test.arg)); diff != "" {
				t.Errorf("parseTargetSpec(%q) diff (-want +got):\n%s", test.arg, diff)
			}
		})
	}
}

// fakeTarget builds a resolved target as licenses.FindLibrariesPerTarget would.
func fakeTarget(pattern, pkgPath string) licenses.TargetLibraries {
	return licenses.TargetLibraries{
		Target: licenses.Target{
			Pattern:    pattern,
			Path:       pkgPath,
			Base:       pkgPath[strings.LastIndex(pkgPath, "/")+1:],
			Dir:        pkgPath[:strings.LastIndex(pkgPath, "/")],
			Rel:        strings.TrimPrefix(pkgPath, "example.com/sample/"),
			Module:     "example.com/sample",
			ModuleBase: "sample",
		},
	}
}

func TestResolveOutputsUsesTheAliasAsName(t *testing.T) {
	specs := []targetSpec{
		{Name: "sample-policy-topology-aware", Pattern: "./cmd/plugins/topology-aware"},
		{Pattern: "./cmd/mpolset"},
	}
	results := []licenses.TargetLibraries{
		fakeTarget("./cmd/plugins/topology-aware", "example.com/sample/cmd/plugins/topology-aware"),
		fakeTarget("./cmd/mpolset", "example.com/sample/cmd/mpolset"),
	}

	got, err := resolveOutputs("{{.Name}}", specs, results)
	if err != nil {
		t.Fatalf("resolveOutputs() = %v, want nil error", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d outputs, want 2", len(got))
	}
	// The alias is used where given; otherwise the package base name is.
	if got[0].Path != "sample-policy-topology-aware" {
		t.Errorf("aliased target rendered to %q, want %q", got[0].Path, "sample-policy-topology-aware")
	}
	if got[1].Path != "mpolset" {
		t.Errorf("unaliased target rendered to %q, want %q", got[1].Path, "mpolset")
	}
}

func TestResolveOutputsTemplateFields(t *testing.T) {
	specs := []targetSpec{{Pattern: "./cmd/plugins/topology-aware"}}
	results := []licenses.TargetLibraries{
		fakeTarget("./cmd/plugins/topology-aware", "example.com/sample/cmd/plugins/topology-aware"),
	}

	for _, test := range []struct{ tmpl, want string }{
		{"{{.Base}}", "topology-aware"},
		{"{{.Rel}}", "cmd/plugins/topology-aware"},
		{"{{.ModuleBase}}/{{.Base}}", "sample/topology-aware"},
		{"licenses.{{.Base}}.csv", "licenses.topology-aware.csv"},
	} {
		t.Run(test.tmpl, func(t *testing.T) {
			got, err := resolveOutputs(test.tmpl, specs, results)
			if err != nil {
				t.Fatalf("resolveOutputs(%q) = %v, want nil error", test.tmpl, err)
			}
			if got[0].Path != test.want {
				t.Errorf("resolveOutputs(%q) = %q, want %q", test.tmpl, got[0].Path, test.want)
			}
		})
	}
}

// TestResolveOutputsRejectsCollisions covers the case a user hits immediately:
// "./cmd/plugins/..." with "{{.Base}}" renders three different "policy"
// packages to one path. Overwriting silently would produce a license set for
// the wrong binary.
func TestResolveOutputsRejectsCollisions(t *testing.T) {
	specs := []targetSpec{{Pattern: "./cmd/plugins/..."}}
	results := []licenses.TargetLibraries{
		fakeTarget("./cmd/plugins/...", "example.com/sample/cmd/plugins/balloons/policy"),
		fakeTarget("./cmd/plugins/...", "example.com/sample/cmd/plugins/template/policy"),
	}

	_, err := resolveOutputs("{{.Base}}", specs, results)
	if err == nil {
		t.Fatal("resolveOutputs() = nil error for two targets rendering to the same path, want an error")
	}
	for _, want := range []string{"policy", "balloons", "template"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q; it must identify both targets and the path",
				err.Error(), want)
		}
	}

	// .Rel is the documented collision-free alternative.
	if _, err := resolveOutputs("{{.Rel}}", specs, results); err != nil {
		t.Errorf("resolveOutputs(\"{{.Rel}}\") = %v, want nil error", err)
	}
}

func TestResolveOutputsRejectsEmptyPath(t *testing.T) {
	specs := []targetSpec{{Pattern: "./cmd/x"}}
	results := []licenses.TargetLibraries{
		fakeTarget("./cmd/x", "example.com/sample/cmd/x"),
	}
	for _, tmpl := range []string{"", "   ", "{{.Name}}"} {
		_, err := resolveOutputs(tmpl, specs, results)
		if tmpl == "{{.Name}}" {
			// .Name is empty for an unaliased target only if Base is empty,
			// which it is not, so this one must succeed.
			if err != nil {
				t.Errorf("resolveOutputs(%q) = %v, want nil error", tmpl, err)
			}
			continue
		}
		if err == nil {
			t.Errorf("resolveOutputs(%q) = nil error, want an error", tmpl)
		}
	}
}

// TestRequireLocalPathsRejectsEscapes covers save, which joins the rendered
// path under --save_path, creates those directories, and deletes them with
// --force.
func TestRequireLocalPathsRejectsEscapes(t *testing.T) {
	specs := []targetSpec{{Pattern: "./cmd/x"}}
	results := []licenses.TargetLibraries{
		fakeTarget("./cmd/x", "example.com/sample/cmd/x"),
	}

	for _, test := range []struct {
		tmpl    string
		wantErr bool
	}{
		{"/etc/{{.Base}}", true},
		{"../../{{.Base}}", true},
		{"{{.Base}}/../../../etc", true},
		{"{{.Base}}", false},
		{"nested/{{.Base}}", false},
	} {
		t.Run(test.tmpl, func(t *testing.T) {
			outputs, err := resolveOutputs(test.tmpl, specs, results)
			if err != nil {
				t.Fatalf("resolveOutputs(%q) = %v, want nil error", test.tmpl, err)
			}
			err = requireLocalPaths(outputs)
			if gotErr := err != nil; gotErr != test.wantErr {
				t.Errorf("requireLocalPaths for %q = %v, want error? %t", test.tmpl, err, test.wantErr)
			}
		})
	}
}

// report has no root directory, so it may legitimately be pointed at an
// absolute path.
func TestResolveOutputsAllowsAbsolutePathsForUnrootedCommands(t *testing.T) {
	specs := []targetSpec{{Pattern: "./cmd/x"}}
	results := []licenses.TargetLibraries{
		fakeTarget("./cmd/x", "example.com/sample/cmd/x"),
	}
	got, err := resolveOutputs("/tmp/out/{{.Base}}.csv", specs, results)
	if err != nil {
		t.Fatalf("resolveOutputs() = %v, want nil error", err)
	}
	if got[0].Path != "/tmp/out/x.csv" {
		t.Errorf("Path = %q, want %q", got[0].Path, "/tmp/out/x.csv")
	}
}

func TestResolveOutputsRejectsBadTemplate(t *testing.T) {
	specs := []targetSpec{{Pattern: "./cmd/x"}}
	results := []licenses.TargetLibraries{
		fakeTarget("./cmd/x", "example.com/sample/cmd/x"),
	}
	if _, err := resolveOutputs("{{.Nope}", specs, results); err == nil {
		t.Error("resolveOutputs() = nil error for an unparseable template, want an error")
	}
	if _, err := resolveOutputs("{{.NoSuchField}}", specs, results); err == nil {
		t.Error("resolveOutputs() = nil error for an unknown field, want an error")
	}
}

func TestResolveOutputsRejectsAliasOnMultipleMatches(t *testing.T) {
	// One name cannot address two outputs.
	specs := []targetSpec{{Name: "everything", Pattern: "./cmd/..."}}
	results := []licenses.TargetLibraries{
		fakeTarget("./cmd/...", "example.com/sample/cmd/a"),
		fakeTarget("./cmd/...", "example.com/sample/cmd/b"),
	}
	_, err := resolveOutputs("{{.Name}}", specs, results)
	if err == nil {
		t.Fatal("resolveOutputs() = nil error for an alias matching two packages, want an error")
	}
	if !strings.Contains(err.Error(), "everything") {
		t.Errorf("error %q does not mention the alias", err.Error())
	}
}
