// Copyright 2022 Google LLC
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

// This file includes all local additions to source package for google/go-licenses use-cases.

// SetCommit overrides commit to a specified commit. Usually, you should pass your version to
// ModuleInfo(). However, when you do not know the version and just wants a link that points to
// a known commit/branch/tag. You can use this method to directly override the commit like
// info.SetCommit("master").
//
// Note this is different from directly passing "master" as version to ModuleInfo(), because for
// modules not at the root of a repo, there are conventions that add a module's relative dir in
// front of the version as the actual git tag. For example, for a sub module at ./submod whose
// version is v1.0.1, the actual git tag should be submod/v1.0.1.
func (i *Info) SetCommit(commit string) {
	if i == nil {
		return
	}
	i.commit = commit
}

// UnverifiedModuleDir reports whether this Info's module directory was chosen
// without a definitive answer from the repository.
//
// For a module whose path ends in "/vN", adjustVersionedModuleDirectory probes
// the repository to decide whether that versioned subdirectory really exists,
// and assumes the unversioned directory when it cannot tell. With no network
// the probe always fails, so the assumption is made blindly and the resulting
// URL may be wrong - for example ".../blob/v2.0.0/LICENSE" where the correct
// URL is ".../blob/v2.0.0/v2/LICENSE".
//
// Callers that persist URLs must not store one for which this returns true, or
// a transient lack of network becomes a permanently wrong cached URL.
func (i *Info) UnverifiedModuleDir() bool {
	if i == nil {
		return false
	}
	return i.unverifiedModuleDir
}
