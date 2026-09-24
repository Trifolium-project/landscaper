#!/usr/bin/env bash
#The audit log: off by default, opened by --log, relocatable with --log-dir, and
#never carrying a credential. Uses "artifact pack", so no tenant is contacted.
#Safe to run anywhere, no credentials needed.

SCRIPT_NAME="11-logging"
source "$(dirname "${BASH_SOURCE[0]}")/lib/common.sh"
setup

LOG_DIR="$WORK_DIR/11-logs"
OUT="$WORK_DIR/11-out"
rm -rf "$LOG_DIR" "$OUT"
SOURCE="$(scratch_artifact)"

#JSON Lines validation without jq: nothing else in testing/ assumes jq is
#installed, and the suite has to run unchanged in a pipeline.
assert_valid_jsonl() {
    local name="$1" file="$2"
    if python3 -c '
import json, sys
path = sys.argv[1]
count = 0
with open(path) as handle:
    for number, line in enumerate(handle, 1):
        line = line.strip()
        if not line:
            continue
        try:
            json.loads(line)
        except Exception as error:
            print("line %d is not valid JSON: %s" % (number, error))
            sys.exit(1)
        count += 1
if count == 0:
    print("the log is empty")
    sys.exit(1)
' "$file"; then
        pass "$name"
    else
        fail "$name" "invalid JSON Lines in $file"
    fi
}

#Prints the distinct values of the "type" field
record_types() {
    python3 -c '
import json, sys
types = set()
with open(sys.argv[1]) as handle:
    for line in handle:
        line = line.strip()
        if line:
            types.add(json.loads(line).get("type", ""))
print(" ".join(sorted(types)))
' "$1"
}

section "no log is written without --log"

#Counted rather than asserting that logs/ is absent: a developer who has used
#--log before would otherwise see this fail for no reason
LOGS_BEFORE="$(find logs -name 'landscaper-*.log' -type f 2>/dev/null | wc -l | tr -d ' ')"

run_landscaper artifact pack "$SOURCE" --skip-version-check --output "$OUT"
assert_exit "pack succeeds" "$LAST_EXIT" 0

LOGS_AFTER="$(find logs -name 'landscaper-*.log' -type f 2>/dev/null | wc -l | tr -d ' ')"
assert_equals "no log was written to the default folder" "$LOGS_AFTER" "$LOGS_BEFORE"
assert_no_file "no log folder appeared" "$LOG_DIR"

section "--log writes a JSON Lines file into --log-dir"

run_landscaper artifact pack "$SOURCE" --skip-version-check --output "$OUT" \
    --log --log-dir "$LOG_DIR"
assert_exit "pack succeeds" "$LAST_EXIT" 0
#filepath.Join cleans the path, and TMPDIR often ends in a slash, so the
#announcement is compared against a normalised form
LOG_DIR_CLEAN="$(python3 -c 'import os,sys; print(os.path.normpath(sys.argv[1]))' "$LOG_DIR")"
assert_contains "the log location is announced" "$OUTPUT" "$LOG_DIR_CLEAN"

LOG_FILE="$(find "$LOG_DIR" -name 'landscaper-*.log' -type f | head -1)"
if [ -n "$LOG_FILE" ]; then
    pass "a timestamped log file was created"
else
    fail "a timestamped log file was created" "nothing matching landscaper-*.log in $LOG_DIR"
    summary
fi

assert_valid_jsonl "every line is valid JSON" "$LOG_FILE"

section "the run and its parameters are recorded"

TYPES="$(record_types "$LOG_FILE")"
assert_contains "a run record is present" "$TYPES" "run"
assert_contains "the command is recorded" "$(cat "$LOG_FILE")" "artifact pack"
assert_contains "the parameters are recorded" "$(cat "$LOG_FILE")" "skip-version-check"

section "the log file is not world readable"

#It carries complete tenant responses
PERMISSIONS="$(ls -l "$LOG_FILE" | cut -c1-10)"
assert_equals "mode is -rw-------" "$PERMISSIONS" "-rw-------"

section "no credential reaches the log"

#The landscape file names the environment variables holding the credentials
for VARIABLE in $(grep -oE '^\s*(login|password|tokenURL):\s*\S+' conf/landscape.yaml 2>/dev/null | awk '{print $2}'); do
    VALUE="$(grep -E "^$VARIABLE=" .env 2>/dev/null | cut -d= -f2-)"
    #Only meaningful for a value long enough not to match by accident
    if [ -n "$VALUE" ] && [ "${#VALUE}" -ge 12 ]; then
        if grep -qF "$VALUE" "$LOG_FILE"; then
            fail "the value of $VARIABLE is absent from the log" "found in $LOG_FILE"
        else
            pass "the value of $VARIABLE is absent from the log"
        fi
    fi
done

assert_not_contains "no bearer token" "$(cat "$LOG_FILE")" "Bearer ey"
assert_not_contains "no basic credentials" "$(cat "$LOG_FILE")" "Authorization\":\"Basic"

section "a second run writes its own file"

run_landscaper artifact pack "$SOURCE" --skip-version-check --output "$OUT" \
    --log --log-dir "$LOG_DIR"
assert_exit "pack succeeds" "$LAST_EXIT" 0

COUNT="$(find "$LOG_DIR" -name 'landscaper-*.log' -type f | wc -l | tr -d ' ')"
#Two runs in the same second share a name and append, which must not lose either
if [ "$COUNT" -ge 1 ]; then
    pass "runs are kept ($COUNT file(s))"
else
    fail "runs are kept" "no log files found"
fi
assert_valid_jsonl "the log is still valid JSON Lines" "$LOG_FILE"

section "a failing run is recorded as failed"

run_landscaper artifact pack /does/not/exist --skip-version-check --output "$OUT" \
    --log --log-dir "$LOG_DIR"
assert_exit "pack fails" "$LAST_EXIT" 1

FAIL_LOG="$(find "$LOG_DIR" -name 'landscaper-*.log' -type f -newer "$LOG_FILE" | head -1)"
[ -z "$FAIL_LOG" ] && FAIL_LOG="$LOG_FILE"
assert_contains "the failure is recorded" "$(cat "$FAIL_LOG")" "fatal"
assert_contains "the run is marked failed" "$(cat "$FAIL_LOG")" '"status":"failed"'

summary
