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
	"text/tabwriter"

	"github.com/spf13/cobra"
)

// listCmd represents the list command
var artifactListCmd = &cobra.Command{
	Use:   "list",
	Short: "List artifacts in package",
	Long: `List artifacts in package`,
	Run: func(cmd *cobra.Command, args []string) {
		artifactList()
	},
}

func init() {
	artifactCmd.AddCommand(artifactListCmd)

	// Here you will define your flags and configuration settings.

	// Cobra supports Persistent Flags which will work for this command
	// and all subcommands, e.g.:
	// listCmd.PersistentFlags().String("foo", "", "A help for foo")

	// Cobra supports local flags which will only run when this command
	// is called directly, e.g.:
	// listCmd.Flags().BoolP("toggle", "t", false, "Help message for toggle")
}

func artifactList() {

	if globalLandscape == nil {
		println("Global landscape is not instantiated")
		return
	}

	system, err := globalLandscape.GetSystem4Environment(environment)
	if err != nil {
		log.Fatalln(err)
	}

	artifacts, err := system.Client.ReadIntegrationDesigntimeArtifacts(*pkg, false)
	if err != nil {
		log.Fatalln(err)
	}

	writer := tabwriter.NewWriter(os.Stdout, 0, 8, 1, '\t', tabwriter.AlignRight)
	fmt.Fprintln(writer, "#\tArtefactId\tVersion\tPackage\tDeploy Status\tDeployed Version")

	for index, art := range artifacts {
		status := "unknown"
		deployedVersion := "-"
		designtimeArtifact, err := system.Client.ReadIntegrationRuntimeArtifact(art.Id)
		if err != nil {
			//fmt.Println(err)
			status = "Not deployed"
			
		} else {
			status = designtimeArtifact.Status
			deployedVersion = designtimeArtifact.Version
		}
		fmt.Fprintf(writer, "%d\t%s\t%s\t%s\t%s\t%s\n", index, art.Id, art.Version, art.PackageId, status,deployedVersion)
		//fmt.Fprintf(writer, "%d\t%s\t%s\n", index, pkg.Id, pkg.Name)
	}
	writer.Flush()
//

}
