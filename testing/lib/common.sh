#!/usr/bin/env bash
#Shared helpers for the landscaper test scripts.
#
#Written for bash rather than fish, so the same scripts run unchanged in a
#CI pipeline. Source it, do not execute it.

set -uo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
LANDSCAPER="${LANDSCAPER:-$REPO_ROOT/landscaper}"

#Everything the scripts create lives here, never in the repository
WORK_DIR="${WORK_DIR:-${TMPDIR:-/tmp}/landscaper-tests}"
ARTIFACT_DIR="${ARTIFACT_DIR:-$REPO_ROOT/artifacts/Order_API_TEST_HARNESS}"
BACKUP_DIR="$WORK_DIR/fixture-backup"

#Landscape coordinates, override to run against another landscape
ARTIFACT_ID="${ARTIFACT_ID:-Order_API_TEST_HARNESS}"
PACKAGE_ID="${PACKAGE_ID:-TestHarnessPreparation}"
ORIGINAL_ENV="${ORIGINAL_ENV:-Dev}"
TARGET_ENV="${TARGET_ENV:-QA}"
TARGET_SUFFIX="${TARGET_SUFFIX:-QA}"

if [ -t 1 ]; then
    C_PASS=$'\033[32m'; C_FAIL=$'\033[31m'; C_SKIP=$'\033[33m'
    C_HEAD=$'\033[1m';  C_OFF=$'\033[0m'
else
    C_PASS=""; C_FAIL=""; C_SKIP=""; C_HEAD=""; C_OFF=""
fi

OUTPUT=""
LAST_EXIT=0
PASSED=0
FAILED=0
SKIPPED=0
FAILED_NAMES=()

#-- reporting -----------------------------------------------------------------

section() { printf '\n%s== %s ==%s\n' "$C_HEAD" "$1" "$C_OFF"; }

pass() { PASSED=$((PASSED + 1)); printf '  %sPASS%s %s\n' "$C_PASS" "$C_OFF" "$1"; }

fail() {
    FAILED=$((FAILED + 1))
    FAILED_NAMES+=("$1")
    printf '  %sFAIL%s %s\n' "$C_FAIL" "$C_OFF" "$1"
    [ $# -gt 1 ] && printf '       %s\n' "$2"
    return 0
}

skip() { SKIPPED=$((SKIPPED + 1)); printf '  %sSKIP%s %s\n' "$C_SKIP" "$C_OFF" "$1"; }

#Prints the tally and sets the exit code of the script
summary() {
    printf '\n%s%s%s  passed %d, failed %d, skipped %d\n' \
        "$C_HEAD" "${SCRIPT_NAME:-tests}" "$C_OFF" "$PASSED" "$FAILED" "$SKIPPED"

    if [ "$FAILED" -gt 0 ]; then
        printf '%sfailed:%s\n' "$C_FAIL" "$C_OFF"
        printf '  - %s\n' "${FAILED_NAMES[@]}"
        return 1
    fi
    return 0
}

#-- assertions ----------------------------------------------------------------

assert_contains() {
    local name="$1" haystack="$2" needle="$3"
    if [[ "$haystack" == *"$needle"* ]]; then
        pass "$name"
    else
        fail "$name" "expected to find: $needle"
    fi
}

assert_not_contains() {
    local name="$1" haystack="$2" needle="$3"
    if [[ "$haystack" != *"$needle"* ]]; then
        pass "$name"
    else
        fail "$name" "did not expect to find: $needle"
    fi
}

assert_equals() {
    local name="$1" got="$2" want="$3"
    if [ "$got" = "$want" ]; then
        pass "$name"
    else
        fail "$name" "got [$got], want [$want]"
    fi
}

assert_exit() {
    local name="$1" got="$2" want="$3"
    if [ "$got" = "$want" ]; then
        pass "$name"
    else
        fail "$name" "exit code $got, want $want"
    fi
}

assert_file() {
    local name="$1" path="$2"
    if [ -f "$path" ]; then
        pass "$name"
    else
        fail "$name" "file missing: $path"
    fi
}

assert_no_file() {
    local name="$1" path="$2"
    if [ ! -e "$path" ]; then
        pass "$name"
    else
        fail "$name" "file should not exist: $path"
    fi
}

#The working tree must survive untouched wherever the rules say so
assert_tree_identical() {
    local name="$1" left="$2" right="$3" diff_output
    if diff_output="$(diff -r "$left" "$right" 2>&1)"; then
        pass "$name"
    else
        fail "$name" "$(printf '%s' "$diff_output" | head -5)"
    fi
}

#-- landscaper helpers --------------------------------------------------------

#Runs the CLI and sets the globals OUTPUT and LAST_EXIT. It deliberately does
#not print, because a command substitution would run it in a subshell and the
#exit code would be lost.
run_landscaper() {
    OUTPUT="$("$LANDSCAPER" "$@" 2>&1)"
    LAST_EXIT=$?
    return 0
}

#Reads one manifest header out of an archive
zip_header() {
    local archive="$1" header="$2"
    unzip -p "$archive" META-INF/MANIFEST.MF 2>/dev/null | grep "^$header:" | head -1 | sed "s/^$header: *//" | tr -d '\r'
}

#Reads one manifest header out of an exploded folder
dir_header() {
    local dir="$1" header="$2"
    grep "^$header:" "$dir/META-INF/MANIFEST.MF" | head -1 | sed "s/^$header: *//" | tr -d '\r'
}

#Version of an artifact as the tenant reports it, empty when absent.
#The package must be given as the BASE id without any suffix: root.go appends
#the suffix of --env to --pkg, so passing an already suffixed id asks the
#tenant for TestHarnessPreparationQAQA and finds nothing.
tenant_version() {
    local artifact="$1" env="$2" package="$3"
    "$LANDSCAPER" artifact list --pkg="$package" --env="$env" 2>/dev/null |
        awk -v id="$artifact" '$2 == id { print $3 }' | head -1
}

#Deployed runtime version, empty when not deployed. The package is the BASE id,
#see tenant_version.
tenant_deployed_version() {
    local artifact="$1" env="$2" package="$3"
    "$LANDSCAPER" artifact list --pkg="$package" --env="$env" 2>/dev/null |
        awk -v id="$artifact" '$2 == id { print $6 }' | head -1
}

#-- fixtures ------------------------------------------------------------------

#A throwaway copy of the fixture, named after the artifact so that the id and
#the package still resolve from the landscape file
scratch_artifact() {
    local name="${1:-$ARTIFACT_ID}"
    local dir="$WORK_DIR/scratch/$name"
    rm -rf "$dir"
    mkdir -p "$(dirname "$dir")"
    cp -R "$BACKUP_DIR" "$dir"
    printf '%s' "$dir"
}

set_version() {
    local dir="$1" version="$2"
    #BSD and GNU sed disagree about -i, so write through a temp file
    sed "s/^Bundle-Version:.*/Bundle-Version: $version/" "$dir/META-INF/MANIFEST.MF" > "$dir/META-INF/MANIFEST.MF.tmp"
    mv "$dir/META-INF/MANIFEST.MF.tmp" "$dir/META-INF/MANIFEST.MF"
}

#Restores the repository fixture from the backup taken during setup
restore_fixture() {
    if [ -d "$BACKUP_DIR" ]; then
        rm -rf "$ARTIFACT_DIR"
        cp -R "$BACKUP_DIR" "$ARTIFACT_DIR"
    fi
}

#-- setup ---------------------------------------------------------------------

#Builds the binary and backs up the fixture. artifacts/ is gitignored, so the
#backup is the only way back from a version bump.
setup() {
    mkdir -p "$WORK_DIR"

    if [ ! -x "$LANDSCAPER" ]; then
        printf 'building %s\n' "$LANDSCAPER"
        ( cd "$REPO_ROOT" && go build -o "$LANDSCAPER" . ) || {
            printf '%sunable to build landscaper%s\n' "$C_FAIL" "$C_OFF"
            exit 1
        }
    fi

    if [ ! -d "$ARTIFACT_DIR" ]; then
        printf '%sfixture not found: %s%s\n' "$C_FAIL" "$ARTIFACT_DIR" "$C_OFF"
        exit 1
    fi

    if [ ! -d "$BACKUP_DIR" ]; then
        mkdir -p "$(dirname "$BACKUP_DIR")"
        cp -R "$ARTIFACT_DIR" "$BACKUP_DIR"
    fi
}

#Tenant scripts refuse to run unless this is set, so a local run can never
#write to a real tenant by accident
require_tenant() {
    if [ "${LANDSCAPER_TEST_TENANT:-0}" != "1" ]; then
        printf '%sskipped:%s %s writes to a real tenant.\n' "$C_SKIP" "$C_OFF" "${SCRIPT_NAME:-this script}"
        printf '        Set LANDSCAPER_TEST_TENANT=1 to run it.\n'
        exit 0
    fi

    if ! "$LANDSCAPER" package list >/dev/null 2>&1; then
        printf '%sthe tenant is not reachable, check .env and conf/landscape.yaml%s\n' "$C_FAIL" "$C_OFF"
        exit 1
    fi
}
