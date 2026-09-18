#!/usr/bin/env bash
#"artifact download": the tenant is read, but nothing is ever written to it.
#Needs credentials, makes no changes to the tenant. Everything lands under
#WORK_DIR, never in the repository.

SCRIPT_NAME="10-download"
source "$(dirname "${BASH_SOURCE[0]}")/lib/common.sh"
setup
require_tenant

OUT="$WORK_DIR/10-out"
rm -rf "$OUT"

TARGET_PACKAGE="$PACKAGE_ID$TARGET_SUFFIX"
TARGET_ARTIFACT="$ARTIFACT_ID$TARGET_SUFFIX"

section "a package of the original environment is extracted per package and artifact"

run_landscaper artifact download --packages "$PACKAGE_ID" --output "$OUT"
assert_exit "download succeeds" "$LAST_EXIT" 0
assert_contains "the artifact is reported as downloaded" "$OUTPUT" "$ARTIFACT_ID"
assert_contains "the status is downloaded" "$OUTPUT" "downloaded"
assert_file "the manifest is on disk" "$OUT/$PACKAGE_ID/$ARTIFACT_ID/META-INF/MANIFEST.MF"
assert_file "the flow content is on disk" "$OUT/$PACKAGE_ID/$ARTIFACT_ID/metainfo.prop"

TENANT_VERSION="$(tenant_version "$ARTIFACT_ID" "$ORIGINAL_ENV" "$PACKAGE_ID")"
assert_equals "the extracted version is the one the tenant holds" \
    "$(dir_header "$OUT/$PACKAGE_ID/$ARTIFACT_ID" "Bundle-Version")" "$TENANT_VERSION"

assert_equals "the symbolic name is not rewritten" \
    "$(dir_header "$OUT/$PACKAGE_ID/$ARTIFACT_ID" "Bundle-SymbolicName")" "$ARTIFACT_ID; singleton:=true"

section "a downloaded folder is a valid input for artifact pack"

run_landscaper artifact pack "$OUT/$PACKAGE_ID/$ARTIFACT_ID" --skip-version-check --output "$WORK_DIR/10-pack"
assert_exit "pack succeeds" "$LAST_EXIT" 0
assert_file "the archive was built" "$WORK_DIR/10-pack/$ARTIFACT_ID.zip"

section "an existing folder is skipped and left alone"

MARKER="$OUT/$PACKAGE_ID/$ARTIFACT_ID/local-change.txt"
printf 'mine' > "$MARKER"

run_landscaper artifact download --packages "$PACKAGE_ID" --output "$OUT"
assert_exit "download succeeds" "$LAST_EXIT" 0
assert_contains "the artifact is reported as skipped" "$OUTPUT" "skipped, folder exists"
assert_file "the local change survived" "$MARKER"

section "--force replaces the folder completely"

run_landscaper artifact download --packages "$PACKAGE_ID" --output "$OUT" --force
assert_exit "download succeeds" "$LAST_EXIT" 0
assert_contains "the artifact is reported as downloaded" "$OUTPUT" "downloaded"
assert_no_file "the local change is gone" "$MARKER"
assert_file "the manifest is back" "$OUT/$PACKAGE_ID/$ARTIFACT_ID/META-INF/MANIFEST.MF"

section "ids are written verbatim for an environment with a suffix"

OUT_TARGET="$WORK_DIR/10-out-$TARGET_ENV"
rm -rf "$OUT_TARGET"

run_landscaper artifact download --artifacts "$ARTIFACT_ID" --env "$TARGET_ENV" --output "$OUT_TARGET"
assert_exit "download succeeds" "$LAST_EXIT" 0
assert_file "package and artifact folders carry the suffix" \
    "$OUT_TARGET/$TARGET_PACKAGE/$TARGET_ARTIFACT/META-INF/MANIFEST.MF"
assert_no_file "the base id is not used" "$OUT_TARGET/$PACKAGE_ID"

assert_equals "the suffixed symbolic name is kept as the tenant has it" \
    "$(dir_header "$OUT_TARGET/$TARGET_PACKAGE/$TARGET_ARTIFACT" "Bundle-SymbolicName")" \
    "$TARGET_ARTIFACT; singleton:=true"

section "--download-all keeps only the packages of the environment"

OUT_ALL="$WORK_DIR/10-out-all"
rm -rf "$OUT_ALL"

run_landscaper artifact download --download-all --env "$TARGET_ENV" --output "$OUT_ALL"
assert_exit "download succeeds" "$LAST_EXIT" 0
assert_file "the suffixed package was downloaded" "$OUT_ALL/$TARGET_PACKAGE/$TARGET_ARTIFACT/META-INF/MANIFEST.MF"
assert_no_file "the package of the other environment was not" "$OUT_ALL/$PACKAGE_ID"

section "the selection flags are mutually exclusive"

run_landscaper artifact download --output "$OUT"
assert_exit "nothing selected fails" "$LAST_EXIT" 1
assert_contains "the message names the flags" "$OUTPUT" "--packages, --artifacts or --download-all"

run_landscaper artifact download --packages "$PACKAGE_ID" --download-all --output "$OUT"
assert_exit "two selectors fail" "$LAST_EXIT" 1
assert_contains "the message asks for one of them" "$OUTPUT" "only one of"

run_landscaper artifact download --packages "$PACKAGE_ID" --pkg "$PACKAGE_ID" --output "$OUT"
assert_exit "the global --pkg is rejected" "$LAST_EXIT" 1
assert_contains "the message points at --packages" "$OUTPUT" "--packages and --artifacts"

section "an unknown artifact is reported, not swallowed"

run_landscaper artifact download --artifacts "No_Such_Flow" --output "$OUT"
assert_exit "an undeclared artifact fails" "$LAST_EXIT" 1
assert_contains "the message explains why" "$OUTPUT" "not declared in any package"

summary
