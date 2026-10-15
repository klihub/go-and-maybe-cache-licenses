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
	"sync"
	"sync/atomic"
	"testing"

	licenseclassifier "github.com/google/licenseclassifier/v2"
	"github.com/google/licenseclassifier/v2/assets"
)

// TestClassifierLoadsCorpusLazily pins the property the cache depends on:
// loading the license corpus costs about a second, so a run whose candidates
// all hit the cache must never pay for it.
func TestClassifierLoadsCorpusLazily(t *testing.T) {
	var loads atomic.Int32
	load := func() (*licenseclassifier.Classifier, error) {
		loads.Add(1)
		return assets.DefaultClassifier()
	}

	c := newClassifier(load)
	if got := loads.Load(); got != 0 {
		t.Fatalf("corpus loaded %d times on construction, want 0", got)
	}

	// An empty path short-circuits, so it must not load the corpus either.
	if _, err := c.Identify(""); err != nil {
		t.Fatalf("Identify(\"\") = %v, want nil error", err)
	}
	if got := loads.Load(); got != 0 {
		t.Fatalf("corpus loaded %d times for an empty path, want 0", got)
	}

	found, err := c.Identify("testdata/LICENSE")
	if err != nil {
		t.Fatalf("Identify() = %v, want nil error", err)
	}
	if len(found) == 0 {
		t.Error("Identify() found no license in testdata/LICENSE")
	}
	if got := loads.Load(); got != 1 {
		t.Fatalf("corpus loaded %d times after the first classification, want 1", got)
	}

	if _, err := c.Identify("testdata/direct/LICENSE"); err != nil {
		t.Fatalf("Identify() = %v, want nil error", err)
	}
	if got := loads.Load(); got != 1 {
		t.Errorf("corpus loaded %d times across two classifications, want 1", got)
	}
}

// TestClassifierLoadsCorpusOnceUnderConcurrency covers the errgroup fan-out in
// Libraries, which classifies many candidates at once.
func TestClassifierLoadsCorpusOnceUnderConcurrency(t *testing.T) {
	var loads atomic.Int32
	c := newClassifier(func() (*licenseclassifier.Classifier, error) {
		loads.Add(1)
		return assets.DefaultClassifier()
	})

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := c.Identify("testdata/LICENSE"); err != nil {
				t.Errorf("Identify() = %v, want nil error", err)
			}
		}()
	}
	wg.Wait()

	if got := loads.Load(); got != 1 {
		t.Errorf("corpus loaded %d times under concurrent classification, want 1", got)
	}
}
