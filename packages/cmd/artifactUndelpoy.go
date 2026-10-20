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

	"github.com/Trifolium-project/landscaper/packages/cpiclient"
	"github.com/spf13/cobra"
)

// undelpoyCmd represents the undelpoy command
var artifactUndelpoyCmd = &cobra.Command{
	Use:   "undeploy",
	Short: "Undeploy artifact from runtime",
	Long: `Undeploy artifact from runtime`,
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
		//The v1 runtime API does not list MCP servers, the Integration Cell does
		if system.Client.HasUISession() {
			mcpServerUndeploy(system.Client, err)
			return
		}
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
//mcpServerUndeploy is artifactUndeploy for an MCP server. notFound is the
//answer of the v1 runtime API, reported when the Integration Cell does not
//know the id either.
func mcpServerUndeploy(client *cpiclient.CPIClient, notFound error) {

	deployed, err := deployedMCPServerArtifact(client, *artifact)
	if err != nil {
		log.Fatalln(err)
	}
	if deployed == nil {
		log.Fatalln(notFound)
	}

	if err := client.UndeployIntegrationCellArtifact(deployed); err != nil {
		log.Fatalln(err)
	}

	writer := tabwriter.NewWriter(os.Stdout, 0, 8, 1, '\t', tabwriter.AlignRight)
	fmt.Fprintf(writer, "Undeploy started...\n\n")
	fmt.Fprintf(writer, "===Artifact metadata===\n\n")

	fmt.Fprintf(writer, "%s\t%s\n", "ID:", deployed.SymbolicName)
	fmt.Fprintf(writer, "%s\t%s\n", "Name:", deployed.Name)
	fmt.Fprintf(writer, "%s\t%s\n", "Version:", deployed.Version)
	fmt.Fprintf(writer, "%s\t%s\n", "Type:", "MCP Server")
	writer.Flush()
}
