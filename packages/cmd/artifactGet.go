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

// getCmd represents the get command
var artifactGetCmd = &cobra.Command{
	Use:   "get",
	Short: "Get artifact metadata and configuration",
	Long: `Get artifact metadata and configuration`,
	Run: func(cmd *cobra.Command, args []string) {
		artifactGet()
	},
}

func init() {
	artifactCmd.AddCommand(artifactGetCmd)

	// Here you will define your flags and configuration settings.

	// Cobra supports Persistent Flags which will work for this command
	// and all subcommands, e.g.:
	// getCmd.PersistentFlags().String("foo", "", "A help for foo")

	// Cobra supports local flags which will only run when this command
	// is called directly, e.g.:
	// getCmd.Flags().BoolP("toggle", "t", false, "Help message for toggle")
}

func artifactGet() {
	if globalLandscape == nil {
		println("Global landscape is not instantiated")
		return
	}

	system, err := globalLandscape.GetSystem4Environment(environment)
	if err != nil {
		log.Fatalln(err)
	}

	//split := strings.Split(*artifact, ":")

	artfct, err := system.Client.ReadIntegrationDesigntimeArtifact(*artifact, "Active")
	if err != nil {
		log.Fatalln(err)
	}

	writer := tabwriter.NewWriter(os.Stdout, 0, 8, 1, '\t', tabwriter.AlignRight)
	fmt.Fprintf(writer, "===Artifact metadata===\n\n")

	fmt.Fprintf(writer, "%s\t%s\n", "ID:", artfct.Id)
	fmt.Fprintf(writer, "%s\t%s\n", "Name:", artfct.Name)
	fmt.Fprintf(writer, "%s\t%s\n", "Version:", artfct.Version)
	fmt.Fprintf(writer, "%s\t%s\n", "Package:", artfct.PackageId)
	
	designtimeArtifact, err := system.Client.ReadIntegrationRuntimeArtifact(*artifact)
	if err != nil {
		//fmt.Println(err)

		fmt.Fprintf(writer, "%s\t%s\n", "Deploy status:", "Not deployed")

	} else {
		fmt.Fprintf(writer, "%s\t%s\n", "Deploy status:", designtimeArtifact.Status)
		fmt.Fprintf(writer, "%s\t%s\n", "Deployed version:", designtimeArtifact.Version)
	}

	conf, err := system.Client.ReadIntegrationDesigntimeArtifactConfigurations(*artifact, "Active")
	fmt.Fprintf(writer, "\n===Configuration===\n\n")

	fmt.Fprintf(writer, "Key\tValue\tType\n")

	for _, configuration := range conf {
		fmt.Fprintf(writer, "%s\t%s\t%s\n", configuration.ParameterKey, configuration.ParameterValue, configuration.DataType)
	}

	//fmt.Fprintf(writer, "%d\t%s\t%s\n", index, pkg.Id, pkg.Name)
	writer.Flush()

}