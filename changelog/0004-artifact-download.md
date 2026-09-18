# Download integration flows from a tenant (`landscaper artifact download`)

Branch: `feature/download-artifacts`

## Context

The tool could only push. `artifact pack` and `artifact upload`
([0002](./0002-artifact-pack-upload.md), [0003](./0003-artifact-upload-symbolic-name.md))
take an exploded integration flow out of the repository and put it into a
tenant; nothing brought one back. Seeding a git repository from an existing
tenant, or refreshing it after somebody edited a flow in the web editor, meant
downloading each archive by hand from the Integration Suite UI and unpacking it.

`landscaper init` ([0001](./0001-landscape-init.md)) already reads a tenant back
into the landscape *configuration* - the `packages:` section with its
parameters. This is the counterpart for the flow *content*.

`CPIClient` already had `DownloadIntegrationDesigntimeArtifact`, used by
`package move` and `artifact upgrade`, but it returns a base64 string in a
struct and there was no way to get an archive onto disk: `packages/iflow` could
zip a folder and read headers out of an archive, and had no extract function at
all.

## Decisions (confirmed with user)

| Question | Decision |
|---|---|
| Layout | `<output>/<package id>/<artifact id>/`, the archive **extracted**, not left as a zip. Default output `artifacts`, changeable with `--output` |
| Version | Always the latest, taken from the per-package artifact list. `Active` - a draft - is downloadable and marked as such in the report |
| Ids on disk | **Verbatim, as the tenant has them.** `--env QA` produces `artifacts/MyPackageQA/MyFlowQA/` and the manifest inside the archive is **not** rewritten. The download is a faithful snapshot |
| Input | Exactly one of `--packages`, `--artifacts`, `--download-all` |
| `--download-all` scope | Every package of the tenant **bound to the selected environment**, by the same longest-suffix-first matching `landscaper init` uses. `--env QA` gets the `...QA` packages only, not the Dev ones sharing the tenant |
| Existing folder | Reported as skipped and left alone; `--force` replaces it |
| A failing artifact | Reported as a row and the run continues; the command still exits 1 at the end |

The verbatim-ids decision is the opposite of what `artifact upload` does, and
deliberately so. Upload has to rename because the tenant derives
`Bundle-SymbolicName` from the OData id (0003); download has no such constraint,
and rewriting the manifest on the way in would mean the folder no longer matches
what the tenant actually holds.

## Files

### New: `packages/cmd/artifactDownload.go`

`artifactDownload` resolves the environment of the global `--env`, turns the
selection flags into physical package ids, reads each package once and extracts
every artifact it was asked for.

 - `validateDownloadFlags` enforces exactly one selector. The global `--pkg` and `--artifact` are **rejected** rather than ignored: `root.go:113-121` has already appended the environment suffix to them and they are single valued, so accepting them would double-suffix.
 - `resolveDownloadTargets` maps base ids to physical ones. `--packages` appends `Environment.Suffix`; `--artifacts` resolves the package through `FindPackageForArtifact`, which takes the base id, and suffixes both segments; `--download-all` delegates to the new `PackageIdsForEnvironment`. Targets are deduplicated and keep the order of the flag values. `validatePathSegment` refuses any id that is not a plain folder name, so a tenant id containing a separator cannot escape `--output`.
 - `downloadPackageArtifacts` downloads the artifacts of one package, turning a failure into a `failed: <reason>` row rather than aborting. An explicitly named artifact the package does not hold is reported the same way.
 - `downloadArtifact` checks the destination **before** issuing any request, so a skipped artifact costs nothing, and validates the answer with `ReadManifestHeaderFromZip` before writing anything. `doRequest` only inspects the leading digit of the status code, so an error page answered with 200 would otherwise be extracted onto disk.

`readTenantArtifactVersion` from `artifactPack.go` is deliberately **not**
reused: it calls `CheckConnection()` per invocation, which fetches a CSRF token
and indexes the response header unchecked (`cpiclient.go:258`). The version is
already in the list response, so one read per package suffices.

### `packages/cpiclient/cpiclient.go`

`DownloadIntegrationDesigntimeArtifactContent(Id, Version)` returns the raw zip
of the `$value` endpoint. The existing `DownloadIntegrationDesigntimeArtifact`
now builds on it, so the URL exists once and its `//TODO: Perform check for
unsuccessful download` is answered by the wrapped error.

It is a new method rather than a reuse because the existing one first calls
`ReadIntegrationDesigntimeArtifact`, which itself calls
`ReadIntegrationDesigntimeArtifactConfigurations` - three round trips per
artifact, doubled again under OAuth2, where a fresh token is fetched per
request - and then base64-encodes an archive the caller would immediately
decode. On the 335-artifact tenant this is the difference between 335 and about
2000 requests.

### `packages/landscape/discover.go`

`PackageIdsForEnvironment(environment)` returns the physical package ids of one
environment plus non-fatal warnings, composed from the machinery `Discover`
already uses - `environmentsOfSystem`, `pickBaseEnvironment`,
`classifyPackages` - so `--download-all` and `init` cannot disagree about which
package belongs where. Packages with `Mode == READ_ONLY` are dropped with a
warning, because SAP-delivered content cannot be downloaded.

The filtering itself is the pure `selectPackagesForEnvironment`, separated out
so it is table-testable without a client. `classifyPackages` already stamps
every binding with its `*Environment`, so selection is one comparison and needs
no special case for the base environment.

Two situations produce a warning instead of a silent empty result: a system with
no suffix-less environment, and an environment without a suffix that is not the
one `pickBaseEnvironment` settled on.

### `packages/iflow/zip.go`

`UnzipToDir(data, destDir)` is the inverse of `ZipDir` and the first extract
helper in the repository.

Entries are written into a temporary folder **created as a sibling of
`destDir`** and renamed into place at the end. A sibling keeps the rename on one
filesystem, and the existing folder is destroyed only once the new content is on
disk - which is what makes `--force` safe, and why the command does not call
`os.RemoveAll` itself. `os.MkdirTemp` creates the staging folder 0700, so it is
chmod'ed to 0755 before the rename.

`archiveEntryPath` rejects absolute entry names and anything that does not stay
under the target after `filepath.Join`, the zip-slip attack; non-regular entries
are skipped, since a symlink inside an archive is another way out. Modes are
fixed at 0644/0755 because archives written by the tenant and by `ZipDir` carry
no unix permissions, so `file.Mode()` would be `0`.

## Verification

```bash
go build ./... && go vet ./... && go test ./packages/...
```

`packages/iflow/zip_test.go` covers the round trip `ZipDir` -> `UnzipToDir`
(content identical, `IsArtifactDir` true, `Bundle-Version` readable), the 0755
folder and 0644 file modes, replacement of an existing folder, an existing
folder surviving a failed extraction, empty directories, an empty archive, and
three zip-slip forms - `../escape.txt`, `src/../../escape.txt` and an absolute
path.

`packages/cmd/artifactDownload_test.go` drives the action helpers against the
`stubTenant` of `artifactUpload_test.go`, which gained the `$value` download, the
paged `IntegrationPackages` collection, per-package artifact ownership and a
`READ_ONLY` mode - additively, so the upload tests are unaffected. Covered:
download by package; `--artifacts` with `--env QA` producing
`TestHarnessPreparationQA/Order_API_TEST_HARNESSQA` and a `$value` call carrying
`Id='Order_API_TEST_HARNESSQA'`; an existing folder skipped without any request;
`--force` replacing it; a draft reported as such; one artifact failing while the
other still lands; an artifact missing from the package; `--download-all`
returning only the QA package for QA and only the Dev one for Dev; flag
validation; traversal rejection; deduplication; and an answer that is not an
archive leaving nothing on disk.

Mutation checked: removing the zip-slip comparison fails
`TestUnzipToDirRejectsZipSlip` and
`TestUnzipToDirKeepsExistingFolderOnFailure`; removing the environment filter in
`selectPackagesForEnvironment` fails `TestDownloadAllKeepsOnlyEnvironmentPackages`
for both environments.

Manually, against the tenant of `conf/landscape.yaml`:

```bash
landscaper artifact download --packages TestHarnessPreparation --output /tmp/dl
landscaper artifact download --artifacts Order_API_TEST_HARNESS --env QA --output /tmp/dl
landscaper artifact download --download-all --env Dev --output /tmp/dl3
```

The first writes `Order_API_TEST_HARNESS` 1.0.13 and `Sample_API` 1.0.2 under
`TestHarnessPreparation/`, with 0755 folders and 0644 files; running it again
reports both as `skipped, folder exists (use --force)`, and `--force` removes a
file planted in the folder beforehand. The second writes
`TestHarnessPreparationQA/Order_API_TEST_HARNESSQA` at 5.20.22, confirming the
suffix is applied to both segments. `--download-all` on `Dev` downloaded 335
artifacts with no failures, skipped 8 SAP-delivered packages, and marked the
drafts `downloaded (draft)`; the same call on `QA` returned the two `...QA`
artifacts only. A downloaded folder packs unchanged with
`artifact pack /tmp/dl/TestHarnessPreparation/Order_API_TEST_HARNESS --skip-version-check`.

`testing/10-download.sh` scripts the same ground and is registered in
`TENANT_SCRIPTS`.

## Implementation notes

 - **`--download-all` covers integration flows only.** `ReadIntegrationDesigntimeArtifacts` reads `IntegrationPackages('X')/IntegrationDesigntimeArtifacts`; value mappings, message mappings and script collections are separate entity sets and are silently absent. The `Long` text says so, because "all" invites the other reading.
 - Round-tripping back is asymmetric, a consequence of the verbatim-ids decision. `artifact upload --target-env QA` on a downloaded `MyFlowQA` folder works, because `trimTargetSuffix` strips a suffix when what remains is a declared artifact. `--target-env Dev` on the same folder fails: the suffix is empty, nothing is trimmed, and `FindPackageForArtifact("MyFlowQA")` errors. "Download from QA, promote to Dev" is therefore not a supported path. `artifact pack` on a downloaded folder needs `--skip-version-check` for the same reason.
 - An explicitly named `READ_ONLY` package is attempted rather than pre-filtered. `--download-all` skips them, but a user who names one gets the tenant's own error in the row instead of a silent omission.
 - `NewCPIBasicAuthClient` sets no `Timeout` on its `http.Client`. A `--download-all` over a large tenant has no upper bound on how long a single stalled request can block. Untouched here, but it is more visible with this command than with any previous one.
 - `classifyPackages` breaks at the first matching suffix, so two environments declared on one system with the *identical* suffix leave the second one with nothing and no warning. Pre-existing, but `--download-all` is the first command where it shows up as an empty result.
