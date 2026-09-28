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
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/Trifolium-project/landscaper/packages/cpiclient"
	"github.com/Trifolium-project/landscaper/packages/util"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

//Exit codes of "package delete", next to exitDeployed and exitFailure
const (
	exitPackageNotFound = 4
	exitPackageDeployed = 5
	exitPackageDeclared = 6
)

//Status of a delete in the report
const (
	deleteStatusDeleted   = "deleted"
	deleteStatusDryRun    = "dry-run"
	deleteStatusNotFound  = "not-found"
	deleteStatusDeployed  = "deployed"
	deleteStatusDeclared  = "declared"
	deleteStatusCancelled = "cancelled"
	deleteStatusFailed    = "failed"
)

var (
	deleteUndeploy *bool
	deleteYes      *bool
	deleteDryRun   *bool
	deleteForce    *bool
	deleteOutput   *string
	deleteTimeout  *time.Duration
	deleteInterval *time.Duration
)

//Replaced by tests, which have no terminal
var (
	stdinIsTerminal           = func() bool { return term.IsTerminal(int(os.Stdin.Fd())) }
	confirmInput    io.Reader = os.Stdin
)

//packageDeleteReport is the JSON document of "package delete"
type packageDeleteReport struct {
	Id         string                   `json:"id"`
	Env        string                   `json:"env"`
	Deleted    bool                     `json:"deleted"`
	DryRun     bool                     `json:"dryRun"`
	Status     string                   `json:"status"`
	Error      string                   `json:"error,omitempty"`
	Artifacts  []*packageArtifactReport `json:"artifacts"`
	Undeployed []string                 `json:"undeployed"`
}

type packageArtifactReport struct {
	Id       string `json:"id"`
	Type     string `json:"type"`
	Version  string `json:"version"`
	Deployed bool   `json:"deployed"`
}

//packageDeleteRequest is what the flags ask for
type packageDeleteRequest struct {
	PackageId string
	Env       string
	//Declared in the landscape configuration
	Declared bool
	Force    bool
	Undeploy bool
	DryRun   bool
	//Asked right before the first write, nil means yes
	Confirm  func() (bool, error)
	Timeout  time.Duration
	Interval time.Duration
}

//packageDeleter is the part of CPIClient the delete needs
type packageDeleter interface {
	runtimeUndeployer
	ReadIntegrationPackageStatus(PackageId string) (*cpiclient.IntegrationPackage, error)
	ReadPackageDesigntimeArtifacts(PackageId string) ([]*cpiclient.PackageArtifact, error)
	DeleteIntegrationPackage(PackageId string) error
}

// packageDeleteCmd represents the package delete command
var packageDeleteCmd = &cobra.Command{
	Use:   "delete",
	Short: "Delete an integration package from Design",
	Long: `Delete an integration package from the Design section of the environment's
tenant, together with all its artifacts.

--pkg takes the package id. A package declared in the landscape configuration
gets the suffix of --env, like everywhere else; any other id is used as it is,
which is what a package created by "package copy" needs.

The tenant deletes a package even when its content is deployed, and leaves that
content running without a design time. This command therefore refuses as long as
anything of the package is deployed; --undeploy undeploys it first and waits
until the runtime no longer holds it.

A package declared in the landscape configuration is refused unless --force is
given, because the landscape would point to nothing afterwards.

--dry-run reports what would happen and writes nothing. Without a terminal,
--yes is required; with one, the command asks.

Exit codes:

  0  deleted, or --dry-run
  4  the package does not exist
  5  content of the package is deployed and --undeploy was not given
  6  the package is declared in the landscape configuration and --force was not given
  1  anything else went wrong, including a declined confirmation`,
	Run: func(cmd *cobra.Command, args []string) {
		packageDelete()
	},
}

func init() {
	packageCmd.AddCommand(packageDeleteCmd)

	deleteUndeploy = packageDeleteCmd.Flags().Bool("undeploy", false, "Undeploy deployed content of the package first")
	deleteYes = packageDeleteCmd.Flags().Bool("yes", false, "Do not ask for confirmation, required without a terminal")
	deleteDryRun = packageDeleteCmd.Flags().Bool("dry-run", false, "Report what would be deleted, write nothing")
	deleteForce = packageDeleteCmd.Flags().Bool("force", false, "Delete a package declared in the landscape configuration")
	deleteOutput = packageDeleteCmd.Flags().String("output", outputText, "Output format: text or json")
	deleteTimeout = packageDeleteCmd.Flags().Duration("timeout", defaultDeployTimeout, "How long to wait for the undeployment and the deletion")
	deleteInterval = packageDeleteCmd.Flags().Duration("interval", defaultDeployInterval, "How often to ask the tenant while waiting")
}

func packageDelete() {
	if globalLandscape == nil {
		println("Global landscape is not instantiated")
		return
	}

	if *deleteOutput != outputText && *deleteOutput != outputJSON {
		log.Fatalf("Unknown output format %q, use %s or %s", *deleteOutput, outputText, outputJSON)
	}

	environment_, err := globalLandscape.GetEnvironment(*environment)
	if err != nil {
		log.Fatalln(err)
	}

	rawId := rawPackageFlag(environment_)
	if rawId == "" {
		log.Fatalln("Pass the package id with --pkg")
	}

	//A declared package follows the suffix rule, anything else is taken verbatim
	packageId := rawId
	_, declared := globalLandscape.Packages[rawId]
	if declared {
		packageId = rawId + environment_.Suffix
	}

	request := packageDeleteRequest{
		PackageId: packageId,
		Env:       environment_.Id,
		Declared:  declared,
		Force:     *deleteForce,
		Undeploy:  *deleteUndeploy,
		DryRun:    *deleteDryRun,
		Timeout:   *deleteTimeout,
		Interval:  *deleteInterval,
	}

	if !*deleteDryRun {
		request.Confirm, err = deleteConfirmation(*deleteYes, packageId, environment_.Id)
		if err != nil {
			log.Fatalln(err)
		}
	}

	report := deletePackage(environment_.System.Client, request)

	//"type" is taken by the audit record itself, hence artifact_type
	for _, artifactReport := range report.Artifacts {
		status := "kept"
		switch {
		case report.Deleted:
			status = "removed"
		case report.DryRun:
			status = "planned"
		}
		auditItem(map[string]interface{}{
			"operation":     "package-delete",
			"package":       report.Id,
			"artifact":      artifactReport.Id,
			"artifact_type": artifactReport.Type,
			"version":       artifactReport.Version,
			"deployed":      artifactReport.Deployed,
			"undeployed":    util.Contains(report.Undeployed, artifactReport.Id),
			"status":        status,
		})
	}
	auditItem(map[string]interface{}{
		"operation": "package-delete",
		"package":   report.Id,
		"status":    report.Status,
		"error":     report.Error,
	})

	if *deleteOutput == outputJSON {
		encoded, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			log.Fatalln(err)
		}
		fmt.Println(string(encoded))
	} else {
		printPackageDeleteText(report)
	}

	exitWith(packageDeleteExitCode(report.Status))
}

//deleteConfirmation returns the question asked before the first write. --yes
//answers it up front; without a terminal nobody could, so the run is refused.
func deleteConfirmation(yes bool, packageId string, env string) (func() (bool, error), error) {

	if yes {
		return nil, nil
	}
	if !stdinIsTerminal() {
		return nil, fmt.Errorf("Standard input is not a terminal, pass --yes to delete package %s without confirmation", packageId)
	}

	return func() (bool, error) {
		//The prompt goes to stderr, so that --output json stays parseable
		fmt.Fprintf(os.Stderr, "Delete package %s in environment %s with all its artifacts? [y/N] ", packageId, env)
		answer, err := bufio.NewReader(confirmInput).ReadString('\n')
		if err != nil && answer == "" {
			return false, nil
		}
		answer = strings.ToLower(strings.TrimSpace(answer))
		return answer == "y" || answer == "yes", nil
	}, nil
}

//deletePackage runs the checks in the documented order and deletes. Nothing is
//written before every check has passed, and nothing at all with DryRun.
func deletePackage(client packageDeleter, request packageDeleteRequest) *packageDeleteReport {

	report := &packageDeleteReport{
		Id:         request.PackageId,
		Env:        request.Env,
		DryRun:     request.DryRun,
		Artifacts:  []*packageArtifactReport{},
		Undeployed: []string{},
	}

	finish := func(status string, err error) *packageDeleteReport {
		report.Status = status
		if err != nil {
			report.Error = err.Error()
		}
		return report
	}

	if _, err := client.ReadIntegrationPackageStatus(request.PackageId); err != nil {
		if cpiclient.HasStatus(err, http.StatusNotFound) {
			return finish(deleteStatusNotFound, fmt.Errorf("Package %s does not exist", request.PackageId))
		}
		return finish(deleteStatusFailed, err)
	}

	if request.Declared && !request.Force {
		return finish(deleteStatusDeclared, fmt.Errorf("Package %s is declared in the landscape configuration, pass --force to delete it anyway", request.PackageId))
	}

	artifacts, err := client.ReadPackageDesigntimeArtifacts(request.PackageId)
	if err != nil {
		return finish(deleteStatusFailed, err)
	}

	runtimeArtifacts, err := client.ReadIntegrationRuntimeArtifacts()
	if err != nil {
		return finish(deleteStatusFailed, err)
	}
	deployedIds := map[string]bool{}
	for _, runtimeArtifact := range runtimeArtifacts {
		deployedIds[runtimeArtifact.Id] = true
	}

	deployed := []string{}
	for _, artifact_ := range artifacts {
		artifactReport := &packageArtifactReport{
			Id:       artifact_.Id,
			Type:     artifact_.Type,
			Version:  artifact_.Version,
			Deployed: deployedIds[artifact_.Id],
		}
		report.Artifacts = append(report.Artifacts, artifactReport)
		if artifactReport.Deployed {
			deployed = append(deployed, artifact_.Id)
		}
	}

	if len(deployed) > 0 && !request.Undeploy {
		return finish(deleteStatusDeployed, fmt.Errorf("Deployed content of package %s: %s. Pass --undeploy to undeploy it first",
			request.PackageId, strings.Join(deployed, ", ")))
	}

	if request.DryRun {
		return finish(deleteStatusDryRun, nil)
	}

	if request.Confirm != nil {
		confirmed, err := request.Confirm()
		if err != nil {
			return finish(deleteStatusFailed, err)
		}
		if !confirmed {
			return finish(deleteStatusCancelled, fmt.Errorf("Deletion of package %s was not confirmed", request.PackageId))
		}
	}

	if len(deployed) > 0 {
		if err := undeployAndWait(client, deployed, request.Timeout, request.Interval); err != nil {
			return finish(deleteStatusFailed, err)
		}
		report.Undeployed = deployed
	}

	if err := client.DeleteIntegrationPackage(request.PackageId); err != nil {
		if cpiclient.HasStatus(err, http.StatusNotFound) {
			return finish(deleteStatusNotFound, fmt.Errorf("Package %s does not exist", request.PackageId))
		}
		return finish(deleteStatusFailed, err)
	}

	//The tenant answers 202 and deletes in the background
	if err := waitForPackageDeletion(client, request.PackageId, request.Timeout, request.Interval); err != nil {
		return finish(deleteStatusFailed, err)
	}

	report.Deleted = true
	return finish(deleteStatusDeleted, nil)
}

//waitForPackageDeletion polls until the package reads as not found
func waitForPackageDeletion(client packageDeleter, packageId string, timeout time.Duration, interval time.Duration) error {

	if interval <= 0 {
		interval = defaultDeployInterval
	}
	deadline := time.Now().Add(timeout)

	for {
		_, err := client.ReadIntegrationPackageStatus(packageId)
		if cpiclient.HasStatus(err, http.StatusNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if !time.Now().Before(deadline) {
			return fmt.Errorf("The deletion of package %s was accepted, but the package is still there after %s", packageId, timeout)
		}
		time.Sleep(interval)
	}
}

func packageDeleteExitCode(status string) int {
	switch status {
	case deleteStatusDeleted, deleteStatusDryRun:
		return exitDeployed
	case deleteStatusNotFound:
		return exitPackageNotFound
	case deleteStatusDeployed:
		return exitPackageDeployed
	case deleteStatusDeclared:
		return exitPackageDeclared
	default:
		return exitFailure
	}
}

func printPackageDeleteText(report *packageDeleteReport) {

	writer := tabwriter.NewWriter(os.Stdout, 0, 8, 1, '\t', tabwriter.AlignRight)
	fmt.Fprintf(writer, "===Package===\n\n")
	fmt.Fprintf(writer, "%s\t%s\n", "ID:", report.Id)
	fmt.Fprintf(writer, "%s\t%s\n", "Environment:", report.Env)
	fmt.Fprintf(writer, "%s\t%s\n", "Status:", report.Status)
	fmt.Fprintf(writer, "%s\t%t\n", "Deleted:", report.Deleted)

	if len(report.Artifacts) > 0 {
		fmt.Fprintf(writer, "\n===Artifacts===\n\n")
		fmt.Fprintln(writer, "#\tArtefactId\tType\tVersion\tDeployed\tUndeployed")
		for index, artifactReport := range report.Artifacts {
			fmt.Fprintf(writer, "%d\t%s\t%s\t%s\t%t\t%t\n", index+1, artifactReport.Id, artifactReport.Type,
				artifactReport.Version, artifactReport.Deployed, util.Contains(report.Undeployed, artifactReport.Id))
		}
	}
	writer.Flush()

	if report.Error != "" {
		log.Println(report.Error)
	}
}
