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

package cache

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-licenses/v2/licenses"
)

const testClassifier = "v2.0.0"

// newTestStore returns a store rooted in a temporary directory, with no
// go.sum hashes registered.
func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := New(Config{
		Dir:               t.TempDir(),
		ClassifierVersion: testClassifier,
	})
	if err != nil {
		t.Fatalf("New() = %v, want nil error", err)
	}
	return s
}

var klogKey = licenses.Key{
	Module:  "k8s.io/klog/v2",
	Version: "v2.90.1",
	Path:    "LICENSE",
}

// newTestStoreIn returns a store rooted at dir, so two stores can share one
// cache directory the way two go-licenses processes would.
func newTestStoreIn(t *testing.T, dir, classifier string, hashes map[string]string) *Store {
	t.Helper()
	s, err := New(Config{Dir: dir, ClassifierVersion: classifier, Hashes: hashes})
	if err != nil {
		t.Fatalf("New() = %v, want nil error", err)
	}
	return s
}

func TestClassifierVersionMismatchIsAMiss(t *testing.T) {
	dir := t.TempDir()
	apache := []licenses.License{{Name: "Apache-2.0", Type: licenses.Notice}}

	old := newTestStoreIn(t, dir, "v2.0.0", nil)
	if err := old.PutLicenses(klogKey, apache); err != nil {
		t.Fatalf("PutLicenses() = %v, want nil error", err)
	}

	// A classifier upgrade can change verdicts, so entries written by the old
	// corpus must not be trusted.
	upgraded := newTestStoreIn(t, dir, "v2.1.0", nil)
	if got, ok := upgraded.Licenses(klogKey); ok {
		t.Errorf("Licenses() = (%v, true) after a classifier upgrade, want a miss", got)
	}

	// The same store that wrote it still reads it.
	if _, ok := old.Licenses(klogKey); !ok {
		t.Error("Licenses() reported a miss for the classifier version that wrote the entry")
	}
}

func TestGoSumMismatchIsAMiss(t *testing.T) {
	dir := t.TempDir()
	apache := []licenses.License{{Name: "Apache-2.0", Type: licenses.Notice}}
	hashes := map[string]string{"k8s.io/klog/v2@v2.90.1": "h1:original="}

	s := newTestStoreIn(t, dir, testClassifier, hashes)
	if err := s.PutLicenses(klogKey, apache); err != nil {
		t.Fatalf("PutLicenses() = %v, want nil error", err)
	}
	if _, ok := s.Licenses(klogKey); !ok {
		t.Fatal("Licenses() reported a miss for a matching hash, want a hit")
	}

	// A cache restored from elsewhere, carrying an entry for module content
	// this project's go.sum does not expect, must not be trusted.
	other := newTestStoreIn(t, dir, testClassifier,
		map[string]string{"k8s.io/klog/v2@v2.90.1": "h1:different="})
	if got, ok := other.Licenses(klogKey); ok {
		t.Errorf("Licenses() = (%v, true) for a mismatched go.sum hash, want a miss", got)
	}
}

func TestURLSurvivesClassifierUpgrade(t *testing.T) {
	dir := t.TempDir()
	const url = "https://github.com/kubernetes/klog/blob/v2.90.1/LICENSE"

	old := newTestStoreIn(t, dir, "v2.0.0", nil)
	if err := old.PutURL(klogKey, url); err != nil {
		t.Fatalf("PutURL() = %v, want nil error", err)
	}

	// URLs do not depend on the classifier corpus, so an upgrade must not
	// force them to be re-resolved over the network.
	upgraded := newTestStoreIn(t, dir, "v2.1.0", nil)
	got, ok := upgraded.URL(klogKey)
	if !ok {
		t.Fatal("URL() reported a miss after a classifier upgrade, want a hit")
	}
	if got != url {
		t.Errorf("URL() = %q, want %q", got, url)
	}
}

func TestLicensesRoundTrip(t *testing.T) {
	s := newTestStore(t)
	want := []licenses.License{{Name: "Apache-2.0", Type: licenses.Notice}}

	if err := s.PutLicenses(klogKey, want); err != nil {
		t.Fatalf("PutLicenses() = %v, want nil error", err)
	}

	got, ok := s.Licenses(klogKey)
	if !ok {
		t.Fatal("Licenses() reported a miss for a key just written, want a hit")
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Licenses() diff (-want +got):\n%s", diff)
	}
}

func TestCachedEmptyIsAHitNotAMiss(t *testing.T) {
	s := newTestStore(t)
	readme := licenses.Key{Module: "k8s.io/klog/v2", Version: "v2.90.1", Path: "README.md"}

	if _, ok := s.Licenses(readme); ok {
		t.Fatal("Licenses() reported a hit before anything was written")
	}

	// Most candidates are READMEs that contain no license. Caching that fact
	// is where most of the classification saving comes from, so a cached empty
	// result must be distinguishable from never having looked.
	if err := s.PutLicenses(readme, nil); err != nil {
		t.Fatalf("PutLicenses() = %v, want nil error", err)
	}
	got, ok := s.Licenses(readme)
	if !ok {
		t.Fatal("Licenses() reported a miss for a cached empty result, want a hit")
	}
	if len(got) != 0 {
		t.Errorf("Licenses() = %v, want empty", got)
	}
}

func TestCaseDifferingPathsDoNotCollide(t *testing.T) {
	s := newTestStore(t)
	// Both of these modules exist. On a case-insensitive filesystem an
	// unescaped layout would serve one module's license for the other.
	upper := licenses.Key{Module: "github.com/Sirupsen/logrus", Version: "v1.0.0", Path: "LICENSE"}
	lower := licenses.Key{Module: "github.com/sirupsen/logrus", Version: "v1.0.0", Path: "LICENSE"}

	if err := s.PutLicenses(upper, []licenses.License{{Name: "MIT", Type: licenses.Notice}}); err != nil {
		t.Fatalf("PutLicenses() = %v, want nil error", err)
	}
	if err := s.PutLicenses(lower, []licenses.License{{Name: "Apache-2.0", Type: licenses.Notice}}); err != nil {
		t.Fatalf("PutLicenses() = %v, want nil error", err)
	}

	gotUpper, ok := s.Licenses(upper)
	if !ok {
		t.Fatal("Licenses() reported a miss for the mixed-case module")
	}
	if gotUpper[0].Name != "MIT" {
		t.Errorf("mixed-case module got %q, want MIT - the two modules collided", gotUpper[0].Name)
	}
}

func TestUncacheableKeysAreIgnored(t *testing.T) {
	s := newTestStore(t)
	// An empty version means the main module, a local replace, or vendored
	// code - all mutable, none cacheable.
	for _, key := range []licenses.Key{
		{Module: "example.com/main", Version: "", Path: "LICENSE"},
		{Module: "", Version: "v1.0.0", Path: "LICENSE"},
	} {
		if err := s.PutLicenses(key, []licenses.License{{Name: "MIT"}}); err != nil {
			t.Errorf("PutLicenses(%+v) = %v, want nil error", key, err)
		}
		if _, ok := s.Licenses(key); ok {
			t.Errorf("Licenses(%+v) reported a hit, want a miss for an uncacheable key", key)
		}
	}
}

func TestCorruptEntryIsAMissAndIsRemoved(t *testing.T) {
	dir := t.TempDir()
	s := newTestStoreIn(t, dir, testClassifier, nil)
	if err := s.PutLicenses(klogKey, []licenses.License{{Name: "MIT"}}); err != nil {
		t.Fatalf("PutLicenses() = %v, want nil error", err)
	}

	name := filepath.Join(dir, "k8s.io", "klog", "v2@v2.90.1", "!l!i!c!e!n!s!e.licenses.json")
	if _, err := os.Stat(name); err != nil {
		t.Fatalf("expected an entry at %s: %v", name, err)
	}
	if err := os.WriteFile(name, []byte("{not json"), 0600); err != nil {
		t.Fatal(err)
	}

	if _, ok := s.Licenses(klogKey); ok {
		t.Error("Licenses() reported a hit for a corrupt entry, want a miss")
	}
	// Self-healing: a corrupt entry is removed rather than being re-read and
	// re-rejected on every future run.
	if _, err := os.Stat(name); !os.IsNotExist(err) {
		t.Errorf("corrupt entry still present after a read, os.Stat err = %v, want IsNotExist", err)
	}
}

func TestPurgeKeepsNewestVersionsPerModule(t *testing.T) {
	dir := t.TempDir()
	s := newTestStoreIn(t, dir, testClassifier, nil)

	versions := []string{"v2.80.0", "v2.90.1", "v2.100.0"}
	for i, v := range versions {
		key := licenses.Key{Module: "k8s.io/klog/v2", Version: v, Path: "LICENSE"}
		if err := s.PutLicenses(key, []licenses.License{{Name: "Apache-2.0"}}); err != nil {
			t.Fatalf("PutLicenses() = %v, want nil error", err)
		}
		// Oldest first, so recency order is the slice order.
		name, err := s.path(key, licensesKind)
		if err != nil {
			t.Fatal(err)
		}
		when := time.Now().Add(time.Duration(i-len(versions)) * time.Hour)
		if err := os.Chtimes(name, when, when); err != nil {
			t.Fatal(err)
		}
	}

	if err := s.Purge(2); err != nil {
		t.Fatalf("Purge(2) = %v, want nil error", err)
	}

	for _, v := range versions[1:] {
		key := licenses.Key{Module: "k8s.io/klog/v2", Version: v, Path: "LICENSE"}
		if _, ok := s.Licenses(key); !ok {
			t.Errorf("Purge(2) removed version %s, want it kept", v)
		}
	}
	gone := licenses.Key{Module: "k8s.io/klog/v2", Version: versions[0], Path: "LICENSE"}
	if _, ok := s.Licenses(gone); ok {
		t.Errorf("Purge(2) kept version %s, want it removed as the oldest of three", versions[0])
	}
}

func TestPurgeZeroRemovesEverything(t *testing.T) {
	dir := t.TempDir()
	s := newTestStoreIn(t, dir, testClassifier, nil)
	if err := s.PutLicenses(klogKey, []licenses.License{{Name: "Apache-2.0"}}); err != nil {
		t.Fatalf("PutLicenses() = %v, want nil error", err)
	}

	if err := s.Purge(0); err != nil {
		t.Fatalf("Purge(0) = %v, want nil error", err)
	}
	if _, ok := s.Licenses(klogKey); ok {
		t.Error("Licenses() reported a hit after Purge(0), want a miss")
	}
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
		t.Errorf("after Purge(0) ReadDir = (%d entries, %v), want an empty directory", len(entries), err)
	}
}

func TestPurgePrunesEmptiedParents(t *testing.T) {
	dir := t.TempDir()
	s := newTestStoreIn(t, dir, testClassifier, nil)
	key := licenses.Key{Module: "github.com/some-org/some-mod", Version: "v1.0.0", Path: "LICENSE"}
	if err := s.PutLicenses(key, []licenses.License{{Name: "MIT"}}); err != nil {
		t.Fatalf("PutLicenses() = %v, want nil error", err)
	}

	// Purge(1) keeps the only version; nothing should be pruned.
	if err := s.Purge(1); err != nil {
		t.Fatalf("Purge(1) = %v, want nil error", err)
	}
	if _, ok := s.Licenses(key); !ok {
		t.Fatal("Purge(1) removed the only version of a module, want it kept")
	}

	// Remove it by purging with a classifier that invalidates the entry, then
	// confirm the empty github.com/some-org tree does not linger.
	other := newTestStoreIn(t, dir, "v9.9.9", nil)
	if err := other.Purge(1); err != nil {
		t.Fatalf("Purge(1) = %v, want nil error", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "github.com")); !os.IsNotExist(err) {
		t.Errorf("emptied parent still present, os.Stat err = %v, want IsNotExist", err)
	}
}

func TestConcurrentWritesOfDifferentKeysAllSurvive(t *testing.T) {
	s := newTestStore(t)
	// This is the test that would have caught a read-modify-write layout:
	// every candidate of one module version is written by a separate goroutine,
	// and all of them must be readable afterwards.
	paths := []string{"LICENSE", "README.md", "NOTICE", "COPYING", "licenses/LICENSE.txt"}

	var wg sync.WaitGroup
	for _, p := range paths {
		wg.Add(1)
		go func(p string) {
			defer wg.Done()
			key := licenses.Key{Module: "k8s.io/klog/v2", Version: "v2.90.1", Path: p}
			if err := s.PutLicenses(key, []licenses.License{{Name: p}}); err != nil {
				t.Errorf("PutLicenses(%q) = %v, want nil error", p, err)
			}
		}(p)
	}
	wg.Wait()

	for _, p := range paths {
		key := licenses.Key{Module: "k8s.io/klog/v2", Version: "v2.90.1", Path: p}
		got, ok := s.Licenses(key)
		if !ok {
			t.Errorf("Licenses(%q) reported a miss, want a hit - a concurrent write was lost", p)
			continue
		}
		if got[0].Name != p {
			t.Errorf("Licenses(%q) = %q, want %q", p, got[0].Name, p)
		}
	}
}

func TestConcurrentWritesOfTheSameKeyLeaveOneValidEntry(t *testing.T) {
	dir := t.TempDir()
	s := newTestStoreIn(t, dir, testClassifier, nil)
	want := []licenses.License{{Name: "Apache-2.0", Type: licenses.Notice}}

	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.PutLicenses(klogKey, want); err != nil {
				t.Errorf("PutLicenses() = %v, want nil error", err)
			}
		}()
	}
	wg.Wait()

	got, ok := s.Licenses(klogKey)
	if !ok {
		t.Fatal("Licenses() reported a miss after concurrent writes, want a hit")
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Licenses() diff (-want +got):\n%s", diff)
	}

	// No temp files may be left behind.
	versionDir := filepath.Join(dir, "k8s.io", "klog", "v2@v2.90.1")
	entries, err := os.ReadDir(versionDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".tmp-") {
			t.Errorf("temp file %q left behind", e.Name())
		}
	}
	if len(entries) != 1 {
		t.Errorf("version directory holds %d files, want exactly 1", len(entries))
	}
}

func TestReadOnlyCacheDegradesToMisses(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0700) })

	s := newTestStoreIn(t, dir, testClassifier, nil)
	// A write may fail, but the caller treats that as non-fatal, and a
	// subsequent read must simply miss rather than blow up.
	_ = s.PutLicenses(klogKey, []licenses.License{{Name: "MIT"}})
	if _, ok := s.Licenses(klogKey); ok {
		t.Error("Licenses() reported a hit from a read-only cache directory")
	}
}
