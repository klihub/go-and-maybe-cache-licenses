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
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// externalPkg is a package from a real dependency, so its module has a
// non-empty version and is therefore cacheable. The main module's own packages
// are not, which is what TestLibrariesNeverCachesTheMainModule covers.
const externalPkg = "k8s.io/klog/v2"

// memCache is an in-memory licenses.Cache, so the wiring tests do not depend
// on the on-disk store or touch the filesystem.
type memCache struct {
	mu   sync.Mutex
	lic  map[Key][]License
	seen map[Key]bool
	url  map[Key]string
}

func newMemCache() *memCache {
	return &memCache{lic: map[Key][]License{}, seen: map[Key]bool{}, url: map[Key]string{}}
}

func (m *memCache) Licenses(k Key) ([]License, bool) {
	if !k.Cacheable() {
		return nil, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.seen[k] {
		return nil, false
	}
	return m.lic[k], true
}

func (m *memCache) PutLicenses(k Key, found []License) error {
	if !k.Cacheable() {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.seen[k] = true
	m.lic[k] = found
	return nil
}

func (m *memCache) URL(k Key) (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.url[k]
	return u, ok
}

func (m *memCache) PutURL(k Key, u string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.url[k] = u
	return nil
}

func (m *memCache) len() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.seen)
}

func TestLibrariesServesSecondRunFromCache(t *testing.T) {
	ctx := context.Background()
	cache := newMemCache()

	cold := newCountingClassifier()
	coldLibs, err := FindLibraries(ctx, Options{Classifier: cold, Cache: cache}, externalPkg)
	if err != nil {
		t.Fatalf("FindLibraries() = %v, want nil error", err)
	}
	if cold.total() == 0 {
		t.Fatal("cold run classified nothing, so the test proves nothing")
	}
	if cache.len() == 0 {
		t.Fatal("cold run populated no cache entries")
	}

	warm := newCountingClassifier()
	warmLibs, err := FindLibraries(ctx, Options{Classifier: warm, Cache: cache}, externalPkg)
	if err != nil {
		t.Fatalf("FindLibraries() = %v, want nil error", err)
	}
	if got := warm.total(); got != 0 {
		t.Errorf("warm run classified %d candidates, want 0", got)
	}
	if diff := cmp.Diff(shapes(coldLibs), shapes(warmLibs)); diff != "" {
		t.Errorf("warm run differs from cold run (-cold +warm):\n%s", diff)
	}
}

func TestLibrariesCachesNegativeResults(t *testing.T) {
	ctx := context.Background()
	cache := newMemCache()

	cold := newCountingClassifier()
	if _, err := FindLibraries(ctx, Options{Classifier: cold, Cache: cache}, externalPkg); err != nil {
		t.Fatalf("FindLibraries() = %v, want nil error", err)
	}

	// Most candidates are READMEs that contain no license, and caching that is
	// where most of the saving comes from. So the cache must hold entries for
	// candidates that classified to nothing.
	var negatives int
	cache.mu.Lock()
	for k := range cache.seen {
		if len(cache.lic[k]) == 0 {
			negatives++
			_ = k
		}
	}
	cache.mu.Unlock()
	if negatives == 0 {
		t.Error("no negative results were cached, want at least one")
	}
}

func TestLibrariesNeverCachesTheMainModule(t *testing.T) {
	ctx := context.Background()
	cache := newMemCache()
	const mainPkg = "github.com/google/go-licenses/v2/licenses/testdata"

	first := newCountingClassifier()
	if _, err := FindLibraries(ctx, Options{Classifier: first, Cache: cache}, mainPkg); err != nil {
		t.Fatalf("FindLibraries() = %v, want nil error", err)
	}
	if first.total() == 0 {
		t.Fatal("first run classified nothing, so the test proves nothing")
	}

	// The main module is mutable: its version is empty and its files can change
	// between runs, so it must be reclassified every time.
	second := newCountingClassifier()
	if _, err := FindLibraries(ctx, Options{Classifier: second, Cache: cache}, mainPkg); err != nil {
		t.Fatalf("FindLibraries() = %v, want nil error", err)
	}
	if second.total() != first.total() {
		t.Errorf("second run classified %d candidates, want %d - the main module must not be cached",
			second.total(), first.total())
	}
}

func TestLibrariesWithoutCacheMatchesLibraries(t *testing.T) {
	ctx := context.Background()
	const mainPkg = "github.com/google/go-licenses/v2/licenses/testdata"

	viaOptions, err := FindLibraries(ctx,
		Options{Classifier: newCountingClassifier(), Cache: nil}, mainPkg)
	if err != nil {
		t.Fatalf("FindLibraries() = %v, want nil error", err)
	}
	viaLegacy, err := Libraries(ctx, newCountingClassifier(), false, nil, mainPkg)
	if err != nil {
		t.Fatalf("Libraries() = %v, want nil error", err)
	}

	if diff := cmp.Diff(shapes(viaLegacy), shapes(viaOptions)); diff != "" {
		t.Errorf("FindLibraries differs from Libraries (-legacy +options):\n%s", diff)
	}
}
