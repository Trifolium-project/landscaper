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
	"strings"
	"text/tabwriter"

	"github.com/Trifolium-project/landscaper/packages/cpiclient"
	"github.com/spf13/cobra"
)

var configurations *[]string

// updateCmd represents the update command
var configUpdateCmd = &cobra.Command{
	Use:   "update",
	Short: "Update config(not implemented)",
	Long:  `Read config(not implemented)`,
	Run: func(cmd *cobra.Command, args []string) {
		configUpdate()
	},
}

func init() {
	configCmd.AddCommand(configUpdateCmd)


	configurations = configUpdateCmd.Flags().StringSliceP("config", "f", []string{}, "List of configuration key:value pairs")
	
	// Here you will define your flags and configuration settings.

	// Cobra supports Persistent Flags which will work for this command
	// and all subcommands, e.g.:
	// updateCmd.PersistentFlags().String("foo", "", "A help for foo")

	// Cobra supports local flags which will only run when this command
	// is called directly, e.g.:
	// updateCmd.Flags().BoolP("toggle", "t", false, "Help message for toggle")
}

func configUpdate() {

	if globalLandscape == nil {
		println("Global landscape is not instantiated")
		return
	}

	system, err := globalLandscape.GetSystem4Environment(environment)
	if err != nil {
		log.Fatalln(err)
	}
	
	/*
	TODO: Add suffix to artefact ID
	environmentObject, _ := globalLandscape.GetEnvironment(*environment)
	aftifactId := ""

	if environmentObject.Suffix == "" {
		aftifactId = *artifact
	} else {
		aftifactId = *artifact + environmentObject.Suffix
	}
	*/

	//Check if this artifact exists, and print it's details
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

	//Get and print current config
	conf, err := system.Client.ReadIntegrationDesigntimeArtifactConfigurations(*artifact, "Active")
	fmt.Fprintf(writer, "\n===Old Configuration===\n\n")

	fmt.Fprintf(writer, "Key\tValue\tType\n")

	for _, oldConfiguration := range conf {
		fmt.Fprintf(writer, "%s\t%s\t%s\n", oldConfiguration.ParameterKey, 
											oldConfiguration.ParameterValue, 
											oldConfiguration.DataType)
	}

	var newConfigurations []*cpiclient.Configuration
	//Check and prepare new configurations
	for _, newConfiguration := range *configurations {

		confTuple := strings.SplitN(newConfiguration, ":", 2)

		if len(confTuple) != 2 {
			log.Fatalf("error while parsing configuration %s", newConfiguration)
		}

		
		dataType, err := getConfigurationType(confTuple[0] ,conf)
		if err != nil {
			log.Fatalln(err)
		}

		newConf := &cpiclient.Configuration{
			ParameterKey:   confTuple[0],
			ParameterValue: confTuple[1],
			DataType:       dataType,
		}

		newConfigurations = append(newConfigurations, newConf)

		
	}

	//Check passed, apply configurations

	for _, newConfiguration := range newConfigurations {
		err = system.Client.UpdateIntegrationDesigntimeArtifactConfiguration(*artifact, "Active", newConfiguration)
	}

	//Read configuration after change
	conf, err = system.Client.ReadIntegrationDesigntimeArtifactConfigurations(*artifact, "Active")
	fmt.Fprintf(writer, "\n===New Configuration===\n\n")

	fmt.Fprintf(writer, "Key\tValue\tType\n")

	for _, configuration := range conf {
		fmt.Fprintf(writer, "%s\t%s\t%s\n", configuration.ParameterKey, 
											configuration.ParameterValue, 
											configuration.DataType)
	}


	writer.Flush()

}

//Get configuration type(xsd:string, xsd:boolean, custom:schedule etc.)
func getConfigurationType(configurationKey string, configurations []*cpiclient.Configuration) (string, error) {
	
	for _, configuration := range configurations{
		if configuration.ParameterKey == configurationKey {
			return configuration.DataType, nil
		}
	}
	return "", fmt.Errorf("configuration key %s is not found", configurationKey)
}