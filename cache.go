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
	"os"
	"path/filepath"

	"github.com/klihub/go-and-maybe-cache-licenses/licenses/cache"
	"github.com/spf13/cobra"
	"k8s.io/klog/v2"
)

// cacheDirEnv overrides the default cache location, which is handy in CI where
// the cache is restored into a workspace-local directory.
const cacheDirEnv = "GO_LICENSES_CACHE_DIR"

var (
	// useCache enables the license cache.
	useCache bool
	// cacheDir overrides the default cache directory.
	cacheDir string
	// purgeCache is the number of module versions to keep per module.
	purgeCache int
)

func init() {
	rootCmd.PersistentFlags().BoolVar(&useCache, "cache", true,
		"Cache license information per module version, so repeated runs neither reclassify license files nor resolve license URLs over the network.")
	rootCmd.PersistentFlags().StringVar(&cacheDir, "cache_dir", "",
		"Directory holding the license cache. Defaults to $"+cacheDirEnv+", else a go-licenses directory under the user cache directory.")
	rootCmd.PersistentFlags().IntVar(&purgeCache, "purge_cache", 0,
		"Purge the license cache and exit without running the subcommand, keeping at most this many module versions per module. 0 purges the whole cache.")

	// A root command is only Runnable if it has a Run or RunE, and cobra skips
	// PersistentPreRunE for a command that is not runnable. Giving the root a
	// RunE that prints help - which is what it did implicitly before - lets
	// "go-licenses --purge_cache=0" work with no subcommand.
	rootCmd.Args = cobra.NoArgs
	rootCmd.RunE = func(cmd *cobra.Command, _ []string) error {
		return cmd.Help()
	}
	rootCmd.PersistentPreRunE = purgeCacheIfRequested
}

// purgeCacheIfRequested purges the cache and exits when --purge_cache is given,
// without running the subcommand.
func purgeCacheIfRequested(cmd *cobra.Command, _ []string) error {
	if !cmd.Flags().Changed("purge_cache") {
		return nil
	}
	store, dir, err := newCacheStore()
	if err != nil {
		return err
	}
	if err := store.Purge(purgeCache); err != nil {
		return err
	}
	if purgeCache == 0 {
		klog.Infof("Purged the license cache in %s.", dir)
	} else {
		klog.Infof("Purged the license cache in %s, keeping %d version(s) per module.", dir, purgeCache)
	}
	// Exiting here is what makes --purge_cache skip the subcommand. Returning
	// an error instead would report a failure for work that succeeded.
	os.Exit(0)
	return nil
}

// newCacheStore opens the license cache, returning it and its directory.
func newCacheStore() (*cache.Store, string, error) {
	dir, err := resolveCacheDir()
	if err != nil {
		return nil, "", err
	}

	// go.sum pins each cached entry to the module content this project expects,
	// which matters when the cache is restored from a shared CI artifact.
	// Projects without a go.sum simply go unvalidated.
	wd, err := os.Getwd()
	if err != nil {
		return nil, "", err
	}
	goSum, err := cache.FindGoSum(wd)
	if err != nil {
		return nil, "", err
	}
	hashes, err := cache.ReadGoSum(goSum)
	if err != nil {
		return nil, "", err
	}

	store, err := cache.New(cache.Config{
		Dir:               dir,
		ClassifierVersion: cache.ClassifierVersion(),
		Hashes:            hashes,
	})
	if err != nil {
		return nil, "", err
	}
	return store, dir, nil
}

// resolveCacheDir returns the cache directory, preferring the flag, then the
// environment, then the user cache directory.
func resolveCacheDir() (string, error) {
	if cacheDir != "" {
		return cacheDir, nil
	}
	if dir := os.Getenv(cacheDirEnv); dir != "" {
		return dir, nil
	}
	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "go-licenses"), nil
}
