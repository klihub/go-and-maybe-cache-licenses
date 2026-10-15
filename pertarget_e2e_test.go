// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// Two packages in this repository with deliberately different dependency sets.
const (
	tgtDirect   = "./licenses/testdata/direct"
	tgtIndirect = "./licenses/testdata/indirect"
)

// TestPerTargetReportMatchesSeparateRuns is the invariant that makes one
// invocation a safe replacement for several.
func TestPerTargetReportMatchesSeparateRuns(t *testing.T) {
	goLicenses := buildGoLicenses(t)
	outDir := t.TempDir()

	// What separate invocations produce today.
	wantDirect := runGL(t, goLicenses, "report", "--cache=false", tgtDirect)
	wantIndirect := runGL(t, goLicenses, "report", "--cache=false", tgtIndirect)
	if wantDirect == wantIndirect {
		t.Fatal("the two targets produced identical reports, so this test proves nothing")
	}

	// One invocation, per-target output.
	tmpl := filepath.Join(outDir, "{{.Name}}.csv")
	runGL(t, goLicenses, "report", "--cache=false", "--target_path", tmpl,
		"direct="+tgtDirect, "indirect="+tgtIndirect)

	for name, want := range map[string]string{"direct": wantDirect, "indirect": wantIndirect} {
		got, err := os.ReadFile(filepath.Join(outDir, name+".csv"))
		if err != nil {
			t.Fatalf("reading per-target output for %s: %v", name, err)
		}
		if diff := cmp.Diff(want, string(got)); diff != "" {
			t.Errorf("per-target report for %s differs from its own invocation (-want +got):\n%s",
				name, diff)
		}
	}
}

func TestPerTargetSaveWritesOneTreePerTarget(t *testing.T) {
	goLicenses := buildGoLicenses(t)
	saveRoot := filepath.Join(t.TempDir(), "licenses")

	runGL(t, goLicenses, "save", "--cache=false", "--save_path", saveRoot,
		"--target_path", "{{.Name}}", "direct="+tgtDirect, "indirect="+tgtIndirect)

	for _, name := range []string{"direct", "indirect"} {
		dir := filepath.Join(saveRoot, name)
		info, err := os.Stat(dir)
		if err != nil {
			t.Errorf("expected a saved tree for %s at %s: %v", name, dir, err)
			continue
		}
		if !info.IsDir() {
			t.Errorf("%s is not a directory", dir)
		}
		var licenseFiles int
		_ = filepath.WalkDir(dir, func(_ string, d os.DirEntry, err error) error {
			if err == nil && !d.IsDir() {
				licenseFiles++
			}
			return nil
		})
		if licenseFiles == 0 {
			t.Errorf("no files saved under %s", dir)
		}
	}
}

func TestPerTargetSingleTargetMatchesPlainRun(t *testing.T) {
	goLicenses := buildGoLicenses(t)
	outDir := t.TempDir()

	want := runGL(t, goLicenses, "report", "--cache=false", tgtDirect)
	runGL(t, goLicenses, "report",
		"--target_path", filepath.Join(outDir, "{{.Base}}.csv"), tgtDirect)

	got, err := os.ReadFile(filepath.Join(outDir, "direct.csv"))
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(want, string(got)); diff != "" {
		t.Errorf("--target_path changed single-target output (-want +got):\n%s", diff)
	}
}

func TestPerTargetCollisionFailsAndWritesNothing(t *testing.T) {
	goLicenses := buildGoLicenses(t)
	outDir := t.TempDir()

	// Both targets render to the same path under {{.ModuleBase}}.
	cmd := exec.Command(goLicenses, "report", "--cache=false",
		"--target_path", filepath.Join(outDir, "{{.ModuleBase}}.csv"), tgtDirect, tgtIndirect)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err == nil {
		t.Fatal("colliding --target_path exited 0, want a non-zero exit")
	}

	entries, err := os.ReadDir(outDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("collision wrote %d files, want none - the check must happen before any output",
			len(entries))
	}
}

func TestPerTargetWithoutTargetPathStillUnions(t *testing.T) {
	goLicenses := buildGoLicenses(t)

	// Without --target_path the existing union behaviour must be unchanged.
	// Note direct already depends on indirect, so those two union to direct's
	// own set; testdata is a strict superset of direct.
	union := runGL(t, goLicenses, "report", "--cache=false", tgtDirect, "./licenses/testdata")
	direct := runGL(t, goLicenses, "report", "--cache=false", tgtDirect)
	if union == direct {
		t.Error("union report equals the single-target report; expected the union to be larger")
	}
	if len(union) < len(direct) {
		t.Errorf("union report is shorter (%d bytes) than the single-target report (%d bytes)",
			len(union), len(direct))
	}
}
