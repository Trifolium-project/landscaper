# Manual test plan - `artifact pack` and `artifact upload`

Test sequence for the two commands added on `feature/artifact-packager`, run against the
sample artifact `artifacts/Order_API_TEST_HARNESS` and the landscape in `conf/landscape.yaml`
(environments `Dev`, suffix none, and `QA`, suffix `QA`, both on system `dev`).

Commands are written for **fish**. Work through the phases by hand, this is not a script.

> **Phase 5 and everything after it write to a real tenant.**
> `artifact delete` and `package delete` are not implemented yet, so the packages created
> during the test have to be removed in the Integration Suite UI afterwards.

> **`artifacts/` is in `.gitignore`**, so a version bump written into `META-INF/MANIFEST.MF`
> cannot be undone with git. Take the backup in phase 0 and use `restore` between the
> version tests.

## Phase 0 - setup

```fish
cd /Users/Aleksandr_Ivanov14/Documents/Dev/landscaper

go build -o landscaper .

cp -R artifacts/Order_API_TEST_HARNESS /tmp/OATH_backup
alias restore 'rm -rf artifacts/Order_API_TEST_HARNESS; cp -R /tmp/OATH_backup artifacts/Order_API_TEST_HARNESS'

grep Bundle-Version artifacts/Order_API_TEST_HARNESS/META-INF/MANIFEST.MF
./landscaper package list
```

`package list` is read only and confirms that the credentials in `.env` reach the tenant.
The sample artifact starts at `Bundle-Version: 1.0.3`.

## Phase 1 - packing without touching the tenant

```fish
./landscaper artifact pack artifacts/Order_API_TEST_HARNESS --skip-version-check

unzip -l build/Order_API_TEST_HARNESS.zip
unzip -p build/Order_API_TEST_HARNESS.zip META-INF/MANIFEST.MF | head -6
```

Expected: all twelve files of the folder, `META-INF/MANIFEST.MF` at the archive root, no
`.DS_Store`. The table reports `Version in Dev` as `-` and `Changed` as `false`.

The archive has to be reproducible:

```fish
./landscaper artifact pack artifacts/Order_API_TEST_HARNESS --skip-version-check --output build/a
./landscaper artifact pack artifacts/Order_API_TEST_HARNESS --skip-version-check --output build/b
shasum build/a/Order_API_TEST_HARNESS.zip build/b/Order_API_TEST_HARNESS.zip
```

Both hashes must be identical.

Packing for another environment renames the identifiers inside the archive and leaves the
working tree alone:

```fish
cp -R artifacts/Order_API_TEST_HARNESS /tmp/OATH_pack_check
./landscaper artifact pack artifacts/Order_API_TEST_HARNESS --target-env=QA --skip-version-check
unzip -p build/Order_API_TEST_HARNESSQA.zip META-INF/MANIFEST.MF | grep -E "^(Bundle|Origin)"
diff -r /tmp/OATH_pack_check artifacts/Order_API_TEST_HARNESS
```

Expected: the archive is `build/Order_API_TEST_HARNESSQA.zip`, `Bundle-SymbolicName` and
`Bundle-Name` carry the `QA` suffix, `Bundle-Version` and `Origin-Bundle-*` are unchanged, and
`diff -r` reports no differences.

## Phase 2 - the version check against Dev

```fish
./landscaper artifact list --pkg=TestHarnessPreparation
./landscaper artifact pack artifacts/Order_API_TEST_HARNESS
```

Two outcomes are correct, depending on what the tenant holds:

 - the artifact is at `1.0.3` or higher in Dev - the command asks for a new version.
   Try `0.0.1` and `1.0.3`, both have to be rejected, then press enter to take the suggestion
 - the package or the artifact is not in Dev - the command packs straight away and reports `-`

Only the version line may have changed:

```fish
grep Bundle-Version artifacts/Order_API_TEST_HARNESS/META-INF/MANIFEST.MF
diff -r /tmp/OATH_backup artifacts/Order_API_TEST_HARNESS
restore
```

## Phase 3 - non interactive runs

The first test only proves something while the local version is still equal to or below the
one in the tenant, so run it before the version is inflated.

```fish
./landscaper artifact pack artifacts/Order_API_TEST_HARNESS < /dev/null ; echo "exit=$status"
restore
```

Expected: exit code 1 and a message naming `--bump`, `--set-version` and `--skip-version-check`.

```fish
./landscaper artifact pack artifacts/Order_API_TEST_HARNESS --bump=patch ; and grep Bundle-Version artifacts/Order_API_TEST_HARNESS/META-INF/MANIFEST.MF
restore
./landscaper artifact pack artifacts/Order_API_TEST_HARNESS --bump=minor ; and grep Bundle-Version artifacts/Order_API_TEST_HARNESS/META-INF/MANIFEST.MF
restore
./landscaper artifact pack artifacts/Order_API_TEST_HARNESS --bump=major ; and grep Bundle-Version artifacts/Order_API_TEST_HARNESS/META-INF/MANIFEST.MF
restore
./landscaper artifact pack artifacts/Order_API_TEST_HARNESS --set-version=3.1.4 ; and grep Bundle-Version artifacts/Order_API_TEST_HARNESS/META-INF/MANIFEST.MF
restore
```

The version is raised relative to the **tenant**, not to the local copy, so against a tenant
at `1.0.3` the three bumps give `1.0.4`, `1.1.0` and `2.0.0`.

These have to fail:

```fish
./landscaper artifact pack artifacts/Order_API_TEST_HARNESS --set-version=0.0.1 ; echo "exit=$status"
./landscaper artifact pack artifacts/Order_API_TEST_HARNESS --set-version=abc   ; echo "exit=$status"
./landscaper artifact pack artifacts/Order_API_TEST_HARNESS --bump=build        ; echo "exit=$status"
```

## Phase 4 - several artifacts and error handling

```fish
cp -R /tmp/OATH_backup artifacts/Sample_API
./landscaper artifact pack artifacts/Order_API_TEST_HARNESS artifacts/Sample_API --skip-version-check
```

`Sample_API` is declared in `conf/landscape.yaml`, but the copied manifest still says
`Bundle-SymbolicName: Order_API_TEST_HARNESS`, so a warning about the mismatch is expected.
The folder name wins.

```fish
./landscaper artifact pack artifacts/Order_API_TEST_HARNESS artifacts/nope --skip-version-check ; echo "exit=$status"
ls build/
```

Expected: the first artifact is reported and its archive exists, then the run stops with exit
code 1 on the broken path.

```fish
cp -R /tmp/OATH_backup artifacts/Unknown_API
./landscaper artifact pack artifacts/Unknown_API ; echo "exit=$status"
./landscaper artifact pack artifacts/Unknown_API --pkg=TestHarnessPreparation
rm -rf artifacts/Unknown_API
```

An artifact that is in no package of the landscape file has to fail with an error naming
`--pkg`, and has to work once `--pkg` is given.

## Phase 5 - first upload to QA

From here the tenant is modified.

```fish
restore
./landscaper artifact upload artifacts/Order_API_TEST_HARNESS --target-env=QA --skip-version-check

./landscaper artifact list --pkg=TestHarnessPreparation --env=QA
./landscaper artifact get --artifact=Order_API_TEST_HARNESS --env=QA
```

Expected: package `TestHarnessPreparationQA` created, artifact `Order_API_TEST_HARNESSQA`
with name `Order API for Test Harness QA`, `Action` is `created`. `artifact get` has to show
the QA parameter values from the landscape file, `urlPath=/QA/erp/order` and
`ds_name=orders_test_harness_QA`.

## Phase 6 - update in place

```fish
echo "//touched by test" >> artifacts/Order_API_TEST_HARNESS/src/main/resources/script/script1.groovy
cp -R artifacts/Order_API_TEST_HARNESS /tmp/OATH_before_qa

./landscaper artifact upload artifacts/Order_API_TEST_HARNESS --target-env=QA

./landscaper artifact list --pkg=TestHarnessPreparation --env=QA
./landscaper artifact get --artifact=Order_API_TEST_HARNESS --env=QA
diff -r /tmp/OATH_before_qa artifacts/Order_API_TEST_HARNESS
```

Expected: `Action` is `updated` - this is the `PUT` that used to fail with *"Could not update
artifact of the package; due to change in the Bundle-symbolicName"* - the configuration from
phase 5 is still in place, and `diff -r` reports **no differences**: QA is not the original
environment, so the working tree is never written.

The version comes from the repository as it stands. `--bump` and `--set-version` are ignored
here, with a warning:

```fish
./landscaper artifact upload artifacts/Order_API_TEST_HARNESS --target-env=QA --bump=patch
```

Expected: `WARNING --bump is ignored for environment QA, ...` and the same version as before.

If the repository version is not higher than the one in QA, a warning is printed and the
upload still goes ahead:

```fish
./landscaper artifact upload artifacts/Order_API_TEST_HARNESS --target-env=QA
```

Expected: `WARNING version X of Order_API_TEST_HARNESSQA is not higher than version Y already
present in environment QA, uploading it anyway`.

## Phase 7 - deploy

```fish
./landscaper artifact upload artifacts/Order_API_TEST_HARNESS --target-env=QA --deploy
sleep 30
./landscaper artifact list --pkg=TestHarnessPreparation --env=QA --only-deployed
```

Deployment is fire and forget, so the runtime status has to be read separately. Expected
status `STARTED` with the version that was just uploaded.

## Phase 8 - a zip archive as input

```fish
#Pack for QA directly - the archive is renamed and named after the suffixed id
./landscaper artifact pack artifacts/Order_API_TEST_HARNESS --target-env=QA
unzip -p build/Order_API_TEST_HARNESSQA.zip META-INF/MANIFEST.MF | grep -E "^(Bundle|Origin)"
./landscaper artifact upload build/Order_API_TEST_HARNESSQA.zip --target-env=QA

#A Dev archive uploaded to QA is rewritten on the way
./landscaper artifact pack artifacts/Order_API_TEST_HARNESS --skip-version-check
./landscaper artifact upload build/Order_API_TEST_HARNESS.zip --target-env=QA
```

Expected from the `unzip`: `Bundle-SymbolicName: Order_API_TEST_HARNESSQA; singleton:=true`,
`Bundle-Name: Order API for Test Harness QA`, the version unchanged, and both
`Origin-Bundle-*` still carrying the base values. Both uploads report `Source` as `zip` and
`Action` as `updated`.

## Phase 9 - mixed input and upload errors

```fish
./landscaper artifact upload artifacts/Sample_API build/Order_API_TEST_HARNESSQA.zip --target-env=QA
```

A folder and an archive in one call, both land in `TestHarnessPreparationQA`.

```fish
./landscaper artifact upload artifacts/Order_API_TEST_HARNESS                   ; echo "exit=$status"
./landscaper artifact upload artifacts/Order_API_TEST_HARNESS --target-env=Nope ; echo "exit=$status"
./landscaper artifact upload artifacts/nope --target-env=QA --skip-version-check ; echo "exit=$status"
```

Expected in turn: `required flag(s) "target-env" not set`, `Environment Nope is not found`,
and `... is neither an integration flow folder nor a zip archive`. All three exit with 1.

```fish
./landscaper artifact upload artifacts/Order_API_TEST_HARNESS --target-env=QA --env=QA --skip-version-check
```

This is the documented trap. `root.go` appends the suffix of `--env` to `--pkg`, so combining
`--env` with `--target-env` suffixes the package twice and the artifact ends up in
`TestHarnessPreparationQAQA`. `--env` is not needed for an upload.

> Running this actually creates `TestHarnessPreparationQAQA` in the tenant, and `package
> delete` is a stub, so it can only be removed in the UI. Skip it unless you want to see the
> trap for yourself.

## Phase 10 - upload to the original environment

```fish
restore
./landscaper artifact upload artifacts/Order_API_TEST_HARNESS --target-env=Dev --bump=patch
```

No suffix is added anywhere. Unlike `package move`, uploading to the original environment is
allowed, because that is the normal merge request case.

## Phase 11 - cleanup

```fish
./landscaper artifact undeploy --artifact=Order_API_TEST_HARNESS --env=QA
restore
rm -rf artifacts/Sample_API build landscaper
```

`artifact delete` and `package delete` are still stubs, so `TestHarnessPreparationQA` and
`TestHarnessPreparationQAQA` have to be removed in the Integration Suite UI by hand.

## Automated tests

The unit and command tests cover the same ground without a tenant:

```fish
go build ./... ; and go vet ./... ; and go test ./packages/...
```
