#!/usr/bin/env bash
#Puts the working tree back and removes the scratch directory.
#
#It does NOT delete anything from the tenant: "artifact delete" and
#"package delete" are still stubs, so whatever the tenant scripts created has
#to be removed in the Integration Suite UI.

SCRIPT_NAME="09-cleanup"
source "$(dirname "${BASH_SOURCE[0]}")/lib/common.sh"

if [ ! -d "$BACKUP_DIR" ]; then
    printf 'no backup at %s, nothing to restore\n' "$BACKUP_DIR"
    exit 0
fi

section "restore the repository fixture"

BEFORE_VERSION="$(dir_header "$ARTIFACT_DIR" Bundle-Version 2>/dev/null || echo unknown)"
BACKUP_VERSION="$(dir_header "$BACKUP_DIR" Bundle-Version)"

if [ "${KEEP_FIXTURE:-0}" = "1" ]; then
    skip "restore, KEEP_FIXTURE=1 (working tree stays at $BEFORE_VERSION)"
else
    restore_fixture
    assert_equals "the fixture is back at its original version" \
        "$(dir_header "$ARTIFACT_DIR" Bundle-Version)" "$BACKUP_VERSION"
    assert_tree_identical "and is identical to the backup" "$BACKUP_DIR" "$ARTIFACT_DIR"
fi

section "remove build output"

rm -rf "$REPO_ROOT/build"/*.zip 2>/dev/null
pass "build artifacts removed"

section "remove the scratch directory"

if [ "${KEEP_WORK_DIR:-0}" = "1" ]; then
    skip "keeping $WORK_DIR"
else
    #The backup is inside WORK_DIR, so it goes too. Restore happened above.
    rm -rf "$WORK_DIR"
    assert_no_file "the scratch directory is gone" "$WORK_DIR"
fi

printf '\nThe tenant is NOT cleaned up. "artifact delete" and "package delete" are\n'
printf 'stubs, so remove anything the tenant scripts created in the UI:\n'
printf '  package %s%s\n' "$PACKAGE_ID" "$TARGET_SUFFIX"

summary
