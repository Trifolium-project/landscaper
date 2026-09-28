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
	"time"

	"github.com/spf13/cobra"
)

var (
	guidelinesRunArtifacts *[]string
	guidelinesRunPackages  *[]string
	guidelinesRunAll       *bool
	guidelinesRunVersion   *string
	guidelinesRunWait      *bool
	guidelinesRunTimeout   *time.Duration
	guidelinesRunInterval  *time.Duration
	guidelinesRunFailOn    *string
	guidelinesRunOutput    *string
	guidelinesRunNoSkips   *bool
)

// artifactGuidelinesRunCmd represents the artifact guidelines run command
var artifactGuidelinesRunCmd = &cobra.Command{
	Use:   "run",
	Short: "Run the design guidelines on artifacts and report the result",
	Long: `Run the design guidelines on the current design time version of each selected
artifact and report the result of every rule.

Guideline skips declared under guidelineSkips in the landscape configuration
are applied to the new execution, unless --no-declared-skips is given.

--output json emits every rule, including compliant and not applicable ones.
The text output lists only the rules that are not compliant or skipped.`,
	Run: func(cmd *cobra.Command, args []string) {
		artifactGuidelinesRun()
	},
}

func init() {
	artifactGuidelinesCmd.AddCommand(artifactGuidelinesRunCmd)

	guidelinesRunArtifacts = artifactGuidelinesRunCmd.Flags().StringSlice("artifacts", []string{}, "Comma separated list of artifact ids, without environment suffix")
	guidelinesRunPackages = artifactGuidelinesRunCmd.Flags().StringSlice("packages", []string{}, "Comma separated list of package ids, without environment suffix")
	guidelinesRunAll = artifactGuidelinesRunCmd.Flags().Bool("all-declared", false, "Every artifact declared in the landscape configuration")
	guidelinesRunVersion = artifactGuidelinesRunCmd.Flags().String("version", guidelineVersionActive, "Artifact version to check, the tenant only holds the current one")
	guidelinesRunWait = artifactGuidelinesRunCmd.Flags().Bool("wait", false, "Wait until each execution is finished")
	guidelinesRunTimeout = artifactGuidelinesRunCmd.Flags().Duration("timeout", defaultDeployTimeout, "How long to wait for an execution")
	guidelinesRunInterval = artifactGuidelinesRunCmd.Flags().Duration("interval", defaultDeployInterval, "How often to ask the tenant while waiting")
	guidelinesRunFailOn = artifactGuidelinesRunCmd.Flags().String("fail-on", "low", "Lowest severity of a not compliant rule that exits with 7: low, medium, high or none")
	guidelinesRunOutput = artifactGuidelinesRunCmd.Flags().String("output", outputText, "Output format: text or json")
	guidelinesRunNoSkips = artifactGuidelinesRunCmd.Flags().Bool("no-declared-skips", false, "Do not apply the guideline skips of the landscape configuration")
}

func artifactGuidelinesRun() {

	if globalLandscape == nil {
		println("Global landscape is not instantiated")
		return
	}

	runGuidelineCommand(guidelineSelection{
		Artifacts:   *guidelinesRunArtifacts,
		Packages:    *guidelinesRunPackages,
		AllDeclared: *guidelinesRunAll,
	}, guidelineOptions{
		Execute:    true,
		Version:    *guidelinesRunVersion,
		Wait:       *guidelinesRunWait,
		Timeout:    *guidelinesRunTimeout,
		Interval:   *guidelinesRunInterval,
		ApplySkips: !*guidelinesRunNoSkips,
		FailOn:     *guidelinesRunFailOn,
	}, *guidelinesRunOutput, "guidelines-run")
}
