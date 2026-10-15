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

// Package cache implements an on-disk license cache.
//
// Entries are stored one file per key and kind, at
//
//	<dir>/<module>/<version>/<path>.<kind>.json
//
// so that a file's content is a pure function of its name. Writers therefore
// never merge: they write a temp file and rename it over the target, with no
// locking and no existence check, because two writers racing on one path are
// necessarily writing identical content for the same immutable module version.
package cache

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/go-licenses/v2/licenses"
	"golang.org/x/mod/module"
)

// schema is the on-disk format version. A mismatch is a miss.
const schema = 1

// Kind suffixes distinguish the two kinds of entry sharing a candidate path.
const (
	licensesKind = "licenses"
	urlKind      = "url"
)

// Config configures a Store.
type Config struct {
	// Dir is the cache root directory.
	Dir string
	// ClassifierVersion gates classification entries: an entry written by a
	// different classifier version is a miss, because a new corpus can change
	// verdicts.
	ClassifierVersion string
	// Hashes maps "module@version" to the go.sum h1 hash. A module absent from
	// the map is not hash-validated.
	Hashes map[string]string
}

// Store is an on-disk license cache. It implements licenses.Cache.
type Store struct {
	cfg Config
}

// New opens or creates the cache described by cfg.
func New(cfg Config) (*Store, error) {
	if cfg.Dir == "" {
		return nil, fmt.Errorf("cache: empty directory")
	}
	return &Store{cfg: cfg}, nil
}

// Purge keeps at most keep version directories per module, discarding the
// least recently used, and removes every entry that fails validation.
// keep == 0 empties the cache entirely.
//
// The unit of purging is the version directory, which falls out of the layout:
// all files for one module version are siblings.
func (s *Store) Purge(keep int) error {
	if keep < 0 {
		return fmt.Errorf("cache: negative keep count %d", keep)
	}
	if _, err := os.Stat(s.cfg.Dir); os.IsNotExist(err) {
		return nil
	}
	if keep == 0 {
		if err := os.RemoveAll(s.cfg.Dir); err != nil {
			return err
		}
		return os.MkdirAll(s.cfg.Dir, 0700)
	}

	versions, err := s.versionDirs()
	if err != nil {
		return err
	}

	// Drop invalid entries first, so a version directory left empty by them is
	// removed regardless of how recently it was touched.
	byModule := map[string][]versionDir{}
	for _, v := range versions {
		empty, err := s.dropInvalid(v.path)
		if err != nil {
			return err
		}
		if empty {
			if err := os.RemoveAll(v.path); err != nil {
				return err
			}
			continue
		}
		byModule[v.module] = append(byModule[v.module], v)
	}

	for _, dirs := range byModule {
		if len(dirs) <= keep {
			continue
		}
		// Most recently used first, so everything from index keep on goes.
		sort.Slice(dirs, func(i, j int) bool { return dirs[i].used.After(dirs[j].used) })
		for _, v := range dirs[keep:] {
			if err := os.RemoveAll(v.path); err != nil {
				return err
			}
		}
	}

	return s.pruneEmptyDirs()
}

// versionDir is one "<module>@<version>" directory.
type versionDir struct {
	path   string    // absolute directory name
	module string    // absolute module directory, i.e. path without "@<version>"
	used   time.Time // newest mtime among the directory's entries
}

// versionDirs finds every version directory in the cache.
func (s *Store) versionDirs() ([]versionDir, error) {
	var found []versionDir
	err := filepath.WalkDir(s.cfg.Dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			// A directory that vanished under us, or one we cannot read, is
			// not worth failing a purge over.
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		// "@" cannot appear in a module path, so this is unambiguous.
		at := strings.LastIndex(d.Name(), "@")
		if at < 0 {
			return nil
		}
		used, empty, err := newestMTime(p)
		if err != nil {
			return nil
		}
		if empty {
			return filepath.SkipDir
		}
		found = append(found, versionDir{
			path:   p,
			module: filepath.Join(filepath.Dir(p), d.Name()[:at]),
			used:   used,
		})
		return filepath.SkipDir
	})
	return found, err
}

// newestMTime returns the newest modification time among the files under dir.
func newestMTime(dir string) (newest time.Time, empty bool, err error) {
	var count int
	err = filepath.WalkDir(dir, func(_ string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		count++
		if info.ModTime().After(newest) {
			newest = info.ModTime()
		}
		return nil
	})
	return newest, count == 0, err
}

// dropInvalid removes the entries under a version directory that no longer
// validate, and reports whether the directory ended up empty.
func (s *Store) dropInvalid(dir string) (empty bool, err error) {
	var kept int
	err = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if s.validFile(p) {
			kept++
			return nil
		}
		return os.Remove(p)
	})
	return kept == 0, err
}

// validFile reports whether the entry stored at p may still be trusted.
//
// The entry names its own key, and that key must map back to p, so a file that
// was renamed or restored under the wrong name is rejected too.
func (s *Store) validFile(p string) bool {
	data, err := os.ReadFile(p)
	if err != nil {
		return false
	}
	var e entry
	if err := json.Unmarshal(data, &e); err != nil {
		return false
	}
	key := licenses.Key{Module: e.Module, Version: e.Version, Path: e.Path}

	kind := licensesKind
	if e.URL != "" {
		kind = urlKind
	}
	want, err := s.path(key, kind)
	if err != nil || want != p {
		return false
	}
	return s.valid(&e, key, kind)
}

// pruneEmptyDirs removes directories left empty by a purge, so that pruning a
// module does not leave an empty "github.com/some-org" behind. The cache root
// itself is kept.
func (s *Store) pruneEmptyDirs() error {
	// Deepest first, so a directory emptied by this walk is itself removable.
	var dirs []string
	err := filepath.WalkDir(s.cfg.Dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && d.IsDir() && p != s.cfg.Dir {
			dirs = append(dirs, p)
		}
		return nil
	})
	if err != nil {
		return err
	}
	for i := len(dirs) - 1; i >= 0; i-- {
		entries, err := os.ReadDir(dirs[i])
		if err != nil {
			continue
		}
		if len(entries) == 0 {
			_ = os.Remove(dirs[i])
		}
	}
	return nil
}

// entry is one cache file.
type entry struct {
	Schema  int    `json:"schema"`
	Module  string `json:"module"`
	Version string `json:"version"`
	Path    string `json:"path"`
	// GoSumH1 is the go.sum h1 hash of the module version, when known.
	GoSumH1 string `json:"gosum_h1,omitempty"`
	// Classifier is set on classification entries only; URLs do not depend on
	// the classifier corpus.
	Classifier string `json:"classifier,omitempty"`
	// Licenses is set on classification entries. An empty, non-nil slice is a
	// cached negative result and is distinct from a missing file.
	Licenses []licenses.License `json:"licenses,omitempty"`
	// URL is set on URL entries.
	URL     string    `json:"url,omitempty"`
	Updated time.Time `json:"updated"`
}

// Licenses returns the cached classification for a candidate file.
func (s *Store) Licenses(key licenses.Key) ([]licenses.License, bool) {
	e, ok := s.read(key, licensesKind)
	if !ok {
		return nil, false
	}
	// Normalize a cached negative result to a nil slice, so that callers see
	// the same value Identify would have returned.
	if len(e.Licenses) == 0 {
		return nil, true
	}
	return e.Licenses, true
}

// PutLicenses stores a classification, which may be empty.
func (s *Store) PutLicenses(key licenses.Key, found []licenses.License) error {
	return s.write(key, licensesKind, &entry{
		Classifier: s.cfg.ClassifierVersion,
		Licenses:   found,
	})
}

// URL returns the cached license URL for a license file.
func (s *Store) URL(key licenses.Key) (string, bool) {
	e, ok := s.read(key, urlKind)
	if !ok || e.URL == "" {
		return "", false
	}
	return e.URL, true
}

// PutURL stores a resolved license URL.
func (s *Store) PutURL(key licenses.Key, url string) error {
	if url == "" {
		return fmt.Errorf("cache: refusing to store an empty URL for %s@%s/%s",
			key.Module, key.Version, key.Path)
	}
	return s.write(key, urlKind, &entry{URL: url})
}

// read loads and validates one entry. Any problem is a miss.
func (s *Store) read(key licenses.Key, kind string) (*entry, bool) {
	if !key.Cacheable() {
		return nil, false
	}
	name, err := s.path(key, kind)
	if err != nil {
		return nil, false
	}
	data, err := os.ReadFile(name)
	if err != nil {
		return nil, false
	}
	var e entry
	if err := json.Unmarshal(data, &e); err != nil {
		// Self-healing: drop a corrupt entry rather than re-reading and
		// re-rejecting it on every future run.
		_ = os.Remove(name)
		return nil, false
	}
	if !s.valid(&e, key, kind) {
		return nil, false
	}
	return &e, true
}

// valid reports whether an entry may be trusted.
//
// Validation is per file: schema and the go.sum hash gate both kinds, while the
// classifier version gates classifications only. URLs do not depend on the
// classifier corpus, so an upgrade costs CPU to reclassify but no network to
// re-resolve.
func (s *Store) valid(e *entry, key licenses.Key, kind string) bool {
	if e.Schema != schema {
		return false
	}
	// Guard against a file that was renamed or restored under the wrong name.
	if e.Module != key.Module || e.Version != key.Version || e.Path != key.Path {
		return false
	}
	// Pin the entry to the module content this project's go.sum expects, which
	// matters for a cache restored from a shared CI artifact. When either side
	// does not know the hash - vendor mode, or an entry written before the
	// hashes were available - there is nothing to compare and the check is
	// skipped.
	if want, ok := s.cfg.Hashes[key.Module+"@"+key.Version]; ok && want != "" &&
		e.GoSumH1 != "" && e.GoSumH1 != want {
		return false
	}
	if kind == licensesKind && e.Classifier != s.cfg.ClassifierVersion {
		return false
	}
	return true
}

// write stores one entry, filling in the fields common to both kinds.
func (s *Store) write(key licenses.Key, kind string, e *entry) error {
	if !key.Cacheable() {
		return nil
	}
	name, err := s.path(key, kind)
	if err != nil {
		return err
	}

	e.Schema = schema
	e.Module = key.Module
	e.Version = key.Version
	e.Path = key.Path
	e.GoSumH1 = s.cfg.Hashes[key.Module+"@"+key.Version]
	e.Updated = time.Now().UTC()

	data, err := json.Marshal(e)
	if err != nil {
		return err
	}
	return writeFileAtomic(name, data)
}

// path returns the absolute file name for one key and kind.
func (s *Store) path(key licenses.Key, kind string) (string, error) {
	mod, err := module.EscapePath(key.Module)
	if err != nil {
		return "", fmt.Errorf("cache: module path %q: %w", key.Module, err)
	}
	ver, err := module.EscapeVersion(key.Version)
	if err != nil {
		return "", fmt.Errorf("cache: version %q: %w", key.Version, err)
	}
	rel, err := escapeFilePath(key.Path)
	if err != nil {
		return "", err
	}
	// "<module>@<version>" as one directory name, as GOMODCACHE does. "@"
	// cannot appear in a module path, so a version directory is never
	// ambiguous with a module directory - which it would be under
	// "<module>/<version>", since a module path element may itself look like a
	// version, as in "k8s.io/klog/v2".
	return filepath.Join(s.cfg.Dir, filepath.FromSlash(mod+"@"+ver),
		filepath.FromSlash(rel)+"."+kind+".json"), nil
}

// escapeFilePath escapes a module-relative file path for use as a cache path.
//
// It applies the same rule as golang.org/x/mod's module path escaping - an
// uppercase letter becomes "!" followed by its lowercase form - so that a
// module containing both LICENSE and License does not collide into one entry on
// a case-insensitive filesystem. x/mod's EscapePath cannot be reused here
// because it validates its argument as a module path, which a file name is not.
func escapeFilePath(p string) (string, error) {
	if p == "" {
		return "", fmt.Errorf("cache: empty file path")
	}
	cleaned := path.Clean(p)
	if path.IsAbs(cleaned) || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", fmt.Errorf("cache: file path %q escapes the module root", p)
	}

	var buf strings.Builder
	for _, r := range cleaned {
		switch {
		case r == '!':
			return "", fmt.Errorf("cache: file path %q contains %q", p, "!")
		case 'A' <= r && r <= 'Z':
			buf.WriteByte('!')
			buf.WriteRune(r + 'a' - 'A')
		default:
			buf.WriteRune(r)
		}
	}
	return buf.String(), nil
}

// writeFileAtomic writes data to name via a temp file and a rename, creating
// parent directories as needed.
//
// The temp file is created in the destination directory, not os.TempDir, since
// rename across filesystems fails with EXDEV. Its name includes the pid so
// concurrent writers never collide on the temp path itself.
func writeFileAtomic(name string, data []byte) error {
	dir := filepath.Dir(name)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, fmt.Sprintf(".tmp-%d-*", os.Getpid()))
	if err != nil {
		return err
	}
	defer func() {
		// Harmless when the rename below already succeeded.
		_ = os.Remove(tmp.Name())
	}()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0600); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), name)
}
