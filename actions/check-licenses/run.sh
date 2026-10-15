#!/usr/bin/env bash
#
# Runs the license tool for the composite action. Nearly all of the work is
# the tool's: it resolves every target in one run, applies the policy, and
# caches what it resolved. This script only assembles arguments, decides
# whether to run from a container image or an installed binary, and turns a
# policy violation into the requested outcome.

set -o pipefail

die() { echo "::error::$*"; exit 1; }

# Resolve inputs. The target list is either given directly or produced by a
# command, so a repository that already defines which binaries it ships can
# print the list from that definition instead of repeating it here.
if [ -n "$INPUT_TARGETS_COMMAND" ]; then
    [ -n "$INPUT_TARGETS" ] && die "targets and targets-command cannot be combined"
    echo "Acquiring targets: $INPUT_TARGETS_COMMAND"
    target_list=$(bash -c "$INPUT_TARGETS_COMMAND") ||
        die "targets-command failed: $INPUT_TARGETS_COMMAND"
else
    target_list="$INPUT_TARGETS"
fi

mapfile -t targets < <(echo "$target_list" | sed 's/[[:space:]]*$//' | grep -v '^$')
if [ "${#targets[@]}" -eq 0 ]; then
    [ -n "$INPUT_TARGETS_COMMAND" ] &&
        die "targets-command produced no targets: $INPUT_TARGETS_COMMAND"
    die "no targets given; set targets or targets-command"
fi
echo "Targets: ${targets[*]}"

# The accepted set can also come from a command, so a repository that
# already defines its policy can have both its local checks and this action
# read the same definition.
if [ -n "$INPUT_ALLOWED_LICENSES_COMMAND" ]; then
    [ -n "$INPUT_ALLOWED_LICENSES" ] &&
        die "allowed-licenses and allowed-licenses-command cannot be combined"
    echo "Acquiring accepted licenses: $INPUT_ALLOWED_LICENSES_COMMAND"
    INPUT_ALLOWED_LICENSES=$(bash -c "$INPUT_ALLOWED_LICENSES_COMMAND" | tr -d '[:space:]') ||
        die "allowed-licenses-command failed: $INPUT_ALLOWED_LICENSES_COMMAND"
    [ -n "$INPUT_ALLOWED_LICENSES" ] ||
        die "allowed-licenses-command produced nothing: $INPUT_ALLOWED_LICENSES_COMMAND"
    echo "Accepted licenses: $INPUT_ALLOWED_LICENSES"
fi

if [ -n "$INPUT_ALLOWED_LICENSES" ] && [ -n "$INPUT_DISALLOWED_TYPES" ]; then
    die "allowed-licenses and disallowed-types cannot be combined"
fi

common=()
[ "$INPUT_INCLUDE_TESTS" = "true" ] && common+=("--include_tests")
[ "$INPUT_MAIN_ONLY" = "true" ] && common+=("--main_only")
[ "$INPUT_PROGRESS" = "true" ] && common+=("--progress")
while read -r prefix; do
    [ -n "$prefix" ] && common+=("--ignore=$prefix")
done < <(echo "$INPUT_IGNORE" | sed 's/[[:space:]]*$//')

# The policy flags belong to the check command alone; save and report reject
# them.
check_only=()
[ -n "$INPUT_ALLOWED_LICENSES" ] && check_only+=("--allowed_licenses=$INPUT_ALLOWED_LICENSES")
[ -n "$INPUT_DISALLOWED_TYPES" ] && check_only+=("--disallowed_types=$INPUT_DISALLOWED_TYPES")

cache_path=$(readlink -m "${INPUT_CACHE_PATH:-.cache/go-licenses}")
mkdir -p "$cache_path"

# Decide how to run the tool. An installed binary keeps the whole thing to
# one process, with no mounts and no user mapping; an image avoids needing a
# Go toolchain on the runner.
if [ -n "$INPUT_TOOL_IMAGE" ]; then
    command -v docker >/dev/null || die "tool-image is set but docker is not available"
    gomodcache=$(go env GOMODCACHE 2>/dev/null) ||
        die "cannot determine GOMODCACHE; run actions/setup-go before this action"
    mkdir -p "$gomodcache"
    echo "Running $INPUT_TOOL_IMAGE"
    go_licenses() {
        docker run --rm \
            --user "$(id -u):$(id -g)" \
            -v "$PWD":/work -w /work \
            -v "$gomodcache":/go/pkg/mod \
            -v "$cache_path":/cache \
            -e GO_LICENSES_CACHE_DIR=/cache \
            -e HOME=/tmp -e GOCACHE=/tmp/gocache -e GOFLAGS= \
            "$INPUT_TOOL_IMAGE" "$@"
    }
else
    command -v go >/dev/null ||
        die "no Go toolchain found; run actions/setup-go before this action"
    echo "Installing ${INPUT_TOOL_MODULE}@${INPUT_TOOL_VERSION}"
    GOFLAGS= go install "${INPUT_TOOL_MODULE}@${INPUT_TOOL_VERSION}" ||
        die "installing the license tool failed"
    binary="$(go env GOPATH)/bin/$(basename "$INPUT_TOOL_MODULE")"
    [ -x "$binary" ] || die "installed the license tool but $binary is missing"
    export GO_LICENSES_CACHE_DIR="$cache_path"
    go_licenses() { "$binary" "$@"; }
fi

# Check the policy. Its output is the record of what was wrong, so keep it
# rather than only reporting that something was.
echo "::group::Checking licenses"
check_log=$(mktemp)
go_licenses check "${common[@]}" "${check_only[@]}" \
    --target_path '{{.Name}}' "${targets[@]}" 2>&1 | tee "$check_log"
check_status=${PIPESTATUS[0]}
echo "::endgroup::"

violations=$(grep -cE "Not allowed license|not allowed license type|Did not find license" "$check_log" || true)
{
    echo "violations=$([ "$check_status" -eq 0 ] && echo false || echo true)"
    echo "violation-count=$violations"
} >> "$GITHUB_OUTPUT"

if [ "$check_status" -ne 0 ]; then
    grep -E "Not allowed license|not allowed license type|Did not find license" "$check_log" |
        while read -r line; do echo "::error::$line"; done
    if [ "$INPUT_ON_VIOLATION" = "error" ]; then
        die "license policy violated ($violations finding(s))"
    fi
    echo "::warning::license policy violated ($violations finding(s))"
fi

# Extract and report only once the policy has been applied, so a failing run
# does not leave output that looks approved.
if [ -n "$INPUT_LICENSES_PATH" ]; then
    echo "::group::Extracting licenses to $INPUT_LICENSES_PATH"
    rm -rf "$INPUT_LICENSES_PATH"
    mkdir -p "$INPUT_LICENSES_PATH"
    go_licenses save "${common[@]}" --save_path "$INPUT_LICENSES_PATH" \
        --target_path '{{.Name}}' "${targets[@]}" || die "extracting licenses failed"
    echo "::endgroup::"
fi

if [ -n "$INPUT_REPORT_PATH" ]; then
    echo "::group::Reporting licenses to $INPUT_REPORT_PATH"
    go_licenses report "${common[@]}" --target_path "$INPUT_REPORT_PATH" \
        "${targets[@]}" || die "reporting licenses failed"
    echo "::endgroup::"
fi
