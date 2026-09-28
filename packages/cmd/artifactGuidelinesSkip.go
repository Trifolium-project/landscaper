/*
Copyright © 2022 Aleksandr Ivanov <shamrockspb@gmail.com>

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/
package cmd

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/Trifolium-project/landscaper/packages/cpiclient"
	"github.com/spf13/cobra"
)

var (
	guidelinesSkipArtifacts *[]string
	guidelinesSkipRules     *[]string
	guidelinesSkipReason    *string
	guidelinesSkipVersion   *string
	guidelinesSkipExecution *string
	guidelinesSkipOutput    *string

	guidelinesUnskipArtifacts *[]string
	guidelinesUnskipRules     *[]string
	guidelinesUnskipVersion   *string
	guidelinesUnskipExecution *string
	guidelinesUnskipOutput    *string
)

//guidelineSkipRow is one line of the skip report
type guidelineSkipRow struct {
	Artifact    string `json:"artifact"`
	Rule        string `json:"rule"`
	ExecutionId string `json:"executionId"`
	//skipped, unskipped, updated, unchanged or failed
	Action string `json:"action"`
	Reason string `json:"reason"`
	Error  string `json:"error,omitempty"`
}

// artifactGuidelinesSkipCmd represents the artifact guidelines skip command
var artifactGuidelinesSkipCmd = &cobra.Command{
	Use:   "skip",
	Short: "Skip design guideline rules for artifacts",
	Long: `Skip design guideline rules an artifact does not follow on purpose. The tenant
records the reason and who skipped the rule, and keeps the skip for later
executions of the artifact.

Repeating a skip is harmless: a rule already skipped with the same reason is
reported as unchanged, one skipped with another reason is reverted and skipped
again, which is reported as updated.

A skip is filed against an execution. Without --execution the latest one is
taken, and the guidelines are run first when the artifact was never checked.
Essential rules cannot be skipped, the tenant refuses them.

To keep skips in git, declare them under guidelineSkips of the artifact in the
landscape configuration instead.`,
	Run: func(cmd *cobra.Command, args []string) {
		artifactGuidelinesSkip(true)
	},
}

// artifactGuidelinesUnskipCmd represents the artifact guidelines unskip command
var artifactGuidelinesUnskipCmd = &cobra.Command{
	Use:   "unskip",
	Short: "Revert skipped design guideline rules of artifacts",
	Long: `Revert skipped design guideline rules, so that they count again. A rule that
is not skipped is reported as unchanged.`,
	Run: func(cmd *cobra.Command, args []string) {
		artifactGuidelinesSkip(false)
	},
}

func init() {
	artifactGuidelinesCmd.AddCommand(artifactGuidelinesSkipCmd)
	artifactGuidelinesCmd.AddCommand(artifactGuidelinesUnskipCmd)

	guidelinesSkipArtifacts = artifactGuidelinesSkipCmd.Flags().StringSlice("artifacts", []string{}, "Comma separated list of artifact ids, without environment suffix")
	guidelinesSkipRules = artifactGuidelinesSkipCmd.Flags().StringSlice("rule", []string{}, "Guideline id to skip, e.g. HANDLE_EXCEPTIONS. Repeat or separate with commas")
	guidelinesSkipReason = artifactGuidelinesSkipCmd.Flags().String("reason", "", "Why the rule does not apply, recorded by the tenant")
	guidelinesSkipVersion = artifactGuidelinesSkipCmd.Flags().String("version", guidelineVersionActive, "Artifact version, the tenant only holds the current one")
	guidelinesSkipExecution = artifactGuidelinesSkipCmd.Flags().String("execution", "", "Execution id to file the skip against instead of the latest one")
	guidelinesSkipOutput = artifactGuidelinesSkipCmd.Flags().String("output", outputText, "Output format: text or json")
	artifactGuidelinesSkipCmd.MarkFlagRequired("rule")
	artifactGuidelinesSkipCmd.MarkFlagRequired("reason")

	guidelinesUnskipArtifacts = artifactGuidelinesUnskipCmd.Flags().StringSlice("artifacts", []string{}, "Comma separated list of artifact ids, without environment suffix")
	guidelinesUnskipRules = artifactGuidelinesUnskipCmd.Flags().StringSlice("rule", []string{}, "Guideline id to revert. Repeat or separate with commas")
	guidelinesUnskipVersion = artifactGuidelinesUnskipCmd.Flags().String("version", guidelineVersionActive, "Artifact version, the tenant only holds the current one")
	guidelinesUnskipExecution = artifactGuidelinesUnskipCmd.Flags().String("execution", "", "Execution id to file the revert against instead of the latest one")
	guidelinesUnskipOutput = artifactGuidelinesUnskipCmd.Flags().String("output", outputText, "Output format: text or json")
	artifactGuidelinesUnskipCmd.MarkFlagRequired("rule")
}

func artifactGuidelinesSkip(skip bool) {

	if globalLandscape == nil {
		println("Global landscape is not instantiated")
		return
	}

	artifacts, rules, reason, version, executionId, output :=
		*guidelinesSkipArtifacts, *guidelinesSkipRules, *guidelinesSkipReason,
		*guidelinesSkipVersion, *guidelinesSkipExecution, *guidelinesSkipOutput
	if !skip {
		artifacts, rules, reason, version, executionId, output =
			*guidelinesUnskipArtifacts, *guidelinesUnskipRules, "",
			*guidelinesUnskipVersion, *guidelinesUnskipExecution, *guidelinesUnskipOutput
	}

	if err := validateGuidelineSelection(guidelineSelection{Artifacts: artifacts}); err != nil {
		log.Fatalln(err)
	}
	if skip && strings.TrimSpace(reason) == "" {
		log.Fatalln("--reason must not be empty, the tenant refuses a skip without one")
	}
	if executionId != "" && len(artifacts) != 1 {
		log.Fatalln("--execution needs exactly one artifact in --artifacts")
	}
	if output != outputText && output != outputJSON {
		log.Fatalf("Unknown output format %q, use %s or %s", output, outputText, outputJSON)
	}

	environment_, err := globalLandscape.GetEnvironment(*environment)
	if err != nil {
		log.Fatalln(err)
	}
	client := environment_.System.Client

	targets, err := resolveGuidelineTargets(client, environment_, guidelineSelection{Artifacts: artifacts})
	if err != nil {
		log.Fatalln(err)
	}

	rows := skipGuidelines(client, targets, rules, reason, version, executionId, skip)

	failed := false
	for _, row := range rows {
		if row.Action == skipActionFailed {
			failed = true
		}
		auditItem(map[string]interface{}{
			"operation":    "guidelines-skip",
			"artifact":     row.Artifact,
			"rule":         row.Rule,
			"execution_id": row.ExecutionId,
			"status":       row.Action,
			"reason":       row.Reason,
			"error":        row.Error,
		})
	}

	if output == outputJSON {
		encoded, err := json.MarshalIndent(rows, "", "  ")
		if err != nil {
			log.Fatalln(err)
		}
		fmt.Println(string(encoded))
	} else {
		writer := tabwriter.NewWriter(os.Stdout, 0, 8, 1, '\t', tabwriter.AlignRight)
		fmt.Fprintf(writer, "#\tArtefactId\tRule\tAction\tReason\tError\n")
		for index, row := range rows {
			fmt.Fprintf(writer, "%d\t%s\t%s\t%s\t%s\t%s\n",
				index+1, row.Artifact, row.Rule, row.Action, row.Reason, row.Error)
		}
		writer.Flush()
	}

	if failed {
		exitWith(exitFailure)
	}
}

//skipGuidelines skips or reverts every rule on every target. The current
//state is read first, so that repeating a call is harmless. A failure is
//recorded in its row and the remaining rules are still processed.
func skipGuidelines(client guidelineClient, targets []*guidelineTarget, rules []string, reason string,
	version string, executionId string, skip bool) []*guidelineSkipRow {

	rows := []*guidelineSkipRow{}

	for _, target := range targets {

		targetExecutionId := executionId
		var current map[string]*cpiclient.DesignGuideline

		err := func() error {
			if targetExecutionId == "" {
				execution, err := ensureGuidelineExecution(client, target.ArtifactId, version)
				if err != nil {
					return err
				}
				targetExecutionId = execution.ExecutionId
			}
			_, guidelines, err := client.ReadDesignGuidelineExecutionResult(target.ArtifactId, version, targetExecutionId)
			if err != nil {
				return err
			}
			current = guidelinesById(guidelines)
			return nil
		}()

		for _, rule := range rules {
			row := &guidelineSkipRow{
				Artifact:    target.ArtifactId,
				Rule:        strings.TrimSpace(rule),
				ExecutionId: targetExecutionId,
				Reason:      reason,
			}

			if err != nil {
				row.Action = skipActionFailed
				row.Error = err.Error()
			} else {
				action, skipErr := fileGuidelineSkip(client, target.ArtifactId, version, targetExecutionId,
					row.Rule, reason, skip, current[row.Rule])
				row.Action = action
				if skipErr != nil {
					row.Error = skipErr.Error()
				}
			}

			rows = append(rows, row)
		}
	}

	return rows
}
