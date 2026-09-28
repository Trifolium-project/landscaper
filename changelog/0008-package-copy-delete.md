# Scriptable `package copy` and `package delete`

Branch: `feature/package-copy-delete`

## Context

iflowgen's SAP reference library (its proposals 0048, 0056, 0057) gets SAP
Business Accelerator Hub content through a tenant, because the Hub does not
serve integration flow content: `package copy` from Discover into Design, then
`artifact download --packages`, then remove the package again, so that the
tenant is as before. Each step had a gap:

- `package copy` had no flags of its own. It read the global `--pkg`, which
  `root.go` suffixes with the suffix of `--env`, so on a suffixed environment it
  asked Discover for an id that does not exist. It had no import mode, no signal
  for a package already in Design, no JSON, and parsed the answer with unchecked
  type assertions that panic on a null field.
- `package delete` was a stub printing "not implemented". A full reference
  library would leave 1,408 packages behind in Dev, removable only by hand.
- `artifact download --packages` did download editable SAP packages, but a
  configure only package ended in a generic failure (exit 1), indistinguishable
  from a real one.

## What the tenant does

Probed on the Dev tenant (Cloud Foundry, 2026-09-28) before the code was
written, with Hub packages that were not in Dev: `PrivateLinkProxy` (editable,
one integration flow) and `SAPIBPReusableIntegrationFlowsExamples` (configure
only, two). Both were deleted again, with the new command.

| Call | Observed |
|---|---|
| `POST CopyIntegrationPackage?Id='<id>'` | **Synchronous**: `201` with the new package in `d` after about 3 s, the artifacts readable immediately |
| same, package already in Design, no `ImportMode` | **`409`** "Copy not successful … due to conflict. In order to create an copy, add 'ImportMode' as 'CREATE_COPY' …". Nothing is written |
| same, id unknown to Discover | **`404`** "… An error occurred while fetching the integration package … from the Integration Content Catalog." |
| same, a custom package that is in Design but not in Discover | **`404`** as well: the tenant checks Discover before Design, so `5` only ever means "a Hub package already copied" |
| `ImportMode='CREATE_COPY'&Suffix='LSC'` | Creates **`<id>.LSC`** - a dot, not appended - with `.LSC` on every artifact id; the name gets `.LSC` too |
| Copy of a configure only package | `201`, `Mode: READ_ONLY`, artifacts listed |
| `GET IntegrationDesigntimeArtifacts(...)/$value` of a configure only package | **`400` "Cannot download the artifact from a configure only package."**, as XML |
| `DELETE IntegrationPackages('<id>')` | **`202`**, deletion in the background; on Dev the package read `404` within 2 s. Unknown package: `404` |
| `DELETE` of a package with a **deployed** flow | **Accepted.** The package is gone, the flow **keeps running** as an orphan in the runtime |
| `GET IntegrationRuntimeArtifacts` | The whole runtime in one list; types seen: `INTEGRATION_FLOW`, `VALUE_MAPPING`, `SCRIPT_COLLECTION`, `MESSAGE_MAPPING`, `REST_API_PROVIDER`, `IMPORTED_ARCHIVES`, `FUNCTION_LIBRARIES` |

The orphan is the reason for the deployed-content guard: the tenant offers none.
The orphan left by the probe was undeployed by hand.

## Decisions

| Question | Decision |
|---|---|
| `package copy` id | `--id`, used verbatim. The global `--pkg` stays an alias: `rawPackageFlag` strips the suffix `root.go` appended |
| Already in Design | Left to the tenant: its `409` maps to exit `5`. No read before the copy, which would only add a race |
| Exit codes of copy | `0` copied, `5` exists, `6` not in Discover, `1` other, as asked |
| JSON of copy | `{source, id, name, mode, vendor, version, importMode, status, error, artifacts:[{id, version, type}]}`, emitted for every outcome, so a caller parsing it learns why too. `id` is the created package - `<id>.<suffix>` with create-copy |
| Import modes | `overwrite`, `overwrite-merge` (in the spec, not in the ticket), `create-copy` with a mandatory `--suffix` |
| `package delete` id | Global `--pkg`, as the ticket asks. Raw id declared in the landscape → `raw + suffix`; anything else verbatim, which is what a copied package needs |
| Order of checks | read (404 → `4`) → declared without `--force` → `6` → artifacts of all four types + one runtime list → deployed without `--undeploy` → `5` → `--dry-run` → `0` → confirmation → undeploy and wait → `DELETE` → wait for `404` |
| Undeploy wait | `undeployAndWait` in `deployStatus.go`, polling the runtime **list**. `readDeployStatus` reads any error as "not deployed", and a network error must not be taken as permission to delete |
| Confirmation | `--yes`; otherwise a `y/N` prompt on stderr (keeps `--output json` parseable) when stdin is a terminal, and exit `1` with a message naming `--yes` when it is not |
| After `DELETE` | Poll until the package reads `404`, `--timeout`/`--interval` shared with the undeploy wait |
| API artifacts | The Integration Content API has no design time collection for them. The four collections are read; a package holding only other types would show fewer artifacts than it has. Its deployed content is still in the runtime list, but only matched by artifact id |
| Download of configure only | The package mode is read first and no content is requested; each artifact is a row `not downloadable (configure-only SAP package)`, exit `7` when nothing failed. `1` still wins. `--download-all` unchanged |
| Exit 7 | Also the guideline violation code of `artifact guidelines`. Kept as the ticket asks; codes are documented per command |
| Audit log | `package-copy` item; `package-delete` item per artifact (`removed`, `planned`, `kept`, with `artifact_type` and `undeployed`) plus one for the package; `not_downloadable` on download items |

## Files

- New `packages/cpiclient/packages.go`: `CopyIntegrationPackage`, `DeleteIntegrationPackage`, `ReadIntegrationPackageStatus`, `ReadPackageDesigntimeArtifacts`, `ReadIntegrationRuntimeArtifacts` (paged, stops if the tenant ignores `$skip`), and `StatusError`/`HasStatus` so callers can tell 404 and 409 apart. `CopyIntegrationPackageFromDiscover` is now a wrapper. `guidelineError` is renamed `odataError`, as both files use it.
- `packages/cmd/packageCopy.go` rewritten; `packageDelete.go` replaces the stub; `rawPackageFlag` in `package.go`; `undeployAndWait` in `deployStatus.go`.
- `packages/cmd/artifactDownload.go`: the loop moved into `downloadTargets`, `downloadExitCode`, configure only rows.
- `assets/IntegrationContent.yaml`: the message mapping and script collection listings of a package, with a note on the delete behaviour.

## Verification

`go test ./packages/...`: the stub tenant of `artifactUpload_test.go` learned
Discover, `409`/`404`, create-copy ids, the background delete, the runtime list
and undeploy. Every exit path of both commands is covered, the dry run asserts
no write call, and removing the deployed guard makes its test fail.

On Dev, with this binary:

```
package copy --id SAPHybrisCloudforCustomerIntegrationwithSAPS4HANA   → 5, nothing written
package copy --id NoSuchHubPackage_landscaper                         → 6
artifact download --packages SAPIBPReusableIntegrationFlowsExamples   → 7, both flows "not downloadable"
package delete --pkg SAPIBPReusableIntegrationFlowsExamples --dry-run --log → 0, only GET in the log
package delete ... < /dev/null (no --yes)                             → 1
package delete --pkg SAPIBPReusableIntegrationFlowsExamples --yes     → 0 (5.7 s); again → 4
package delete --pkg TestHarnessPreparation --dry-run                 → 6
package copy --id PrivateLinkProxy → download (1 flow) → deploy → package delete --yes → 5
package delete --pkg PrivateLinkProxy --yes --undeploy --output json  → 0, runtime 404 afterwards
package delete --pkg PrivateLinkProxy.LSC --yes                       → 0
```

`testing/14-package-copy-delete.sh` repeats the loop as a tenant scenario.

## Implementation notes

- The first `--undeploy` run on Dev showed the per artifact audit records missing: their `"type"` key (the artifact type) overwrote the record's own `"type":"item"`. Renamed to `artifact_type`.
- A transient TLS handshake timeout during one copy was reported as `status: failed`, exit `1`, with nothing written; the retry succeeded. `doRequest` has no retries, as before.
