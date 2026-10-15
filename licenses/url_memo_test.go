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
	"sync"
	"testing"

	"github.com/google/go-licenses/v2/internal/third_party/pkgsite/source"
)

// vanityLib builds a library whose module path needs a meta-tag fetch to
// resolve, so resolution is observable by whether it was attempted.
func vanityLib(memo *urlMemo, c Cache) *Library {
	return &Library{
		Packages:    []string{"gopkg.in/inf.v0"},
		LicenseFile: "/modcache/gopkg.in/inf.v0/LICENSE",
		module: &Module{
			Path:    "gopkg.in/inf.v0",
			Version: "v0.9.1",
			Dir:     "/modcache/gopkg.in/inf.v0",
		},
		cache: c,
		memo:  memo,
	}
}

var vanityKey = Key{Module: "gopkg.in/inf.v0", Version: "v0.9.1", Path: "LICENSE"}

func TestFileURLUsesMemoizedSuccess(t *testing.T) {
	memo := newURLMemo()
	memo.record(vanityKey, "https://memoized.example/LICENSE", nil)
	lib := vanityLib(memo, nil)

	// A nil client cannot resolve anything, so a result at all proves the memo
	// was consulted before any resolution was attempted.
	got, err := lib.FileURL(context.Background(), nil, lib.LicenseFile)
	if err != nil {
		t.Fatalf("FileURL() = %v, want nil error", err)
	}
	if got != "https://memoized.example/LICENSE" {
		t.Errorf("FileURL() = %q, want the memoized URL", got)
	}
}

// TestFileURLUsesMemoizedFailure is the one that matters for run time. A
// failed lookup is deliberately never written to the persistent cache, so
// without an in-run memo every target sharing a dependency pays the HTTP
// timeout again.
func TestFileURLUsesMemoizedFailure(t *testing.T) {
	memo := newURLMemo()
	sentinel := fmt.Errorf("resolution already failed")
	memo.record(vanityKey, "", sentinel)
	lib := vanityLib(memo, nil)

	_, err := lib.FileURL(context.Background(), nil, lib.LicenseFile)
	if err == nil {
		t.Fatal("FileURL() = nil error, want the memoized failure")
	}
	if !errorsContain(err, sentinel.Error()) {
		t.Errorf("FileURL() = %v, want it to report the memoized failure %q", err, sentinel)
	}
}

func TestFileURLRecordsResolutionInMemo(t *testing.T) {
	memo := newURLMemo()
	lib := &Library{
		Packages:    []string{"github.com/google/trillian"},
		LicenseFile: "/modcache/github.com/google/trillian/LICENSE",
		module: &Module{
			Path:    "github.com/google/trillian",
			Version: "v1.2.3",
			Dir:     "/modcache/github.com/google/trillian",
		},
		memo: memo,
	}
	key := Key{Module: "github.com/google/trillian", Version: "v1.2.3", Path: "LICENSE"}

	if _, ok := memo.lookup(key); ok {
		t.Fatal("memo already holds the key before any resolution")
	}
	if _, err := lib.FileURL(context.Background(), source.NewClientForTesting(), lib.LicenseFile); err != nil {
		t.Fatalf("FileURL() = %v, want nil error", err)
	}
	remembered, ok := memo.lookup(key)
	if !ok {
		t.Fatal("resolution was not recorded in the memo")
	}
	if remembered.err != nil {
		t.Errorf("memo recorded an error %v, want none", remembered.err)
	}
	if remembered.url == "" {
		t.Error("memo recorded an empty URL")
	}
}

func TestURLMemoIsSafeForConcurrentUse(t *testing.T) {
	memo := newURLMemo()
	var wg sync.WaitGroup
	for i := range 16 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			k := Key{Module: fmt.Sprintf("example.com/m%d", i%4), Version: "v1.0.0", Path: "LICENSE"}
			memo.record(k, "https://example.com/x", nil)
			memo.lookup(k)
		}(i)
	}
	wg.Wait()
}

func errorsContain(err error, want string) bool {
	return err != nil && len(want) > 0 && contains(err.Error(), want)
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// countingCache records how often a URL lookup is attempted.
type countingCache struct {
	memCache
	urlLookups int
}

func (c *countingCache) URL(k Key) (string, bool) {
	c.urlLookups++
	return c.memCache.URL(k)
}

// TestFileURLResolvesOncePerRun is the behaviour that bounds run time: two
// targets sharing a dependency must resolve its URL once between them, not
// once each. Resolution is short-circuited by the memo before it reaches even
// the cache, so counting cache lookups counts resolution attempts.
func TestFileURLResolvesOncePerRun(t *testing.T) {
	memo := newURLMemo()
	cache := &countingCache{memCache: *newMemCache()}

	// Two separate Library values, as two targets would produce, sharing the
	// run's memo.
	for i := range 2 {
		lib := &Library{
			Packages:    []string{"github.com/google/trillian"},
			LicenseFile: "/modcache/github.com/google/trillian/LICENSE",
			module: &Module{
				Path:    "github.com/google/trillian",
				Version: "v1.2.3",
				Dir:     "/modcache/github.com/google/trillian",
			},
			cache: cache,
			memo:  memo,
		}
		if _, err := lib.FileURL(context.Background(), source.NewClientForTesting(), lib.LicenseFile); err != nil {
			t.Fatalf("FileURL() call %d = %v, want nil error", i, err)
		}
	}

	if cache.urlLookups != 1 {
		t.Errorf("resolution attempted %d times across two targets, want 1", cache.urlLookups)
	}
}
