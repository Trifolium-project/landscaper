# packages/landscape

Turns `conf/landscape.yaml` into a runtime model, and (for `init`) reads a
tenant back into that model and writes it out again.

## Model

`LandscapeYAML` is the wire struct; `buildLandscapeFromManifest` converts it to
the runtime model, **turning every array into a map** keyed by id:

```
Landscape{Name, Systems map[string]*System, Packages map[string]*Package,
          Environments map[string]*Environment, OriginalEnvironment *Environment}
Package{Id, Artifacts map[string]*Artifact}
Artifact{Id, Template, Configurations map[string]*Configuration}   //keyed by environment id
```

`Environment.Suffix` is `""` for the base environment and drives every id
rewrite in the tool. `Parameter.Type` defaults to `xsd:string` when empty.

`System.Client` is a live `*cpiclient.CPIClient`, built at load time.

## Loading

`NewLandscape(configFile)` -> `godotenv.Load()` -> `os.ReadFile` ->
`yaml.Unmarshal` -> `buildLandscapeFromManifest`.

- Credentials are the **names** of environment variables, resolved with `os.Getenv`. `host` falls back to the literal string if no such variable exists.
- An empty login or password prompts on stdin (`bufio.Reader`, `term.ReadPassword`). **This hangs a pipeline.**
- An unknown `originalEnvironment` silently yields `nil`, and `root.go` then nil-derefs it.

## Quirks

- **`GetArtifactConfiguration` uses `defer recover()` instead of nil checks** and returns `nil, nil` when the package, artifact or environment is missing, logging "Using config from original environment". Callers cannot distinguish "no parameters" from "not found".
- `GetEnvironment` and `GetSystem4Environment` do return proper errors.
- `FindPackageForArtifact(artifactId)` takes the **base** id without suffix and errors when the artifact is unknown or declared in more than one package.

## discover.go

Reverse direction, used only by `landscaper init`.

- `pickBaseEnvironment` prefers the suffix-less environment, then `OriginalEnvironment`.
- `classifyPackages` matches suffixed packages to their base, **longest suffix first**, and only binds `FooQA` to `Foo` when `Foo` exists in the same tenant - so a package that merely ends in `QA` is left alone. `trimEnvironmentSuffix` refuses to trim when `id == suffix`.
- `reduceConfigurations` keeps the original environment's full parameter set and reduces the others to the differences; `dropEmptyConfigurations` prunes what is left.
- `isSkippedParameter` honours `DefaultSkipParameters = ["SAP_ProfileId"]`; a trailing `*` matches a prefix.
- Warnings are collected as `[]string` and printed by the caller, never fatal.

## export.go

`RenderPackages` / `WritePackages` rewrite **only** the `packages:` section by
doing `yaml.Node` surgery - `childNode`, then
`setChildNode(landscapeNode, "packages", &node, "environments")` to splice it in
before `environments`. This preserves comments, key order and the other
sections. Output is sorted for determinism, indent 2, written via
`os.CreateTemp` + `os.Rename`.

Dedicated `*Export` structs with `yaml:",omitempty"` tags keep empty internal
fields out of the file. Do not marshal the runtime model directly.

Tests: `discover_test.go`, `export_test.go` - table-driven, `t.TempDir()`, with
a `const minimalLandscapeFile` YAML literal.
