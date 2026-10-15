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
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestReadGoSum(t *testing.T) {
	dir := t.TempDir()
	name := filepath.Join(dir, "go.sum")
	// Real go.sum shape: every module has a tree hash and a go.mod hash, and
	// only the tree hash describes the content whose licenses we classify.
	content := `github.com/go-logr/logr v1.2.0 h1:QK40JKJyMdUDz+h+xvCsru/bJhvG0UxvePV0ufL/AcE=
github.com/go-logr/logr v1.2.0/go.mod h1:jdQByPbusPIv2/zmleS9BjJVeZ6kBagPoEUsqbVz/1A=
k8s.io/klog/v2 v2.90.1 h1:m4bYOKall2MmOiRaR1J+We67Do7vm9KiQVlGlD9hHqc=
k8s.io/klog/v2 v2.90.1/go.mod h1:y1WjHnz7Dj687irZUWR/WLkLc5N1YHtjLdmgWjndZn0=

golang.org/x/mod v0.27.0 h1:kb+q2PyFnEADO2IEF935ehFUXlWiNjJWtRNgBLSfbxQ=
`
	if err := os.WriteFile(name, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}

	got, err := ReadGoSum(name)
	if err != nil {
		t.Fatalf("ReadGoSum() = %v, want nil error", err)
	}
	want := map[string]string{
		"github.com/go-logr/logr@v1.2.0": "h1:QK40JKJyMdUDz+h+xvCsru/bJhvG0UxvePV0ufL/AcE=",
		"k8s.io/klog/v2@v2.90.1":         "h1:m4bYOKall2MmOiRaR1J+We67Do7vm9KiQVlGlD9hHqc=",
		"golang.org/x/mod@v0.27.0":       "h1:kb+q2PyFnEADO2IEF935ehFUXlWiNjJWtRNgBLSfbxQ=",
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("ReadGoSum() diff (-want +got):\n%s", diff)
	}
}

func TestReadGoSumMissingFileIsNotAnError(t *testing.T) {
	// Vendor mode and non-module setups have no go.sum. Hash validation is
	// then skipped rather than failing the run.
	got, err := ReadGoSum(filepath.Join(t.TempDir(), "go.sum"))
	if err != nil {
		t.Errorf("ReadGoSum() = %v, want nil error for a missing go.sum", err)
	}
	if len(got) != 0 {
		t.Errorf("ReadGoSum() = %v, want no hashes", got)
	}
}

func TestFindGoSumWalksUp(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.sum"), []byte(""), 0600); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(root, "cmd", "plugins", "topology-aware")
	if err := os.MkdirAll(nested, 0700); err != nil {
		t.Fatal(err)
	}

	// go-licenses is run from a subdirectory as often as from the module root.
	got, err := FindGoSum(nested)
	if err != nil {
		t.Fatalf("FindGoSum() = %v, want nil error", err)
	}
	if want := filepath.Join(root, "go.sum"); got != want {
		t.Errorf("FindGoSum() = %q, want %q", got, want)
	}
}

func TestFindGoSumReturnsEmptyWhenAbsent(t *testing.T) {
	got, err := FindGoSum(t.TempDir())
	if err != nil {
		t.Errorf("FindGoSum() = %v, want nil error", err)
	}
	if got != "" {
		t.Errorf("FindGoSum() = %q, want an empty path", got)
	}
}
