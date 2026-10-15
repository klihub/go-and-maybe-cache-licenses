// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      https://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package source

import (
	"context"
	"fmt"
	"net/http"
	"testing"
)

// roundTripFunc adapts a function to http.RoundTripper.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func clientWith(rt roundTripFunc) *Client {
	return &Client{httpClient: &http.Client{Transport: rt}}
}

// A module with a "/vN" suffix needs a probe to decide whether the versioned
// subdirectory is real. These tests pin what happens to that decision in each
// outcome, because a URL chosen without a definitive answer must not be cached.
func TestUnverifiedModuleDir(t *testing.T) {
	const modulePath = "github.com/google/licenseclassifier/v2"

	for _, test := range []struct {
		desc           string
		rt             roundTripFunc
		wantDir        string
		wantUnverified bool
	}{
		{
			desc: "versioned subdirectory exists",
			rt: func(r *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Body: http.NoBody}, nil
			},
			wantDir:        "v2",
			wantUnverified: false,
		},
		{
			desc: "server says the versioned subdirectory is absent",
			rt: func(r *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 404, Body: http.NoBody}, nil
			},
			wantDir: "",
			// A 404 is a definitive answer, so the fallback is trustworthy.
			wantUnverified: false,
		},
		{
			desc: "no network",
			rt: func(r *http.Request) (*http.Response, error) {
				return nil, fmt.Errorf("dial tcp: network is unreachable")
			},
			wantDir: "",
			// The fallback may well be wrong, so it must be marked.
			wantUnverified: true,
		},
	} {
		t.Run(test.desc, func(t *testing.T) {
			info, err := ModuleInfo(context.Background(), clientWith(test.rt), modulePath, "v2.0.0")
			if err != nil {
				t.Fatalf("ModuleInfo() = %v, want nil error", err)
			}
			if info.moduleDir != test.wantDir {
				t.Errorf("moduleDir = %q, want %q", info.moduleDir, test.wantDir)
			}
			if got := info.UnverifiedModuleDir(); got != test.wantUnverified {
				t.Errorf("UnverifiedModuleDir() = %t, want %t", got, test.wantUnverified)
			}
		})
	}
}
