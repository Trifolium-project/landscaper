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

var template *string
var writer *tabwriter.Writer
var toDeployUpgraded *bool

// createCmd represents the create command
var artifactUpgradeCmd = &cobra.Command{
	Use:   "upgrade",
	Short: "Upgrade artifact",
	Long:  `Upgrade artifact`,
	Run: func(cmd *cobra.Command, args []string) {
		artifactUpgrade()
	},
}

func init() {
	artifactCmd.AddCommand(artifactUpgradeCmd)

	iflowList = artifactUpgradeCmd.Flags().StringSliceP("iflow", "f", []string{}, "List of integration flows to upgrade")
	template = artifactUpgradeCmd.Flags().String("template", "", "Template iflow")
	toDeployUpgraded = artifactUpgradeCmd.Flags().Bool("deploy", false, "Indicate whether necessary to deploy changed artifacts")

	artifactUpgradeCmd.MarkFlagRequired("template")	
	// Here you will define your flags and configuration settings.

	// Cobra supports Persistent Flags which will work for this command
	// and all subcommands, e.g.:
	// createCmd.PersistentFlags().String("foo", "", "A help for foo")

	// Cobra supports local flags which will only run when this command
	// is called directly, e.g.:
	// createCmd.Flags().BoolP("toggle", "t", false, "Help message for toggle")
}

func artifactUpgrade() {

	//Perform intial checks
	if globalLandscape == nil {
		println("Global landscape is not instantiated")
		return
	}

	if *template == "" {
		println("Please specify template")
		return
	}

	//Get list of iflows by template
	artifactList := globalLandscape.GetArtifactsByTemplate(*template)

	//TODO: If iflows are not empty, Get intersection of this list and iflows

	writer = tabwriter.NewWriter(os.Stdout, 0, 8, 1, '\t', tabwriter.AlignRight)
	fmt.Fprintf(writer, "#\tArtefactId\tVersion\tPackage\tUpgraded\tDeployed\n")

	client := globalLandscape.OriginalEnvironment.System.Client
	sourceArtifact, err := client.ReadIntegrationDesigntimeArtifact(*template, "Active")
	if err != nil {
		log.Fatalln(err)
	}

	//Resulting list pass to the function, that moves artifacts (with version check, deploy logic and so on)
	for index, artifact := range artifactList {

		targetArtifact, err := client.ReadIntegrationDesigntimeArtifact(artifact.Id, "Active")
		if err != nil {
			log.Fatalln(err)
		}

		upgraded, err := upgradeArtifactVersion(sourceArtifact, targetArtifact)
		if err != nil {
			log.Fatalln(err)
		}

		//Deploy
		
		if *toDeployUpgraded && upgraded {
			err = client.DeployIntegrationDesigntimeArtifact(targetArtifact.Id, sourceArtifact.Version)
			if err != nil {
				log.Fatalln(err)
			}
		}
		fmt.Fprintf(writer, "%d\t%s\t%s\t%s\t%t\t%t\n", index+1, artifact.Id, sourceArtifact.Version, targetArtifact.PackageId, upgraded, *toDeploy && upgraded)

	}

	writer.Flush()

}

//Recreate target artifact from source
func upgradeArtifactVersion(sourceArtifact *cpiclient.IntegrationDesigntimeArtifact, targetArtifact *cpiclient.IntegrationDesigntimeArtifact) (bool, error) {

	//Check version
	//TODO: Ensure that version is fetched as "Active", when iflow is in draft state
	if sourceArtifact.Version == "Active" {
		log.Fatalf("Artifact %s is in Draft state. Please save it as version.", sourceArtifact.Id)
	}

	if sourceArtifact.Version == targetArtifact.Version {
		log.Default().Printf("%s is not upgraded - version is the same as in template %s (%s)", targetArtifact.Id, sourceArtifact.Id, targetArtifact.Version)
		return false, nil
	}

	//Save configurations
	//targetArtifact.Configurations, err = globalLandscape.OriginalEnvironment.System.Client.ReadIntegrationDesigntimeArtifactConfigurations(targetArtifact.Id, "Active")
	//if err != nil {
	//	log.Fatalln(err)
	//}

	//Delete target integration flow

	globalLandscape.OriginalEnvironment.System.Client.DeleteIntegrationDesigntimeArtifact(targetArtifact.Id, targetArtifact.Version)

	//Download source iflow

	newArtifact, err := globalLandscape.OriginalEnvironment.System.Client.DownloadIntegrationDesigntimeArtifact(sourceArtifact.Id, sourceArtifact.Version)
	if err != nil {
		log.Fatalln(err)
	}
	newArtifact.Name = targetArtifact.Name
	newArtifact.PackageId = targetArtifact.PackageId
	newArtifact.Id = targetArtifact.Id
	newArtifact.Description = targetArtifact.Description
	newArtifact.Receiver = targetArtifact.Receiver
	newArtifact.Sender = targetArtifact.Sender

	//Upgrade version from source
	err = globalLandscape.OriginalEnvironment.System.Client.UploadIntegrationDesigntimeArtifact(newArtifact)
	if err != nil {
		log.Fatalln(err)
	}

	//Update configurations
	for _, config := range targetArtifact.Configurations {
		conf := &cpiclient.Configuration{
			ParameterKey:   config.ParameterKey,
			ParameterValue: config.ParameterValue,
			DataType:       config.DataType,
		}

		err = globalLandscape.OriginalEnvironment.System.Client.UpdateIntegrationDesigntimeArtifactConfiguration(newArtifact.Id, newArtifact.Version, conf)
		if err != nil {
			log.Fatalln(err)
		}
	}

	return true, nil
}
