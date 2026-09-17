#!/usr/bin/env bash
#Several artifacts in one call, package resolution, and the error paths of
#"artifact pack". Reads nothing from a tenant.

SCRIPT_NAME="03-multi-artifact"
source "$(dirname "${BASH_SOURCE[0]}")/lib/common.sh"
setup

OUT="$WORK_DIR/03-out"
rm -rf "$OUT"

FIRST="$(scratch_artifact "$ARTIFACT_ID")"
#A second artifact that the landscape also declares. Its manifest still carries
#the symbolic name of the original, which is what triggers the mismatch warning
SECOND="$(scratch_artifact Sample_API)"
UNKNOWN="$(scratch_artifact Unknown_API)"

section "several artifacts in one call"

run_landscaper artifact pack "$FIRST" "$SECOND" --skip-version-check --output "$OUT"
assert_exit "pack succeeds" "$LAST_EXIT" 0
assert_file "first archive written" "$OUT/$ARTIFACT_ID.zip"
assert_file "second archive written" "$OUT/Sample_API.zip"
assert_contains "both artifacts are listed" "$OUTPUT" "Sample_API"
assert_contains "rows are numbered" "$OUTPUT" "2	"

section "a folder name that disagrees with the manifest is reported"

assert_contains "the mismatch is warned about" "$OUTPUT" "does not match Bundle-SymbolicName"
assert_equals "the folder name wins as the artifact id" \
    "$(zip_header "$OUT/Sample_API.zip" Bundle-SymbolicName)" \
    "$(dir_header "$SECOND" Bundle-SymbolicName)"

section "a failure halfway through still reports what was produced"

PARTIAL="$WORK_DIR/03-partial"
rm -rf "$PARTIAL"
run_landscaper artifact pack "$FIRST" "$WORK_DIR/03-does-not-exist" --skip-version-check --output "$PARTIAL"
assert_exit "exits 1" "$LAST_EXIT" 1
assert_contains "the good artifact is still in the table" "$OUTPUT" "$ARTIFACT_ID"
assert_file "and its archive was written" "$PARTIAL/$ARTIFACT_ID.zip"
assert_contains "the broken path is named" "$OUTPUT" "03-does-not-exist"

section "an artifact the landscape does not declare"

run_landscaper artifact pack "$UNKNOWN" --output "$OUT"
assert_exit "exits 1" "$LAST_EXIT" 1
assert_contains "the error points at --pkg" "$OUTPUT" "--pkg"

run_landscaper artifact pack "$UNKNOWN" --pkg="$PACKAGE_ID" --skip-version-check --output "$OUT"
assert_exit "an explicit --pkg makes it work" "$LAST_EXIT" 0
assert_file "the archive is written" "$OUT/Unknown_API.zip"

section "output location"

CUSTOM="$WORK_DIR/03-custom/nested"
rm -rf "$WORK_DIR/03-custom"
run_landscaper artifact pack "$FIRST" --skip-version-check --output "$CUSTOM"
assert_exit "--output creates the folder it needs" "$LAST_EXIT" 0
assert_file "the archive lands there" "$CUSTOM/$ARTIFACT_ID.zip"

summary
