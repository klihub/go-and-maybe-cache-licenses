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

package main

import (
	"github.com/google/go-licenses/v2/licenses"
	"k8s.io/klog/v2"
)

// libraryOptions builds the options shared by the check, save and report
// commands.
//
// A cache that cannot be opened is reported and skipped rather than failing the
// run: the cache is an accelerator, never a precondition.
func libraryOptions() (licenses.Options, error) {
	classifier, err := licenses.NewClassifier()
	if err != nil {
		return licenses.Options{}, err
	}
	opts := licenses.Options{
		Classifier:   classifier,
		IncludeTests: includeTests,
		IgnoredPaths: ignore,
	}
	if !useCache {
		return opts, nil
	}
	store, dir, err := newCacheStore()
	if err != nil {
		klog.Warningf("Not using the license cache: %v", err)
		return opts, nil
	}
	klog.V(1).Infof("Using the license cache in %s.", dir)
	opts.Cache = store
	return opts, nil
}
