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
	"fmt"
	"log"
	"os"
	"sort"
	"text/tabwriter"

	"github.com/Trifolium-project/landscaper/packages/landscape"
	"github.com/spf13/cobra"
)

//Default location of the generated landscape file
const defaultGeneratedLandscapeFile = "conf/landscape-generated.yaml"

var (
	initPackages        *[]string
	initOutput          *string
	initInPlace         *bool
	initAllParameters   *bool
	initIncludeReadOnly *bool
	initSkipParameters  *[]string
)

// initCmd represents the init command
var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Gather the landscape declaration from the systems",
	Long: `Gather the landscape declaration from the systems.

Connects to every system of the landscape file, reads its packages, the artifacts
of every package and the configuration parameters of every artifact, and writes
the packages section of the landscape file.

Systems, environments and the original environment have to be declared in the
landscape file beforehand, see conf/landscape-minimal-example.yaml.

Environments, that are hosted on the same system, are distinguished by the suffix
of their package ids. Suffixed packages are grouped under the base package, so
that one package has one declaration with a configuration per environment.`,
	Run: func(cmd *cobra.Command, args []string) {
		landscapeInit()
	},
}

func init() {
	rootCmd.AddCommand(initCmd)

	initPackages = initCmd.Flags().StringSlice("packages", []string{}, "Comma separated list of package ids to gather, without environment suffix. All packages by default")
	initOutput = initCmd.Flags().String("output", defaultGeneratedLandscapeFile, "Path of the generated landscape file")
	initInPlace = initCmd.Flags().Bool("in-place", false, "Update the landscape file itself instead of writing a new one")
	initAllParameters = initCmd.Flags().Bool("all-parameters", false, "Write all parameters of every environment, instead of only those, that differ from the original environment")
	initIncludeReadOnly = initCmd.Flags().Bool("include-readonly", false, "Include packages, that are delivered by SAP and cannot be changed")
	initSkipParameters = initCmd.Flags().StringSlice("skip-parameters", landscape.DefaultSkipParameters, "Parameters, that are not written to the landscape file, because they cannot be changed. A trailing * matches a prefix. Empty value writes all parameters")
}

func landscapeInit() {

	if globalLandscape == nil {
		println("Global landscape is not instantiated")
		return
	}

	sourceFile := landscapeFilePath()

	targetFile := *initOutput
	if *initInPlace {
		targetFile = sourceFile
	}

	fmt.Printf("Gathering landscape %s...\n", globalLandscape.Name)

	packages, warnings, err := globalLandscape.Discover(landscape.DiscoverOptions{
		Packages:        *initPackages,
		IncludeReadOnly: *initIncludeReadOnly,
		AllParameters:   *initAllParameters,
		SkipParameters:  *initSkipParameters,
	})
	if err != nil {
		log.Fatalln(err)
	}

	for _, warning := range warnings {
		log.Println(warning)
	}

	err = globalLandscape.WritePackages(sourceFile, targetFile, packages)
	if err != nil {
		log.Fatalln(err)
	}

	printLandscapeInitSummary(packages)

	fmt.Printf("Landscape is written to %s\n", targetFile)
}

//Path of the landscape file, that was used to build the global landscape
func landscapeFilePath() string {

	if *landscapeFile != "" {
		return *landscapeFile
	}

	return defaultLandscapeFile
}

func printLandscapeInitSummary(packages map[string]*landscape.Package) {

	packageIds := []string{}
	for packageId := range packages {
		packageIds = append(packageIds, packageId)
	}
	sort.Strings(packageIds)

	writer := tabwriter.NewWriter(os.Stdout, 0, 8, 1, '\t', tabwriter.AlignRight)
	fmt.Fprintln(writer, "#\tPackageId\tArtifacts\tEnvironments\tParameters")

	index := 0
	for _, packageId := range packageIds {
		package_ := packages[packageId]

		environments := map[string]bool{}
		parameterCount := 0
		for _, artifact := range package_.Artifacts {
			for environmentId, configuration := range artifact.Configurations {
				environments[environmentId] = true
				parameterCount += len(configuration.Parameters)
			}
		}

		index++
		fmt.Fprintf(writer, "%d\t%s\t%d\t%d\t%d\n", index, package_.Id, len(package_.Artifacts), len(environments), parameterCount)
	}
	writer.Flush()
}
