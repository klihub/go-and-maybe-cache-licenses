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

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/klihub/go-and-maybe-cache-licenses/licenses"
	"github.com/spf13/cobra"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

var (
	checkHelp = "Checks whether licenses for a package are not allowed."
	checkCmd  = &cobra.Command{
		Use:   "check <package> [package...]",
		Short: checkHelp,
		Long:  checkHelp + packageHelp,
		Args:  cobra.MinimumNArgs(1),
		RunE:  checkMain,
	}

	allowedLicenses []string
	disallowedTypes []string
)

func init() {
	checkCmd.Flags().StringSliceVar(&allowedLicenses, "allowed_licenses", []string{}, "list of allowed license names, can't be used in combination with disallowed_types")
	checkCmd.Flags().StringSliceVar(&disallowedTypes, "disallowed_types", []string{}, "list of disallowed license types, can't be used in combination with allowed_licenses (default: forbidden, unknown)")

	rootCmd.AddCommand(checkCmd)
}

func checkMain(_ *cobra.Command, args []string) error {
	var disallowedLicenseTypes []licenses.Type

	allowedLicenseNames := getAllowedLicenseNames()
	disallowedLicenseTypes = getDisallowedLicenseTypes()

	hasLicenseNames := len(allowedLicenseNames) > 0
	hasLicenseType := len(disallowedLicenseTypes) > 0

	if hasLicenseNames && hasLicenseType {
		return errors.New("allowed_licenses && disallowed_types can't be used at the same time")
	}

	if !hasLicenseNames && !hasLicenseType {
		// fallback to original behaviour to avoid breaking changes
		disallowedLicenseTypes = []licenses.Type{licenses.Forbidden, licenses.Unknown}
		hasLicenseType = true
	}

	opts, err := libraryOptions()
	if err != nil {
		return err
	}

	if targetPath == "" {
		libs, err := licenses.FindLibraries(context.Background(), opts, args...)
		if err != nil {
			return err
		}
		if checkLibraries(libs, "", allowedLicenseNames, disallowedLicenseTypes, hasLicenseNames, hasLicenseType) {
			os.Exit(1)
		}
		return nil
	}

	// Per-target mode exists for the diagnostics: checking the union would
	// still catch every violation, since the union is a superset, but it could
	// not say which target the offending library belongs to.
	outputs, err := resolvePerTarget(opts, args)
	if err != nil {
		return err
	}
	found := false
	for _, out := range outputs {
		if checkLibraries(out.Libraries, out.Name, allowedLicenseNames, disallowedLicenseTypes,
			hasLicenseNames, hasLicenseType) {
			found = true
		}
	}
	if found {
		os.Exit(1)
	}
	return nil
}

// checkLibraries reports whether any library violates the policy, writing
// diagnostics to stderr. target, when non-empty, prefixes each message.
func checkLibraries(libs []*licenses.Library, target string, allowedLicenseNames []string,
	disallowedLicenseTypes []licenses.Type, hasLicenseNames, hasLicenseType bool) bool {
	prefix := ""
	if target != "" {
		prefix = target + ": "
	}

	// indicate that a forbidden license was found
	found := false

	for _, lib := range libs {
		if lib.LicenseFile == "" {
			fmt.Fprintf(os.Stderr, "%sDid not find license for library '%v'.\n", prefix, lib)
			found = true
			continue
		}

		for _, license := range lib.Licenses {
			if hasLicenseNames && !isAllowedLicenseName(license.Name, allowedLicenseNames) {
				fmt.Fprintf(os.Stderr, "%sNot allowed license '%s' found for library '%v'.\n",
					prefix, license.Name, lib)
				found = true
			} else if hasLicenseType && isDisallowedLicenseType(license.Type, disallowedLicenseTypes) {
				fmt.Fprintf(
					os.Stderr,
					"%sLicense '%s' of not allowed license type '%s' found for library '%v'.\n",
					prefix,
					license.Name,
					cases.Title(language.English).String(license.Type.String()),
					lib)
				found = true
			}
		}
	}

	return found
}

func getDisallowedLicenseTypes() []licenses.Type {
	if len(disallowedTypes) == 0 {
		return []licenses.Type{}
	}

	excludedLicenseTypes := make([]licenses.Type, 0)

	for _, v := range disallowedTypes {
		switch strings.TrimSpace(strings.ToLower(v)) {
		case "forbidden":
			excludedLicenseTypes = append(excludedLicenseTypes, licenses.Forbidden)
		case "notice":
			excludedLicenseTypes = append(excludedLicenseTypes, licenses.Notice)
		case "permissive":
			excludedLicenseTypes = append(excludedLicenseTypes, licenses.Permissive)
		case "reciprocal":
			excludedLicenseTypes = append(excludedLicenseTypes, licenses.Reciprocal)
		case "restricted":
			excludedLicenseTypes = append(excludedLicenseTypes, licenses.Restricted)
		case "unencumbered":
			excludedLicenseTypes = append(excludedLicenseTypes, licenses.Unencumbered)
		case "unknown":
			excludedLicenseTypes = append(excludedLicenseTypes, licenses.Unknown)
		default:
			fmt.Fprintf(
				os.Stderr,
				"Unknown license type '%s' provided.\n"+
					"Allowed types: forbidden, notice, permissive, reciprocal, restricted, unencumbered, unknown\n",
				v)
		}
	}

	return excludedLicenseTypes
}

func isDisallowedLicenseType(licenseType licenses.Type, excludedLicenseTypes []licenses.Type) bool {
	for _, excluded := range excludedLicenseTypes {
		if excluded == licenseType {
			return true
		}
	}

	return false
}

func getAllowedLicenseNames() []string {
	if len(allowedLicenses) == 0 {
		return []string{}
	}

	var allowed []string

	for _, licenseName := range allowedLicenses {
		allowed = append(allowed, strings.TrimSpace(licenseName))
	}

	return allowed
}

func isAllowedLicenseName(licenseName string, allowedLicenseNames []string) bool {
	for _, allowed := range allowedLicenseNames {
		if allowed == licenseName {
			return true
		}
	}

	return false
}
