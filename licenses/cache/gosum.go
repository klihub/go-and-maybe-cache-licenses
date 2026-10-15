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
	"bufio"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
)

// classifierModule is the module whose version gates cached classifications.
const classifierModule = "github.com/google/licenseclassifier/v2"

// ReadGoSum returns the module tree hashes recorded in a go.sum file, keyed by
// "module@version".
//
// The "/go.mod" hashes are skipped: they cover only the go.mod file, whereas a
// cached classification describes the module's extracted tree.
//
// A missing go.sum is not an error. Vendor mode and non-module setups have
// none, and hash validation is then simply skipped.
func ReadGoSum(name string) (map[string]string, error) {
	hashes := map[string]string{}
	if name == "" {
		return hashes, nil
	}
	f, err := os.Open(name)
	if err != nil {
		if os.IsNotExist(err) {
			return hashes, nil
		}
		return nil, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 3 {
			continue
		}
		modulePath, version, hash := fields[0], fields[1], fields[2]
		if strings.HasSuffix(version, "/go.mod") {
			continue
		}
		hashes[modulePath+"@"+version] = hash
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return hashes, nil
}

// FindGoSum returns the path of the go.sum governing dir, searching dir and its
// parents. It returns an empty path when there is none, since go-licenses is
// often run from a subdirectory of the module root.
func FindGoSum(dir string) (string, error) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	for {
		name := filepath.Join(dir, "go.sum")
		if _, err := os.Stat(name); err == nil {
			return name, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", nil
		}
		dir = parent
	}
}

// ClassifierVersion returns the version of the license classifier this binary
// was built against, which gates cached classifications: a new corpus can
// change verdicts, so entries written by a different version are not trusted.
//
// It returns "unknown" when build information is unavailable, which keeps the
// cache self-consistent for that build rather than silently disabling
// validation.
func ClassifierVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	for _, dep := range info.Deps {
		if dep.Path == classifierModule {
			if dep.Version != "" {
				return dep.Version
			}
			return "unknown"
		}
	}
	return "unknown"
}
