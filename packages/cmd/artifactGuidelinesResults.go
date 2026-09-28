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
	"log"

	"github.com/spf13/cobra"
)

var (
	guidelinesResultsArtifacts *[]string
	guidelinesResultsPackages  *[]string
	guidelinesResultsAll       *bool
	guidelinesResultsVersion   *string
	guidelinesResultsExecution *string
	guidelinesResultsFailOn    *string
	guidelinesResultsOutput    *string
)

// artifactGuidelinesResultsCmd represents the artifact guidelines results command
var artifactGuidelinesResultsCmd = &cobra.Command{
	Use:   "results",
	Short: "Report the latest design guideline results of artifacts",
	Long: `Report the result of the latest design guideline execution of each selected
artifact, without running the guidelines again. The tenant keeps only the
latest execution of an artifact.

An artifact that was never checked is reported as not-executed.`,
	Run: func(cmd *cobra.Command, args []string) {
		artifactGuidelinesResults()
	},
}

func init() {
	artifactGuidelinesCmd.AddCommand(artifactGuidelinesResultsCmd)

	guidelinesResultsArtifacts = artifactGuidelinesResultsCmd.Flags().StringSlice("artifacts", []string{}, "Comma separated list of artifact ids, without environment suffix")
	guidelinesResultsPackages = artifactGuidelinesResultsCmd.Flags().StringSlice("packages", []string{}, "Comma separated list of package ids, without environment suffix")
	guidelinesResultsAll = artifactGuidelinesResultsCmd.Flags().Bool("all-declared", false, "Every artifact declared in the landscape configuration")
	guidelinesResultsVersion = artifactGuidelinesResultsCmd.Flags().String("version", guidelineVersionActive, "Artifact version, the tenant only holds the current one")
	guidelinesResultsExecution = artifactGuidelinesResultsCmd.Flags().String("execution", "", "Execution id to read instead of the latest one, needs a single artifact")
	guidelinesResultsFailOn = artifactGuidelinesResultsCmd.Flags().String("fail-on", "low", "Lowest severity of a not compliant rule that exits with 7: low, medium, high or none")
	guidelinesResultsOutput = artifactGuidelinesResultsCmd.Flags().String("output", outputText, "Output format: text or json")
}

func artifactGuidelinesResults() {

	if globalLandscape == nil {
		println("Global landscape is not instantiated")
		return
	}

	//An execution id belongs to one artifact
	if *guidelinesResultsExecution != "" && len(*guidelinesResultsArtifacts) != 1 {
		log.Fatalln("--execution needs exactly one artifact in --artifacts")
	}

	runGuidelineCommand(guidelineSelection{
		Artifacts:   *guidelinesResultsArtifacts,
		Packages:    *guidelinesResultsPackages,
		AllDeclared: *guidelinesResultsAll,
	}, guidelineOptions{
		Version:     *guidelinesResultsVersion,
		ExecutionId: *guidelinesResultsExecution,
		FailOn:      *guidelinesResultsFailOn,
	}, *guidelinesResultsOutput, "guidelines-results")
}
