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

// undelpoyCmd represents the undelpoy command
var artifactUndelpoyCmd = &cobra.Command{
	Use:   "undeploy",
	Short: "A brief description of your command",
	Long: `A longer description that spans multiple lines and likely contains examples
and usage of using your command. For example:

Cobra is a CLI library for Go that empowers applications.
This application is a tool to generate the needed files
to quickly create a Cobra application.`,
	Run: func(cmd *cobra.Command, args []string) {
		artifactUndeploy()
	},
}

func init() {
	artifactCmd.AddCommand(artifactUndelpoyCmd)

	// Here you will define your flags and configuration settings.

	// Cobra supports Persistent Flags which will work for this command
	// and all subcommands, e.g.:
	// undelpoyCmd.PersistentFlags().String("foo", "", "A help for foo")

	// Cobra supports local flags which will only run when this command
	// is called directly, e.g.:
	// undelpoyCmd.Flags().BoolP("toggle", "t", false, "Help message for toggle")
}


func artifactUndeploy() {
	if globalLandscape == nil {
		println("Global landscape is not instantiated")
		return
	}

	system, err := globalLandscape.GetSystem4Environment(environment)
	if err != nil {
		log.Fatalln(err)
	}

	artfct, err := system.Client.ReadIntegrationRuntimeArtifact(*artifact)
	if err != nil {
		log.Fatalln(err)
	}

	err = system.Client.UndeployIntegrationRuntimeArtifact(*artifact)
	if err != nil {
		log.Fatalln(err)
	}


	writer := tabwriter.NewWriter(os.Stdout, 0, 8, 1, '\t', tabwriter.AlignRight)
	fmt.Fprintf(writer, "Undeploy started...\n\n")
	fmt.Fprintf(writer, "===Artifact metadata===\n\n")

	fmt.Fprintf(writer, "%s\t%s\n", "ID:", artfct.Id)
	fmt.Fprintf(writer, "%s\t%s\n", "Name:", artfct.Name)
	fmt.Fprintf(writer, "%s\t%s\n", "Version:", artfct.Version)
	fmt.Fprintf(writer, "%s\t%s\n", "Type:", artfct.Type)

	writer.Flush()

}