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
	"os/exec"
	"path/filepath"
	"testing"
)

// buildGoLicenses builds the CLI into a temporary directory and returns its
// path.
func buildGoLicenses(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "go-licenses")
	cmd := exec.Command("go", "build", "-o", path)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("building go-licenses:\n%s", out)
	}
	return path
}

// runGL runs go-licenses and returns stdout, failing the test on error.
func runGL(t *testing.T, bin string, args ...string) string {
	t.Helper()
	cmd := exec.Command(bin, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go-licenses %v: %v\n%s", args, err, stderr.String())
	}
	return string(out)
}
