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
	"net/http"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/Trifolium-project/landscaper/packages/cpiclient"
	"github.com/spf13/cobra"
)

//Exit codes of "package copy", next to exitDeployed and exitFailure
const (
	exitPackageExists        = 5
	exitPackageNotInDiscover = 6
)

//Status of a copy in the report
const (
	copyStatusCopied   = "copied"
	copyStatusExists   = "exists"
	copyStatusNotFound = "not-found"
	copyStatusFailed   = "failed"
)

//Values of --import-mode and what the API expects
var packageImportModes = map[string]string{
	"overwrite":       cpiclient.ImportModeOverwrite,
	"overwrite-merge": cpiclient.ImportModeOverwriteMerge,
	"create-copy":     cpiclient.ImportModeCreateCopy,
}

var (
	copyId         *string
	copyImportMode *string
	copySuffix     *string
	copyOutput     *string
)

//packageCopyReport is the JSON document of "package copy"
type packageCopyReport struct {
	//Discover id that was asked for
	Source string `json:"source"`
	//Design id that was created, differs from Source with create-copy
	Id         string                  `json:"id"`
	Name       string                  `json:"name"`
	Mode       string                  `json:"mode"`
	Vendor     string                  `json:"vendor"`
	Version    string                  `json:"version"`
	ImportMode string                  `json:"importMode"`
	Status     string                  `json:"status"`
	Error      string                  `json:"error,omitempty"`
	Artifacts  []*copiedArtifactReport `json:"artifacts"`
}

type copiedArtifactReport struct {
	Id      string `json:"id"`
	Version string `json:"version"`
	Type    string `json:"type"`
}

// packageCopyCmd represents the package copy command
var packageCopyCmd = &cobra.Command{
	Use:   "copy",
	Short: "Copy a package from Discover to Design",
	Long: `Copy an integration package from the Discover section (SAP Business
Accelerator Hub content) into the Design section of the environment's tenant,
and report the package created together with its artifacts.

--id takes the technical name of the package as the Hub shows it. It is used as
it is, the environment suffix is never appended. The global --pkg is accepted
as an alias.

A package that is already in Design is left alone unless --import-mode says
what to do with it:

  overwrite        replace the package in Design
  overwrite-merge  replace it, keeping the configuration of its artifacts
  create-copy      create another copy, --suffix is added as ".<suffix>" to
                   the package id and the artifact ids

Exit codes:

  0  copied
  5  the package is already in Design and no --import-mode was given
  6  the package is not in Discover
  1  anything else went wrong`,
	Run: func(cmd *cobra.Command, args []string) {
		packageCopy()
	},
}

func init() {
	packageCmd.AddCommand(packageCopyCmd)

	copyId = packageCopyCmd.Flags().String("id", "", "Technical name of the package in Discover, as on the SAP Business Accelerator Hub")
	copyImportMode = packageCopyCmd.Flags().String("import-mode", "", "What to do when the package is already in Design: overwrite, overwrite-merge or create-copy")
	copySuffix = packageCopyCmd.Flags().String("suffix", "", "Suffix of the new copy, required with --import-mode create-copy")
	copyOutput = packageCopyCmd.Flags().String("output", outputText, "Output format: text or json")
}

func packageCopy() {
	if globalLandscape == nil {
		println("Global landscape is not instantiated")
		return
	}

	environment_, err := globalLandscape.GetEnvironment(*environment)
	if err != nil {
		log.Fatalln(err)
	}

	sourceId := *copyId
	if sourceId == "" {
		sourceId = rawPackageFlag(environment_)
	}
	if sourceId == "" {
		log.Fatalln("Pass the technical name of the package with --id")
	}

	importMode, err := validateCopyFlags(*copyImportMode, *copySuffix, *copyOutput)
	if err != nil {
		log.Fatalln(err)
	}

	report := copyPackage(environment_.System.Client, sourceId, importMode, *copySuffix)

	auditItem(map[string]interface{}{
		"operation":   "package-copy",
		"source":      report.Source,
		"package":     report.Id,
		"import_mode": report.ImportMode,
		"status":      report.Status,
		"artifacts":   len(report.Artifacts),
		"error":       report.Error,
	})

	if *copyOutput == outputJSON {
		encoded, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			log.Fatalln(err)
		}
		fmt.Println(string(encoded))
	} else {
		printPackageCopyText(report)
	}

	exitWith(packageCopyExitCode(report.Status))
}

//validateCopyFlags checks the flags and returns the import mode the API expects
func validateCopyFlags(importMode string, suffix string, output string) (string, error) {

	if output != outputText && output != outputJSON {
		return "", fmt.Errorf("Unknown output format %q, use %s or %s", output, outputText, outputJSON)
	}

	if importMode == "" {
		if suffix != "" {
			return "", fmt.Errorf("--suffix is only used with --import-mode create-copy")
		}
		return "", nil
	}

	apiMode, found := packageImportModes[strings.ToLower(importMode)]
	if !found {
		return "", fmt.Errorf("Unknown --import-mode %q, use overwrite, overwrite-merge or create-copy", importMode)
	}
	if apiMode == cpiclient.ImportModeCreateCopy && suffix == "" {
		return "", fmt.Errorf("--import-mode create-copy needs --suffix")
	}
	if apiMode != cpiclient.ImportModeCreateCopy && suffix != "" {
		return "", fmt.Errorf("--suffix is only used with --import-mode create-copy")
	}

	return apiMode, nil
}

//packageCopier is the part of CPIClient the copy needs
type packageCopier interface {
	CopyIntegrationPackage(DiscoverPackageId string, importMode string, suffix string) (*cpiclient.IntegrationPackage, error)
	ReadPackageDesigntimeArtifacts(PackageId string) ([]*cpiclient.PackageArtifact, error)
}

//copyPackage copies and reads back what was created. The tenant decides whether
//the package exists: it answers 409 without an import mode, and 404 for an id
//Discover does not know, in both cases without writing anything.
func copyPackage(client packageCopier, sourceId string, importMode string, suffix string) *packageCopyReport {

	report := &packageCopyReport{
		Source:     sourceId,
		Id:         sourceId,
		ImportMode: importMode,
		Artifacts:  []*copiedArtifactReport{},
	}

	created, err := client.CopyIntegrationPackage(sourceId, importMode, suffix)
	switch {
	case cpiclient.HasStatus(err, http.StatusConflict):
		report.Status = copyStatusExists
		report.Error = err.Error()
		return report
	case cpiclient.HasStatus(err, http.StatusNotFound):
		report.Status = copyStatusNotFound
		report.Error = err.Error()
		return report
	case err != nil:
		report.Status = copyStatusFailed
		report.Error = err.Error()
		return report
	}

	report.Status = copyStatusCopied
	if created.Id != "" {
		report.Id = created.Id
	}
	report.Name = created.Name
	report.Mode = created.Mode
	report.Vendor = created.Vendor
	report.Version = created.Version

	//The package is copied at this point, a failing read only costs the list
	artifacts, err := client.ReadPackageDesigntimeArtifacts(report.Id)
	if err != nil {
		report.Error = fmt.Sprintf("copied, but the artifacts cannot be read: %s", err)
		return report
	}
	for _, artifact_ := range artifacts {
		report.Artifacts = append(report.Artifacts, &copiedArtifactReport{
			Id:      artifact_.Id,
			Version: artifact_.Version,
			Type:    artifact_.Type,
		})
	}

	return report
}

func packageCopyExitCode(status string) int {
	switch status {
	case copyStatusCopied:
		return exitDeployed
	case copyStatusExists:
		return exitPackageExists
	case copyStatusNotFound:
		return exitPackageNotInDiscover
	default:
		return exitFailure
	}
}

func printPackageCopyText(report *packageCopyReport) {

	if report.Status != copyStatusCopied {
		log.Printf("Package %s not copied (%s): %s", report.Source, report.Status, report.Error)
		return
	}

	writer := tabwriter.NewWriter(os.Stdout, 0, 8, 1, '\t', tabwriter.AlignRight)
	fmt.Fprintf(writer, "===Package metadata===\n\n")
	fmt.Fprintf(writer, "%s\t%s\n", "ID:", report.Id)
	fmt.Fprintf(writer, "%s\t%s\n", "Name:", report.Name)
	fmt.Fprintf(writer, "%s\t%s\n", "Version:", report.Version)
	fmt.Fprintf(writer, "%s\t%s\n", "Mode:", report.Mode)
	fmt.Fprintf(writer, "%s\t%s\n", "Vendor:", report.Vendor)

	fmt.Fprintf(writer, "\n===Artifact list===\n\n")
	fmt.Fprintln(writer, "#\tArtefactId\tVersion\tType")
	for index, artifact_ := range report.Artifacts {
		fmt.Fprintf(writer, "%d\t%s\t%s\t%s\n", index+1, artifact_.Id, artifact_.Version, artifact_.Type)
	}
	writer.Flush()

	if report.Error != "" {
		log.Println(report.Error)
	}
}
