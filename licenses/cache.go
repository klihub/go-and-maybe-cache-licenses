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

// Key identifies one candidate license file inside one module version.
//
// Only modules with a non-empty Version are cacheable. An empty version means
// the module is the main module under development, replaced by a local
// directory, or vendored, none of which are immutable.
type Key struct {
	// Module is the module path, e.g. "k8s.io/klog/v2".
	Module string
	// Version is the module version, e.g. "v2.90.1".
	Version string
	// Path is the candidate file's path relative to the module root,
	// e.g. "LICENSE".
	Path string
}

// Cacheable reports whether the key refers to an immutable module version that
// may be cached.
func (k Key) Cacheable() bool {
	return k.Module != "" && k.Version != "" && k.Path != ""
}

// Cache memoizes license classification and URL resolution across runs.
//
// All methods must be safe for concurrent use: Libraries classifies candidates
// from an errgroup, and several go-licenses processes may share one cache.
//
// A cache is an accelerator and must never fail a run. Implementations report
// errors from the Put methods so callers can log them, but callers treat them
// as non-fatal.
type Cache interface {
	// Licenses returns the cached classification for a candidate file.
	// ok is false on a miss. A cached empty classification - a file that was
	// examined and found to contain no license - returns (nil, true).
	Licenses(Key) (licenses []License, ok bool)

	// PutLicenses stores a classification, which may be empty.
	PutLicenses(Key, []License) error

	// URL returns the cached license URL for a license file.
	URL(Key) (string, bool)

	// PutURL stores a resolved license URL.
	PutURL(Key, string) error
}
