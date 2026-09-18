# Test scripts

Executable scenarios for `artifact pack` and `artifact upload`. Each script
asserts rather than just printing, reports `PASS`/`FAIL` per check and exits
non zero if anything failed, so they can be dropped into a pipeline as they are.

Written for **bash**, not fish, so that the same scripts run unchanged in CI.

## Running them

```bash
./testing/run-all.sh                            # local only, no credentials
./testing/run-all.sh --with-tenant              # everything, WRITES TO A TENANT
./testing/run-all.sh --with-tenant --cleanup    # and restore the fixture afterwards
```

Any script can be run on its own:

```bash
./testing/01-pack-local.sh
LANDSCAPER_TEST_TENANT=1 ./testing/04-upload-target-env.sh
```

## The scripts

| Script | Tenant | Covers |
|---|---|---|
| `00-unit-tests.sh` | no | `go build`, `go vet`, `go test ./packages/...` |
| `01-pack-local.sh` | no | Archive layout, reproducibility, the archive-only rename, `--output` |
| `02-version-rules.sh` | **reads** | The whole version rule: local ahead, the non interactive failure, `--bump` relative to the tenant, `--set-version`, `--skip-version-check`, the target environment warning |
| `03-multi-artifact.sh` | no | Several artifacts per call, partial progress on failure, symbolic name mismatch, package resolution and `--pkg` |
| `04-upload-target-env.sh` | **writes** | Upload to a suffixed environment: the symbolic name rename, the update that used to be rejected, the repository never being written, ignored version flags, configuration |
| `05-upload-original-env.sh` | **writes** | Upload to the original environment: no suffix, `--bump` written back to the working copy |
| `06-zip-input.sh` | **writes** | A ready archive as input, both already suffixed and base named, mixed with a folder |
| `07-deploy.sh` | **writes, deploys** | `--deploy` and the runtime status afterwards |
| `08-upload-errors.sh` | no* | Missing `--target-env`, unknown environment, bad paths, undeclared artifact |
| `09-cleanup.sh` | no | Restores the fixture and removes the scratch directory |
| `10-download.sh` | **reads** | `artifact download`: the folder layout, the version, the skip and `--force` rules, verbatim ids for a suffixed environment, `--download-all` scoping, the flag validation |

\* `08` runs one extra tenant assertion when `LANDSCAPER_TEST_TENANT=1`.

`lib/common.sh` holds the assertions, the fixture helpers and the tenant guard.
Source it, do not run it.

## Safety

**Scripts marked "writes" change a real tenant.** They refuse to run unless
`LANDSCAPER_TEST_TENANT=1` is set, so a local run cannot touch a tenant by
accident.

**`artifacts/` is gitignored**, so a version bump written into
`META-INF/MANIFEST.MF` cannot be undone with git. `setup` copies the fixture to
`$WORK_DIR/fixture-backup` on the first run and `09-cleanup.sh` restores it.
Almost every script works on a throwaway copy under `$WORK_DIR/scratch`
instead of the repository folder; `05-upload-original-env.sh` is the one that
deliberately exercises the write-back path.

**The tenant is never cleaned up.** `artifact delete` and `package delete` are
still stubs, so the package the tenant scripts create has to be removed in the
Integration Suite UI.

The `--env` plus `--target-env` double suffix trap is deliberately **not**
scripted: running it creates a `...QAQA` package that cannot be deleted from
the CLI. It is described in `MANUAL-TEST-PLAN.md`.

## Configuration

| Variable | Default | Meaning |
|---|---|---|
| `LANDSCAPER_TEST_TENANT` | `0` | Must be `1` for the tenant scripts to run |
| `LANDSCAPER` | `<repo>/landscaper` | Binary under test, built by `setup` if missing |
| `WORK_DIR` | `$TMPDIR/landscaper-tests` | Scratch copies, backups and archives |
| `ARTIFACT_ID` / `PACKAGE_ID` | `Order_API_TEST_HARNESS` / `TestHarnessPreparation` | Fixture coordinates |
| `ORIGINAL_ENV` / `TARGET_ENV` / `TARGET_SUFFIX` | `Dev` / `QA` / `QA` | Environments from `conf/landscape.yaml` |
| `DEPLOY_WAIT` | `60` | Seconds `07-deploy.sh` waits for the runtime |
| `KEEP_FIXTURE` / `KEEP_WORK_DIR` | `0` | Leave the working tree or the scratch directory alone during cleanup |

To point the suite at another landscape, override the coordinates:

```bash
ARTIFACT_ID=My_Flow PACKAGE_ID=MyPackage TARGET_ENV=PRD TARGET_SUFFIX=PRD \
  LANDSCAPER_TEST_TENANT=1 ./testing/04-upload-target-env.sh
```

## Writing another script

```bash
#!/usr/bin/env bash
SCRIPT_NAME="10-my-scenario"
source "$(dirname "${BASH_SOURCE[0]}")/lib/common.sh"
setup
require_tenant                      # only if it writes

section "what is being checked"
run_landscaper artifact pack "$(scratch_artifact)" --skip-version-check --output "$WORK_DIR/out"
assert_exit "pack succeeds" "$LAST_EXIT" 0
assert_contains "something in the output" "$OUTPUT" "expected text"

summary
```

`run_landscaper` sets the globals `OUTPUT` and `LAST_EXIT`; it does not print,
because a command substitution would run it in a subshell and lose the exit
code. Add the script to the right list in `run-all.sh`.

`MANUAL-TEST-PLAN.md` is the narrative walkthrough of the same ground, useful
when a failure needs to be reproduced by hand.
