package cmd

import (
	"fmt"
	"testing"
	"time"

	"github.com/Trifolium-project/landscaper/packages/cpiclient"
)

//fakeRuntime replays a scripted sequence of runtime states, so the polling
//rules can be tested without a tenant and without waiting in real time
type fakeRuntime struct {
	//One entry per call. The last entry is repeated once exhausted.
	states []*cpiclient.IntegrationRuntimeArtifact
	//Returned when the artifact is in ERROR
	errorInformation *cpiclient.RuntimeErrorInformation
	calls            int
	errorCalls       int
}

func (fake *fakeRuntime) ReadIntegrationRuntimeArtifact(ArtifactId string) (*cpiclient.IntegrationRuntimeArtifact, error) {

	fake.calls++

	if len(fake.states) == 0 {
		return nil, fmt.Errorf("not deployed")
	}

	index := fake.calls - 1
	if index >= len(fake.states) {
		index = len(fake.states) - 1
	}

	state := fake.states[index]
	if state == nil {
		return nil, fmt.Errorf("not deployed")
	}

	return state, nil
}

func (fake *fakeRuntime) ReadIntegrationRuntimeArtifactErrorInformation(ArtifactId string) (*cpiclient.RuntimeErrorInformation, error) {
	fake.errorCalls++
	return fake.errorInformation, nil
}

func runtimeState(status string, version string) *cpiclient.IntegrationRuntimeArtifact {
	return &cpiclient.IntegrationRuntimeArtifact{Status: status, Version: version}
}

//The trap this whole feature exists to avoid: immediately after a redeploy the
//tenant still reports the PREVIOUS version as STARTED. Accepting that would
//report success for a deployment that has not happened yet.
func TestWaitRejectsTheOldVersionReportedAsStarted(t *testing.T) {
	fake := &fakeRuntime{states: []*cpiclient.IntegrationRuntimeArtifact{
		runtimeState(runtimeStatusStarted, "1.0.3"),  //the version before the deploy
		runtimeState(runtimeStatusStarting, "1.0.3"), //the tenant catches up
		runtimeState(runtimeStatusStarted, "1.0.4"),  //the version just deployed
	}}

	status := waitForDeployment(fake, "Order_API_TEST_HARNESS", "1.0.4", time.Second, time.Millisecond)

	if status.ExitCode() != exitDeployed {
		t.Errorf("ExitCode = %d, want %d", status.ExitCode(), exitDeployed)
	}
	if status.Version != "1.0.4" {
		t.Errorf("Version = %q, want the version just deployed", status.Version)
	}
	if status.TimedOut {
		t.Error("the wait timed out although the new version appeared")
	}
	if fake.calls < 3 {
		t.Errorf("polled %d times, want it to keep asking until the version matched", fake.calls)
	}
}

func TestWaitReturnsAsSoonAsTheVersionMatches(t *testing.T) {
	fake := &fakeRuntime{states: []*cpiclient.IntegrationRuntimeArtifact{
		runtimeState(runtimeStatusStarted, "1.0.4"),
	}}

	status := waitForDeployment(fake, "X", "1.0.4", time.Second, time.Millisecond)

	if status.ExitCode() != exitDeployed {
		t.Errorf("ExitCode = %d, want %d", status.ExitCode(), exitDeployed)
	}
	if fake.calls != 1 {
		t.Errorf("polled %d times, want 1", fake.calls)
	}
}

func TestWaitReportsErrorWithTheTenantsExplanation(t *testing.T) {
	fake := &fakeRuntime{
		states: []*cpiclient.IntegrationRuntimeArtifact{
			runtimeState(runtimeStatusStarting, "1.0.4"),
			runtimeState(runtimeStatusError, "1.0.4"),
		},
		errorInformation: &cpiclient.RuntimeErrorInformation{
			Message: cpiclient.RuntimeErrorMessage{MessageText: "Deployment of the artifact failed"},
			Text:    "Deployment of the artifact failed\nScript resource not found [missing_script.groovy]",
		},
	}

	status := waitForDeployment(fake, "X", "1.0.4", time.Second, time.Millisecond)

	if status.ExitCode() != exitDeployError {
		t.Errorf("ExitCode = %d, want %d", status.ExitCode(), exitDeployError)
	}
	if status.ErrorText == "" {
		t.Fatal("the tenant's explanation was not recorded")
	}
	if fake.errorCalls != 1 {
		t.Errorf("error information fetched %d times, want exactly 1", fake.errorCalls)
	}
	if status.TimedOut {
		t.Error("an error is final, it must not be reported as a timeout")
	}
}

//An ERROR is final whatever version it belongs to, otherwise a failed redeploy
//would be polled until the timeout
func TestWaitTreatsErrorAsFinalEvenForTheOldVersion(t *testing.T) {
	fake := &fakeRuntime{
		states:           []*cpiclient.IntegrationRuntimeArtifact{runtimeState(runtimeStatusError, "1.0.3")},
		errorInformation: &cpiclient.RuntimeErrorInformation{Text: "boom"},
	}

	status := waitForDeployment(fake, "X", "1.0.4", time.Second, time.Millisecond)

	if status.ExitCode() != exitDeployError {
		t.Errorf("ExitCode = %d, want %d", status.ExitCode(), exitDeployError)
	}
	if fake.calls != 1 {
		t.Errorf("polled %d times, want it to stop at the error", fake.calls)
	}
}

func TestWaitTimesOutWhileStarting(t *testing.T) {
	fake := &fakeRuntime{states: []*cpiclient.IntegrationRuntimeArtifact{
		runtimeState(runtimeStatusStarting, "1.0.4"),
	}}

	status := waitForDeployment(fake, "X", "1.0.4", 10*time.Millisecond, time.Millisecond)

	if status.ExitCode() != exitStillDeploying {
		t.Errorf("ExitCode = %d, want %d", status.ExitCode(), exitStillDeploying)
	}
	if !status.TimedOut {
		t.Error("TimedOut was not set")
	}
	if got := status.Summary(); got != "STARTING (timed out)" {
		t.Errorf("Summary = %q", got)
	}
}

//A version that never appears is a timeout, not a success
func TestWaitTimesOutWhenTheNewVersionNeverAppears(t *testing.T) {
	fake := &fakeRuntime{states: []*cpiclient.IntegrationRuntimeArtifact{
		runtimeState(runtimeStatusStarted, "1.0.3"),
	}}

	status := waitForDeployment(fake, "X", "1.0.4", 10*time.Millisecond, time.Millisecond)

	if status.ExitCode() != exitStillDeploying {
		t.Errorf("ExitCode = %d, want %d", status.ExitCode(), exitStillDeploying)
	}
	if !status.TimedOut {
		t.Error("TimedOut was not set")
	}
}

//A runtime artifact that never appears means the deploy never took effect
func TestWaitReportsNotDeployed(t *testing.T) {
	fake := &fakeRuntime{}

	status := waitForDeployment(fake, "X", "1.0.4", 10*time.Millisecond, time.Millisecond)

	if status.ExitCode() != exitNotDeployed {
		t.Errorf("ExitCode = %d, want %d", status.ExitCode(), exitNotDeployed)
	}
	if got := status.Summary(); got != "Not deployed" {
		t.Errorf("Summary = %q", got)
	}
}

//A draft has no comparable version, so any version is accepted
func TestWaitAcceptsAnyVersionForADraft(t *testing.T) {
	fake := &fakeRuntime{states: []*cpiclient.IntegrationRuntimeArtifact{
		runtimeState(runtimeStatusStarted, "1.0.9"),
	}}

	status := waitForDeployment(fake, "X", draftVersion, time.Second, time.Millisecond)

	if status.ExitCode() != exitDeployed {
		t.Errorf("ExitCode = %d, want %d", status.ExitCode(), exitDeployed)
	}
}

func TestVersionMatches(t *testing.T) {
	tests := []struct {
		name     string
		runtime  string
		expected string
		want     bool
	}{
		{"equal", "1.0.4", "1.0.4", true},
		{"the old version is rejected", "1.0.3", "1.0.4", false},
		{"no expectation accepts anything", "1.0.3", "", true},
		{"a draft accepts anything", "1.0.3", "Active", true},
		{"a draft is case insensitive", "1.0.3", "active", true},
		{"surrounding space is ignored", " 1.0.4 ", "1.0.4", true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := versionMatches(test.runtime, test.expected); got != test.want {
				t.Errorf("versionMatches(%q, %q) = %t, want %t", test.runtime, test.expected, got, test.want)
			}
		})
	}
}

//A single read must not poll, and must not fetch error information for a
//healthy artifact
func TestReadDeployStatusDoesNotFetchErrorInformationWhenStarted(t *testing.T) {
	fake := &fakeRuntime{states: []*cpiclient.IntegrationRuntimeArtifact{
		runtimeState(runtimeStatusStarted, "1.0.4"),
	}}

	status := readDeployStatus(fake, "X")

	if status.ExitCode() != exitDeployed {
		t.Errorf("ExitCode = %d, want %d", status.ExitCode(), exitDeployed)
	}
	if fake.errorCalls != 0 {
		t.Errorf("error information was fetched %d times for a healthy artifact", fake.errorCalls)
	}
}

//An ERROR the tenant will not explain still has to say something
func TestReadDeployStatusWithoutErrorInformation(t *testing.T) {
	fake := &fakeRuntime{states: []*cpiclient.IntegrationRuntimeArtifact{
		runtimeState(runtimeStatusError, "1.0.4"),
	}}

	status := readDeployStatus(fake, "X")

	if status.ErrorText == "" {
		t.Error("an ERROR status was reported with no explanation at all")
	}
	if status.ExitCode() != exitDeployError {
		t.Errorf("ExitCode = %d, want %d", status.ExitCode(), exitDeployError)
	}
}

func TestDeployStatusExitCodes(t *testing.T) {
	tests := []struct {
		name   string
		status *deployStatus
		want   int
	}{
		{"nil", nil, exitFailure},
		{"not deployed", &deployStatus{Deployed: false}, exitNotDeployed},
		{"started", &deployStatus{Deployed: true, Status: runtimeStatusStarted}, exitDeployed},
		{"error", &deployStatus{Deployed: true, Status: runtimeStatusError}, exitDeployError},
		{"starting", &deployStatus{Deployed: true, Status: runtimeStatusStarting}, exitStillDeploying},
		{"timed out", &deployStatus{Deployed: true, Status: runtimeStatusStarted, TimedOut: true}, exitStillDeploying},
		{"unknown status", &deployStatus{Deployed: true, Status: "SOMETHING_ELSE"}, exitFailure},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.status.ExitCode(); got != test.want {
				t.Errorf("ExitCode = %d, want %d", got, test.want)
			}
		})
	}
}

//The error cell of the result table must stay on one line
func TestDeployStatusErrorIsOneLine(t *testing.T) {
	status := &deployStatus{ErrorText: "Deployment failed\nScript resource not found"}

	got := deployStatusError(status)
	if got != "Deployment failed Script resource not found" {
		t.Errorf("deployStatusError = %q", got)
	}
	if deployStatusError(nil) != "-" {
		t.Errorf("a missing status must render as -")
	}
}
