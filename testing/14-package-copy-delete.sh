#!/usr/bin/env bash
#package copy, artifact download of SAP content, and package delete: the copy ->
#download -> delete loop, every exit code, the deployed-content guard, --dry-run
#and the confirmation.
#
#WRITES TO A TENANT. It copies two SAP Business Accelerator Hub packages into
#Design, deploys one integration flow, and deletes both packages again. It
#refuses to start when either package is already in Design, and only ever
#deletes what it copied itself.

SCRIPT_NAME="14-package-copy-delete"
source "$(dirname "${BASH_SOURCE[0]}")/lib/common.sh"
setup
require_tenant

#Editable, one integration flow
EDITABLE_ID="${EDITABLE_ID:-PrivateLinkProxy}"
EDITABLE_FLOW="${EDITABLE_FLOW:-AzureBlobConnectivityPrivateLinkServiceSample}"
#Configure only, integration flows that cannot be downloaded
READONLY_ID="${READONLY_ID:-SAPIBPReusableIntegrationFlowsExamples}"

run_landscaper package list --env "$ORIGINAL_ENV"
for id in "$EDITABLE_ID" "$READONLY_ID"; do
    if printf '%s\n' "$OUTPUT" | awk '{print $2}' | grep -qx "$id"; then
        printf '%s%s is already in Design, this script would delete it. Remove it or pick another package.%s\n' "$C_FAIL" "$id" "$C_OFF"
        exit 1
    fi
done

COPIED=()
cleanup() {
    for id in "${COPIED[@]}"; do
        "$LANDSCAPER" package delete --pkg "$id" --env "$ORIGINAL_ENV" --yes --undeploy >/dev/null 2>&1
    done
}
trap cleanup EXIT

section "package copy: exit codes without writing"

run_landscaper package copy --id NoSuchHubPackage_landscaper_test --env "$ORIGINAL_ENV"
assert_exit "a package unknown to Discover exits 6" "$LAST_EXIT" 6

run_landscaper package copy --id "$EDITABLE_ID" --env "$ORIGINAL_ENV" --import-mode create-copy
assert_exit "create-copy without --suffix is refused" "$LAST_EXIT" 1

section "copy, download, delete an editable package"

run_landscaper package copy --id "$EDITABLE_ID" --env "$ORIGINAL_ENV" --output json
assert_exit "copy exits 0" "$LAST_EXIT" 0
COPIED+=("$EDITABLE_ID")
CREATED_ID="$(printf '%s' "$OUTPUT" | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])' 2>/dev/null)"
assert_equals "the created package id is reported" "$CREATED_ID" "$EDITABLE_ID"
assert_contains "its flow is listed" "$OUTPUT" "$EDITABLE_FLOW"

#The tenant checks Discover before Design, so "exists" needs a Hub package
run_landscaper package copy --id "$EDITABLE_ID" --env "$ORIGINAL_ENV" --output json
assert_exit "copying it again exits 5" "$LAST_EXIT" 5
assert_contains "and is reported as exists" "$OUTPUT" '"status": "exists"'

DOWNLOAD_DIR="$WORK_DIR/14-download"
rm -rf "$DOWNLOAD_DIR"
run_landscaper artifact download --packages "$EDITABLE_ID" --env "$ORIGINAL_ENV" --output "$DOWNLOAD_DIR"
assert_exit "download of the editable SAP package exits 0" "$LAST_EXIT" 0
assert_file "the flow is extracted" "$DOWNLOAD_DIR/$EDITABLE_ID/$EDITABLE_FLOW/META-INF/MANIFEST.MF"

section "package delete: guards"

run_landscaper package delete --pkg NoSuchPackage_landscaper_test --env "$ORIGINAL_ENV" --yes
assert_exit "an unknown package exits 4" "$LAST_EXIT" 4

run_landscaper package delete --pkg "$PACKAGE_ID" --env "$ORIGINAL_ENV" --dry-run
assert_exit "a declared package exits 6" "$LAST_EXIT" 6

OUTPUT="$("$LANDSCAPER" package delete --pkg "$EDITABLE_ID" --env "$ORIGINAL_ENV" 2>&1 </dev/null)"
LAST_EXIT=$?
assert_exit "without a terminal --yes is required" "$LAST_EXIT" 1
assert_contains "and the message says so" "$OUTPUT" "--yes"

LOG_DIR="$WORK_DIR/14-logs"
rm -rf "$LOG_DIR"
run_landscaper package delete --pkg "$EDITABLE_ID" --env "$ORIGINAL_ENV" --dry-run --output json --log --log-dir "$LOG_DIR"
assert_exit "--dry-run exits 0" "$LAST_EXIT" 0
assert_contains "and deletes nothing" "$OUTPUT" '"deleted": false'
if python3 -c '
import glob, json, sys
for path in glob.glob(sys.argv[1] + "/*.log"):
    for line in open(path):
        record = json.loads(line)
        if record.get("type") == "http" and record.get("method") != "GET":
            sys.exit("write call: %s %s" % (record["method"], record["url"]))
' "$LOG_DIR"; then
    pass "the audit log of the dry run holds only GET calls"
else
    fail "the dry run wrote to the tenant"
fi

run_landscaper artifact deploy --artifact "$EDITABLE_FLOW" --env "$ORIGINAL_ENV" --wait --timeout 150s
assert_exit "the copied flow deploys" "$LAST_EXIT" 0

run_landscaper package delete --pkg "$EDITABLE_ID" --env "$ORIGINAL_ENV" --yes
assert_exit "deployed content exits 5" "$LAST_EXIT" 5
assert_contains "and the deployed flow is named" "$OUTPUT" "$EDITABLE_FLOW"

run_landscaper package delete --pkg "$EDITABLE_ID" --env "$ORIGINAL_ENV" --yes --undeploy --output json
assert_exit "--undeploy then delete exits 0" "$LAST_EXIT" 0
assert_contains "the flow was undeployed" "$OUTPUT" "\"undeployed\": [
    \"$EDITABLE_FLOW\""

run_landscaper package list --env "$ORIGINAL_ENV"
assert_not_contains "the package is gone" "$OUTPUT" "$EDITABLE_ID"

run_landscaper artifact get --artifact "$EDITABLE_FLOW" --env "$ORIGINAL_ENV"
assert_not_contains "and nothing of it runs" "$OUTPUT" "STARTED"

section "a configure only package"

run_landscaper package copy --id "$READONLY_ID" --env "$ORIGINAL_ENV" --output json
assert_exit "copy exits 0" "$LAST_EXIT" 0
COPIED+=("$READONLY_ID")
assert_contains "the mode is READ_ONLY" "$OUTPUT" '"mode": "READ_ONLY"'

run_landscaper artifact download --packages "$READONLY_ID" --env "$ORIGINAL_ENV" --output "$DOWNLOAD_DIR"
assert_exit "download exits 7" "$LAST_EXIT" 7
assert_contains "every flow is reported" "$OUTPUT" "not downloadable (configure-only SAP package)"

run_landscaper package delete --pkg "$READONLY_ID" --env "$ORIGINAL_ENV" --yes
assert_exit "delete exits 0" "$LAST_EXIT" 0

run_landscaper package delete --pkg "$READONLY_ID" --env "$ORIGINAL_ENV" --yes
assert_exit "deleting it again exits 4" "$LAST_EXIT" 4

summary
