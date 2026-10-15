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
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// countingClassifier classifies by file name and counts what it was asked to
// classify, so a test can assert that work is not repeated.
type countingClassifier struct {
	mu    sync.Mutex
	calls map[string]int
}

func newCountingClassifier() *countingClassifier {
	return &countingClassifier{calls: map[string]int{}}
}

func (c *countingClassifier) Identify(licensePath string) ([]License, error) {
	if licensePath == "" {
		return nil, nil
	}
	c.mu.Lock()
	c.calls[licensePath]++
	c.mu.Unlock()

	if strings.HasPrefix(strings.ToUpper(filepath.Base(licensePath)), "LICENSE") {
		return []License{{Name: "Apache-2.0", Type: Notice}}, nil
	}
	return nil, nil
}

func (c *countingClassifier) total() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, v := range c.calls {
		n += v
	}
	return n
}

// libShape is the externally visible part of a Library, for comparing results
// across different ways of producing them.
type libShape struct {
	Name        string
	LicenseFile string
	Licenses    []License
}

func shapes(libs []*Library) []libShape {
	out := make([]libShape, 0, len(libs))
	for _, l := range libs {
		out = append(out, libShape{l.Name(), l.LicenseFile, l.Licenses})
	}
	return out
}

const (
	pkgTestdata = "github.com/google/go-licenses/v2/licenses/testdata"
	pkgDirect   = "github.com/google/go-licenses/v2/licenses/testdata/direct"
	pkgIndirect = "github.com/google/go-licenses/v2/licenses/testdata/indirect"
)

// testdataClassifier is the stub from library_test.go, as a Classifier.
func testdataClassifier() classifierStub {
	notice := []License{{Name: "foo", Type: Notice}}
	return classifierStub{
		licenses: map[string][]License{
			"testdata/LICENSE":          notice,
			"testdata/direct/LICENSE":   notice,
			"testdata/indirect/LICENSE": notice,
		},
	}
}

// TestPerTargetMatchesPerInvocation is the invariant that makes one invocation
// a safe substitute for several: each target's library set must be exactly what
// that target alone would have produced.
func TestPerTargetMatchesPerInvocation(t *testing.T) {
	ctx := context.Background()
	classifier := testdataClassifier()

	// These two targets have deliberately different dependency sets.
	patterns := []string{pkgTestdata, pkgDirect, pkgIndirect}

	got, err := FindLibrariesPerTarget(ctx, Options{Classifier: classifier}, patterns...)
	if err != nil {
		t.Fatalf("FindLibrariesPerTarget() = %v, want nil error", err)
	}
	if len(got) != len(patterns) {
		t.Fatalf("FindLibrariesPerTarget() returned %d targets, want %d", len(got), len(patterns))
	}

	for i, pattern := range patterns {
		want, err := FindLibraries(ctx, Options{Classifier: classifier}, pattern)
		if err != nil {
			t.Fatalf("FindLibraries(%q) = %v, want nil error", pattern, err)
		}
		if got[i].Target.Pattern != pattern {
			t.Errorf("target %d has Pattern %q, want %q - results must be in argument order",
				i, got[i].Target.Pattern, pattern)
		}
		if diff := cmp.Diff(shapes(want), shapes(got[i].Libraries)); diff != "" {
			t.Errorf("target %q differs from its own invocation (-want +got):\n%s", pattern, diff)
		}
	}
}

// TestPerTargetSetsDiffer guards against the union bug this feature exists to
// fix: if every target got the same libraries, the test above would pass
// trivially.
func TestPerTargetSetsDiffer(t *testing.T) {
	got, err := FindLibrariesPerTarget(context.Background(),
		Options{Classifier: testdataClassifier()}, pkgTestdata, pkgIndirect)
	if err != nil {
		t.Fatalf("FindLibrariesPerTarget() = %v, want nil error", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d targets, want 2", len(got))
	}
	if len(got[0].Libraries) == len(got[1].Libraries) {
		t.Fatalf("both targets have %d libraries; the deeper target must have more, "+
			"otherwise this is the union, not per-target resolution", len(got[0].Libraries))
	}
	if len(got[0].Libraries) <= len(got[1].Libraries) {
		t.Errorf("testdata has %d libraries and indirect has %d, want testdata to have more",
			len(got[0].Libraries), len(got[1].Libraries))
	}
}

func TestPerTargetPopulatesTemplateFields(t *testing.T) {
	got, err := FindLibrariesPerTarget(context.Background(),
		Options{Classifier: testdataClassifier()}, pkgDirect)
	if err != nil {
		t.Fatalf("FindLibrariesPerTarget() = %v, want nil error", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d targets, want 1", len(got))
	}

	// Derive the module path from the package paths above instead of hardcoding
	// it, so these assertions survive the module being renamed - which is what
	// a fork does.
	module := strings.TrimSuffix(pkgTestdata, "/licenses/testdata")

	// The last element of the module path, skipping a major version element
	// such as "v2". Computed here independently of the implementation.
	elems := strings.Split(module, "/")
	wantModuleBase := elems[len(elems)-1]
	if regexp.MustCompile(`^v[0-9]+$`).MatchString(wantModuleBase) && len(elems) > 1 {
		wantModuleBase = elems[len(elems)-2]
	}

	tgt := got[0].Target
	for _, test := range []struct {
		field, got, want string
	}{
		{"Pattern", tgt.Pattern, pkgDirect},
		{"Path", tgt.Path, pkgDirect},
		{"Base", tgt.Base, "direct"},
		{"Dir", tgt.Dir, pkgTestdata},
		{"Module", tgt.Module, module},
		{"ModuleBase", tgt.ModuleBase, wantModuleBase},
		{"Rel", tgt.Rel, "licenses/testdata/direct"},
	} {
		if test.got != test.want {
			t.Errorf("Target.%s = %q, want %q", test.field, test.got, test.want)
		}
	}
}

// TestPerTargetClassifiesSharedCandidatesOnce is the reason per-target
// resolution belongs in one process: the targets overlap heavily, and a
// candidate shared between them must not be classified twice.
func TestPerTargetClassifiesSharedCandidatesOnce(t *testing.T) {
	counting := newCountingClassifier()
	if _, err := FindLibrariesPerTarget(context.Background(),
		Options{Classifier: counting}, pkgTestdata, pkgDirect, pkgIndirect); err != nil {
		t.Fatalf("FindLibrariesPerTarget() = %v, want nil error", err)
	}

	counting.mu.Lock()
	defer counting.mu.Unlock()
	for path, n := range counting.calls {
		if n != 1 {
			t.Errorf("candidate %s classified %d times, want 1", path, n)
		}
	}
	if len(counting.calls) == 0 {
		t.Fatal("nothing was classified, so the test proves nothing")
	}
}

func TestPerTargetMainOnlyFiltersLibraryPackages(t *testing.T) {
	ctx := context.Background()
	opts := Options{Classifier: testdataClassifier(), MainOnly: true}

	// None of the testdata packages are main packages, so all are filtered out.
	got, err := FindLibrariesPerTarget(ctx, opts, pkgDirect, pkgIndirect)
	if err != nil {
		t.Fatalf("FindLibrariesPerTarget() = %v, want nil error", err)
	}
	if len(got) != 0 {
		t.Errorf("MainOnly kept %d non-main targets, want 0", len(got))
	}
}
