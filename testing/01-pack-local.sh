#!/usr/bin/env bash
#Packing that never contacts a tenant: archive layout, reproducibility, and the
#archive-only rename for a suffixed environment.
#Safe to run anywhere, no credentials needed.

SCRIPT_NAME="01-pack-local"
source "$(dirname "${BASH_SOURCE[0]}")/lib/common.sh"
setup

OUT="$WORK_DIR/01-out"
rm -rf "$OUT"
SOURCE="$(scratch_artifact)"
LOCAL_VERSION="$(dir_header "$SOURCE" Bundle-Version)"

section "the archive holds the whole folder, manifest at the root"

run_landscaper artifact pack "$SOURCE" --skip-version-check --output "$OUT"
assert_exit "pack succeeds" "$LAST_EXIT" 0
assert_file "archive written" "$OUT/$ARTIFACT_ID.zip"

ENTRIES="$(unzip -Z1 "$OUT/$ARTIFACT_ID.zip" 2>/dev/null)"
SOURCE_COUNT="$(find "$SOURCE" -type f ! -name '.DS_Store' | wc -l | tr -d ' ')"
ARCHIVE_COUNT="$(printf '%s\n' "$ENTRIES" | grep -c .)"
assert_equals "every file is packed" "$ARCHIVE_COUNT" "$SOURCE_COUNT"
assert_contains "manifest sits at the archive root" "$ENTRIES" "META-INF/MANIFEST.MF"
assert_not_contains "no .DS_Store" "$ENTRIES" ".DS_Store"

section "no tenant was contacted"

assert_contains "tenant version reported as unknown" "$OUTPUT" "-"
assert_equals "packed version is the local one" \
    "$(zip_header "$OUT/$ARTIFACT_ID.zip" Bundle-Version)" "$LOCAL_VERSION"

section "the archive is reproducible"

run_landscaper artifact pack "$SOURCE" --skip-version-check --output "$OUT/a"
run_landscaper artifact pack "$SOURCE" --skip-version-check --output "$OUT/b"
FIRST="$(shasum "$OUT/a/$ARTIFACT_ID.zip" | cut -d' ' -f1)"
SECOND="$(shasum "$OUT/b/$ARTIFACT_ID.zip" | cut -d' ' -f1)"
assert_equals "packing twice gives the same bytes" "$FIRST" "$SECOND"

section "packing for the original environment leaves identifiers alone"

assert_equals "Bundle-SymbolicName unchanged" \
    "$(zip_header "$OUT/$ARTIFACT_ID.zip" Bundle-SymbolicName)" \
    "$(dir_header "$SOURCE" Bundle-SymbolicName)"
assert_equals "Bundle-Name unchanged" \
    "$(zip_header "$OUT/$ARTIFACT_ID.zip" Bundle-Name)" \
    "$(dir_header "$SOURCE" Bundle-Name)"

section "packing for a suffixed environment renames inside the archive only"

BEFORE="$WORK_DIR/01-before"
rm -rf "$BEFORE"; cp -R "$SOURCE" "$BEFORE"

run_landscaper artifact pack "$SOURCE" --target-env="$TARGET_ENV" --skip-version-check --output "$OUT"
assert_exit "pack for $TARGET_ENV succeeds" "$LAST_EXIT" 0

SUFFIXED_ZIP="$OUT/${ARTIFACT_ID}${TARGET_SUFFIX}.zip"
assert_file "archive is named after the suffixed id" "$SUFFIXED_ZIP"
assert_no_file "the unsuffixed name is not reused" "$OUT/${ARTIFACT_ID}.zip.qa"

BASE_SYMBOLIC="$(dir_header "$BEFORE" Bundle-SymbolicName)"
BASE_NAME="$(dir_header "$BEFORE" Bundle-Name)"

assert_equals "Bundle-SymbolicName is suffixed in the archive" \
    "$(zip_header "$SUFFIXED_ZIP" Bundle-SymbolicName)" \
    "${BASE_SYMBOLIC/$ARTIFACT_ID/${ARTIFACT_ID}${TARGET_SUFFIX}}"
assert_equals "Bundle-Name is suffixed in the archive" \
    "$(zip_header "$SUFFIXED_ZIP" Bundle-Name)" "$BASE_NAME $TARGET_SUFFIX"
assert_equals "Bundle-Version is untouched" \
    "$(zip_header "$SUFFIXED_ZIP" Bundle-Version)" "$LOCAL_VERSION"
assert_equals "Origin-Bundle-SymbolicName keeps the origin" \
    "$(zip_header "$SUFFIXED_ZIP" Origin-Bundle-SymbolicName)" "$ARTIFACT_ID"

assert_tree_identical "the working copy is never renamed" "$BEFORE" "$SOURCE"

section "a folder without a manifest is rejected"

EMPTY="$WORK_DIR/01-empty"
rm -rf "$EMPTY"; mkdir -p "$EMPTY"
run_landscaper artifact pack "$EMPTY" --skip-version-check --output "$OUT"
assert_exit "exits 1" "$LAST_EXIT" 1
assert_contains "names the missing manifest" "$OUTPUT" "META-INF/MANIFEST.MF"

summary
