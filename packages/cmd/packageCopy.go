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
var packageCopyCmd = &cobra.Command{
	Use:   "copy",
	Short: "Package copy from discover to design tab",
	Long:  `Package copy from discover to design tab`,
	Run: func(cmd *cobra.Command, args []string) {
		packageCopy()
	},
}

func init() {
	packageCmd.AddCommand(packageCopyCmd)

	// Here you will define your flags and configuration settings.

	// Cobra supports Persistent Flags which will work for this command
	// and all subcommands, e.g.:
	// getCmd.PersistentFlags().String("foo", "", "A help for foo")

	// Cobra supports local flags which will only run when this command
	// is called directly, e.g.:
	// getCmd.Flags().BoolP("toggle", "t", false, "Help message for toggle")
}

func packageCopy() {
	if globalLandscape == nil {
		println("Global landscape is not instantiated")
		return
	}

	system, err := globalLandscape.GetSystem4Environment(environment)
	if err != nil {
		log.Fatalln(err)
	}

	//split := strings.Split(*artifact, ":")

	pkgObj, err := system.Client.CopyIntegrationPackageFromDiscover(*pkg)
	if err != nil {
		log.Fatalln(err)
	}

	writer := tabwriter.NewWriter(os.Stdout, 0, 8, 1, '\t', tabwriter.AlignRight)
	fmt.Fprintf(writer, "===Package metadata===\n\n")

	fmt.Fprintf(writer, "%s\t%s\n", "ID:", pkgObj.Id)
	fmt.Fprintf(writer, "%s\t%s\n", "Name:", pkgObj.Name)
	fmt.Fprintf(writer, "%s\t%s\n", "Version:", pkgObj.Version)
	fmt.Fprintf(writer, "%s\t%s\n", "ShortText:", pkgObj.ShortText)

	fmt.Fprintf(writer, "\n===Artifact list===\n\n")

	artifacts, err := system.Client.ReadIntegrationDesigntimeArtifacts(*pkg, false)
	if err != nil {
		log.Fatalln(err)
	}

	fmt.Fprintln(writer, "#\tArtefactId\tVersion\tName")

	for index, art := range artifacts {
		fmt.Fprintf(writer, "%d\t%s\t%s\t%s\n", index, art.Id, art.Version, art.Name)

	}


	writer.Flush()

}
