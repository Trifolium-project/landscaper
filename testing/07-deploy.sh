#!/usr/bin/env bash
#The --deploy flag. Deployment is fire and forget, so the runtime status has to
#be polled afterwards.
#
#WRITES TO A TENANT AND DEPLOYS. Set LANDSCAPER_TEST_TENANT=1 to run.

SCRIPT_NAME="07-deploy"
source "$(dirname "${BASH_SOURCE[0]}")/lib/common.sh"
setup
require_tenant

OUT="$WORK_DIR/07-out"
rm -rf "$OUT"

DEPLOY_WAIT="${DEPLOY_WAIT:-60}"
TARGET_ARTIFACT="${ARTIFACT_ID}${TARGET_SUFFIX}"

SOURCE="$(scratch_artifact)"
STAMP="$(date +%H%M%S)"
set_version "$SOURCE" "5.$(( 10#${STAMP:0:2} )).$(( 10#${STAMP:2:2} ))"
WANT_VERSION="$(dir_header "$SOURCE" Bundle-Version)"

section "upload without --deploy does not change the runtime"

BEFORE_DEPLOYED="$(tenant_deployed_version "$TARGET_ARTIFACT" "$TARGET_ENV" "$PACKAGE_ID")"
run_landscaper artifact upload "$SOURCE" --target-env="$TARGET_ENV" --output "$OUT"
assert_exit "upload succeeds" "$LAST_EXIT" 0
assert_contains "the row reports Deployed as false" "$OUTPUT" "false"
assert_equals "the deployed version is unchanged" \
    "$(tenant_deployed_version "$TARGET_ARTIFACT" "$TARGET_ENV" "$PACKAGE_ID")" "$BEFORE_DEPLOYED"

section "upload with --deploy"

run_landscaper artifact upload "$SOURCE" --target-env="$TARGET_ENV" --deploy --output "$OUT"
assert_exit "upload succeeds" "$LAST_EXIT" 0
assert_contains "the row reports Deployed as true" "$OUTPUT" "true"

section "the runtime catches up (waiting ${DEPLOY_WAIT}s)"

DEPLOYED=""
for _ in $(seq 1 "$DEPLOY_WAIT"); do
    DEPLOYED="$(tenant_deployed_version "$TARGET_ARTIFACT" "$TARGET_ENV" "$PACKAGE_ID")"
    [ "$DEPLOYED" = "$WANT_VERSION" ] && break
    sleep 1
done

assert_equals "the deployed version is the one just uploaded" "$DEPLOYED" "$WANT_VERSION"

run_landscaper artifact list --pkg="$PACKAGE_ID" --env="$TARGET_ENV" --only-deployed
assert_exit "artifact list --only-deployed succeeds" "$LAST_EXIT" 0
assert_contains "the artifact is listed as deployed" "$OUTPUT" "$TARGET_ARTIFACT"
assert_contains "with a started status" "$OUTPUT" "STARTED"

summary
