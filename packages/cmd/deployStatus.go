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
	"strings"
	"time"

	"github.com/Trifolium-project/landscaper/packages/cpiclient"
)

//Runtime statuses the tenant reports
const (
	runtimeStatusStarted  = "STARTED"
	runtimeStatusStarting = "STARTING"
	runtimeStatusError    = "ERROR"
)

//Exit codes, so that an automated caller can react without parsing the output
const (
	exitDeployed    = 0 //STARTED
	exitFailure     = 1 //anything else that went wrong
	exitDeployError = 2 //ERROR
	exitStillDeploying = 3 //timed out while STARTING
	exitNotDeployed = 4 //no runtime artifact at all
)

//Defaults of the wait flags
const (
	defaultDeployTimeout  = 180 * time.Second
	defaultDeployInterval = 5 * time.Second
)

//runtimeReader is the part of CPIClient the waiting needs. An interface keeps
//the polling rules testable without a tenant.
type runtimeReader interface {
	ReadIntegrationRuntimeArtifact(ArtifactId string) (*cpiclient.IntegrationRuntimeArtifact, error)
	ReadIntegrationRuntimeArtifactErrorInformation(ArtifactId string) (*cpiclient.RuntimeErrorInformation, error)
}

//deployStatus is the outcome of looking at a runtime artifact
type deployStatus struct {
	//Deployed is false when the tenant holds no runtime artifact at all
	Deployed   bool
	Status     string
	Version    string
	DeployedOn string
	DeployedBy string
	//ErrorText is the flattened error information, empty unless Status is ERROR
	ErrorText string
	Error     *cpiclient.RuntimeErrorInformation
	//TimedOut is true when the wait gave up while the artifact was still starting
	TimedOut bool
}

//ExitCode maps an outcome onto the process exit code
func (status *deployStatus) ExitCode() int {

	switch {
	case status == nil:
		return exitFailure
	case !status.Deployed:
		return exitNotDeployed
	case status.Status == runtimeStatusError:
		return exitDeployError
	case status.TimedOut || status.Status == runtimeStatusStarting:
		return exitStillDeploying
	case status.Status == runtimeStatusStarted:
		return exitDeployed
	default:
		return exitFailure
	}
}

//Summary is the one line the result table shows
func (status *deployStatus) Summary() string {
	if status == nil {
		return "unknown"
	}
	if !status.Deployed {
		return "Not deployed"
	}
	if status.TimedOut {
		return status.Status + " (timed out)"
	}
	return status.Status
}

//readDeployStatus looks at the runtime artifact once, fetching the error
//information only when the tenant reports a failure
func readDeployStatus(client runtimeReader, artifactId string) *deployStatus {

	runtimeArtifact, err := client.ReadIntegrationRuntimeArtifact(artifactId)
	if err != nil || runtimeArtifact == nil {
		//The tenant answers a missing runtime artifact with an error, which is
		//how "never deployed" is reported
		return &deployStatus{Deployed: false}
	}

	status := &deployStatus{
		Deployed:   true,
		Status:     runtimeArtifact.Status,
		Version:    runtimeArtifact.Version,
		DeployedOn: runtimeArtifact.DeployedOn,
		DeployedBy: runtimeArtifact.DeployedBy,
	}

	if status.Status == runtimeStatusError {
		information, err := client.ReadIntegrationRuntimeArtifactErrorInformation(artifactId)
		if err == nil && information != nil {
			status.Error = information
			status.ErrorText = information.Text
		}
		if status.ErrorText == "" {
			status.ErrorText = "the tenant reported ERROR without error information"
		}
	}

	return status
}

//waitForDeployment polls until the artifact settles, and returns as soon as it
//does. expectedVersion guards against the trap that makes a naive poll useless:
//immediately after a redeploy the tenant still reports the PREVIOUS version as
//STARTED, so a status is only accepted once the runtime version matches the one
//just deployed. An empty expectedVersion, or a design time version of "Active",
//accepts any version.
func waitForDeployment(client runtimeReader, artifactId string, expectedVersion string,
	timeout time.Duration, interval time.Duration) *deployStatus {

	if interval <= 0 {
		interval = defaultDeployInterval
	}

	deadline := time.Now().Add(timeout)
	var status *deployStatus

	for {
		status = readDeployStatus(client, artifactId)

		if isSettled(status, expectedVersion) {
			return status
		}

		if !time.Now().Before(deadline) {
			break
		}

		remaining := time.Until(deadline)
		if remaining < interval {
			remaining = interval
		}
		time.Sleep(interval)
	}

	//Out of time. A run that never left STARTING, or that never showed the new
	//version, is reported as still deploying rather than as a success.
	if status == nil {
		return &deployStatus{Deployed: false}
	}
	if status.Status != runtimeStatusError {
		status.TimedOut = true
	}

	return status
}

//isSettled reports whether polling can stop
func isSettled(status *deployStatus, expectedVersion string) bool {

	if status == nil {
		return false
	}

	//A missing runtime artifact right after a deploy means the tenant has not
	//registered it yet, so keep waiting rather than reporting "not deployed"
	if !status.Deployed {
		return false
	}

	//An error is final whatever version it belongs to
	if status.Status == runtimeStatusError {
		return true
	}

	if status.Status == runtimeStatusStarting {
		return false
	}

	return versionMatches(status.Version, expectedVersion)
}

//versionMatches reports whether the runtime is running the version that was
//just deployed. "Active" is a draft marker rather than a version, so it can
//never be compared and any version is accepted.
func versionMatches(runtimeVersion string, expectedVersion string) bool {

	expected := strings.TrimSpace(expectedVersion)
	if expected == "" || strings.EqualFold(expected, draftVersion) {
		return true
	}

	return strings.TrimSpace(runtimeVersion) == expected
}

//deployStatusError renders the error text for a table cell, on one line
func deployStatusError(status *deployStatus) string {
	if status == nil || status.ErrorText == "" {
		return "-"
	}
	return singleLine(status.ErrorText)
}

//printDeployError writes the block that "artifact get" and "artifact deploy"
//show when the tenant reports a failed deployment
func printDeployError(status *deployStatus) {
	if status == nil || status.ErrorText == "" {
		return
	}
	fmt.Printf("\nDeploy error:\n%s\n", status.ErrorText)
}
