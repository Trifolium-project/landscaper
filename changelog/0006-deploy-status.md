# Machine checkable deployment results (`--wait`, `--output json`, exit codes)

Branch: `feature/deploy-status`

## Context

`artifact deploy` and `artifact upload --deploy` were fire and forget. The
tenant accepts a deploy request and answers immediately; whether the flow ever
started was invisible. `DeployIntegrationDesigntimeArtifact` discards the task
id it gets back, nothing polled, and a deployment that failed looked exactly
like one that succeeded — the command printed its table and exited 0.

`artifact get` did show the runtime status, but when that status was `ERROR` it
said only `ERROR`. The tenant knows why, and exposes it at
`IntegrationRuntimeArtifacts('<Id>')/ErrorInformation/$value`, which the client
never called.

That is enough for a human watching a terminal and not enough for anything
automated. The motivating case is a loop where a generator writes an integration
flow, landscaper uploads and deploys it, and the generator has to read the
result and fix what it got wrong. That needs three things this change adds: a
result that is waited for, an explanation of a failure, and an exit code.

## Decisions

| Question | Decision |
|---|---|
| How is the result reported | `--wait` polls until the deployment settles; without it the commands behave exactly as before |
| Machine readable form | `artifact get --output json` emits one document; `text` stays the default and is unchanged |
| Exit codes | `0` STARTED, `2` ERROR, `3` timeout or still STARTING, `4` not deployed, `1` anything else |
| Defaults | `--timeout 180s`, `--interval 5s` |
| A failed upload of several artifacts | Stops at the first failed deployment, reporting the rows already produced, which is what the command already did for any other failure |
| Environment suffixes | Unchanged: the runtime id is the suffixed artifact id, exactly as upload builds it |

**Text output is backward compatible.** `artifact get` prints the same block it
always did, with the error appended underneath when there is one. The upload
table gains columns **only** when `--wait` is given.

## Files

### New: `packages/cpiclient/errorinformation.go`

`ReadIntegrationRuntimeArtifactErrorInformation(Id)` calls
`IntegrationRuntimeArtifacts('<Id>')/ErrorInformation/$value` and parses the
answer. A healthy deployment has no error information, which the tenant reports
as 204 or 404, so both yield `nil, nil` and the caller can ask unconditionally.

`ParseRuntimeErrorInformation` is exported so the parsing is testable against
recorded payloads without a tenant. It handles **three shapes**, because a real
tenant does not send what the documentation describes:

 - the documented tree, `message` with `parameter` inside it and `childInstances` below;
 - the shape this tenant actually sends, where **`parameter` is a sibling of `message`** and `messageText` is empty — the whole diagnostic lives in that top level array, so a parser that reads only `message.parameter` reports `GenerationFailed` and nothing else;
 - a nested document arriving as a JSON **string inside a parameter**, spelling its children `childMessageInstances`, which is flattened rather than printed as punctuation.

It also reads through SAP's own misspelling of `subsytemPartName`, falls back to
the message id when `messageText` is empty, tolerates numbers and nulls in the
parameter array, accepts a bare string body, and drops caret only lines from a
syntax error report. `Text` is every message of the tree, outermost first, one
per line.

### `packages/cpiclient/cpiclient.go`

`doRequest` now delegates to `doRequestWithStatus`, which returns the status
code as well. Every existing caller is untouched; the new endpoint needs it to
tell "no error information" from a real failure, because a non-2xx otherwise
arrives only as the body text.

### New: `packages/cmd/deployStatus.go`

The polling rules, behind a `runtimeReader` interface so they are testable with
a fake client and no tenant.

 - `readDeployStatus` looks once, and fetches the error information **only** when the status is `ERROR`.
 - `waitForDeployment` polls until the artifact settles. **The version check is the point of it:** immediately after a redeploy the tenant keeps reporting the *previous* version as `STARTED`, so a naive poll returns success for a deployment that has not happened. A status is accepted only once the runtime version equals the version just deployed. An `ERROR` is final whatever version it belongs to, otherwise a failed redeploy would be polled until the timeout. A design time version of `Active` is a draft marker rather than a version and accepts anything.
 - `deployStatus.ExitCode` maps the outcome onto the documented codes, and `Summary` onto the cell the table prints.

### `packages/cmd/artifactGet.go`

`--output text|json`, `--wait`, `--timeout`, `--interval`. The text path prints
the block it always printed and appends `Deploy error:` with the flattened text.
The JSON path emits `{id, name, version, package, runtime, configuration}` with
`runtime` null when nothing is deployed and `runtime.error` null when it
deployed cleanly. The command ends through `exitWith`.

### `packages/cmd/artifactDelpoy.go`, `packages/cmd/artifactUpload.go`

The same three flags. With `--wait` the upload table gains `Runtime Status` and
`Error`, and the loop stops at the first failed deployment after flushing the
rows already produced. `uploadRow` gains the status; `waitForDeployment` is
given `row.UploadVersion`, the version the tenant confirmed, not the local one.

### `packages/cmd/root.go`

`exitWith` closes the audit log of [0005](./0005-audit-logging.md) before
`os.Exit`, since these commands need codes beyond 0 and 1 and `os.Exit` runs no
deferred function.

## Verification

```bash
go build ./... && go vet ./... && go test ./packages/...
```

`packages/cpiclient/errorinformation_test.go` covers the nested tree two levels
deep, the flat case, five forms of "no error information", a bare string body,
odd parameter types, the message id fallback, the `childMessageInstances` shape
including deduplication of a cause repeated at every level, and a payload the
tenant truncated itself. Two fixtures are **recorded verbatim from the tenant**
rather than invented.

`packages/cmd/deployStatus_test.go` drives the polling with a fake client that
replays a scripted sequence of runtime states, so the rules are tested without a
tenant and without waiting. The central case, `TestWaitRejectsTheOldVersionReportedAsStarted`,
replays `STARTED 1.0.3 → STARTING 1.0.3 → STARTED 1.0.4` and asserts that the
wait keeps asking until the new version appears.

Mutation checked: making `versionMatches` always return true fails that test
with `Version = "1.0.3", want the version just deployed` and fails
`TestWaitTimesOutWhenTheNewVersionNeverAppears` with `ExitCode = 0, want 3` —
that is, the stale status would be reported as a successful deployment.

`testing/12-deploy-status.sh` (24 assertions, registered in `TENANT_SCRIPTS`)
runs the whole thing against a real tenant. It builds a broken flow by copying
the fixture and deleting `script1.groovy`, which the `.iflw` still references,
then asserts exit code 2, the new columns, the error block, and that the text
names `script1.groovy`. It also covers exit 0 for a sound flow, exit 4 for one
that was uploaded but never deployed, the JSON shape in both the healthy and the
failed case, and a redeploy.

Observed against the tenant:

```
#  ArtefactId                 ...  Deployed  Runtime Status  Error
1  TEST_DEPLOY_STATUS_BROKEN  ...  true      ERROR           GenerationFailed ...

Deploy error:
GenerationFailed
The generation and build of the artifact were unsuccessful. ...
Generation and build failed for TEST_DEPLOY_STATUS_BROKEN as validation of resource is failed
Script file 'script1.groovy' not found
```

with exit code 2, and exit code 4 for an undeployed artifact.

## Implementation notes

 - **The tenant's error payload does not match its own documentation.** `assets/IntegrationContent.yaml` defines `RuntimeArtifactErrorInformation` as an object with a single `Id` field. The real answer is the `message` / `parameter` / `childInstances` document described above. The parser was written against the documentation, and the top level `parameter` array was only discovered by reading the raw payload out of the audit log added in [0005](./0005-audit-logging.md) — until then the command reported `GenerationFailed` and silently dropped `Script file 'script1.groovy' not found`, which is the only part a caller can act on.
 - **A tenant truncates its own error text** (`... [truncated]`), which makes the nested document unparseable. Its newlines are still escaped at that point, so they are unescaped and split into lines rather than left as one blob. The cause stays readable; the JSON punctuation around it does not go away.
 - `--wait` on `artifact deploy` and on `artifact get` uses the design time version as the expectation. For a draft (`Active`) there is no version to compare, so any running version is accepted — a draft cannot be checked this way.
 - Exit codes mean the commands now leave through `os.Exit` rather than returning. Anything added after the `exitWith` call in those actions will not run.
 - The repository convention recorded in `CLAUDE.md` was "no JSON output, no `--output-format`". `artifact get --output json` is a deliberate exception, for automated callers; the default stays `text` and every other command is unchanged.
 - `testing/12-deploy-status.sh` leaves three artifacts behind (`TEST_DEPLOY_STATUS_BROKEN`, `..._OK`, `..._UNDEPLOYED`). `artifact delete` is still a stub, so they have to be removed in the Integration Suite UI. It reuses fixed ids so repeated runs update them instead of creating more.
 - The scenario script deliberately does not assert the success path against the shared fixture: the draft of `Order_API_TEST_HARNESS` in this tenant fails to deploy on its own (a Camel Simple expression with an unterminated `${...}`), which is a pre-existing condition of the tenant rather than of this change.
