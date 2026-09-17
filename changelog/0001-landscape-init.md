# Landscape initialization command (`landscaper init`)

*Implemented on 2026-09-15 on branch `feature/landscape-initialization`. The sections below are the plan the implementation followed; see "Implementation notes" at the end for what was added on top of it.*

## Context

`conf/landscape.yaml` describes a CPI landscape: `systems` (tenants), `environments` (logical stages mapped onto systems, each with an id-`suffix`), and `packages` → `artifacts` → per-environment `configurations` → `parameters`. Today the `packages:` section must be hand-written, which is impractical for a real tenant with dozens of packages and hundreds of iflow parameters.

`init` closes that gap: given a landscape file that already declares `systems` and `environments` (the connection info has to come from somewhere), it connects to every referenced system, enumerates packages and their designtime artifacts, reads each artifact's configuration parameters, and emits a fully populated `packages:` section.

The non-obvious part is how logical environments map onto physical packages:

- **Any** tenant can host a suffix-less environment plus one or more suffixed ones, and each tenant has its own set. A landscape with systems `dev` (hosting `Dev` with no suffix and `QA` with suffix `QA`) and `prod` (hosting `Prod` with no suffix and `PreProd` with suffix `PreProd`) yields four physical packages for one logical package: `Foo` + `FooQA` on `dev`, `Foo` + `FooPreProd` on `prod`. All four must collapse into **one** `packages:` entry whose artifacts carry a configuration block per environment — `Dev`, `QA`, `Prod`, `PreProd`, the original environment included. Grouping is therefore keyed on the base id **across all systems**, while suffix stripping is decided **per system** using only that system's environments.
- Suffixes are arbitrary strings declared in `environments[].suffix` in the landscape file. Nothing is hardcoded and no naming convention is assumed — the suffix set is read from the loaded landscape and is different per system.

### Prerequisite

`init` cannot bootstrap from nothing: it needs `systems` (host + credential env-var names) and `environments` (ids, suffixes, system bindings) plus `originalEnvironment` to know what to connect to and how to interpret suffixes. A hand-written minimal file is required first — `conf/landscape-minimal-example.yaml` is exactly that shape and should be documented as the starting point. The command validates that at least one system and one environment are present, and that `originalEnvironment` resolves, exiting with a message pointing at that example otherwise (note `landscape.go` performs no validation at all today and would just nil-deref).

## Decisions (confirmed with user)

- Output defaults to a **new file** `conf/landscape-generated.yaml` (`--output`); `--in-place` rewrites the source landscape file, replacing only its `packages:` node.
- The **original environment gets its own configuration block containing its full parameter set** — it is the documented baseline. Every other environment emits **only the parameters whose value differs from that baseline** (plus keys absent from it). `--all-parameters` emits the full set for every environment.
- `FooQA` is correlated to base `Foo` **only if `Foo` also exists on the same tenant** (checked against that tenant's own package list and that tenant's own suffixes); otherwise `FooQA` is its own base package.
- Read-only (SAP-delivered) packages are **skipped** unless `--include-readonly`, or unless named explicitly in `--packages`.

## Files

### New: `packages/cmd/landscapeInit.go`

House style copied from `packages/cmd/artifactList.go` (Apache header, `var xxxCmd = &cobra.Command{...}`, `init()` registering on the parent, thin `Run` delegating to an unexported function, `globalLandscape == nil` guard, `log.Fatalln(err)` on error, `text/tabwriter` summary to stdout).

`Use: "init"`, registered on `rootCmd`. Flags (all local to the command):

| Flag | Type | Default | Meaning |
|---|---|---|---|
| `--packages` | `StringSlice` | all | **Un-suffixed/base** package ids to gather |
| `--output` | `string` | `conf/landscape-generated.yaml` | Destination file |
| `--in-place` | `bool` | false | Rewrite the `--landscape-file` instead |
| `--all-parameters` | `bool` | false | Skip the diff-against-original filter |
| `--include-readonly` | `bool` | false | Include `Mode == "READ_ONLY"` packages |

Do **not** reuse the global `--pkg` flag: `packages/cmd/root.go:112-119` already appends the env suffix to `*pkg` and `*artifact` during `initConfig`, which is exactly the transformation this command must perform itself, per environment.

Flow: guard → `globalLandscape.Discover(opts)` → print warnings to stderr → `landscape.WritePackages(...)` → tabwriter summary (`Package / Artifacts / Environments / Params`).

### New: `packages/landscape/discover.go`

Pure orchestration + correlation over the already-built `*Landscape` (`packages/landscape/landscape.go:33-74`), reusing `System.Client` — no new connection handling.

```go
type DiscoverOptions struct {
    Packages        []string // base ids; empty = all
    IncludeReadOnly bool
    AllParameters   bool
}

func (l *Landscape) Discover(opts DiscoverOptions) (map[string]*Package, []string, error)
```

One accumulator `map[baseId]*Package` is shared across all systems, so bindings discovered on different tenants merge into the same entry. Per system (iterate `l.Systems`, skipping systems no environment references):

1. `envsOnSystem` = environments whose `System.Id` matches — each system has its own suffix set, all read from the landscape, none hardcoded. `baseEnv` = the one with `Suffix == ""` (prefer `l.OriginalEnvironment` if several on the *same* system; warn otherwise). Several suffix-less environments across *different* systems is the normal case (`Dev` on `dev`, `Prod` on `prod`) and is not a warning. Suffixed envs sorted by `len(Suffix)` descending so a longer suffix wins over a shorter prefix of it.
2. `client.ReadIntegrationPackages()` once per system; build a set of tenant package ids.
3. Classify each package id: first suffixed env where `strings.HasSuffix(id, suffix)` **and** the trimmed id exists in the tenant set → `(base, env)`; otherwise `(id, baseEnv)`. Skip `Mode == "READ_ONLY"` unless `IncludeReadOnly` or the base id is explicitly listed in `opts.Packages`.
4. For each selected (base, env, physicalPkgId): `client.ReadIntegrationDesigntimeArtifacts(physicalPkgId, false)`, then per artifact `client.ReadIntegrationDesigntimeArtifactConfigurations(art.Id, art.Version)`. Call configurations explicitly rather than passing `fetchConfig=true` — that path (`cpiclient.go:443-447`) silently discards errors; here they become warnings. Base artifact id = `strings.TrimSuffix(art.Id, env.Suffix)` when the suffix is present.
5. Merge into the shared `map[baseId]*Package` — a base package already created while scanning another system gains artifacts/configurations rather than being replaced. Uses the existing `Package{Id, Artifacts map[string]*Artifact}` / `Artifact{Id, Template, Configurations map[string]*Configuration}` / `Configuration{Environment, Parameters []*Parameter}` types. `Parameter.Type` from CPI's `DataType`, defaulting to `xsd:string` to match `landscape.go:261-264`. `Template` stays empty (hand-maintained).
6. Parameter filter. The original environment's block is always written in full — it is the baseline and must be documented. For every other environment, keep a parameter only when its value differs from the baseline value for the same base package/artifact/key, or the key is absent from the baseline; `AllParameters` disables this filter and writes the full set everywhere. An artifact that exists only in non-original environments has no baseline → emit all its parameters plus a warning. Because the baseline is needed before the other environments can be filtered, scan the original environment's system first (or buffer everything and apply the filter in a second pass — simpler and avoids ordering constraints).

Warnings (returned, printed to stderr, never fatal): multiple suffix-less environments on one system; an environment whose `System` is nil; a requested `--packages` id found on no system; a package missing in the original environment; per-artifact configuration read failures.

### New: `packages/landscape/export.go`

```go
func RenderPackages(pkgs map[string]*Package) ([]byte, error)              // standalone YAML doc
func WritePackagesInPlace(landscapeFilePath string, pkgs map[string]*Package) error
```

Define dedicated export structs with explicit lowercase `yaml` tags and `omitempty` (`id`, `artifacts`, `template`, `configurations`, `environment`, `parameters`, `key`, `value`, `type`) rather than reusing the anonymous structs in `LandscapeYAML` (`landscape.go:76-110`) — those would emit `template: ""` noise. Sort packages, artifacts, and parameters by id/key so output is deterministic.

For `--in-place`: unmarshal the source file into a `yaml.Node`, locate the `landscape` mapping, and replace-or-insert its `packages` value node. `yaml.v3` node round-tripping preserves comments and the untouched `systems`/`environments`/`originalEnvironment` sections. Write via a temp file + rename.

### Modified: `packages/cpiclient/cpiclient.go` (+ `packages/cpiclient/artifact.go`, currently an empty stub)

1. **Nil-safe JSON field access.** Add `func jsonString(m map[string]interface{}, key string) string` and `jsonBool(...)`. `Description`, `ShortText`, `Products`, `Keywords`, etc. come back as JSON `null` on real tenants, and the unchecked assertions in `ReadIntegrationPackages` (`cpiclient.go:696-717`), `ReadIntegrationPackage` (`:747-767`), `ReadIntegrationDesigntimeArtifacts` (`:433-442`) and `ReadIntegrationDesigntimeArtifactConfigurations` (`:322-326`) will panic on them. A tenant-wide scan hits this immediately, so switching those four parsers to the helpers is a prerequisite, not a nicety. Extract the package-element parse into `parseIntegrationPackage(map[string]interface{}) *IntegrationPackage` shared by the list and single-entity calls.
2. **Pagination.** There is none anywhere in the client (no `$top`/`$skip`/`__next`). Add `ReadAllIntegrationPackages()` which loops `?$format=json&$top=500&$skip=N`, reusing `parseIntegrationPackage`, until a short page comes back. `Discover` uses this; `ReadIntegrationPackages` keeps its current signature for `packageList.go`.

Both additions go in `artifact.go`/a small new `packages/cpiclient/parse.go` to keep the diff in the 902-line file focused.

### New: `packages/landscape/discover_test.go`

The correlation step is the only genuinely tricky logic and is pure, so factor it out as `classifyPackages(pkgIds []string, envs []*Environment, baseEnv *Environment) (map[string][]pkgBinding, []string)` (called once per system) plus the merge over its results, and table-test: `Foo`/`FooQA` on one tenant; `FooQA` with no `Foo`; overlapping suffixes (`Q`/`QA`); no suffix-less env on a system; a package whose id equals the suffix; and the **two-system case** — `dev`{`Dev`:"", `QA`:"QA"} + `prod`{`Prod`:"", `PreProd`:"PreProd"} with `Foo`,`FooQA` on dev and `Foo`,`FooPreProd` on prod, asserting a single `Foo` entry bound to all four environments. Follows the existing `packages/util/util_test.go` style.

## Verification

1. `go build ./... && go vet ./...`
2. `go test ./packages/landscape/... ./packages/util/...` — correlation table tests pass.
3. Dry run against the real tenant in `conf/landscape.yaml` (credentials from `.env`):
   `go run . init --landscape-file conf/landscape.yaml --packages <oneKnownPackage>` — inspect `conf/landscape-generated.yaml`: one entry per base package, artifacts un-suffixed, a `Dev` block with the complete parameter set and a `QA` block containing only the parameters that differ from it.
4. Full scan: `go run . init` — completes without panics on packages with null descriptions, read-only SAP content is absent, the summary table matches `go run . package list`.
4b. Multi-system grouping: build a scratch landscape file declaring two systems and four environments (two suffix-less, two suffixed — arbitrary suffix names, e.g. `QA` and `PreProd`) pointed at whatever tenants are reachable, and confirm a package present on both tenants appears **once**, with a configuration block for every environment including the original. If only one tenant is reachable, cover this with the `discover_test.go` two-system table case and simulate the second system by declaring both environments' suffixes on the same host.
5. `cp conf/landscape.yaml /tmp/ls.yaml && go run . init --landscape-file /tmp/ls.yaml --in-place` — `diff` shows only the added `packages:` block; comments and `systems`/`environments` untouched.
6. Round-trip: point `--landscape-file` at the generated file and run `go run . artifact list --pkg <base> --env QA` — the suffixing in `root.go` resolves to the real `<base>QA` package, proving the emitted ids are the un-suffixed ones the rest of the tool expects.


## Implementation notes

Delivered as planned. Files:

- `packages/cmd/landscapeInit.go` - the `init` command and its flags.
- `packages/landscape/discover.go` - `Discover`, the per system correlation (`classifyPackages`, `pickBaseEnvironment`, `trimEnvironmentSuffix`) and the baseline reduction (`reduceConfigurations`).
- `packages/landscape/export.go` - `RenderPackages` / `WritePackages`, yaml.Node based so that comments and the other sections of the source file survive.
- `packages/landscape/discover_test.go`, `packages/landscape/export_test.go` - correlation table tests, including the two system case, and a write/reload round trip.
- `packages/cpiclient/parse.go` - `jsonString` / `jsonBool` / `parseIntegrationPackage` and the paginated `ReadAllIntegrationPackages`.
- `packages/cpiclient/cpiclient.go` - the five existing parsers switched to the nil safe helpers.
- `README.md` - section "Gathering the landscape definition automatically".

Two changes outside the plan, both needed to run the command at all:

1. `packages/landscape/landscape.go` - the `host` of a system is now resolved through the environment, like `login`, `password` and `tokenURL` already were. `conf/landscape.yaml` declares `host: DEV_ENV_HOST_VAR`, which used to be taken literally, so no command could connect. The literal value is still used when no such environment variable exists, so the example files keep working.
2. `packages/cmd/root.go` - the default landscape file path became the constant `defaultLandscapeFile`, shared with the new command.

Verification against a live tenant (50 packages): a full scan produced a 12375 line landscape file in about three minutes without warnings, SAP delivered read only packages were left out, and the generated file loads back. Package `TestHarnessPreparation` and package `TestHarnessPreparationQA` were correctly folded into one declaration with a `Dev` and a `QA` configuration per artifact, while `QATestHarnessPreparation` stayed a package of its own. With the default parameter filter the `QA` blocks of that package disappear, because its values are identical to `Dev` - visible again with `--all-parameters`.


## Addendum - non changeable parameters are filtered out

`SAP_ProfileId` is maintained by SAP out of the integration profile of an iflow and cannot be written back through the API - `packageMove` would have tried to apply it and aborted the transport on the rejection. In the tenant, that the command was verified against, it was the single most frequent parameter of the generated file: 335 of 5004 parameters.

Such parameters are now left out of the generated file:

- `DiscoverOptions.SkipParameters` and `landscape.DefaultSkipParameters` (`= []string{"SAP_ProfileId"}`) in `packages/landscape/discover.go`. The keys are dropped in `gatherPackage` while the parameters are read, so they never become a baseline of the diff against the original environment, never reach the file and are not counted in the summary.
- `isSkippedParameter` matches a key either completely, or by prefix, if the pattern ends with an asterisk. Blank patterns are ignored, so an empty flag value means "write everything".
- `dropEmptyConfigurations` removes configurations without parameters and artifacts without configurations afterwards. An artifact, whose only parameter was `SAP_ProfileId`, would otherwise be written as an environment entry without parameters. Packages are kept even if they lose all their artifacts, because a package without artifacts is still transported - `conf/landscape-example.yaml` declares `CRMIntegrationPackage` exactly like that. The warning about artifacts, that do not exist in the original environment, is suppressed for artifacts, that are dropped anyway.
- New flag `--skip-parameters` of the `init` command, default `SAP_ProfileId`. `--skip-parameters=SAP_*` drops the whole reserved namespace, `--skip-parameters=` restores the previous behaviour.

Only the `init` command is affected. The apply path - `packageMove.go` and `configUpdate.go` - still writes every parameter, that a landscape file declares.

Tests: `TestIsSkippedParameter`, `TestDropEmptyConfigurations` and `TestReduceConfigurationsWithoutBaselineAndWithoutParameters` in `packages/landscape/discover_test.go`.

Verification against the live tenant:

- Package `Dummy` went from 8 artifacts with 11 parameters to 2 artifacts with 3 parameters. Six of its iflows had nothing but `SAP_ProfileId`.
- Full scan: 5004 parameters and 335 artifacts became 4669 parameters and 255 artifacts, 12375 lines became 11374, without warnings. The generated file still loads.
- `--skip-parameters=` reproduces the output of the previous implementation byte for byte.
- `--skip-parameters=SAP_*` on `SAPHybrisCloudforCustomerIntegrationwithSAPS4HANA` removed 81 parameters instead of 80 - the extra one is the user defined `SAP_Client`, which the default list deliberately keeps.
