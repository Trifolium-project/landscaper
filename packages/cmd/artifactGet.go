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
	"encoding/json"
	"fmt"
	"log"
	"os"
	"text/tabwriter"
	"time"

	"github.com/Trifolium-project/landscaper/packages/cpiclient"
	"github.com/spf13/cobra"
)

//Output formats of "artifact get"
const (
	outputText = "text"
	outputJSON = "json"
)

var (
	getOutput   *string
	getWait     *bool
	getTimeout  *time.Duration
	getInterval *time.Duration
)

//artifactReport is the JSON document "artifact get --output json" emits
type artifactReport struct {
	Id            string                 `json:"id"`
	Name          string                 `json:"name"`
	Version       string                 `json:"version"`
	Package       string                 `json:"package"`
	Runtime       *runtimeReport         `json:"runtime"`
	Configuration []*configurationReport `json:"configuration"`
}

type runtimeReport struct {
	Status     string                           `json:"status"`
	Version    string                           `json:"version"`
	DeployedOn string                           `json:"deployedOn"`
	DeployedBy string                           `json:"deployedBy"`
	Error      *cpiclient.RuntimeErrorInformation `json:"error"`
	//ErrorText is the flattened form, so a caller does not have to walk the tree
	ErrorText string `json:"errorText,omitempty"`
}

type configurationReport struct {
	Key   string `json:"key"`
	Value string `json:"value"`
	Type  string `json:"type"`
}

// getCmd represents the get command
var artifactGetCmd = &cobra.Command{
	Use:   "get",
	Short: "Get artifact metadata and configuration",
	Long: `Get artifact metadata, the deployment result and configuration.

When the tenant reports a failed deployment the error information it returns is
printed under "Deploy error:".

--output json emits the same information as one JSON document, and --wait polls
until the deployment settles, so that an automated caller can act on the result:

  0  the artifact is deployed and STARTED
  2  the deployment failed, the tenant reported ERROR
  3  still STARTING when the wait timed out
  4  the artifact is not deployed
  1  anything else went wrong`,
	Run: func(cmd *cobra.Command, args []string) {
		artifactGet()
	},
}

func init() {
	artifactCmd.AddCommand(artifactGetCmd)

	getOutput = artifactGetCmd.Flags().String("output", outputText, "Output format: text or json")
	getWait = artifactGetCmd.Flags().Bool("wait", false, "Wait until the deployment is finished")
	getTimeout = artifactGetCmd.Flags().Duration("timeout", defaultDeployTimeout, "How long to wait for the deployment")
	getInterval = artifactGetCmd.Flags().Duration("interval", defaultDeployInterval, "How often to ask the tenant while waiting")
}

func artifactGet() {
	if globalLandscape == nil {
		println("Global landscape is not instantiated")
		return
	}

	if *getOutput != outputText && *getOutput != outputJSON {
		log.Fatalf("Unknown output format %q, use %s or %s", *getOutput, outputText, outputJSON)
	}

	system, err := globalLandscape.GetSystem4Environment(environment)
	if err != nil {
		log.Fatalln(err)
	}

	//*artifact already carries the environment suffix, applied in root.go, so
	//the runtime id is the same id the upload used
	artfct, err := system.Client.ReadIntegrationDesigntimeArtifact(*artifact, "Active")
	if err != nil {
		log.Fatalln(err)
	}

	status := lookUpDeployStatus(system.Client, *artifact, artfct.Version)

	configurations, err := system.Client.ReadIntegrationDesigntimeArtifactConfigurations(*artifact, "Active")
	if err != nil {
		log.Println(err)
	}

	if *getOutput == outputJSON {
		printArtifactJSON(artfct, status, configurations)
	} else {
		printArtifactText(artfct, status, configurations)
	}

	exitWith(status.ExitCode())
}

//lookUpDeployStatus reads the runtime state once, or polls it when --wait was given
func lookUpDeployStatus(client runtimeReader, artifactId string, designtimeVersion string) *deployStatus {

	if getWait != nil && *getWait {
		return waitForDeployment(client, artifactId, designtimeVersion, *getTimeout, *getInterval)
	}

	return readDeployStatus(client, artifactId)
}

//printArtifactText keeps the layout the command has always printed, and adds
//the error block underneath when the tenant reports a failure
func printArtifactText(artfct *cpiclient.IntegrationDesigntimeArtifact, status *deployStatus,
	configurations []*cpiclient.Configuration) {

	writer := tabwriter.NewWriter(os.Stdout, 0, 8, 1, '\t', tabwriter.AlignRight)
	fmt.Fprintf(writer, "===Artifact metadata===\n\n")

	fmt.Fprintf(writer, "%s\t%s\n", "ID:", artfct.Id)
	fmt.Fprintf(writer, "%s\t%s\n", "Name:", artfct.Name)
	fmt.Fprintf(writer, "%s\t%s\n", "Version:", artfct.Version)
	fmt.Fprintf(writer, "%s\t%s\n", "Package:", artfct.PackageId)

	if !status.Deployed {
		fmt.Fprintf(writer, "%s\t%s\n", "Deploy status:", "Not deployed")
	} else {
		fmt.Fprintf(writer, "%s\t%s\n", "Deploy status:", status.Summary())
		fmt.Fprintf(writer, "%s\t%s\n", "Deployed version:", status.Version)
	}

	fmt.Fprintf(writer, "\n===Configuration===\n\n")
	fmt.Fprintf(writer, "Key\tValue\tType\n")

	for _, configuration := range configurations {
		fmt.Fprintf(writer, "%s\t%s\t%s\n", configuration.ParameterKey, configuration.ParameterValue, configuration.DataType)
	}

	writer.Flush()

	printDeployError(status)
}

//printArtifactJSON emits the whole record as one document
func printArtifactJSON(artfct *cpiclient.IntegrationDesigntimeArtifact, status *deployStatus,
	configurations []*cpiclient.Configuration) {

	report := &artifactReport{
		Id:            artfct.Id,
		Name:          artfct.Name,
		Version:       artfct.Version,
		Package:       artfct.PackageId,
		Configuration: []*configurationReport{},
	}

	if status.Deployed {
		report.Runtime = &runtimeReport{
			Status:     status.Status,
			Version:    status.Version,
			DeployedOn: status.DeployedOn,
			DeployedBy: status.DeployedBy,
			Error:      status.Error,
			ErrorText:  status.ErrorText,
		}
	}

	for _, configuration := range configurations {
		report.Configuration = append(report.Configuration, &configurationReport{
			Key:   configuration.ParameterKey,
			Value: configuration.ParameterValue,
			Type:  configuration.DataType,
		})
	}

	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		log.Fatalln(err)
	}

	fmt.Println(string(encoded))
}
