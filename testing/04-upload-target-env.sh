#!/usr/bin/env bash
#Uploading to an environment that is NOT the original one, which is the merge
#request case. Covers the symbolic name rename that made the tenant reject the
#update, and the rule that the repository is never written.
#
#WRITES TO A TENANT. Set LANDSCAPER_TEST_TENANT=1 to run.

SCRIPT_NAME="04-upload-target-env"
source "$(dirname "${BASH_SOURCE[0]}")/lib/common.sh"
setup
require_tenant

OUT="$WORK_DIR/04-out"
rm -rf "$OUT"

TARGET_ARTIFACT="${ARTIFACT_ID}${TARGET_SUFFIX}"
TARGET_PACKAGE="${PACKAGE_ID}${TARGET_SUFFIX}"

BEFORE_VERSION="$(tenant_version "$TARGET_ARTIFACT" "$TARGET_ENV" "$PACKAGE_ID")"
printf '%s holds %s at %s\n' "$TARGET_ENV" "$TARGET_ARTIFACT" "${BEFORE_VERSION:-nothing}"

#A version nobody else is using, so the run is independent of tenant state
STAMP="$(date +%H%M%S)"
NEW_VERSION="7.$(( 10#${STAMP:0:2} )).$(( 10#${STAMP:2:2} ))"

SOURCE="$(scratch_artifact)"
set_version "$SOURCE" "$NEW_VERSION"
UNTOUCHED="$WORK_DIR/04-untouched"
rm -rf "$UNTOUCHED"; cp -R "$SOURCE" "$UNTOUCHED"

section "upload to $TARGET_ENV"

run_landscaper artifact upload "$SOURCE" --target-env="$TARGET_ENV" --output "$OUT"
assert_exit "upload succeeds" "$LAST_EXIT" 0
assert_contains "the suffixed artifact id is reported" "$OUTPUT" "$TARGET_ARTIFACT"
assert_contains "the suffixed package is reported" "$OUTPUT" "$TARGET_PACKAGE"
assert_contains "the source is a folder" "$OUTPUT" "folder"

if [ -n "$BEFORE_VERSION" ]; then
    assert_contains "an existing artifact is updated, not recreated" "$OUTPUT" "updated"
else
    assert_contains "a new artifact is created" "$OUTPUT" "created"
fi

section "the version comes from the repository and lands in the tenant"

assert_equals "the tenant now reports the uploaded version" \
    "$(tenant_version "$TARGET_ARTIFACT" "$TARGET_ENV" "$PACKAGE_ID")" "$NEW_VERSION"

section "the repository is never written for a target environment"

assert_tree_identical "the working copy is byte identical" "$UNTOUCHED" "$SOURCE"
assert_equals "Bundle-SymbolicName is never suffixed on disk" \
    "$(dir_header "$SOURCE" Bundle-SymbolicName)" \
    "$(dir_header "$UNTOUCHED" Bundle-SymbolicName)"

section "the archive carries the suffixed identifiers"

ARCHIVE="$OUT/$TARGET_ARTIFACT.zip"
assert_file "the archive is named after the suffixed id" "$ARCHIVE"
assert_contains "Bundle-SymbolicName is suffixed" \
    "$(zip_header "$ARCHIVE" Bundle-SymbolicName)" "$TARGET_ARTIFACT"
assert_contains "Bundle-Name is suffixed" \
    "$(zip_header "$ARCHIVE" Bundle-Name)" "$TARGET_SUFFIX"
assert_equals "Origin-Bundle-SymbolicName records the origin" \
    "$(zip_header "$ARCHIVE" Origin-Bundle-SymbolicName)" "$ARTIFACT_ID"
assert_not_contains "the suffix is not applied twice" \
    "$(zip_header "$ARCHIVE" Bundle-SymbolicName)" "${TARGET_SUFFIX}${TARGET_SUFFIX}"

section "a second upload is the update the tenant used to reject"

#This is the regression: the tenant compares the symbolic name of the archive
#with the one it stored at creation and answers Bad Request when they differ
run_landscaper artifact upload "$SOURCE" --target-env="$TARGET_ENV" --output "$OUT"
assert_exit "the update succeeds" "$LAST_EXIT" 0
assert_not_contains "no Bundle-symbolicName error" "$OUTPUT" "Bundle-symbolicName"
assert_contains "it is reported as an update" "$OUTPUT" "updated"

section "an equal version warns but still uploads"

assert_contains "the misalignment is warned about" "$OUTPUT" "WARNING"
assert_contains "the warning says it proceeds anyway" "$OUTPUT" "uploading it anyway"

section "--bump and --set-version do not apply here"

run_landscaper artifact upload "$SOURCE" --target-env="$TARGET_ENV" --bump=patch --set-version=6.0.0 --output "$OUT"
assert_exit "upload succeeds" "$LAST_EXIT" 0
assert_contains "--bump is reported as ignored" "$OUTPUT" "--bump is ignored"
assert_contains "--set-version is reported as ignored" "$OUTPUT" "--set-version is ignored"
assert_equals "the tenant version is unchanged by the flags" \
    "$(tenant_version "$TARGET_ARTIFACT" "$TARGET_ENV" "$PACKAGE_ID")" "$NEW_VERSION"
assert_tree_identical "and the repository is still untouched" "$UNTOUCHED" "$SOURCE"

section "configuration of the target environment is applied"

run_landscaper artifact get --artifact="$ARTIFACT_ID" --env="$TARGET_ENV"
assert_exit "artifact get succeeds" "$LAST_EXIT" 0
assert_contains "the artifact carries the suffixed id" "$OUTPUT" "$TARGET_ARTIFACT"
assert_contains "and the suffixed package" "$OUTPUT" "$TARGET_PACKAGE"
assert_contains "a configuration section is present" "$OUTPUT" "Configuration"

summary
