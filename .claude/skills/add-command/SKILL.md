---
name: add-command
description: Add a new cobra command or subcommand to the landscaper CLI in the house style. Use when asked to add, implement or flesh out a landscaper command - including filling in one of the existing "not implemented" stubs such as package delete, artifact delete, artifact move, package get, package create, config read, showInfo or check.
---

# Adding a landscaper command

## 1. Check whether a stub already exists

These files exist and only print "not implemented". **Fill the stub in, do not
create a new file:**

`artifactCreate.go`, `artifactDelete.go`, `artifactMove.go`, `packageGet.go`,
`packageCreate.go`, `packageDelete.go`, `configRead.go`, `showInfo.go`, `check.go`

Otherwise create `packages/cmd/<parent><Action>.go` in lowerCamel:
`artifactList.go`, `packageMove.go`, `configUpdate.go`.

## 2. Write the file

Copy the structure from `packages/cmd/artifactPack.go` (positional args, shared
helper) or `packages/cmd/artifactList.go` (simplest complete example):

```go
/*
Copyright © 2022 Aleksandr Ivanov <shamrockspb@gmail.com>
... full Apache 2.0 header, copy it verbatim from any existing file ...
*/
package cmd

import (...)

var xxxSomething *string

// xxxCmd represents the xxx command
var artifactXxxCmd = &cobra.Command{
	Use:   "xxx",
	Short: "One line, no trailing period",
	Long:  `Longer description`,
	Run: func(cmd *cobra.Command, args []string) {
		artifactXxx(args)
	},
}

func init() {
	artifactCmd.AddCommand(artifactXxxCmd)
	xxxSomething = artifactXxxCmd.Flags().String("something", "", "Help text")
}

func artifactXxx(args []string) {
	if globalLandscape == nil {
		println("Global landscape is not instantiated")
		return
	}
	...
}
```

Register on `rootCmd`, `packageCmd`, `artifactCmd` or `configCmd`.

## 3. Rules

- `Run` is a thin delegate. All logic goes in the unexported action func.
- **No `RunE`.** Errors use `log.Fatalln(err)`, which exits 1.
- Flag vars are package-level pointers assigned in `init()`. **Prefix them with the command name** - `packages/cmd` is one namespace and `targetEnv`, `toDeploy`, `iflowList` are already taken by `packageMove.go`.
- Put helper functions that can fail *below* the action and have them **return errors** rather than fatal - that is what makes them unit-testable. `packArtifact` and `uploadArtifact` are the examples.
- Getting a client: `globalLandscape.GetSystem4Environment(environment)` for the `--env` system, or `globalLandscape.GetEnvironment(id)` then `env.System.Client`.

## 4. The `--artifact` / `--pkg` trap

`root.go:115,120` appends the `--env` environment suffix to the global `--pkg`
and `--artifact` flags **before any command runs**.

- A command taking **filesystem paths must use positional args** (`cobra.MinimumNArgs(1)`), never `--artifact`, or the path gets a suffix glued onto it.
- A command with its own `--target-env` must document that `--env` is not to be passed as well, or the package is suffixed twice.

## 5. Output

```go
writer := tabwriter.NewWriter(os.Stdout, 0, 8, 1, '\t', tabwriter.AlignRight)
fmt.Fprintln(writer, "#\tArtefactId\tVersion\tPackage")
//... one Fprintf per row, index starting at 1
writer.Flush()
```

Numbered table for lists, `===Section===` key/value blocks for single records.
When looping over several inputs, write each row as it completes and flush
before any `log.Fatalln`, so partial progress is reported.

## 6. Finish

- Test it. `packages/cmd/artifactUpload_test.go` shows how to drive an action against an `httptest.NewTLSServer` stub tenant.
- `go build ./... && go vet ./... && go test ./packages/...`
- **Do not run `gofmt -w`** - it reformats every file in this go1.17 codebase.
- Add a usage section to `README.md` with a realistic command and its output.
- Add `changelog/000N-<feature>.md` following `0001-landscape-init.md`: Context, Decisions, Files, Verification, Implementation notes.
