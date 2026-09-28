# Design guideline checks (`artifact guidelines`)

Branch: `feature/design-guidelines`

## Context

SAP Cloud Integration ships its own static analysis of integration flows, the
design guidelines: about forty rules on exception handling, streaming, security
and scripting, each with a severity, run on an artifact version and skippable
per rule with a reason. It is SAP's rule set and it is kept current by SAP, which
is exactly what automated design checks need.

landscaper is the only design time access an agent generating integration flows
has to a tenant, so the checks belong here: generated flows were deployed
without SAP's own check, migrated flows were never scored, and there was no
compliance view of a tenant. The request (ticket 0050 of the generation repo)
asked for run / results / skip / unskip, batch selection, exit code 7 on
violations at or above a severity, declarative skips in `landscape.yaml`, and
the rule catalogue.

## What the API actually does

The bundled `assets/IntegrationContent.yaml` predates the feature and had none
of these calls. The current Business Accelerator Hub spec does, and so does the
tenant's `$metadata`. Everything below was probed against the Dev tenant
(Cloud Foundry, 2026-09-28) with `Order_API_TEST_HARNESS` before the client was
written.

| Purpose | Call | Observed |
|---|---|---|
| Execute | `POST ExecuteIntegrationDesigntimeArtifactsGuidelines?Id='<id>'&Version='active'` | **Synchronous.** Answers `200 text/plain` with the bare execution id once the execution is finished. **Adding `$format=json` gives `501 Not implemented`.** An unknown artifact gives `500 Unable to get Data: Request: <id> IFlow`, as XML unless `Accept: application/json` is sent. An old version gives `404`: the tenant only holds the current design time version |
| Executions | `GET IntegrationDesigntimeArtifacts(Id,Version)/DesignGuidelineExecutionResults` | Only the **latest** execution is listed. An artifact never checked returns one placeholder: `ExecutionId ""`, `ExecutionStatus NOT_EXECUTED`, `ExecutionTime "0"`. Status is `FAIL` with violations; `ExecutionTime` is epoch milliseconds as a string |
| Results | `GET …/DesignGuidelineExecutionResults('<execId>')?$expand=DesignGuidelines` | 39 rules on this tenant, every activated rule including not applicable ones. **An older execution id is rejected**: `400 … the execution ID is invalid` |
| Skip / revert | `PUT …/$links/DesignGuidelineExecutionResults('<execId>')` body `{GuidelineId, IsGuidelineSkipped, SkipReason}` | See below |
| Report | `GET …('<execId>')/$value?type=xls` | Not used |
| Catalogue | `GET DesignGuidelines` | **404** "Could not find an entity set", although `$metadata` declares the set |

The skip call, where the spec and the tenant disagree most:

- SAP's swagger names the key **`GudelineId`**. The tenant answers `400 Illegal argument for method call with message 'GudelineId'`. **`GuidelineId`** works.
- A skip is filed against an execution but **outlives it**: the next execution reports the rule as skipped with the same reason and `SkippedBy`.
- A skipped rule keeps **`Compliance: Non-Compliant`**; only `IsGuidelineSkipped` changes.
- `SkipReason` is required for a skip (`400 SkipReason must not be empty.`) and not needed for a revert.
- **Neither direction is idempotent**: skipping a skipped rule gives `400 The design guideline is already skipped for the artifact.`, even to change the reason, and reverting one that is not skipped gives `400 You cannot revert a design guideline that is not skipped.`
- Some rules are **essential** and cannot be skipped: `CAMEL_CLASSES_USAGE` gives `400 … the essential design guidelines cannot be skipped for the artifact.`
- An unknown rule gives `400 … there is no design guideline available with the given ID.`

`ViolatedComponents` is Java map notation of BPMN element ids and names,
`{CallActivity_59=Build Response, CallActivity_75=Build Delete Response}`, and
null for a compliant rule.

**Roles.** Not verified. The probe ran with the OAuth client the Dev landscape
already uses for upload and deploy, and every call succeeded with it; its roles
were not inspected. Presumably reading results needs the design time read role
(`WorkspacePackagesRead`) and executing and skipping, which change content
metadata, need the edit role (`WorkspacePackagesEdit`), but that was not tested
against a client lacking either.

**Tenant versions.** Only one Cloud Foundry tenant was available. SAP documents
the guidelines for Neo and Cloud Foundry; the client tolerates the other shapes
it could plausibly meet - an execution id wrapped as an OData function import
result, XML error documents, an execution that is still `RUNNING` - but they are
untested against a real tenant.

## Decisions

| Question | Decision |
|---|---|
| Command layout | `artifact guidelines run \| results \| skip \| unskip \| rules`, all under `artifact`. The ticket's top level `guidelines rules` would have been the only command outside the `artifact`/`package`/`config` groups |
| Selection | Exactly one of `--artifacts`, `--packages`, `--all-declared`, ids **without** suffix, as `artifact download` does. The global `--pkg`/`--artifact` are refused because `root.go` has already suffixed them. `--packages` (plural, as in download, not the ticket's `--package`) reads the package from the tenant, so undeclared artifacts are checked too |
| Version | `--version active` by default. The tenant holds only the current version anyway |
| `--wait` | Kept, because the API is documented as asynchronous, but the tenant answers synchronously so the first read is already final. Without `--wait` an unfinished execution is reported as `not-finished` |
| Exit codes | `0` nothing at or above `--fail-on`, `7` violations, `3` not finished or never executed, `1` any other failure. Precedence `1 > 3 > 7 > 0` |
| `--fail-on` | `low` by default, so any violation fails; `medium`, `high`, `none`. An unknown severity counts as high, so a new severity name cannot slip through |
| Batch failures | An artifact that fails is a row with an `error`, the batch goes on, the exit code is 1 at the end |
| Skipped rule | Normalised status `skipped` wins over `Compliance`, and does not count as a violation |
| Idempotence | `skip`/`unskip` read the rule first: `unchanged` when the tenant already holds the state, `updated` (revert, then skip) for a new reason. Needed because a pipeline or an agent re-runs commands |
| Declared skips | `guidelineSkips: [{rule, reason}]` on the artifact. A reason is mandatory, checked when the file is loaded. The declared reason replaces one filed by hand. A refused skip is reported, never fatal |
| When declared skips apply | Always on `guidelines run` (`--no-declared-skips` to turn off). On `artifact upload` only with **`--apply-guideline-skips`**, so existing pipelines do not start running executions they did not ask for. Applied after the configuration, before `--deploy` |
| Catalogue | `rules` takes `--artifacts` and reads the rules of their latest execution, executing first when there is none |
| `init` | Carries the declared skips over when it regenerates `packages:`; the tenant cannot know them |
| Text output | Numbered table per artifact, then only the rules that need attention (`not-compliant`, `skipped`) per artifact, then `===Summary===`. `--output json` carries every rule |

## Files

### New: `packages/cpiclient/guidelines.go`

`ExecuteIntegrationDesigntimeArtifactGuidelines`, `ReadDesignGuidelineExecutions`,
`ReadDesignGuidelineExecutionResult`, `SkipDesignGuideline`. All go through
`doRequest`, so every call lands in the `--log` audit log with the usual header
redaction. `DesignGuideline.Components()` parses `ViolatedComponents`, anchoring
on the element id pattern so a comma in a display name does not split it.
`guidelineError` reduces the raw OData error document - JSON or XML - to its
message.

### New: `packages/cmd/guidelines.go`

Everything the commands share, behind a `guidelineClient` interface: target
resolution, execute and poll, latest execution, idempotent `fileGuidelineSkip`,
declared skips, normalisation, the report and its JSON shape, exit codes,
printing. Also `applyUploadGuidelineSkips` for upload.

### New: `packages/cmd/artifactGuidelines*.go`

The parent command and one file per subcommand; `skip` and `unskip` share
`artifactGuidelinesSkip.go` and one action.

### `packages/cmd/artifactUpload.go`

`--apply-guideline-skips`, and a `Guideline Skips` column (`1/1`, `0/1 failed`,
`-`) that appears only with the flag.

### `packages/landscape/landscape.go`, `export.go`; `packages/cmd/landscapeInit.go`

`Artifact.GuidelineSkips`, the YAML field, validation, `GetGuidelineSkips`,
`CarryOverGuidelineSkips`, and the export of the field.

### `assets/IntegrationContent.yaml`

The guideline paths and definitions from the current spec, with a comment on
where the tenant disagrees, and the `429` response they reference.

## Verification

`go test ./packages/...`: the cmd tests drive the commands against the stub
tenant of `artifactUpload_test.go`, extended with the guideline calls and every
refusal listed above, so a regression to `$format=json`, `GudelineId` or a
non idempotent skip fails a test. `cpiclient` and `landscape` have table tests
for the parsers and the YAML.

`testing/13-design-guidelines.sh` (tenant, writes) against Dev: 20 checks,
including a skip filed by hand being replaced by the declared one. Manually on
Dev, both artifacts named by the ticket:

```
$ landscaper artifact guidelines run --env Dev --artifacts Order_API_TEST_HARNESS,GENAI_OrderQuote --wait
1  Order_API_TEST_HARNESS  TestHarnessPreparation  1.0.18  not-compliant  7  0  7
2  GENAI_OrderQuote                                1.0.1   not-compliant  11 0  11
...
Violations (--fail-on low):  18
$ echo $?
7
```

## Implementation notes

- A test found that `--packages` on a suffixed environment turned an undeclared `Other_FlowQA` into `Other_FlowQAQA`: `trimTargetSuffix` only strips the suffix of declared artifacts, and the id was then suffixed again. The tenant id is now used as it is.
- The first tenant run of the scenario failed on the declared skip because an earlier manual run had left the rule skipped with another reason. That is how the non idempotent skip was found; `fileGuidelineSkip` exists because of it.
