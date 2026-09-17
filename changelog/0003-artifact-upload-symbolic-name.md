# Suffix Bundle-SymbolicName in the packaged artifact (`artifact pack`, `artifact upload`)

Branch: `feature/artifact-packager`

Supersedes the "the zip is not rewritten" decision of
[0002-artifact-pack-upload.md](./0002-artifact-pack-upload.md).

## Context

Uploading to an environment that shares a tenant with the original one failed on
the second attempt:

```
<code>Bad Request</code>
<message xml:lang="en">Could not update artifact of the package; due to change in the Bundle-symbolicName.</message>
```

Upload to `Dev` worked, upload to `QA` did not.

**Mechanism.** We sent OData `Id = Order_API_TEST_HARNESSQA` while the archive
still carried `Bundle-SymbolicName: Order_API_TEST_HARNESS`. The initial `POST`
was accepted and the tenant stored the symbolic name derived from the id it was
given. The following `PUT` then compared the archive against what it had stored,
saw `Order_API_TEST_HARNESS` where it expected `Order_API_TEST_HARNESSQA`, and
rejected the change. `Dev` was unaffected only because its suffix is empty, so
nothing could diverge.

0002 decided to leave the archive untouched and let only the OData metadata
carry the suffix, on the grounds that `package move` does the same.
`package move` gets away with it because it never round-trips an artifact
through a `PUT` - it deletes and recreates. That decision was wrong for upload.

## Decisions (confirmed with user)

The environment the content goes to now determines who owns the version, and
the archive is renamed whenever a suffix applies.

| | Original environment | Any other environment |
|---|---|---|
| Version source | tenant check, may bump | **the repository, as it is** |
| `META-INF/MANIFEST.MF` in the repository | written on bump | **never written** |
| Version not higher than the target | prompt, `--bump`, `--set-version`, else error | **warning on stdout, upload proceeds** |
| `--bump` / `--set-version` | apply | **ignored, with a warning** |
| `Bundle-SymbolicName` / `Bundle-Name` in the **archive** | suffixed when the suffix is non empty | suffixed when the suffix is non empty |
| `Bundle-SymbolicName` in the **repository** | **never** | **never** |

The rename rule is uniform - rewrite when `Environment.Suffix != ""` - which is
exactly the shared-tenant case. `Origin-Bundle-Name` and
`Origin-Bundle-SymbolicName` are deliberately left alone as the record of where
the flow came from.

## Files

### `packages/iflow/manifest.go`

The in-memory rewrite was extracted out of `SetBundleVersion` so the on-disk and
the archive path share it:

 - `setHeaders(data, updates)` - unexported core, replaces the named header lines and drops their continuation lines, preserving every other byte and the original terminators. Headers are applied in sorted order, so a rewrite is reproducible.
 - `RewriteManifestHeaders(data, updates)` - exported wrapper for archive use.
 - `ApplySuffixToSymbolicName(value, suffix)` - appends the suffix to the **name part only**, keeping OSGi directives: `Order_API; singleton:=true` becomes `Order_APIQA; singleton:=true`.
 - `ReadManifestBytes(dir)` - the raw manifest, so the caller can rewrite it without a second read.

`SetBundleVersion` keeps its behaviour and is now a thin file wrapper.

### `packages/iflow/zip.go`

 - `ZipDirWithOverrides(srcDir, overrides)` - substitutes the content of the given archive paths instead of reading them from disk. `ZipDir` is a nil-override call. This is how the artifact is renamed without touching the working copy.
 - `RewriteZipManifest(data, updates)` - unzip, rewrite the manifest entry, rezip. Needed because `artifact upload build/X.zip --target-env=QA` would otherwise hit the identical failure with a ready archive.
 - `ReadManifestHeaderFromZip(data, header)` - exported.

### `packages/cmd/artifactPack.go`

`packOptions` gains `Suffix` (archive-only rename, empty means none) and
`AllowVersionUpdate` (true only for the original environment). `packResult`
gains `TargetArtifactId`.

`packArtifact` splits on `AllowVersionUpdate`: the original environment keeps
the existing `resolvePackedVersion` path including the write-back, everything
else calls the new `warnOnVersionMisalignment` and packs the repository version
unchanged. `manifestOverrides` builds the renamed manifest when a suffix
applies. The archive is now written as `<artifactId><Suffix>.zip`, so the Dev
and QA builds of one flow no longer overwrite each other.

New `--target-env` flag on `artifact pack`: absent keeps the original-environment
behaviour, present switches to the target rules, which makes a QA archive
inspectable without uploading it.

### `packages/cmd/artifactUpload.go`

Computes `isOriginal` once and passes `Suffix` and `AllowVersionUpdate` down.
Ready archives go through `rewriteArchiveForEnvironment`. `targetBundleName` is
now computed once and used **both** for the OData `Name` and for `Bundle-Name`
in the archive - the drift between those two was the root cause, so they no
longer have independent sources.

`warnIgnoredVersionFlags` reports `--bump` and `--set-version` as ignored
outside the original environment.

Warnings go to **stdout** with `fmt.Printf`, as requested, rather than to
stderr with `log.Printf` like the rest of `packages/cmd`.

## Verification

```bash
go build ./... && go vet ./... && go test ./packages/...
```

Three existing tests encoded the old rules and were rewritten rather than
patched: the bump test split into an original-environment case that still bumps
and a target-environment case asserting the repository manifest is
**byte-identical** afterwards; the non-interactive error case retargeted to the
original environment; the archive filename expectation updated to the suffixed
name.

New tests: the uploaded archive for QA carries
`Bundle-SymbolicName: Order_API_TEST_HARNESSQA; singleton:=true` and
`Bundle-Name: ... QA` while the repository stays untouched; a create followed by
an update - the `PUT` that used to fail - keeps the symbolic name stable; an
environment without a suffix leaves the archive alone; a `.zip` argument to a
suffixed environment is renamed; `ApplySuffixToSymbolicName`,
`RewriteManifestHeaders`, `ZipDirWithOverrides` and `RewriteZipManifest` are
covered directly, including that the wrapped `Import-Package` block and the CRLF
terminators survive.

Mutation checked: disabling the rename fails
`TestUploadArtifactSuffixesSymbolicNameInTheArchive` and
`TestUploadArtifactTwiceKeepsSymbolicNameStable`; allowing repository writes for
a non-original environment fails
`TestUploadArtifactNeverWritesRepositoryForTargetEnvironment`.

Manually:

```bash
landscaper artifact pack artifacts/Order_API_TEST_HARNESS --target-env=QA --skip-version-check
unzip -p build/Order_API_TEST_HARNESSQA.zip META-INF/MANIFEST.MF | grep -E "^(Bundle|Origin)"
```

gives the suffixed `Bundle-Name` and `Bundle-SymbolicName`, the unchanged
`Bundle-Version` and the untouched `Origin-Bundle-*`, with `diff -r` confirming
the source folder is identical. Packing the same folder without `--target-env`
produces `build/Order_API_TEST_HARNESS.zip` with the base names.

## Implementation notes

 - The stub tenant in `artifactUpload_test.go` now derives the stored version from `Bundle-Version` in the uploaded archive on both `POST` and `PUT`, as a real tenant does. Previously `PUT` left the stored version untouched, which masked what the reported version after an update actually is.
 - `quotedIdFromPath` in the test only understands `Entity('Id')`, not the named-key form `Entity(Id='X',Version='Active')`. The `PUT` handler takes the id from the request body instead.
 - The `PUT` with a `Bundle-Version` equal to the stored one was an open question when this was written. It is **resolved**: a live tenant accepts it, so warn-and-proceed is viable and the warning does not need to become a hard stop.

## Findings from the live test run

Running `TESTING.md` end to end against a tenant confirmed the fix - the `PUT`
that previously returned *"due to change in the Bundle-symbolicName"* now
succeeds - and surfaced two further defects, both introduced by this change and
both fixed here.

**Uploading an already suffixed archive failed.** Naming the archive
`<artifactId><Suffix>.zip` meant `artifact pack --target-env=QA` produced
`Order_API_TEST_HARNESSQA.zip`, and uploading that file took
`Order_API_TEST_HARNESSQA` as the *base* id. Package resolution then failed
with "not declared in any package", and had it succeeded the id would have
become `Order_API_TEST_HARNESSQAQA`. `trimTargetSuffix` now strips the target
suffix from the derived id, but only when what remains is an artifact the
landscape declares, so an artifact whose name genuinely ends with the suffix is
left alone.

**The archive rename was not idempotent.** With the id fixed, the same upload
still failed, because `rewriteArchiveForEnvironment` appended the suffix to a
symbolic name that already carried it, producing
`Order_API_TEST_HARNESSQAQA` inside the archive. The rewrite is now expressed
as a replacement rather than an append: `iflow.ReplaceSymbolicName` sets the
name part to the known target id and keeps the OSGi directives, and
`Bundle-Name` is de-suffixed before the suffix is applied. Packing a folder was
never affected, because the repository copy is never suffixed.

Both are covered by `TestUploadAlreadySuffixedArchiveIsNotSuffixedTwice` and
`TestTrimTargetSuffix`.

Also corrected: a path that does not exist was reported as "not declared in any
package of the landscape configuration", because package resolution ran before
the path was validated. `uploadArtifact` now checks the path first.
