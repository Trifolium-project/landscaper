#!/usr/bin/env bash
#Machine readable deployment results: --wait, the exit codes, and the error
#information the tenant returns for a flow that cannot be deployed.
#
#WRITES TO A TENANT. It uploads a deliberately broken integration flow under a
#fixed id, so repeated runs update that one artifact instead of creating more.
#The artifact is left behind - "artifact delete" is still a stub - and has to be
#removed in the Integration Suite UI.

SCRIPT_NAME="12-deploy-status"
source "$(dirname "${BASH_SOURCE[0]}")/lib/common.sh"
setup
require_tenant

#Renames an exploded flow so its Bundle-SymbolicName matches the folder, which
#is what artifact upload requires
rename_symbolic_name() {
    python3 - "$1" "$2" <<'RENAME'
import os, re, sys
root, artifact_id = sys.argv[1], sys.argv[2]
manifest = os.path.join(root, "META-INF", "MANIFEST.MF")
data = open(manifest, encoding="utf-8").read()
data = re.sub(r"Bundle-SymbolicName: [^;\r\n]+", "Bundle-SymbolicName: " + artifact_id, data, count=1)
data = re.sub(r"Bundle-Name: [^\r\n]+", "Bundle-Name: " + artifact_id, data, count=1)
open(manifest, "w", encoding="utf-8").write(data)
RENAME
}

BROKEN_ID="${BROKEN_ID:-TEST_DEPLOY_STATUS_BROKEN}"
BROKEN_DIR="$WORK_DIR/12-broken/$BROKEN_ID"
rm -rf "$WORK_DIR/12-broken"
mkdir -p "$WORK_DIR/12-broken"

#A copy of the fixture with one referenced Groovy script removed. The .iflw
#still points at it, so generation fails during deployment.
cp -R "$BACKUP_DIR" "$BROKEN_DIR"
rename_symbolic_name "$BROKEN_DIR" "$BROKEN_ID"

#The .iflw still references this script, so generation fails during deployment
BROKEN_SCRIPT="$BROKEN_DIR/src/main/resources/script/script1.groovy"
if [ ! -f "$BROKEN_SCRIPT" ]; then
    printf '%sthe fixture no longer contains script1.groovy, adjust this script%s\n' "$C_FAIL" "$C_OFF"
    exit 1
fi
rm -f "$BROKEN_SCRIPT"

section "a healthy artifact reports STARTED and exits 0"

#The shared fixture is deliberately not used here: its draft in the tenant may
#itself be broken, and this section has to assert the success path. A copy this
#script uploads and deploys is known to be sound.
HEALTHY_ID="${BROKEN_ID}_OK"
HEALTHY_DIR="$(scratch_artifact "$HEALTHY_ID")"
rename_symbolic_name "$HEALTHY_DIR" "$HEALTHY_ID"

run_landscaper artifact upload "$HEALTHY_DIR" --target-env "$ORIGINAL_ENV" \
    --pkg "$PACKAGE_ID" --skip-version-check --deploy --wait --timeout 150s
assert_exit "upload --deploy --wait exits 0 for a sound flow" "$LAST_EXIT" 0
assert_contains "the runtime status column is present" "$OUTPUT" "Runtime Status"
assert_contains "the runtime reports STARTED" "$OUTPUT" "STARTED"

run_landscaper artifact get --artifact "$HEALTHY_ID" --env "$ORIGINAL_ENV"
assert_exit "get exits 0 for a started artifact" "$LAST_EXIT" 0
assert_contains "the deploy status is shown" "$OUTPUT" "STARTED"
assert_not_contains "no error block is printed" "$OUTPUT" "Deploy error:"

section "--output json emits the runtime block"

run_landscaper artifact get --artifact "$HEALTHY_ID" --env "$ORIGINAL_ENV" --output json
assert_exit "get exits 0" "$LAST_EXIT" 0

if printf '%s' "$OUTPUT" | python3 -c '
import json, sys
report = json.load(sys.stdin)
for field in ("id", "name", "version", "package", "runtime", "configuration"):
    if field not in report:
        sys.exit("missing field: " + field)
runtime = report["runtime"]
if runtime is None:
    sys.exit("runtime is null for a deployed artifact")
for field in ("status", "version", "deployedOn", "deployedBy", "error"):
    if field not in runtime:
        sys.exit("missing runtime field: " + field)
if runtime["error"] is not None:
    sys.exit("error should be null for a healthy artifact")
'; then
    pass "the json document has the documented shape"
else
    fail "the json document has the documented shape" "see the output above"
fi

section "an undeployed artifact exits 4"

#Uploaded but never deployed, so the tenant holds no runtime artifact
UNDEPLOYED="$(scratch_artifact "${BROKEN_ID}_UNDEPLOYED")"
rename_symbolic_name "$UNDEPLOYED" "${BROKEN_ID}_UNDEPLOYED"
run_landscaper artifact upload "$UNDEPLOYED" --target-env "$ORIGINAL_ENV" \
    --pkg "$PACKAGE_ID" --skip-version-check
assert_exit "upload without --deploy succeeds" "$LAST_EXIT" 0

run_landscaper artifact get --artifact "${BROKEN_ID}_UNDEPLOYED" --env "$ORIGINAL_ENV"
assert_exit "get exits 4 for an artifact that is not deployed" "$LAST_EXIT" 4
assert_contains "the status says so" "$OUTPUT" "Not deployed"

section "a broken flow exits 2 and explains why"

run_landscaper artifact upload "$BROKEN_DIR" --target-env "$ORIGINAL_ENV" \
    --pkg "$PACKAGE_ID" --skip-version-check --deploy --wait --timeout 150s
assert_exit "upload --deploy --wait exits 2 on a failed deployment" "$LAST_EXIT" 2
assert_contains "the result table gained the runtime status column" "$OUTPUT" "Runtime Status"
assert_contains "the runtime status is ERROR" "$OUTPUT" "ERROR"
assert_contains "the error block is printed" "$OUTPUT" "Deploy error:"

#The point of the feature: the text has to name the cause, not just say that
#deployment failed
assert_contains "the error text names the missing script" "$OUTPUT" "script1.groovy"

section "artifact get reports the same failure"

run_landscaper artifact get --artifact "$BROKEN_ID" --env "$ORIGINAL_ENV"
assert_exit "get exits 2 for a failed deployment" "$LAST_EXIT" 2
assert_contains "the error block is printed" "$OUTPUT" "Deploy error:"
assert_contains "the error text names the missing script" "$OUTPUT" "script1.groovy"

section "the failure is machine readable"

run_landscaper artifact get --artifact "$BROKEN_ID" --env "$ORIGINAL_ENV" --output json
assert_exit "get --output json exits 2" "$LAST_EXIT" 2

if printf '%s' "$OUTPUT" | python3 -c '
import json, sys
report = json.load(sys.stdin)
runtime = report.get("runtime")
if runtime is None:
    sys.exit("runtime is null for a deployed artifact")
if runtime.get("status") != "ERROR":
    sys.exit("status is %r, want ERROR" % runtime.get("status"))
error = runtime.get("error")
if not error:
    sys.exit("error is empty for a failed deployment")
if "message" not in error:
    sys.exit("the error has no message")
text = runtime.get("errorText") or ""
if "script1.groovy" not in text:
    sys.exit("errorText does not name the cause: %r" % text)
'; then
    pass "the json error block names the cause"
else
    fail "the json error block names the cause" "see the output above"
fi

section "artifact deploy --wait reports a healthy redeploy"

#A redeploy is where the tenant briefly keeps reporting the PREVIOUS version as
#STARTED, which is what the version check in waitForDeployment guards against
run_landscaper artifact deploy --artifact "$HEALTHY_ID" --env "$ORIGINAL_ENV" \
    --wait --timeout 150s
assert_exit "deploy --wait exits 0" "$LAST_EXIT" 0
assert_contains "the runtime status is reported" "$OUTPUT" "Runtime Status"
assert_contains "the runtime reports STARTED" "$OUTPUT" "STARTED"

summary
