#!/usr/bin/env bash
#Uploading to the original environment, the only one whose version the
#repository owns and the only one a bump may be written back for.
#
#WRITES TO A TENANT. Set LANDSCAPER_TEST_TENANT=1 to run.

SCRIPT_NAME="05-upload-original-env"
source "$(dirname "${BASH_SOURCE[0]}")/lib/common.sh"
setup
require_tenant

OUT="$WORK_DIR/05-out"
rm -rf "$OUT"

TENANT_VERSION="$(tenant_version "$ARTIFACT_ID" "$ORIGINAL_ENV" "$PACKAGE_ID")"
printf '%s holds %s at %s\n' "$ORIGINAL_ENV" "$ARTIFACT_ID" "${TENANT_VERSION:-nothing}"

AHEAD="$(awk -v v="$TENANT_VERSION" 'BEGIN{split(v,p,"."); print p[1]"."p[2]"."(p[3]+1)}')"
AFTER_BUMP="$(awk -v v="$AHEAD" 'BEGIN{split(v,p,"."); print p[1]"."p[2]"."(p[3]+1)}')"

section "a repository version above the tenant is uploaded as it is"

SOURCE="$(scratch_artifact)"
set_version "$SOURCE" "$AHEAD"

run_landscaper artifact upload "$SOURCE" --target-env="$ORIGINAL_ENV" --bump=patch --output "$OUT"
assert_exit "upload succeeds" "$LAST_EXIT" 0
assert_contains "no suffix is added to the id" "$OUTPUT" "$ARTIFACT_ID"
assert_not_contains "and definitely not the target suffix" "$OUTPUT" "${ARTIFACT_ID}${TARGET_SUFFIX}"
assert_equals "--bump is a no-op while the repository is ahead" \
    "$(dir_header "$SOURCE" Bundle-Version)" "$AHEAD"
assert_equals "the tenant receives that version" \
    "$(tenant_version "$ARTIFACT_ID" "$ORIGINAL_ENV" "$PACKAGE_ID")" "$AHEAD"

section "once the repository matches the tenant, --bump raises it"

run_landscaper artifact upload "$SOURCE" --target-env="$ORIGINAL_ENV" --bump=patch --output "$OUT"
assert_exit "upload succeeds" "$LAST_EXIT" 0
assert_equals "the manifest is bumped in the working copy" \
    "$(dir_header "$SOURCE" Bundle-Version)" "$AFTER_BUMP"
assert_equals "the tenant receives the bumped version" \
    "$(tenant_version "$ARTIFACT_ID" "$ORIGINAL_ENV" "$PACKAGE_ID")" "$AFTER_BUMP"

section "only the version line of the manifest changes"

REFERENCE="$(scratch_artifact reference)"
set_version "$REFERENCE" "$AFTER_BUMP"
assert_tree_identical "nothing else in the folder moved" "$REFERENCE" "$SOURCE"

section "the archive is not renamed for an environment without a suffix"

ARCHIVE="$OUT/$ARTIFACT_ID.zip"
assert_file "the archive keeps the base name" "$ARCHIVE"
assert_equals "Bundle-SymbolicName is the base one" \
    "$(zip_header "$ARCHIVE" Bundle-SymbolicName)" "$(dir_header "$SOURCE" Bundle-SymbolicName)"
assert_equals "Bundle-Name is the base one" \
    "$(zip_header "$ARCHIVE" Bundle-Name)" "$(dir_header "$SOURCE" Bundle-Name)"

section "uploading to the original environment is allowed"

#Unlike package move, which refuses to write to the original environment,
#because a merge request is deployed there as a matter of course
assert_not_contains "no refusal" "$OUTPUT" "Cannot import changes to original environment"

summary
