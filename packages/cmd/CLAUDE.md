# packages/cmd

One file per command. See the root `CLAUDE.md` for the file map and the global
flag trap; this file is the detail needed to write or change a command.

## Skeleton

Copy `artifactPack.go` or `artifactList.go`. The shape is always:

```go
/*
Copyright © 2022 Aleksandr Ivanov <shamrockspb@gmail.com>
... Apache 2.0 header ...
*/
package cmd

import (...)

//Flag values, package level pointers assigned in init()
var somethingFlag *string

// xxxCmd represents the xxx command
var artifactXxxCmd = &cobra.Command{
	Use:   "xxx",
	Short: "One line",
	Long:  `Longer text`,
	Run: func(cmd *cobra.Command, args []string) {
		artifactXxx()          //thin delegate, never logic inline
	},
}

func init() {
	artifactCmd.AddCommand(artifactXxxCmd)
	somethingFlag = artifactXxxCmd.Flags().String("something", "", "Help")
}

func artifactXxx() {
	if globalLandscape == nil {
		println("Global landscape is not instantiated")
		return
	}
	...
}
```

Parents to register on: `rootCmd`, `packageCmd`, `artifactCmd`, `configCmd`.

## Flags

- Package-level pointer vars, assigned from `Flags().String/Bool/StringSliceP(...)` in `init()`, dereferenced in the action. Name them for the command (`uploadDeploy`, not `deploy`) - the whole package shares one namespace and `targetEnv`/`toDeploy` are already taken by `packageMove.go`.
- In use: `StringSliceP` for repeatable values (`-f/--iflow`), `MarkFlagRequired`, `cobra.MinimumNArgs` for positional args.
- **Global flags `--pkg` and `--artifact` arrive already suffixed** with the `--env` environment suffix, applied in `root.go:115,120`. A command that takes filesystem paths must use **positional args**, not `--artifact`.

## artifact pack / upload

Two rules that are easy to break when touching `artifactPack.go` or `artifactUpload.go`:

- The archive's `Bundle-SymbolicName` has to match the OData `Id`, so both it and `Bundle-Name` are suffixed **inside the archive** when `Environment.Suffix != ""`. Use `packOptions.Suffix`; never write the suffix into the repository.
- `packOptions.AllowVersionUpdate` is true **only** for the original environment. It is the single switch that permits `iflow.SetBundleVersion` to touch the working tree. Everything else warns and packs the repository version unchanged.

`packArtifact` is shared by both commands, so a change to either rule affects both.

## Errors and exit codes

`log.Fatalln(err)` / `log.Fatalf(...)`, which exits 1 - correct for pipelines.
No `RunE`, no returned errors from action funcs. Helper functions *below* the
action may return errors normally; `packArtifact` and `uploadArtifact` do, which
is also what makes them testable.

When a command processes a list, flush the tabwriter before the fatal so the
rows already produced are still reported.

## Output

```go
writer := tabwriter.NewWriter(os.Stdout, 0, 8, 1, '\t', tabwriter.AlignRight)
fmt.Fprintln(writer, "#\tArtefactId\tVersion\tPackage\tDeploy Status")
...
writer.Flush()
```

Numbered tables with a `#` column for lists; `===Section===` key/value blocks for
single records (`artifactGet.go`, `configUpdate.go`).

## Tests

`artifactUpload_test.go` is the model. It builds an `httptest.NewTLSServer`
imitating the OData endpoints, points a `landscape.Landscape` at it, and calls
the action helpers directly. Because `CPIClient` constructs its own
`http.Client` with no `Transport`, the test cert is trusted by swapping
`TLSClientConfig` on `http.DefaultTransport` and restoring it in `t.Cleanup`.

Set the package-level flag pointers directly in the test (`setUploadFlags`)
rather than going through cobra.
