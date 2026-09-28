#!/usr/bin/env bash
#Design guidelines: run, results, skip and unskip, the declared guidelineSkips,
#--fail-on and its exit code, and the JSON document.
#
#WRITES TO A TENANT. It runs the guidelines on the fixture artifact, which
#replaces its latest execution, and skips one rule, which is reverted at the
#end. Needs design guidelines activated for the tenant.

SCRIPT_NAME="13-design-guidelines"
source "$(dirname "${BASH_SOURCE[0]}")/lib/common.sh"
setup
require_tenant

#A rule every flow without an exception subprocess violates, and that can be
#skipped. CAMEL_CLASSES_USAGE is essential and cannot.
SKIP_RULE="${SKIP_RULE:-CONTINUE_MESSAGE_PROCESSING_EVEN_AFTER_AN_EXCEPTION}"
ESSENTIAL_RULE="${ESSENTIAL_RULE:-CAMEL_CLASSES_USAGE}"

#A copy of the landscape with a declared skip, so conf/landscape.yaml is not touched
SKIP_LANDSCAPE="$WORK_DIR/13-landscape.yaml"
python3 - "$REPO_ROOT/conf/landscape.yaml" "$SKIP_LANDSCAPE" "$ARTIFACT_ID" "$SKIP_RULE" <<'DECLARE'
import re, sys
source, target, artifact, rule = sys.argv[1:5]
data = open(source, encoding="utf-8").read()
pattern = re.compile(r"^(\s*)- id: " + re.escape(artifact) + r"\s*$", re.M)
match = pattern.search(data)
if not match:
    sys.exit("artifact %s is not declared in conf/landscape.yaml" % artifact)
indent = match.group(1) + "  "
block = "\n%sguidelineSkips:\n%s  - rule: %s\n%s    reason: Declared by 13-design-guidelines.sh" % (indent, indent, rule, indent)
data = data[:match.end()] + block + data[match.end():]
open(target, "w", encoding="utf-8").write(data)
DECLARE

section "run reports every rule in JSON"

run_landscaper artifact guidelines run --env "$ORIGINAL_ENV" --artifacts "$ARTIFACT_ID" \
    --wait --output json --fail-on none --no-declared-skips
assert_exit "run exits 0 with --fail-on none" "$LAST_EXIT" 0

if printf '%s' "$OUTPUT" | python3 -c '
import json, sys
report = json.load(sys.stdin)
for field in ("environment", "failOn", "summary", "artifacts"):
    if field not in report:
        sys.exit("missing field: " + field)
artifact = report["artifacts"][0]
if not artifact["executionId"] or artifact["status"] not in ("compliant", "not-compliant"):
    sys.exit("unexpected artifact block: %r" % {k: v for k, v in artifact.items() if k != "rules"})
if len(artifact["rules"]) < 10:
    sys.exit("expected every activated rule, got %d" % len(artifact["rules"]))
for field in ("id", "name", "category", "severity", "status", "skipped", "skipReason", "violatedComponents"):
    if field not in artifact["rules"][0]:
        sys.exit("missing rule field: " + field)
'; then
    pass "the JSON document has the summary, the artifact and every rule"
else
    fail "the JSON document is incomplete"
fi

section "--fail-on and exit code 7"

run_landscaper artifact guidelines results --env "$ORIGINAL_ENV" --artifacts "$ARTIFACT_ID" --fail-on low
if printf '%s' "$OUTPUT" | grep -q "not-compliant"; then
    assert_exit "a not compliant rule exits 7" "$LAST_EXIT" 7
else
    assert_exit "a compliant artifact exits 0" "$LAST_EXIT" 0
fi
assert_contains "the summary block is printed" "$OUTPUT" "===Summary==="

run_landscaper artifact guidelines results --env "$ORIGINAL_ENV" --artifacts "$ARTIFACT_ID" --fail-on critical
assert_exit "an unknown --fail-on is refused" "$LAST_EXIT" 1

run_landscaper artifact guidelines results --env "$ORIGINAL_ENV"
assert_exit "a missing selector is refused" "$LAST_EXIT" 1

section "a declared skip is applied and reported"

#Filed by hand with another reason first: the tenant refuses to skip a skipped
#rule, so the declared reason can only win by reverting and skipping again
run_landscaper artifact guidelines skip --env "$ORIGINAL_ENV" --artifacts "$ARTIFACT_ID" \
    --rule "$SKIP_RULE" --reason "filed by hand"
assert_exit "a skip by hand exits 0" "$LAST_EXIT" 0

run_landscaper artifact guidelines skip --env "$ORIGINAL_ENV" --artifacts "$ARTIFACT_ID" \
    --rule "$SKIP_RULE" --reason "filed by hand"
assert_exit "repeating the same skip exits 0" "$LAST_EXIT" 0
assert_contains "and reports it as unchanged" "$OUTPUT" "unchanged"

run_landscaper --landscape-file "$SKIP_LANDSCAPE" artifact guidelines run --env "$ORIGINAL_ENV" \
    --artifacts "$ARTIFACT_ID" --wait --output json --fail-on none
assert_exit "run with a declared skip exits 0" "$LAST_EXIT" 0

if printf '%s' "$OUTPUT" | python3 -c '
import json, sys
rule_id = sys.argv[1]
artifact = json.load(sys.stdin)["artifacts"][0]
skips = artifact.get("declaredSkips") or {}
if skips.get("applied") != 1:
    sys.exit("declaredSkips = %r" % skips)
rule = [r for r in artifact["rules"] if r["id"] == rule_id][0]
if rule["status"] != "skipped" or "13-design-guidelines" not in rule["skipReason"]:
    sys.exit("rule = %r" % rule)
' "$SKIP_RULE"; then
    pass "the rule is skipped with the declared reason"
else
    fail "the declared skip was not applied"
    printf '%s\n' "$OUTPUT" | head -20
fi

section "skip and unskip"

run_landscaper artifact guidelines skip --env "$ORIGINAL_ENV" --artifacts "$ARTIFACT_ID" \
    --rule "$ESSENTIAL_RULE" --reason "must be refused"
assert_exit "an essential rule cannot be skipped" "$LAST_EXIT" 1
assert_contains "the tenant's reason is shown" "$OUTPUT" "essential"

run_landscaper artifact guidelines unskip --env "$ORIGINAL_ENV" --artifacts "$ARTIFACT_ID" --rule "$SKIP_RULE"
assert_exit "unskip exits 0" "$LAST_EXIT" 0
assert_contains "the row reports the revert" "$OUTPUT" "unskipped"

run_landscaper artifact guidelines unskip --env "$ORIGINAL_ENV" --artifacts "$ARTIFACT_ID" --rule "$SKIP_RULE"
assert_exit "repeating the unskip exits 0" "$LAST_EXIT" 0
assert_contains "and reports it as unchanged" "$OUTPUT" "unchanged"

run_landscaper artifact guidelines results --env "$ORIGINAL_ENV" --artifacts "$ARTIFACT_ID" --output json --fail-on none
if printf '%s' "$OUTPUT" | python3 -c '
import json, sys
rule_id = sys.argv[1]
artifact = json.load(sys.stdin)["artifacts"][0]
rule = [r for r in artifact["rules"] if r["id"] == rule_id][0]
if rule["skipped"]:
    sys.exit("still skipped")
' "$SKIP_RULE"; then
    pass "the skip is reverted in the tenant"
else
    fail "the skip is still in the tenant"
fi

section "rules"

run_landscaper artifact guidelines rules --env "$ORIGINAL_ENV" --artifacts "$ARTIFACT_ID"
assert_exit "rules exits 0" "$LAST_EXIT" 0
assert_contains "the catalogue lists the skipped rule" "$OUTPUT" "$SKIP_RULE"

summary
