# landscaper

CLI tool for managing SAP Cloud Integration (CPI) tenants: transport packages and
integration flows between environments, apply per-environment configuration, and
deploy from a git repository into a tenant.

Go 1.17, cobra + viper. Module `github.com/Trifolium-project/landscaper`.

## Commands

```bash
go build ./... && go vet ./... && go test ./packages/...
go build -o landscaper .          # local binary
./go-executable-build.bash        # cross-compile into build/
```

There is **no CI** - see `documentation/git-workflow.md`. Branches: feature off
`develop`, squash-merge into `develop`, real merge commit `develop` -> `main`.

## Architecture

```
packages/cmd  ->  packages/landscape  ->  packages/cpiclient
                  packages/iflow  (pure, no project dependencies)
```

`cmd` holds one file per command and all global state. `landscape` turns
`conf/landscape.yaml` into a runtime model and owns a `cpiclient.CPIClient` per
system. `cpiclient` is the SAP OData v1 HTTP client. `iflow` is pure local file
handling - manifests, versions, zip - with no cobra and no HTTP.

## File map

| Path | Responsibility |
|---|---|
| `main.go` | Calls `cmd.Execute()`, nothing else |
| `packages/cmd/root.go` | `rootCmd`, global flags, `initConfig()` - **mutates flags, see Traps** |
| `packages/cmd/package.go`, `artifact.go`, `config.go` | Empty parent commands for grouping |
| `packages/cmd/packageMove.go` | Transport a package tenant -> tenant. The reference for suffix/config logic |
| `packages/cmd/packageCopy.go`, `packageList.go` | Copy from Discover, list packages |
| `packages/cmd/artifactPack.go` | `artifact pack` + `packArtifact`, shared with upload |
| `packages/cmd/artifactUpload.go` | `artifact upload` - git repo -> tenant |
| `packages/cmd/artifactUpgrade.go` | Template-based upgrade within one tenant (beta) |
| `packages/cmd/artifactList.go`, `artifactGet.go`, `artifactDelpoy.go`, `artifactUndelpoy.go` | Read and runtime operations (note the `Delpoy` typos in the filenames) |
| `packages/cmd/landscapeInit.go` | `init` - generate the `packages:` section from a tenant |
| `packages/cmd/configUpdate.go` | Update artifact configuration parameters |
| `packages/cpiclient/cpiclient.go` | The whole client: auth, CSRF, all OData calls |
| `packages/cpiclient/parse.go` | Null-safe JSON helpers, paginated package read |
| `packages/landscape/landscape.go` | YAML model, `NewLandscape`, `GetEnvironment`, `FindPackageForArtifact` |
| `packages/landscape/discover.go` | Read a tenant back into a landscape model, suffix matching |
| `packages/landscape/export.go` | Write `packages:` back into YAML preserving comments |
| `packages/iflow/manifest.go` | Read/write `META-INF/MANIFEST.MF` |
| `packages/iflow/version.go` | `Version`, `ParseVersion`, `Compare`, `Bump` |
| `packages/iflow/zip.go` | `ZipDir`, `WriteFileAtomic`, read headers out of an archive |
| `packages/util/util.go` | `Contains` |
| `conf/landscape*.yaml` | Landscape definitions. `landscape.yaml` is gitignored |
| `assets/IntegrationContent.yaml` | SAP's OData swagger. **159KB - grep it, never read it whole** |
| `changelog/000N-*.md` | Design + implementation doc per feature. Write one for each feature |
| `TESTING.md` | Manual test plan for `artifact pack` / `artifact upload` |

### Stub commands

These are registered but only print "not implemented". Do not assume a
subcommand works because it appears in `--help`:

`artifact create`, `artifact delete`, `artifact move`, `package get`,
`package create`, `package delete`, `config read`, `showInfo`, `check`.

Do not determine this by grepping for "not implemented": `config update` is
**fully implemented** but still says "(not implemented)" in its `Short` and
`Long`, and `check.go` capitalises it as "Not implemented". Check the `Run`
body instead.

## Conventions

- One command per file, named `<parent><Action>.go` in lowerCamel: `artifactList.go`, `packageMove.go`.
- Structure: Apache license header -> `package cmd` -> package-level flag pointer vars -> `var xxxCmd = &cobra.Command{...}` -> `func init()` registering on the parent -> unexported action func.
- `Run` is always a thin delegate. **No `RunE` anywhere** - errors go to `log.Fatalln`, which exits 1.
- Every action opens with the `globalLandscape == nil` guard.
- Output is `text/tabwriter` to stdout: either a numbered table with a `#` column, or `===Section===` key/value blocks. No JSON output, no `--output-format`.
- Comments are `//Text` with no space, matching the existing files.

## Traps

Things that are expensive to rediscover. Read these before changing anything.

- **`root.go:115` and `root.go:120` mutate the global flags before any command runs**: `*pkg = *pkg + env.Suffix` and `*artifact = *artifact + env.Suffix`, using the suffix of `--env`. Consequences: never route a filesystem path through `--artifact`; never combine `--env` with `--target-env` or the package is suffixed twice; a new command taking paths must use positional args.
- **The archive's `Bundle-SymbolicName` must match the OData `Id` it is uploaded under.** The tenant derives the symbolic name from the id at creation and rejects any later `PUT` whose archive disagrees ("due to change in the Bundle-symbolicName"). `artifact pack`/`upload` therefore suffix `Bundle-SymbolicName` and `Bundle-Name` **inside the archive** whenever `Environment.Suffix != ""` - never in the repository. See `changelog/0003-artifact-upload-symbolic-name.md`.
- **Only the original environment owns the version.** Uploading anywhere else takes the version from `META-INF/MANIFEST.MF` as it is, never writes the working tree, and ignores `--bump`/`--set-version`. A pipeline deploying to QA can therefore not produce a commit.
- **`Version == "Active"`** in an API response means the artifact is a **draft** in the tenant, not a version string. `packageMove` aborts on it; `artifact pack` cannot compare it.
- **`assets/IntegrationContent.yaml` is 159KB** (~40K tokens). Grep it for the endpoint you need.
- **Known bug, do not copy**: `packageMove.go:250-256` has inverted branches and nil-derefs `sourceConf.DataType` when the source artifact lacks a parameter. The surrounding `defer recover()` hides it. `artifactUpload.go` deliberately does not reuse that loop.
- **Do not run `gofmt -w`.** The repo is formatted with go1.17 gofmt. Modern gofmt rewrites comment spacing and license-block indentation in *every* file - `gofmt -l packages/` listing all of them is expected, not a problem to fix.
- **Gitignored**: `.env`, `conf/landscape.yaml`, `artifacts/`, `build/`, `landscaper`. Edits under `artifacts/` cannot be undone with git - back the folder up before changing a manifest.
- Credentials: `landscape.yaml` stores the *names* of environment variables, resolved with `os.Getenv` from `.env`. An empty login or password triggers an interactive prompt (`landscape.go`), which will hang a pipeline.
- `GetArtifactConfiguration` uses `defer recover()` instead of nil checks and returns `nil, nil` when the package, artifact or environment is missing.

## Tests

```bash
go test ./packages/...
```

- `packages/landscape`, `packages/util`, `packages/iflow`: table-driven stdlib tests, `t.TempDir()` for fixtures. No testify, no mocks.
- `packages/cmd`: drives `packArtifact` / `uploadArtifact` against an in-process `httptest.NewTLSServer`. The client builds its own `http.Client`, so the test cert is trusted by swapping `TLSClientConfig` on `http.DefaultTransport` and restoring it after. See `artifactUpload_test.go` before writing a new command test.
- `packages/cpiclient` has no tests.

Live-tenant verification is manual; `TESTING.md` is the worked example.
