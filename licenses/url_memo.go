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

import "sync"

// urlMemo remembers license URL resolutions for the duration of one run,
// including the ones that failed.
//
// Failures are deliberately never written to the persistent cache, so that a
// transient network failure cannot become a permanently wrong cached URL. But
// without remembering them for the run, every target that shares an
// unreachable dependency re-attempts the lookup and pays the HTTP timeout
// again. Resolving several targets in one run turns that into one timeout per
// target rather than one in total.
//
// The memo lives in memory and dies with the process, so it never makes a
// failure outlive the conditions that caused it.
type urlMemo struct {
	mu      sync.Mutex
	results map[Key]urlResult
}

type urlResult struct {
	url string
	err error
}

func newURLMemo() *urlMemo {
	return &urlMemo{results: map[Key]urlResult{}}
}

// lookup returns a remembered resolution, if there is one.
//
// The result is returned whole rather than as separate values: the error it
// carries is the remembered outcome, not a failure of the lookup, and
// returning it on its own invites callers - and linters - to read it as one.
func (m *urlMemo) lookup(key Key) (urlResult, bool) {
	if m == nil {
		return urlResult{}, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.results[key]
	return r, ok
}

// record remembers a resolution, successful or not.
func (m *urlMemo) record(key Key, url string, err error) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.results[key] = urlResult{url: url, err: err}
}
