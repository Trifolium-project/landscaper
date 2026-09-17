# Artifact packing and upload (`landscaper artifact pack`, `landscaper artifact upload`)

Branch: `feature/artifact-packager`

## Context

Landscaper could only move content between tenants. `package move` reads a package from the original environment, downloads every artifact, renames it for the target environment and uploads it again; `artifact upgrade` does the same within one tenant against a template. In both cases the artifact never touches the disk - it lives in memory as the base64 string `IntegrationDesigntimeArtifact.ArtifactContent`.

What was missing is the other direction: taking an integration flow out of the local git repository and putting it into a tenant. That is what a merge request pipeline needs - deploy the change under review to the Integration Suite, from CI/CD or from a developer machine.

Two commands cover it:

 - `artifact pack` turns an exploded iflow folder into a zip archive, guarding against a version that the tenant already holds
 - `artifact upload` sends folders and archives to a chosen environment, reusing the suffix, package and configuration rules of `package move`

## Decisions (confirmed with user)

 - **Version rule.** The packed version has to be strictly higher than the one in the tenant. Equal or lower means a new version is asked for; higher, or absent from the tenant, is packed immediately. The originally described rule was inverted and would have pushed a stale flow over a newer one.
 - **Non interactive runs.** `--bump patch|minor|major` and `--set-version X.Y.Z` resolve the version without asking. If a new version is required, stdin is not a terminal and none of `--bump`, `--set-version` or `--skip-version-check` is given, the command fails instead of blocking on a prompt.
 - **Output.** Archives go to `build/`, overridable with `--output`. The new version is written back into the working tree, so it can be committed with the change.
 - **Suffixes.** Artifact id, artifact name and package id receive the suffix of the target environment. The package is created when it is missing. Configuration parameters of the target environment are applied after upload.
 - **Existing artifacts** are updated in place with `PUT ...(Id='X',Version='Active')`, not deleted and recreated, so history and configuration survive.
 - **Which tenant is checked.** `artifact pack` on its own checks the original environment. Packing as part of an upload checks the target environment, because that is where the content lands.
 - **CLI shape.** Subcommands of the existing `artifact` group, taking positional paths. `--artifact` could not be used, because `root.go` appends the environment suffix to it, which would corrupt a filesystem path.
 - **The zip is not rewritten.** `Bundle-SymbolicName` inside the archive keeps its base value while the OData id carries the suffix, which is exactly what `package move` does today.

## Files

### New: `packages/iflow/`

A package with no cobra and no HTTP dependency, so the interesting logic is testable on its own.

`manifest.go` - reads and writes `META-INF/MANIFEST.MF`. The file is handled as physical lines with their terminators kept verbatim, because SAP writes CRLF and wraps long headers at 72 columns with a leading space. `readHeader` joins continuation lines, `SetBundleVersion` replaces only the `Bundle-Version` line and leaves every other byte alone. Headers are matched case insensitively, as the JAR specification requires.

`version.go` - `Version`, `ParseVersion`, `Compare` and `Bump`. Accepts `1`, `1.0`, `1.0.3` and the OSGi four component form `1.0.3.qualifier`, whose qualifier is kept in `Raw` but ignored when comparing and bumping.

`zip.go` - `ZipDir` walks the folder and writes entries relative to it, so `META-INF/MANIFEST.MF` sits at the archive root, which is what the Integration Suite expects. Entries are sorted and stamped with a fixed timestamp, so packing the same folder twice yields identical bytes. `.DS_Store`, `.git`, `Thumbs.db` and `__MACOSX` are left out. `ReadBundleVersionFromZip` reads the manifest back out of an archive, so an upload of a ready zip can still report its version. `WriteFileAtomic` writes through a temporary file and renames it, following `WritePackages` in `packages/landscape/export.go`.

### New: `packages/cmd/artifactPack.go`

The command plus `packArtifact`, the routine `artifact upload` also calls. `resolvePackedVersion` holds the version rule and its precedence: `--set-version`, then `--bump`, then a terminal prompt, then an error. The suggested version is always derived from the tenant version, not from the local one, so a repository that has fallen behind still produces something the tenant accepts.

Two states have no comparable version and are packed unchanged: an artifact that is absent from the tenant, and an artifact that the tenant reports as `Active`, which means it is saved as a draft there.

`readTenantArtifactVersion` verifies the connection first. Without that, an unreachable tenant looks exactly like an empty one and the version check would be skipped silently - the wrong outcome for a pipeline.

### New: `packages/cmd/artifactUpload.go`

Resolves the target package from the landscape definition, or from `--pkg`, ensures it exists, packs folders and reads archives, chooses between create and update, applies configuration and optionally deploys.

The tenant version is read once, up front. It answers two questions at the same time - whether a bump is needed and whether the artifact has to be created or updated - and the second one is needed even when `--skip-version-check` is given.

The configuration loop deliberately does not copy `packageMove.go`. The branches there are inverted and dereference a nil `sourceConf` when the source artifact lacks a parameter. There is no source artifact here at all, so the parameter type comes straight from the landscape definition, where `buildLandscapeFromManifest` already defaults it to `xsd:string`.

`package move` refuses to write to the original environment. That guard is intentionally not copied: uploading a merge request to the original environment is the normal case.

### Modified: `packages/cpiclient/cpiclient.go`

Added `UpdateIntegrationDesigntimeArtifact`, a `PUT` on `IntegrationDesigntimeArtifacts(Id='X',Version='Active')`. Modelled on `UploadIntegrationDesigntimeArtifact`, with the unchecked `json.Marshal` error of that function fixed in the copy.

### Modified: `packages/landscape/landscape.go`

Added `FindPackageForArtifact`, which finds the package declaring an artifact. It reports an error naming `--pkg` when the artifact is unknown or declared more than once, rather than guessing.

### New tests

`packages/iflow/manifest_test.go`, `version_test.go`, `zip_test.go` - manifest round trips including the CRLF and continuation line cases, the version rules, and the archive layout and determinism.

`packages/cmd/artifactUpload_test.go`, `artifactPack_test.go` - the first tests in `packages/cmd`. They drive `packArtifact` and `uploadArtifact` against an in process `httptest.NewTLSServer` that imitates the OData endpoints. The client builds its own `http.Client` without a transport, so the test certificate is trusted by swapping `TLSClientConfig` on `http.DefaultTransport` and restoring it afterwards.

## Verification

```bash
go build ./... && go vet ./... && go test ./packages/...
```

Covered by the tests: suffixing of id, name and package; creation of a missing package; `PUT` for an existing artifact and `POST` for a new one, including the case where `--skip-version-check` must not turn an update into a create; the bump written into the manifest without damaging the rest of it; the error when a bump is required in a non interactive run; configuration parameters applied for the target environment; deploy; and a zip archive as input. The tests were mutation checked - inverting the create/update decision and disabling the bump both make them fail.

Manually, against the sample artifact:

```bash
landscaper artifact pack artifacts/Order_API_TEST_HARNESS --skip-version-check
unzip -l build/Order_API_TEST_HARNESS.zip
unzip -p build/Order_API_TEST_HARNESS.zip META-INF/MANIFEST.MF
```

All twelve files of the folder end up in the archive, `META-INF/MANIFEST.MF` at its root, `.DS_Store` left out. Packing several artifacts where one path is broken reports the ones already packed and exits with code 1.

Still to be run against a live tenant: create, update, configuration and deploy end to end.

## Implementation notes

 - `root.go` appends the suffix of `--env` to `--pkg` and `--artifact` before any command runs. `--env` is therefore not to be combined with `--target-env`, or the package is suffixed twice. This is noted in the README.
 - The archive keeps a fixed 1980-01-01 timestamp on every entry. A zero value renders as an invalid date in some tools, and the file modification time would break reproducibility.
 - Deploy stays fire and forget, as everywhere else in the tool. The returned task id is discarded and the deployment status is not polled.
 - The version the tenant reports after an upload is used for the configuration calls and for the deploy, rather than the version from the local manifest. The tenant is the authority.

## Addendum - the archive is renamed after all

Two decisions recorded above were wrong and are superseded by
[0003-artifact-upload-symbolic-name.md](./0003-artifact-upload-symbolic-name.md).

**"The zip is not rewritten."** It has to be. The tenant derives the symbolic
name of an artifact from the id it is created under, so an archive uploaded as
`Order_API_TEST_HARNESSQA` that still says `Order_API_TEST_HARNESS` inside is
rejected on the next update with *"Could not update artifact of the package; due
to change in the Bundle-symbolicName"*. The reasoning that `package move` does
not rewrite either missed that `package move` deletes and recreates instead of
issuing a `PUT`. `Bundle-SymbolicName` and `Bundle-Name` are now suffixed inside
the archive whenever the environment suffix is non empty - never in the
repository.

**"Packing as part of an upload checks the target environment."** Only the
original environment owns the version now. For any other environment the
version is taken from the repository as it is, `META-INF/MANIFEST.MF` is never
written back, `--bump` and `--set-version` are ignored, and a version that is
not higher than the target's produces a warning rather than a prompt or an
error.

The archive is also named `<artifactId><Suffix>.zip`, so the builds for two
environments no longer overwrite each other.
