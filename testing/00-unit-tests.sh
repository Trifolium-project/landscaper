#!/usr/bin/env bash
#Build, vet and the Go test suite. Run this first: if it fails, the scripts
#below are testing a binary that is already known to be broken.
#Needs no credentials.

SCRIPT_NAME="00-unit-tests"
source "$(dirname "${BASH_SOURCE[0]}")/lib/common.sh"

cd "$REPO_ROOT" || exit 1

section "go build"
OUTPUT="$(go build ./... 2>&1)"; LAST_EXIT=$?
assert_exit "the module builds" "$LAST_EXIT" 0
[ -n "$OUTPUT" ] && printf '%s\n' "$OUTPUT"

section "go vet"
OUTPUT="$(go vet ./... 2>&1)"; LAST_EXIT=$?
assert_exit "vet is clean" "$LAST_EXIT" 0
[ -n "$OUTPUT" ] && printf '%s\n' "$OUTPUT"

section "go test"
OUTPUT="$(go test ./packages/... 2>&1)"; LAST_EXIT=$?
assert_exit "all packages pass" "$LAST_EXIT" 0
printf '%s\n' "$OUTPUT" | grep -v "no test files"

summary
