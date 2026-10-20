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

var onlyDeployed *bool

// listCmd represents the list command
var artifactListCmd = &cobra.Command{
	Use:   "list",
	Short: "List artifacts in package",
	Long: `List the integration flows and MCP servers of a package.

MCP servers are not available through the OData API. They are listed only when
the system has uiHost and uiCookie, a browser session of the Integration Suite
UI, see the README. Their deploy status is the one of the Integration Cell
runtime.`,
	Run: func(cmd *cobra.Command, args []string) {
		artifactList()
	},
}

func init() {
	artifactCmd.AddCommand(artifactListCmd)



	onlyDeployed = artifactListCmd.Flags().Bool("only-deployed", false, "Indicate whether necessary to list only deployed artifacts")
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

	//MCP servers are not in the OData API, they are read through the UI
	//session of the system. Without one an empty package may well hold some.
	var mcpServers []*cpiclient.MCPServer
	var mcpDeployed []*cpiclient.IntegrationCellArtifact
	var mcpErr error
	if system.Client.HasUISession() {
		mcpServers, mcpErr = system.Client.ReadMCPServers(*pkg)
		if mcpErr == nil && len(mcpServers) > 0 {
			mcpDeployed, mcpErr = system.Client.ReadIntegrationCellArtifacts(mcpRuntimeLocation)
		}
	} else if len(artifacts) == 0 {
		fmt.Fprintf(os.Stderr, "Package %s has no integration flows. %s\n", *pkg, cpiclient.ErrNoUISession)
	}

	writer := tabwriter.NewWriter(os.Stdout, 0, 8, 1, '\t', tabwriter.AlignRight)
	fmt.Fprintln(writer, "#\tArtefactId\tVersion\tPackage\tDeploy Status\tDeployed Version\tType")
	index := 0
	for _, art := range artifacts {
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
		if !(*onlyDeployed && status == "Not deployed") {
			index++
			fmt.Fprintf(writer, "%d\t%s\t%s\t%s\t%s\t%s\t%s\n", index, art.Id, art.Version, art.PackageId, status,deployedVersion, "Integration Flow")
		}
		
		//fmt.Fprintf(writer, "%d\t%s\t%s\n", index, pkg.Id, pkg.Name)
	}
	//The status of an MCP server comes from the Integration Cell runtime
	for _, server := range mcpServers {
		status := "Not deployed"
		deployedVersion := "-"
		if deployed := findMCPServerArtifact(mcpDeployed, server.Id); deployed != nil {
			status = deployed.SemanticState
			deployedVersion = deployed.Version
		}
		if !(*onlyDeployed && status == "Not deployed") {
			index++
			fmt.Fprintf(writer, "%d\t%s\t%s\t%s\t%s\t%s\t%s\n", index, server.Id, server.Version, server.PackageId, status, deployedVersion, "MCP Server")
		}
	}
	writer.Flush()

	if mcpErr != nil {
		log.Fatalln(mcpErr)
	}

}
