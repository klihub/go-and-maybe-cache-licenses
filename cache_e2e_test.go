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
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// countCacheEntries returns the number of JSON entries under dir.
func countCacheEntries(t *testing.T, dir string) int {
	t.Helper()
	n := 0
	err := filepath.WalkDir(dir, func(_ string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(d.Name(), ".json") {
			n++
		}
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("walking %s: %v", dir, err)
	}
	return n
}

// TestCacheProducesIdenticalOutput is the invariant that matters most: a cached
// run must be indistinguishable from an uncached one.
func TestCacheProducesIdenticalOutput(t *testing.T) {
	goLicenses := buildGoLicenses(t)
	cacheDir := filepath.Join(t.TempDir(), "cache")

	originalWorkDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(originalWorkDir) })

	workdir := filepath.Join(originalWorkDir, "testdata/modules/cli02")
	if err := os.Chdir(workdir); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("go", "mod", "download").CombinedOutput(); err != nil {
		t.Fatalf("downloading go modules:\n%s", out)
	}

	run := func(t *testing.T, args ...string) string {
		t.Helper()
		cmd := exec.Command(goLicenses, args...)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("running go-licenses %v: %v\n%s", args, err, stderr.String())
		}
		return string(out)
	}

	golden, err := os.ReadFile("licenses.csv")
	if err != nil {
		t.Fatal(err)
	}

	uncached := run(t, "report", ".", "--cache=false")
	if diff := cmp.Diff(string(golden), uncached); diff != "" {
		t.Fatalf("uncached report does not match the golden file (-golden +got):\n%s", diff)
	}

	cold := run(t, "report", ".", "--cache_dir", cacheDir)
	if diff := cmp.Diff(string(golden), cold); diff != "" {
		t.Errorf("cold-cache report differs from the golden file (-golden +got):\n%s", diff)
	}
	if n := countCacheEntries(t, cacheDir); n == 0 {
		t.Fatal("cold run wrote no cache entries")
	}

	warm := run(t, "report", ".", "--cache_dir", cacheDir)
	if diff := cmp.Diff(string(golden), warm); diff != "" {
		t.Errorf("warm-cache report differs from the golden file (-golden +got):\n%s", diff)
	}
}

func TestCacheDisabledWritesNothing(t *testing.T) {
	goLicenses := buildGoLicenses(t)
	cacheDir := filepath.Join(t.TempDir(), "cache")

	originalWorkDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(originalWorkDir) })
	if err := os.Chdir(filepath.Join(originalWorkDir, "testdata/modules/cli02")); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(goLicenses, "check", ".", "--cache=false", "--cache_dir", cacheDir)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("running go-licenses check: %v\n%s", err, stderr.String())
	}

	if n := countCacheEntries(t, cacheDir); n != 0 {
		t.Errorf("--cache=false wrote %d cache entries, want 0", n)
	}
}

func TestPurgeCacheEmptiesTheCache(t *testing.T) {
	goLicenses := buildGoLicenses(t)
	cacheDir := filepath.Join(t.TempDir(), "cache")

	originalWorkDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(originalWorkDir) })
	if err := os.Chdir(filepath.Join(originalWorkDir, "testdata/modules/cli02")); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("go", "mod", "download").CombinedOutput(); err != nil {
		t.Fatalf("downloading go modules:\n%s", out)
	}

	cmd := exec.Command(goLicenses, "check", ".", "--cache_dir", cacheDir)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("running go-licenses check: %v\n%s", err, stderr.String())
	}
	if n := countCacheEntries(t, cacheDir); n == 0 {
		t.Fatal("check wrote no cache entries, so the purge test proves nothing")
	}

	// --purge_cache must work without a subcommand, and must not run one.
	cmd = exec.Command(goLicenses, "--purge_cache=0", "--cache_dir", cacheDir)
	stderr.Reset()
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("running go-licenses --purge_cache=0: %v\n%s", err, stderr.String())
	}
	if n := countCacheEntries(t, cacheDir); n != 0 {
		t.Errorf("--purge_cache=0 left %d entries, want 0", n)
	}
}

func TestPurgeCacheSkipsTheSubcommand(t *testing.T) {
	goLicenses := buildGoLicenses(t)
	cacheDir := filepath.Join(t.TempDir(), "cache")

	originalWorkDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(originalWorkDir) })
	// nolicense05 makes check exit 1. With --purge_cache the subcommand must
	// not run at all, so the exit code must be 0.
	if err := os.Chdir(filepath.Join(originalWorkDir, "testdata/modules/nolicense05")); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(goLicenses, "--purge_cache=0", "--cache_dir", cacheDir, "check", ".")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Errorf("go-licenses --purge_cache=0 check . = %v, want exit 0 with the subcommand skipped\n%s",
			err, stderr.String())
	}
}
