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
	"runtime/debug"
	"text/tabwriter"

	"github.com/Trifolium-project/landscaper/packages/cpiclient"
	"github.com/Trifolium-project/landscaper/packages/util"
	"github.com/spf13/cobra"
)

var targetEnv *string
var iflowList *[]string
var toDeploy *bool

// moveCmd represents the move command
var packageMoveCmd = &cobra.Command{
	Use:   "move",
	Short: "Transport whole package, or selected flows to target env",
	Long: `Transport whole package, or selected flows to target env`,
	Run: func(cmd *cobra.Command, args []string) {
		/*
			finished := make(chan bool)
			finished <- false

			bar := progressbar.DefaultBytes(
				-1,
				fmt.Sprintf("Transporting %s to %s...",*pkg, *targetEnv),
			)

			go packageMove(finished)

			for ! <- finished {
				bar.Add(1)
				time.Sleep(50 * time.Millisecond)
			}

		*/

		//showProgress()
		fmt.Printf("Transporting %s to %s...\n", *pkg, *targetEnv)
		packageMove()

	},
}

/*
func showProgress(){
	bar := progressbar.DefaultBytes(
		-1,
		fmt.Sprintf("Transporting %s to %s...",*pkg, *targetEnv),
	)

	for i := 0; i < 1000; i++ {
		bar.Add(1)
		time.Sleep(50 * time.Millisecond)
	}
}
*/
func init() {
	packageCmd.AddCommand(packageMoveCmd)

	// Here you will define your flags and configuration settings.
	targetEnv = packageMoveCmd.Flags().String("target-env", "", "Target environment")
	toDeploy = packageMoveCmd.Flags().BoolP("deploy", "d", false, "Indicate whether necessary to deploy changed artifacts in target environment")
	iflowList = packageMoveCmd.Flags().StringSliceP("iflow", "f", []string{}, "List of integration flows to")
	// Cobra supports Persistent Flags which will work for this command
	// and all subcommands, e.g.:
	// moveCmd.PersistentFlags().String("foo", "", "A help for foo")

	// Cobra supports local flags which will only run when this command
	// is called directly, e.g.:
	// moveCmd.Flags().BoolP("toggle", "t", false, "Help message for toggle")
}

func finish(finished chan bool) {
	finished <- true
}

func packageMove() {
	defer func() {
        if r := recover(); r != nil {
            fmt.Println("stacktrace from panic: \n" + string(debug.Stack()))
        }
    }()

	//defer finish(finished)

	if globalLandscape == nil {
		println("Global landscape is not instantiated")
		return
	}

	originalEnvironment := globalLandscape.OriginalEnvironment

	if *targetEnv == originalEnvironment.Id {
		log.Fatalln("Cannot import changes to original environment, stopping execution")
	}

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

	sourceArtifacts, err := originalEnvironment.System.Client.ReadIntegrationDesigntimeArtifacts(*pkg, fetchArtifactConfig)
	if err != nil {
		log.Fatalln(err)
	}

	//Filter out unnecessary artifacts, if iflowList is not empty
	if len(*iflowList) > 0 {
		var filteredSourceArtifacts []*cpiclient.IntegrationDesigntimeArtifact

		for _, sourceArtifacts := range sourceArtifacts {
			if util.Contains(*iflowList, sourceArtifacts.Id) {
				filteredSourceArtifacts = append(filteredSourceArtifacts, sourceArtifacts)
			}
		}
		sourceArtifacts = filteredSourceArtifacts
	}

	//Check that there is no artifact in draft state in source package
	draftIFlows := ""
	for _, sourceArtifact := range sourceArtifacts {

		if sourceArtifact.Version == "Active" {
			draftIFlows += sourceArtifact.Id + "|"
		}

	}

	if draftIFlows != "" {
		log.Fatalf("These artifacts in package %s are in Draft state: %s. Please save them as version.", *pkg, draftIFlows)
	}

	//Transport package
	tagretPackage, err := targetEnvironment.System.Client.ReadIntegrationPackage(targetPackageId)
	if err != nil {
		log.Println(err)
	} else {
		//println(tagretPackage.Id)
	}
	currentTargetArtifactVersions := make(map[string]string)
	if tagretPackage == nil {

		tagretPackage := &cpiclient.IntegrationPackage{
			Id:          targetPackageId,
			Name:        targetEnvironment.Suffix + " " + sourcePackage.Name,
			Description: sourcePackage.Description,
			ShortText:   sourcePackage.ShortText + "(environment - '" + targetEnvironment.Id + "')",
			Vendor:      sourcePackage.Vendor,
			Version:     sourcePackage.Version,

			Keywords: "",
		}

		err = targetEnvironment.System.Client.CreateIntegrationPackage(tagretPackage)
		if err != nil {
			log.Fatalln(err)
		}

	} else {
		targetArtifacts, err := targetEnvironment.System.Client.ReadIntegrationDesigntimeArtifacts(targetPackageId, fetchArtifactConfig)
		if err != nil {
			log.Fatalln(err)
		}

		for _, targetArtifact := range targetArtifacts {
			//Create map - artifact(base name) - version for future checks
			currentTargetArtifactVersions[targetArtifact.Id] = targetArtifact.Version
		}
	}

	writer := tabwriter.NewWriter(os.Stdout, 0, 8, 1, '\t', tabwriter.AlignRight)
	fmt.Fprintf(writer, "#\tArtefactId\tVersion\tPackage\tTransferred to %s\tDeployed\n", *targetEnv)

	//Transport artifacts
	for index, sourceArtifact := range sourceArtifacts {
		id := sourceArtifact.Id + targetEnvironment.Suffix

		transportArtifact := false
		artifactExistsInTarget := false
		if version, ok := currentTargetArtifactVersions[id]; ok {
			artifactExistsInTarget = true
			if version != sourceArtifact.Version {
				transportArtifact = true
			}
		} else {
			transportArtifact = true
		}

		if transportArtifact {

			parameters, err := globalLandscape.GetArtifactConfiguration(*targetEnv, sourceArtifact.PackageId, sourceArtifact.Id)

			id := sourceArtifact.Id + targetEnvironment.Suffix

			if artifactExistsInTarget {
				version := currentTargetArtifactVersions[id]

				targetEnvironment.System.Client.DeleteIntegrationDesigntimeArtifact(id, version)
				if err != nil {
					log.Fatalln(err)
				}
			}

			newArtifact, err := originalEnvironment.System.Client.DownloadIntegrationDesigntimeArtifact(sourceArtifact.Id, sourceArtifact.Version)
			if err != nil {
				log.Fatalln(err)
			}
			newArtifact.Name = sourceArtifact.Name + " " + targetEnvironment.Suffix
			newArtifact.PackageId = targetPackageId
			newArtifact.Id = id
			newArtifact.Description = sourceArtifact.Description
			newArtifact.Version = sourceArtifact.Version

			err = targetEnvironment.System.Client.UploadIntegrationDesigntimeArtifact(newArtifact)
			if err != nil {
				log.Fatalln(err)
			}

			for _, parameter := range parameters {
				sourceConf, err := sourceArtifact.GetConfiguration(parameter.Key)
				var conf  *cpiclient.Configuration
				if err != nil {
					conf = &cpiclient.Configuration{
						ParameterKey:   parameter.Key,
						ParameterValue: parameter.Value,
						DataType:       sourceConf.DataType,
					}
				} else {
					conf = &cpiclient.Configuration{
						ParameterKey:   parameter.Key,
						ParameterValue: parameter.Value,
						DataType:       parameter.Type,
					}
				}
				err = targetEnvironment.System.Client.UpdateIntegrationDesigntimeArtifactConfiguration(newArtifact.Id, newArtifact.Version, conf)
				if err != nil {
					log.Fatalln(err)
				}
			}

			if *toDeploy {
				err = targetEnvironment.System.Client.DeployIntegrationDesigntimeArtifact(newArtifact.Id, newArtifact.Version)
				if err != nil {
					log.Fatalln(err)
				}

			}

			fmt.Fprintf(writer, "%d\t%s\t%s\t%s\t%t\t%t\n", index+1, newArtifact.Id, newArtifact.Version, newArtifact.PackageId, true, *toDeploy)

			//fmt.Fprintf(writer, "%d\t%s\t%s\n", index, pkg.Id, pkg.Name)
		} else {
			fmt.Fprintf(writer, "%d\t%s\t%s\t%s\t%t\t%t\n", index+1, id, sourceArtifact.Version, targetPackageId, false, false)
		}
	}
	writer.Flush()
}

//TODO: Download backup package and iflows before making change
