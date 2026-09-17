#!/usr/bin/env bash
#Uploading a ready archive instead of a folder, in both directions: an archive
#packed for the target environment and one packed with the base identifiers.
#
#WRITES TO A TENANT. Set LANDSCAPER_TEST_TENANT=1 to run.

SCRIPT_NAME="06-zip-input"
source "$(dirname "${BASH_SOURCE[0]}")/lib/common.sh"
setup
require_tenant

OUT="$WORK_DIR/06-out"
rm -rf "$OUT"

TARGET_ARTIFACT="${ARTIFACT_ID}${TARGET_SUFFIX}"
TARGET_PACKAGE="${PACKAGE_ID}${TARGET_SUFFIX}"

SOURCE="$(scratch_artifact)"
STAMP="$(date +%H%M%S)"
set_version "$SOURCE" "6.$(( 10#${STAMP:0:2} )).$(( 10#${STAMP:2:2} ))"
PACKED_VERSION="$(dir_header "$SOURCE" Bundle-Version)"

section "an archive packed for the target environment"

run_landscaper artifact pack "$SOURCE" --target-env="$TARGET_ENV" --skip-version-check --output "$OUT"
assert_exit "pack succeeds" "$LAST_EXIT" 0

SUFFIXED_ZIP="$OUT/$TARGET_ARTIFACT.zip"
assert_file "named after the suffixed id" "$SUFFIXED_ZIP"
assert_contains "already carries the suffixed symbolic name" \
    "$(zip_header "$SUFFIXED_ZIP" Bundle-SymbolicName)" "$TARGET_ARTIFACT"

#Regression: the file name is the suffixed id, so it must not be taken as the
#base id and suffixed a second time
run_landscaper artifact upload "$SUFFIXED_ZIP" --target-env="$TARGET_ENV" --output "$OUT"
assert_exit "uploading it succeeds" "$LAST_EXIT" 0
assert_contains "the source is reported as a zip" "$OUTPUT" "zip"
assert_contains "the id is suffixed exactly once" "$OUTPUT" "$TARGET_ARTIFACT"
assert_not_contains "not twice" "$OUTPUT" "${TARGET_ARTIFACT}${TARGET_SUFFIX}"
assert_contains "the package is suffixed exactly once" "$OUTPUT" "$TARGET_PACKAGE"
assert_not_contains "not twice" "$OUTPUT" "${TARGET_PACKAGE}${TARGET_SUFFIX}"
assert_not_contains "no Bundle-symbolicName error" "$OUTPUT" "Bundle-symbolicName"
assert_equals "the tenant holds the packed version" \
    "$(tenant_version "$TARGET_ARTIFACT" "$TARGET_ENV" "$PACKAGE_ID")" "$PACKED_VERSION"

section "an archive packed with the base identifiers is renamed on the way"

run_landscaper artifact pack "$SOURCE" --skip-version-check --output "$OUT"
BASE_ZIP="$OUT/$ARTIFACT_ID.zip"
assert_file "the base archive exists" "$BASE_ZIP"
assert_not_contains "and carries the base symbolic name" \
    "$(zip_header "$BASE_ZIP" Bundle-SymbolicName)" "$TARGET_ARTIFACT"

run_landscaper artifact upload "$BASE_ZIP" --target-env="$TARGET_ENV" --output "$OUT"
assert_exit "uploading it succeeds" "$LAST_EXIT" 0
assert_contains "it lands under the suffixed id" "$OUTPUT" "$TARGET_ARTIFACT"
assert_not_contains "no Bundle-symbolicName error" "$OUTPUT" "Bundle-symbolicName"

section "a folder and an archive in one call"

run_landscaper artifact upload "$SOURCE" "$BASE_ZIP" --target-env="$TARGET_ENV" --output "$OUT"
assert_exit "upload succeeds" "$LAST_EXIT" 0
assert_contains "the folder row is present" "$OUTPUT" "folder"
assert_contains "the zip row is present" "$OUTPUT" "zip"

section "no stray suffixed package was created"

run_landscaper package list
assert_not_contains "no double suffixed package exists" "$OUTPUT" "${TARGET_PACKAGE}${TARGET_SUFFIX}"

summary
