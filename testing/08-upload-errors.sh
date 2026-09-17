#!/usr/bin/env bash
#Error handling of "artifact upload". Every case below is rejected before
#anything is written, so this script does not modify a tenant, but it does need
#credentials for the cases that get as far as resolving an environment.

SCRIPT_NAME="08-upload-errors"
source "$(dirname "${BASH_SOURCE[0]}")/lib/common.sh"
setup

OUT="$WORK_DIR/08-out"
rm -rf "$OUT"
SOURCE="$(scratch_artifact)"

section "the target environment is required"

run_landscaper artifact upload "$SOURCE"
assert_exit "exits 1" "$LAST_EXIT" 1
assert_contains "cobra names the missing flag" "$OUTPUT" "target-env"

section "an unknown environment"

run_landscaper artifact upload "$SOURCE" --target-env=NoSuchEnvironment
assert_exit "exits 1" "$LAST_EXIT" 1
assert_contains "the environment is named" "$OUTPUT" "NoSuchEnvironment"
assert_contains "and reported as not found" "$OUTPUT" "not found"

section "a path that is neither a folder nor an archive"

run_landscaper artifact upload "$WORK_DIR/08-does-not-exist" --target-env="$TARGET_ENV" --skip-version-check
assert_exit "exits 1" "$LAST_EXIT" 1
assert_contains "the path is named" "$OUTPUT" "08-does-not-exist"
#The path has to be blamed, not the landscape file: resolving the package first
#produced a misleading "not declared in any package" message
assert_contains "the path is what is blamed" "$OUTPUT" "neither an integration flow folder nor a zip"
assert_not_contains "the landscape file is not blamed" "$OUTPUT" "not declared in any package"

section "a folder that is not an integration flow"

NOT_AN_IFLOW="$WORK_DIR/08-not-an-iflow"
rm -rf "$NOT_AN_IFLOW"; mkdir -p "$NOT_AN_IFLOW"
run_landscaper artifact upload "$NOT_AN_IFLOW" --target-env="$TARGET_ENV" --skip-version-check
assert_exit "exits 1" "$LAST_EXIT" 1
assert_contains "the path is rejected" "$OUTPUT" "neither an integration flow folder nor a zip"

section "an artifact the landscape does not declare"

UNKNOWN="$(scratch_artifact Unknown_API)"
run_landscaper artifact upload "$UNKNOWN" --target-env="$TARGET_ENV" --skip-version-check
assert_exit "exits 1" "$LAST_EXIT" 1
assert_contains "the error points at --pkg" "$OUTPUT" "--pkg"

section "nothing was written"

#Every case above failed before the first request, so the target package must
#still look exactly as it did
if [ "${LANDSCAPER_TEST_TENANT:-0}" = "1" ]; then
    run_landscaper package list
    assert_not_contains "no package was created for the unknown artifact" "$OUTPUT" "Unknown_API"
else
    skip "tenant check, set LANDSCAPER_TEST_TENANT=1 to include it"
fi

summary
