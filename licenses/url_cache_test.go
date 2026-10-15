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
	"testing"

	"github.com/klihub/go-and-maybe-cache-licenses/internal/third_party/pkgsite/source"
)

func TestFileURLServedFromCache(t *testing.T) {
	c := newMemCache()
	lib := &Library{
		Packages:    []string{"k8s.io/klog/v2"},
		LicenseFile: "/modcache/k8s.io/klog/v2/LICENSE",
		module: &Module{
			Path:    "k8s.io/klog/v2",
			Version: "v2.90.1",
			Dir:     "/modcache/k8s.io/klog/v2",
		},
		cache: c,
	}
	key := Key{Module: "k8s.io/klog/v2", Version: "v2.90.1", Path: "LICENSE"}
	const sentinel = "https://cached.example/klog/LICENSE"
	if err := c.PutURL(key, sentinel); err != nil {
		t.Fatal(err)
	}

	// A sentinel no resolution would ever produce, and a nil client, so a hit
	// proves the cache was consulted before any network work.
	got, err := lib.FileURL(context.Background(), nil, lib.LicenseFile)
	if err != nil {
		t.Fatalf("FileURL() = %v, want nil error", err)
	}
	if got != sentinel {
		t.Errorf("FileURL() = %q, want the cached %q", got, sentinel)
	}
}

func TestFileURLCachesResolvedURL(t *testing.T) {
	c := newMemCache()
	lib := &Library{
		Packages:    []string{"github.com/google/trillian"},
		LicenseFile: "/modcache/github.com/google/trillian/LICENSE",
		module: &Module{
			Path:    "github.com/google/trillian",
			Version: "v1.2.3",
			Dir:     "/modcache/github.com/google/trillian",
		},
		cache: c,
	}

	got, err := lib.FileURL(context.Background(), source.NewClientForTesting(), lib.LicenseFile)
	if err != nil {
		t.Fatalf("FileURL() = %v, want nil error", err)
	}
	const want = "https://github.com/google/trillian/blob/v1.2.3/LICENSE"
	if got != want {
		t.Fatalf("FileURL() = %q, want %q", got, want)
	}

	key := Key{Module: "github.com/google/trillian", Version: "v1.2.3", Path: "LICENSE"}
	cached, ok := c.URL(key)
	if !ok {
		t.Fatal("resolved URL was not cached")
	}
	if cached != want {
		t.Errorf("cached URL = %q, want %q", cached, want)
	}
}

// TestFileURLDoesNotCacheUnverifiedURL covers the pre-existing bug this cache
// would otherwise make permanent: for a "/vN" module, the correct URL depends
// on a probe, and with no network the fallback is a guess. Caching that guess
// would turn a transient network failure into a permanently wrong URL.
func TestFileURLDoesNotCacheUnverifiedURL(t *testing.T) {
	c := newMemCache()
	lib := &Library{
		Packages:    []string{"github.com/google/licenseclassifier/v2"},
		LicenseFile: "/modcache/github.com/google/licenseclassifier/v2/LICENSE",
		module: &Module{
			Path:    "github.com/google/licenseclassifier/v2",
			Version: "v2.0.0",
			Dir:     "/modcache/github.com/google/licenseclassifier/v2",
		},
		cache: c,
	}

	// NewClientForTesting has no HTTP client, so the probe cannot complete.
	got, err := lib.FileURL(context.Background(), source.NewClientForTesting(), lib.LicenseFile)
	if err != nil {
		t.Fatalf("FileURL() = %v, want nil error", err)
	}
	if got == "" {
		t.Fatal("FileURL() = \"\", want a URL - the caller should still get its best guess")
	}

	key := Key{Module: "github.com/google/licenseclassifier/v2", Version: "v2.0.0", Path: "LICENSE"}
	if cached, ok := c.URL(key); ok {
		t.Errorf("cached an unverified URL %q, want nothing cached", cached)
	}
}

func TestFileURLWithoutCacheStillResolves(t *testing.T) {
	lib := &Library{
		Packages:    []string{"github.com/google/trillian"},
		LicenseFile: "/modcache/github.com/google/trillian/LICENSE",
		module: &Module{
			Path:    "github.com/google/trillian",
			Version: "v1.2.3",
			Dir:     "/modcache/github.com/google/trillian",
		},
	}
	got, err := lib.FileURL(context.Background(), source.NewClientForTesting(), lib.LicenseFile)
	if err != nil {
		t.Fatalf("FileURL() = %v, want nil error", err)
	}
	const want = "https://github.com/google/trillian/blob/v1.2.3/LICENSE"
	if got != want {
		t.Errorf("FileURL() = %q, want %q", got, want)
	}
}
