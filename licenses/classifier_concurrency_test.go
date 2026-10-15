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
	"testing"

	"github.com/google/go-cmp/cmp"
)

// TestIdentifyIsSafeForConcurrentUse pins the invariant that Libraries relies
// on: it classifies candidates from an errgroup, all sharing one Classifier.
//
// Run this with -race. licenseclassifier's Classifier is not itself safe for
// concurrent use, so without serialization inside Identify this races in
// sergi/go-diff and can mis-score a license.
func TestIdentifyIsSafeForConcurrentUse(t *testing.T) {
	c, err := NewClassifier()
	if err != nil {
		t.Fatalf("NewClassifier() = %v, want nil error", err)
	}

	// Several files, so goroutines contend on the corpus rather than each
	// repeating identical work.
	files := []string{
		"testdata/LICENSE",
		"testdata/direct/LICENSE",
		"testdata/indirect/LICENSE",
		"testdata/MIT/LICENSE.MIT",
	}

	// Classify once, serially, to establish the expected answers.
	want := map[string][]License{}
	for _, f := range files {
		found, err := c.Identify(f)
		if err != nil {
			t.Fatalf("Identify(%q) = %v, want nil error", f, err)
		}
		if len(found) == 0 {
			t.Fatalf("Identify(%q) found no license; the fixture is not useful", f)
		}
		want[f] = found
	}

	var wg sync.WaitGroup
	got := make([]map[string][]License, 8)
	for i := range got {
		got[i] = map[string][]License{}
		wg.Add(1)
		go func(out map[string][]License) {
			defer wg.Done()
			for _, f := range files {
				found, err := c.Identify(f)
				if err != nil {
					t.Errorf("Identify(%q) = %v, want nil error", f, err)
					return
				}
				out[f] = found
			}
		}(got[i])
	}
	wg.Wait()

	// Concurrency must not change the answers either.
	for i, g := range got {
		if diff := cmp.Diff(want, g); diff != "" {
			t.Errorf("goroutine %d classified differently (-want +got):\n%s", i, diff)
		}
	}
}
