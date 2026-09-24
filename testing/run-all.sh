#!/usr/bin/env bash
#Runs the test scripts in order and prints one tally at the end.
#
#  ./testing/run-all.sh                 local scripts only, no credentials
#  ./testing/run-all.sh --with-tenant   everything, WRITES TO A REAL TENANT
#  ./testing/run-all.sh --with-tenant --cleanup   and restore afterwards

set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

WITH_TENANT=0
WITH_CLEANUP=0
for arg in "$@"; do
    case "$arg" in
        --with-tenant) WITH_TENANT=1 ;;
        --cleanup)     WITH_CLEANUP=1 ;;
        -h|--help)     sed -n '2,9p' "${BASH_SOURCE[0]}"; exit 0 ;;
        *)             printf 'unknown option: %s\n' "$arg"; exit 2 ;;
    esac
done

LOCAL_SCRIPTS=(00-unit-tests.sh 01-pack-local.sh 03-multi-artifact.sh 08-upload-errors.sh 11-logging.sh)
TENANT_SCRIPTS=(02-version-rules.sh 04-upload-target-env.sh 05-upload-original-env.sh 06-zip-input.sh 07-deploy.sh 10-download.sh 12-deploy-status.sh)

SCRIPTS=("${LOCAL_SCRIPTS[@]}")
if [ "$WITH_TENANT" = "1" ]; then
    export LANDSCAPER_TEST_TENANT=1
    SCRIPTS=(00-unit-tests.sh 01-pack-local.sh 02-version-rules.sh 03-multi-artifact.sh
             04-upload-target-env.sh 05-upload-original-env.sh 06-zip-input.sh
             07-deploy.sh 08-upload-errors.sh 10-download.sh 11-logging.sh 12-deploy-status.sh)
fi

FAILED_SCRIPTS=()
for script in "${SCRIPTS[@]}"; do
    printf '\n\033[1m########## %s ##########\033[0m\n' "$script"
    if ! "$HERE/$script"; then
        FAILED_SCRIPTS+=("$script")
    fi
done

if [ "$WITH_CLEANUP" = "1" ]; then
    printf '\n\033[1m########## 09-cleanup.sh ##########\033[0m\n'
    "$HERE/09-cleanup.sh" || FAILED_SCRIPTS+=("09-cleanup.sh")
fi

printf '\n\033[1m########## result ##########\033[0m\n'
if [ "${#FAILED_SCRIPTS[@]}" -eq 0 ]; then
    printf '\033[32mall %d scripts passed\033[0m\n' "${#SCRIPTS[@]}"
    [ "$WITH_TENANT" = "0" ] && printf 'tenant scripts were not run, add --with-tenant\n'
    exit 0
fi

printf '\033[31m%d script(s) failed:\033[0m\n' "${#FAILED_SCRIPTS[@]}"
printf '  - %s\n' "${FAILED_SCRIPTS[@]}"
exit 1
