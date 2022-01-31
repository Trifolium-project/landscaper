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

var targetEnv *string
var toDeploy *bool

// moveCmd represents the move command
var packageMoveCmd = &cobra.Command{
	Use:   "move",
	Short: "A brief description of your command",
	Long: `A longer description that spans multiple lines and likely contains examples
and usage of using your command. For example:

Cobra is a CLI library for Go that empowers applications.
This application is a tool to generate the needed files
to quickly create a Cobra application.`,
	Run: func(cmd *cobra.Command, args []string) {
		packageMove()
	},
}

func init() {
	packageCmd.AddCommand(packageMoveCmd)

	// Here you will define your flags and configuration settings.
	targetEnv = packageMoveCmd.Flags().String("target-env", "", "Target environment")
	toDeploy = packageMoveCmd.Flags().BoolP("deploy", "d", false , "Indicate whether necessary to deploy changed artifacts in target environment")
	// Cobra supports Persistent Flags which will work for this command
	// and all subcommands, e.g.:
	// moveCmd.PersistentFlags().String("foo", "", "A help for foo")

	// Cobra supports local flags which will only run when this command
	// is called directly, e.g.:
	// moveCmd.Flags().BoolP("toggle", "t", false, "Help message for toggle")
}

func packageMove() {

	if globalLandscape == nil {
		println("Global landscape is not instantiated")
		return
	}

	originalEnvironment := globalLandscape.OriginalEnvironment

	targetEnvironment, err := globalLandscape.GetEnvironment(*targetEnv)
	if err != nil {
		log.Fatalln(err)
	}

	targetPackageId := *pkg + targetEnvironment.Suffix

	

	sourcePackage, err := originalEnvironment.System.Client.ReadIntegrationPackage(*pkg)
	if err != nil {
		log.Fatalln(err)
	}

	fetchArtifactConfig := true

	artifacts, err := originalEnvironment.System.Client.ReadIntegrationDesigntimeArtifacts(*pkg, fetchArtifactConfig)
	if err != nil {
		log.Fatalln(err)
	}

	

	tagretPackage, err := targetEnvironment.System.Client.ReadIntegrationPackage(targetPackageId)
	if err != nil {
		log.Println(err)
	} else {
		println(tagretPackage.Id)
	}

	if tagretPackage == nil {

		tagretPackage := &cpiclient.IntegrationPackage{
			Id:          targetPackageId,
			Name:        targetEnvironment.Suffix + " " + sourcePackage.Name,
			Description: sourcePackage.Description,
			ShortText:   sourcePackage.ShortText + "(environment - '" + targetEnvironment.Suffix + "')",

			Keywords:    "",
		}

		err = targetEnvironment.System.Client.CreateIntegrationPackage(tagretPackage)
		if err != nil {
			log.Fatalln(err)
		}

	}

	writer := tabwriter.NewWriter(os.Stdout, 0, 8, 1, '\t', tabwriter.AlignRight)
	fmt.Fprintln(writer, "#\tArtefactId\tVersion\tPackage")

	for index, art := range artifacts {

		newArtifact, err := originalEnvironment.System.Client.DownloadIntegrationDesigntimeArtifact(art.Id, art.Version)
		if err != nil {
			log.Fatalln(err)
		}

		newArtifact.PackageId = targetPackageId
		newArtifact.Id = art.Id + targetEnvironment.Suffix
		newArtifact.Description = art.Description + " " + targetEnvironment.Suffix
		
		err = targetEnvironment.System.Client.UploadIntegrationDesigntimeArtifact(newArtifact)
		if err != nil {
			log.Fatalln(err)
		}

		if *toDeploy {
			err = targetEnvironment.System.Client.DeployIntegrationDesigntimeArtifact(newArtifact.Id, newArtifact.Version)
			if err != nil {
				log.Fatalln(err)
			}
		}

		fmt.Fprintf(writer, "%d\t%s\t%s\t%s\n", index, art.Id, art.Version, art.PackageId)
		//fmt.Fprintf(writer, "%d\t%s\t%s\n", index, pkg.Id, pkg.Name)
	}
	writer.Flush()
}
