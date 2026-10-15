// Copyright 2019 Google Inc. All Rights Reserved.
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
	"fmt"
	"go/build"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"

	"github.com/google/go-licenses/v2/internal/third_party/pkgsite/source"
	"golang.org/x/sync/errgroup"
	"golang.org/x/tools/go/packages"
	"k8s.io/klog/v2"
)

// Library is a collection of packages covered by the same license file.
type Library struct {
	// LicenseFile is the path of the file containing the library's license.
	LicenseFile string
	// Packages contains import paths for Go packages in this library.
	// It may not be the complete set of all packages in the library.
	Packages []string
	// Parent go module.
	module *Module
	// List of licenses for found at the LicenseFile.
	Licenses []License
	// cache memoizes license URL resolution across runs. Set by
	// FindLibraries; nil when caching is disabled.
	cache Cache
	// memo remembers URL resolutions within one run, including failures.
	// Set by FindLibraries.
	memo *urlMemo
}

// PackagesError aggregates all Packages[].Errors into a single error.
type PackagesError struct {
	pkgs []*packages.Package
}

func (e PackagesError) Error() string {
	var str strings.Builder
	fmt.Fprintf(&str, "errors for %q:", e.pkgs)
	packages.Visit(e.pkgs, nil, func(pkg *packages.Package) {
		for _, err := range pkg.Errors {
			fmt.Fprintf(&str, "\n%s: %s", pkg.PkgPath, err)
		}
	})
	return str.String()
}

// Options configures library discovery.
type Options struct {
	// Classifier identifies the licenses in a candidate file.
	Classifier Classifier
	// Cache memoizes classification across runs. A nil Cache disables caching.
	Cache Cache
	// IncludeTests includes packages only imported by testing code.
	IncludeTests bool
	// IgnoredPaths are package path prefixes to ignore. Dependencies of the
	// ignored packages are still checked.
	IgnoredPaths []string
	// Progress, when non-nil, is called as candidate files are classified,
	// with the number finished and the total. The total is known before
	// classification starts. It may be called from several goroutines.
	Progress func(done, total int)
	// MainOnly restricts per-target resolution to main packages, which is
	// useful when a wildcard pattern expands to library packages that are not
	// shipped. It has no effect on FindLibraries.
	MainOnly bool
}

// Libraries returns the collection of libraries used by this package, directly or transitively.
// A library is a collection of one or more packages covered by the same license file.
// Packages not covered by a license will be returned as individual libraries.
// Standard library packages will be ignored.
//
// Deprecated: use FindLibraries, which additionally accepts a Cache.
func Libraries(ctx context.Context, classifier Classifier, includeTests bool, ignoredPaths []string, importPaths ...string) ([]*Library, error) {
	return FindLibraries(ctx, Options{
		Classifier:   classifier,
		IncludeTests: includeTests,
		IgnoredPaths: ignoredPaths,
	}, importPaths...)
}

// pkgInfo describes one package that needs a license.
type pkgInfo struct {
	// pkgPath is the import path of the package.
	pkgPath string
	// modulePath is the module path of the package.
	modulePath string

	// pkgDir is the directory containing the package's source code.
	pkgDir string
	// moduleDir is the directory containing the module's source code.
	moduleDir string
}

// resolution is the part of library discovery that does not depend on which
// target is being reported: the loaded package graph, the candidate files, and
// their classifications.
//
// Splitting it out is what lets one invocation serve several targets. Loading
// all targets at once costs barely more than loading one, since they share
// nearly all their dependencies, and each candidate is then classified once for
// all of them.
type resolution struct {
	opts          Options
	memo          *urlMemo
	roots         []*packages.Package
	allModules    map[string]*Module
	allPackages   []pkgInfo
	pkgCandidates map[string][]string  // package dir -> candidate files
	foundLicenses map[string][]License // candidate file -> licenses, non-empty only
}

// FindLibraries returns the collection of libraries used by the given packages,
// directly or transitively, consulting opts.Cache to avoid reclassifying
// candidate files it has already seen.
func FindLibraries(ctx context.Context, opts Options, importPaths ...string) ([]*Library, error) {
	res, err := resolve(ctx, opts, importPaths...)
	if err != nil {
		return nil, err
	}
	return res.librariesFor(res.allPackages), nil
}

// resolve runs the phases shared by every target.
func resolve(ctx context.Context, opts Options, importPaths ...string) (*resolution, error) {
	classifier := opts.Classifier
	includeTests := opts.IncludeTests
	ignoredPaths := opts.IgnoredPaths
	// These are the steps we take to find libraries:
	// 1. we list all modules and all packages
	// 2. for each package, we find a list of candidates
	// 3. we deduplicate all candidates
	// 4. for each candidate, we classify if the candidate is a license file
	// 5. for each package, we select the first candidates that is a license
	//    file & add the package to a list of packages for that license file
	// 6. we return an array of libraries (which are the license files, the
	//    found licenses in that file, all the packages that had that file as
	//    its first candidate and the module in which those packages live)

	cfg := &packages.Config{
		Context: ctx,
		Mode:    packages.NeedImports | packages.NeedDeps | packages.NeedFiles | packages.NeedName | packages.NeedModule,
		Tests:   includeTests,
	}

	rootPkgs, err := packages.Load(cfg, importPaths...)
	if err != nil {
		return nil, err
	}

	vendoredSearch := []*Module{}
	for _, parentPkg := range rootPkgs {
		if parentPkg.Module == nil {
			continue
		}

		module := newModule(parentPkg.Module)
		if module.Dir == "" {
			continue
		}

		vendoredSearch = append(vendoredSearch, module)
	}

	allModules := map[string]*Module{}
	allPackages := []pkgInfo{}
	{
		pkgErrorOccurred := false
		otherErrorOccurred := false
		packages.Visit(rootPkgs, func(p *packages.Package) bool {
			if len(p.Errors) > 0 {
				pkgErrorOccurred = true
				return false
			}
			if isStdLib(p) {
				// No license requirements for the Go standard library.
				return false
			}
			if includeTests && isTestBinary(p) {
				// A test binary only imports the standard library, so we do not need to check its license.
				// Moreover, Find below will return an error because pkgDir is not under p.Module.Dir
				// as pkgDir is under GOCACHE instead.
				return false
			}
			for _, i := range ignoredPaths {
				if strings.HasPrefix(p.PkgPath, i) {
					// Marked to be ignored.
					return true
				}
			}

			if len(p.OtherFiles) > 0 {
				klog.Warningf("%q contains non-Go code that can't be inspected for further dependencies:\n%s", p.PkgPath, strings.Join(p.OtherFiles, "\n"))
			}

			var pkgDir string
			switch {
			case len(p.GoFiles) > 0:
				pkgDir = filepath.Dir(p.GoFiles[0])
			case len(p.CompiledGoFiles) > 0:
				pkgDir = filepath.Dir(p.CompiledGoFiles[0])
			case len(p.OtherFiles) > 0:
				pkgDir = filepath.Dir(p.OtherFiles[0])
			default:
				// This package is empty - nothing to do.
				return true
			}

			if p.Module == nil {
				otherErrorOccurred = true
				klog.Errorf("Package %s does not have module info. Non go modules projects are no longer supported. For feedback, refer to https://github.com/google/go-licenses/issues/128.", p.PkgPath)
				return false
			}

			module := newModule(p.Module)

			if module.Dir == "" {
				// A known cause is that the module is vendored, so some information is lost.
				isVendored := strings.Contains(pkgDir, "/vendor/")
				if !isVendored {
					klog.Warningf("module %s does not have dir and it's not vendored, cannot discover the license URL. Report to go-licenses developer if you see this.", module.Path)
				} else {
					// This is vendored. Handle this known special case.

					// Extra note why we identify a vendored package like this.
					//
					// For a normal package:
					// * if it's not in a module, lib.module == nil
					// * if it's in a module, lib.module.Dir != ""
					// Only vendored modules will have lib.module != nil && lib.module.Path != "" && lib.module.Dir == "" as far as I know.
					// So the if condition above is already very strict for vendored packages.
					// On top of it, we checked the lib.LicensePath contains a vendor folder in it.
					// So it's rare to have a false positive for both conditions at the same time, although it may happen in theory.
					//
					// These assumptions may change in the future,
					// so we need to keep this updated with go tooling changes.
					for _, parentModule := range vendoredSearch {
						if strings.HasPrefix(pkgDir, parentModule.Dir) {
							module = parentModule
							break
						}
					}

					if module.Dir == "" {
						klog.Warningf("cannot find parent package of vendored module %s", module.Path)
					}
				}
			}

			allPackages = append(allPackages, pkgInfo{
				pkgPath:    p.PkgPath,
				modulePath: module.Path,
				pkgDir:     pkgDir,
				moduleDir:  module.Dir,
			})
			allModules[module.Path] = module

			return true
		}, nil)
		if pkgErrorOccurred {
			return nil, PackagesError{
				pkgs: rootPkgs,
			}
		}
		if otherErrorOccurred {
			return nil, fmt.Errorf("some errors occurred when loading direct and transitive dependency packages")
		}
	}

	pkgCandidates := map[string][]string{}
	allCandidates := map[string]struct{}{}
	// candidateKeys maps a candidate file to its cache key. A candidate always
	// lies inside exactly one module directory, so the mapping is unambiguous.
	// Candidates of uncacheable modules get a zero Key, which Key.Cacheable
	// rejects.
	candidateKeys := map[string]Key{}
	for _, pkg := range allPackages {
		candidates, err := FindCandidates(pkg.pkgDir, pkg.moduleDir)
		if err != nil {
			return nil, err
		}

		pkgCandidates[pkg.pkgDir] = candidates
		for _, candidate := range candidates {
			allCandidates[candidate] = struct{}{}
			if _, ok := candidateKeys[candidate]; !ok {
				candidateKeys[candidate] = cacheKey(allModules[pkg.modulePath], pkg.moduleDir, candidate)
			}
		}
	}

	group, _ := errgroup.WithContext(ctx)
	foundLicenseSlice := make([]struct {
		candidate string
		licenes   []License
	}, len(allCandidates))
	// Classification is the slow phase on a cold cache, and its size is known
	// here, so progress over it is a real proportion.
	totalCandidates := len(allCandidates)
	var classified atomic.Int64
	reportProgress := func() {
		if opts.Progress != nil {
			opts.Progress(int(classified.Add(1)), totalCandidates)
		}
	}
	counter := 0
	for candidate := range allCandidates {
		idx := counter
		counter++
		candidate := candidate

		group.Go(func() error {
			key := candidateKeys[candidate]
			cacheable := opts.Cache != nil && key.Cacheable()

			if cacheable {
				if cached, ok := opts.Cache.Licenses(key); ok {
					foundLicenseSlice[idx] = struct {
						candidate string
						licenes   []License
					}{
						candidate: candidate,
						licenes:   cached,
					}
					reportProgress()
					return nil
				}
			}

			licenses, err := classifier.Identify(candidate)
			if err != nil {
				klog.Errorf("Failed to parse %s: %v", candidate, err)
				reportProgress()
				return nil // Continue even if one LICENSE file fails to parse.
			}

			if cacheable {
				// Store negative results too: most candidates are READMEs with
				// no license, and not reclassifying those is most of the gain.
				// A cache failure must never fail the run.
				if err := opts.Cache.PutLicenses(key, licenses); err != nil {
					klog.Warningf("Failed to cache license info for %s: %v", candidate, err)
				}
			}

			foundLicenseSlice[idx] = struct {
				candidate string
				licenes   []License
			}{
				candidate: candidate,
				licenes:   licenses,
			}
			reportProgress()
			return nil
		})
	}

	if err := group.Wait(); err != nil {
		return nil, err
	}

	foundLicenses := map[string][]License{}
	for _, found := range foundLicenseSlice {
		if len(found.licenes) == 0 {
			continue
		}

		foundLicenses[found.candidate] = found.licenes
	}

	return &resolution{
		opts:          opts,
		memo:          newURLMemo(),
		roots:         rootPkgs,
		allModules:    allModules,
		allPackages:   allPackages,
		pkgCandidates: pkgCandidates,
		foundLicenses: foundLicenses,
	}, nil
}

// librariesFor groups the given packages into libraries by the license file
// that covers them. This is the only phase that depends on which target is
// being reported.
func (r *resolution) librariesFor(pkgs []pkgInfo) []*Library {
	opts, allModules := r.opts, r.allModules
	pkgCandidates, foundLicenses := r.pkgCandidates, r.foundLicenses

	pkgsByLicense := make(map[string][]pkgInfo)
	for _, pkg := range pkgs {
		candidates := pkgCandidates[pkg.pkgDir]

		bestCandidate := ""
		for _, candidate := range candidates {
			if _, ok := foundLicenses[candidate]; ok {
				bestCandidate = candidate
				break
			}
		}

		pkgsByLicense[bestCandidate] = append(pkgsByLicense[bestCandidate], pkg)
	}

	var libraries []*Library
	for licenseFile, pkgs := range pkgsByLicense {
		if licenseFile == "" {
			// No license for these packages - return each one as a separate library.
			for _, p := range pkgs {
				libraries = append(libraries, &Library{
					Packages: []string{p.pkgPath},
					module:   allModules[p.modulePath],
					cache:    opts.Cache,
					memo:     r.memo,
				})
			}
			continue
		}

		lib := &Library{
			LicenseFile: licenseFile,
			Licenses:    foundLicenses[licenseFile],
			Packages:    make([]string, len(pkgs)),
			module:      allModules[pkgs[0].modulePath],
			cache:       opts.Cache,
			memo:        r.memo,
		}

		for i, p := range pkgs {
			lib.Packages[i] = p.pkgPath
		}

		libraries = append(libraries, lib)
	}

	// Sort libraries to produce a stable result for snapshot diffing.
	sort.Slice(libraries, func(i, j int) bool {
		return libraries[i].Name() < libraries[j].Name()
	})

	return libraries
}

// cacheKey builds the cache key for a candidate file inside a module.
//
// It returns the zero Key - which Key.Cacheable rejects - for anything mutable:
// a module with no version, which covers the main module under development and
// local replace directives, or a module with no directory, which covers
// vendored code.
func cacheKey(module *Module, moduleDir, candidate string) Key {
	if module == nil || module.Version == "" || moduleDir == "" {
		return Key{}
	}
	rel, err := filepath.Rel(moduleDir, candidate)
	if err != nil || rel == "" || strings.HasPrefix(rel, "..") {
		return Key{}
	}
	return Key{
		Module:  module.Path,
		Version: module.Version,
		Path:    filepath.ToSlash(rel),
	}
}

// Name is the common prefix of the import paths for all of the packages in this library.
func (l *Library) Name() string {
	return commonAncestor(l.Packages)
}

func commonAncestor(paths []string) string {
	if len(paths) == 0 {
		return ""
	}
	if len(paths) == 1 {
		return paths[0]
	}
	sort.Strings(paths)
	min, max := paths[0], paths[len(paths)-1]
	lastSlashIndex := 0
	for i := 0; i < len(min) && i < len(max); i++ {
		if min[i] != max[i] {
			return min[:lastSlashIndex]
		}
		if min[i] == '/' {
			lastSlashIndex = i
		}
	}
	return min
}

func (l *Library) String() string {
	return l.Name()
}

// FileURL attempts to determine the URL for a file in this library using
// go module name and version.
func (l *Library) FileURL(ctx context.Context, cl *source.Client, filePath string) (string, error) {
	if l == nil {
		return "", fmt.Errorf("library is nil")
	}
	wrap := func(err error) error {
		return fmt.Errorf("getting file URL in library %s: %w", l.Name(), err)
	}
	m := l.module
	if m == nil {
		return "", wrap(fmt.Errorf("empty go module info"))
	}
	if m.Dir == "" {
		return "", wrap(fmt.Errorf("empty go module dir"))
	}

	// Resolve the module-relative path first, so a cache hit can skip
	// source.ModuleInfo entirely. That is the whole point: ModuleInfo fetches
	// go-source meta tags over HTTP for module paths it does not recognise
	// statically, which is the only network access left in a license run.
	relativePath, err := filepath.Rel(m.Dir, filePath)
	if err != nil {
		return "", wrap(err)
	}
	key := Key{Module: m.Path, Version: m.Version, Path: filepath.ToSlash(relativePath)}

	// A resolution already attempted in this run is not attempted again, even
	// if it failed. Several targets usually share most of their dependencies,
	// and an unreachable one would otherwise cost a full HTTP timeout per
	// target. See urlMemo.
	if remembered, ok := l.memo.lookup(key); ok {
		if remembered.err != nil {
			return "", wrap(remembered.err)
		}
		return remembered.url, nil
	}

	url, err := l.resolveFileURL(ctx, cl, key, relativePath)
	l.memo.record(key, url, err)
	if err != nil {
		return "", wrap(err)
	}
	return url, nil
}

// resolveFileURL determines the URL for a file, consulting the cache before
// reaching out to the repository. It returns unwrapped errors so that each
// caller can attribute them to its own library.
func (l *Library) resolveFileURL(ctx context.Context, cl *source.Client, key Key, relativePath string) (string, error) {
	m := l.module

	cacheable := l.cache != nil && key.Cacheable()
	if cacheable {
		if url, ok := l.cache.URL(key); ok {
			return url, nil
		}
	}

	remote, err := source.ModuleInfo(ctx, cl, m.Path, m.Version)
	if err != nil {
		return "", err
	}
	if m.Version == "" {
		// This always happens for the module in development.
		// Note#1 if we pass version=HEAD to source.ModuleInfo, github tag for modules not at the root
		// of the repo will be incorrect, because there's a convention that:
		// * I have a module at github.com/google/go-licenses/submod.
		// * The module is of version v1.0.0.
		// Then the github tag should be submod/v1.0.0.
		// In our case, if we pass HEAD as version, the result commit will be submod/HEAD which is incorrect.
		// Therefore, to workaround this problem, we directly set the commit after getting module info.
		//
		// Note#2 repos have different branches as default, some use the
		// master branch and some use the main branch. However, HEAD
		// always refers to the default branch, so it's better than
		// both of master/main when we do not know which branch is default.
		// Examples:
		// * https://github.com/google/go-licenses/blob/HEAD/LICENSE
		// points to latest commit of master branch.
		// * https://github.com/google/licenseclassifier/blob/HEAD/LICENSE
		// points to latest commit of main branch.
		remote.SetCommit("HEAD")
		klog.Warningf("module %s has empty version, defaults to HEAD. The license URL may be incorrect. Please verify!", m.Path)
	}
	// TODO: there are still rare cases this may result in an incorrect URL.
	// https://github.com/google/go-licenses/issues/73#issuecomment-1005587408
	url := remote.FileURL(relativePath)

	// Never persist a URL the repository did not confirm. For a "/vN" module
	// the correct directory depends on a probe, and when that probe cannot
	// complete the result is a guess - caching it would turn a transient
	// network failure into a permanently wrong URL.
	if cacheable && url != "" && !remote.UnverifiedModuleDir() {
		if err := l.cache.PutURL(key, url); err != nil {
			klog.Warningf("Failed to cache license URL for %s/%s: %v", key.Module, key.Path, err)
		}
	}
	return url, nil
}

func (l *Library) Version() string {
	if l.module != nil {
		return l.module.Version
	}
	return ""
}

// isStdLib returns true if this package is part of the Go standard library.
func isStdLib(pkg *packages.Package) bool {
	if pkg.Name == "unsafe" {
		// Special case unsafe stdlib, because it does not contain go files.
		return true
	}
	if len(pkg.GoFiles) == 0 {
		return false
	}
	prefix := build.Default.GOROOT
	sep := string(filepath.Separator)
	if !strings.HasSuffix(prefix, sep) {
		prefix += sep
	}
	return strings.HasPrefix(pkg.GoFiles[0], prefix)
}

// isTestBinary returns true iff pkg is a test binary.
func isTestBinary(pkg *packages.Package) bool {
	return strings.HasSuffix(pkg.PkgPath, ".test")
}
