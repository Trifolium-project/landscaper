#!/usr/bin/env bash
#The version rules of "artifact pack": the tenant is read, but nothing is ever
#written to it. Needs credentials, makes no changes.

SCRIPT_NAME="02-version-rules"
source "$(dirname "${BASH_SOURCE[0]}")/lib/common.sh"
setup
require_tenant

OUT="$WORK_DIR/02-out"
rm -rf "$OUT"

TENANT_VERSION="$(tenant_version "$ARTIFACT_ID" "$ORIGINAL_ENV" "$PACKAGE_ID")"
if [ -z "$TENANT_VERSION" ]; then
    printf 'artifact %s is not in %s, upload it once before running this script\n' "$ARTIFACT_ID" "$ORIGINAL_ENV"
    exit 1
fi
printf '%s holds %s at %s\n' "$ORIGINAL_ENV" "$ARTIFACT_ID" "$TENANT_VERSION"

#Derived from the tenant, so the script does not care what state it is in
BEHIND="$(awk -F. -v v="$TENANT_VERSION" 'BEGIN{split(v,p,"."); print p[1]"."p[2]"."(p[3]>0?p[3]-1:0)}')"
AHEAD="$(awk  -F. -v v="$TENANT_VERSION" 'BEGIN{split(v,p,"."); print p[1]"."p[2]"."(p[3]+1)}')"

section "a local version above the tenant is packed unchanged"

SOURCE="$(scratch_artifact)"
set_version "$SOURCE" "$AHEAD"
run_landscaper artifact pack "$SOURCE" --output "$OUT"
assert_exit "pack succeeds" "$LAST_EXIT" 0
assert_contains "the tenant version is reported" "$OUTPUT" "$TENANT_VERSION"
assert_equals "the manifest is not touched" "$(dir_header "$SOURCE" Bundle-Version)" "$AHEAD"

section "a version at or below the tenant needs a decision"

SOURCE="$(scratch_artifact)"
set_version "$SOURCE" "$TENANT_VERSION"
run_landscaper artifact pack "$SOURCE" --output "$OUT" < /dev/null
assert_exit "without a terminal and without flags it fails" "$LAST_EXIT" 1
assert_contains "the error names --bump" "$OUTPUT" "--bump"
assert_contains "the error names --set-version" "$OUTPUT" "--set-version"
assert_contains "the error names --skip-version-check" "$OUTPUT" "--skip-version-check"
assert_equals "a failed run leaves the manifest alone" \
    "$(dir_header "$SOURCE" Bundle-Version)" "$TENANT_VERSION"

section "the bump is relative to the tenant, not to the local copy"

WANT_PATCH="$(awk -v v="$TENANT_VERSION" 'BEGIN{split(v,p,"."); print p[1]"."p[2]"."(p[3]+1)}')"
WANT_MINOR="$(awk -v v="$TENANT_VERSION" 'BEGIN{split(v,p,"."); print p[1]"."(p[2]+1)".0"}')"
WANT_MAJOR="$(awk -v v="$TENANT_VERSION" 'BEGIN{split(v,p,"."); print (p[1]+1)".0.0"}')"

for level_want in "patch:$WANT_PATCH" "minor:$WANT_MINOR" "major:$WANT_MAJOR"; do
    LEVEL="${level_want%%:*}"
    WANT="${level_want##*:}"

    SOURCE="$(scratch_artifact)"
    #Deliberately behind, to show the bump follows the tenant
    set_version "$SOURCE" "$BEHIND"
    run_landscaper artifact pack "$SOURCE" --bump="$LEVEL" --output "$OUT"
    assert_exit "--bump=$LEVEL succeeds" "$LAST_EXIT" 0
    assert_equals "--bump=$LEVEL writes $WANT into the manifest" \
        "$(dir_header "$SOURCE" Bundle-Version)" "$WANT"
    assert_equals "--bump=$LEVEL packs $WANT" \
        "$(zip_header "$OUT/$ARTIFACT_ID.zip" Bundle-Version)" "$WANT"
done

section "--set-version"

SOURCE="$(scratch_artifact)"
set_version "$SOURCE" "$TENANT_VERSION"
run_landscaper artifact pack "$SOURCE" --set-version=9.9.9 --output "$OUT"
assert_exit "an explicit version above the tenant is accepted" "$LAST_EXIT" 0
assert_equals "it is written into the manifest" "$(dir_header "$SOURCE" Bundle-Version)" "9.9.9"

SOURCE="$(scratch_artifact)"
set_version "$SOURCE" "$TENANT_VERSION"
run_landscaper artifact pack "$SOURCE" --set-version=0.0.1 --output "$OUT"
assert_exit "a version not above the tenant is rejected" "$LAST_EXIT" 1
assert_contains "the error explains why" "$OUTPUT" "not higher"

run_landscaper artifact pack "$SOURCE" --set-version=abc --output "$OUT"
assert_exit "an unparseable version is rejected" "$LAST_EXIT" 1

run_landscaper artifact pack "$SOURCE" --bump=build --output "$OUT"
assert_exit "an unknown bump level is rejected" "$LAST_EXIT" 1
assert_contains "the error lists the levels" "$OUTPUT" "patch"

section "--skip-version-check does not read the tenant"

SOURCE="$(scratch_artifact)"
set_version "$SOURCE" "$BEHIND"
run_landscaper artifact pack "$SOURCE" --skip-version-check --output "$OUT"
assert_exit "pack succeeds even though the version is behind" "$LAST_EXIT" 0
assert_equals "the behind version is packed as it is" \
    "$(zip_header "$OUT/$ARTIFACT_ID.zip" Bundle-Version)" "$BEHIND"

section "packing for a target environment never writes the manifest"

TARGET_VERSION="$(tenant_version "${ARTIFACT_ID}${TARGET_SUFFIX}" "$TARGET_ENV" "$PACKAGE_ID")"
SOURCE="$(scratch_artifact)"
BEFORE="$WORK_DIR/02-before"
rm -rf "$BEFORE"; cp -R "$SOURCE" "$BEFORE"

if [ -n "$TARGET_VERSION" ]; then
    #Equal to the target, which is the case that warns
    set_version "$SOURCE" "$TARGET_VERSION"
    cp -R "$SOURCE/." "$BEFORE/"

    run_landscaper artifact pack "$SOURCE" --target-env="$TARGET_ENV" --output "$OUT"
    assert_exit "pack succeeds" "$LAST_EXIT" 0
    assert_contains "a misalignment warning is printed" "$OUTPUT" "WARNING"
    assert_contains "the warning says it proceeds" "$OUTPUT" "uploading it anyway"
    assert_equals "the version is packed as it is" \
        "$(zip_header "$OUT/${ARTIFACT_ID}${TARGET_SUFFIX}.zip" Bundle-Version)" "$TARGET_VERSION"
else
    skip "misalignment warning, ${ARTIFACT_ID}${TARGET_SUFFIX} is not in $TARGET_ENV yet"
fi

run_landscaper artifact pack "$SOURCE" --target-env="$TARGET_ENV" --bump=patch --set-version=8.8.8 --output "$OUT"
assert_exit "pack succeeds" "$LAST_EXIT" 0
assert_contains "--bump is reported as ignored" "$OUTPUT" "--bump is ignored"
assert_contains "--set-version is reported as ignored" "$OUTPUT" "--set-version is ignored"
assert_tree_identical "the working copy is never written for $TARGET_ENV" "$BEFORE" "$SOURCE"

summary
