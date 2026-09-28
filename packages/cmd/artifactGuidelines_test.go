package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Trifolium-project/landscaper/packages/cpiclient"
	"github.com/Trifolium-project/landscaper/packages/landscape"
)

//stubGuideline is one rule the stub tenant reports
type stubGuideline struct {
	Id            string
	Severity      string
	Applicability string
	Compliance    string
	Violated      string
	//Essential rules cannot be skipped
	Essential bool
}

//stubGuidelines imitates what a real tenant answered, see
//changelog/0007-design-guidelines.md
type stubGuidelines struct {
	//Artifact id to its rules. An artifact missing here does not exist.
	Rules map[string][]*stubGuideline
	//Artifact id to rule to skip reason. Skips outlive executions.
	Skips map[string]map[string]string
	//Artifact id to its only execution, the tenant keeps no history
	Latest  map[string]string
	Times   map[string]int64
	counter int
}

func (tenant *stubTenant) enableGuidelines() *stubGuidelines {
	tenant.Guidelines = &stubGuidelines{
		Rules:  map[string][]*stubGuideline{},
		Skips:  map[string]map[string]string{},
		Latest: map[string]string{},
		Times:  map[string]int64{},
	}
	return tenant.Guidelines
}

//testRules is a trimmed down version of the answer for Order_API_TEST_HARNESS
func testRules() []*stubGuideline {
	return []*stubGuideline{
		{Id: "CAMEL_CLASSES_USAGE", Severity: "High", Applicability: "Applicable", Compliance: "Compliant", Essential: true},
		{Id: "HANDLE_EXCEPTIONS", Severity: "High", Applicability: "Applicable", Compliance: "Non-Compliant", Violated: "{Process_1=Integration Process}"},
		{Id: "CONTINUE_MESSAGE_PROCESSING_EVEN_AFTER_AN_EXCEPTION", Severity: "Low", Applicability: "Applicable", Compliance: "Non-Compliant",
			Violated: "{Process_54=Handle GET Order, Process_70=Handle DELETE Order}"},
		{Id: "USE_BYTE_ARRAY_AS_OUTPUT_TYPE", Severity: "Low", Applicability: "Not Applicable", Compliance: "Not Applicable"},
	}
}

func writeODataError(writer http.ResponseWriter, status int, message string) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	fmt.Fprintf(writer, `{"error":{"code":"Bad Request","message":{"lang":"en","value":%q}}}`, message)
}

//handleGuidelines answers the design guideline calls and reports whether the
//request was one of them
func (tenant *stubTenant) handleGuidelines(writer http.ResponseWriter, request *http.Request) bool {

	guidelines := tenant.Guidelines
	if guidelines == nil {
		return false
	}

	path := request.URL.Path
	artifactId := quotedIdFromPath(path, "IntegrationDesigntimeArtifacts(Id=")

	switch {

	case request.Method == http.MethodPost && strings.HasSuffix(path, "/ExecuteIntegrationDesigntimeArtifactsGuidelines"):
		if _, found := request.URL.Query()["$format"]; found {
			writeODataError(writer, http.StatusNotImplemented, "Not implemented")
			return true
		}
		id := strings.Trim(request.URL.Query().Get("Id"), "'")
		if guidelines.Rules[id] == nil {
			writer.WriteHeader(http.StatusInternalServerError)
			fmt.Fprintf(writer, `<?xml version='1.0' encoding='UTF-8'?><error xmlns="http://schemas.microsoft.com/ado/2007/08/dataservices/metadata"><code>Internal Server Error</code><message xml:lang="en">Unable to get Data: Request: %s IFlow</message></error>`, id)
			return true
		}
		guidelines.counter++
		executionId := fmt.Sprintf("execution%d", guidelines.counter)
		guidelines.Latest[id] = executionId
		guidelines.Times[id] = 1790580727000 + int64(guidelines.counter)
		writer.Header().Set("Content-Type", "text/plain; charset=utf-8")
		writer.Write([]byte(executionId))

	case request.Method == http.MethodPut && strings.Contains(path, "/$links/DesignGuidelineExecutionResults("):
		executionId := quotedIdFromPath(path, "DesignGuidelineExecutionResults(")
		body := tenant.Calls[len(tenant.Calls)-1].Body
		if executionId != guidelines.Latest[artifactId] {
			writeODataError(writer, http.StatusBadRequest, "You can't perform the operation as the execution ID is invalid.")
			return true
		}
		if _, typo := body["GudelineId"]; typo {
			writeODataError(writer, http.StatusBadRequest, "Illegal argument for method call with message 'GudelineId'.")
			return true
		}
		ruleId, _ := body["GuidelineId"].(string)
		var rule *stubGuideline
		for _, candidate := range guidelines.Rules[artifactId] {
			if candidate.Id == ruleId {
				rule = candidate
			}
		}
		if rule == nil {
			writeODataError(writer, http.StatusBadRequest, "You can't perform this operation as there is no design guideline available with the given ID.")
			return true
		}
		if rule.Essential {
			writeODataError(writer, http.StatusBadRequest, "You can't perform this operation as the essential design guidelines cannot be skipped for the artifact.")
			return true
		}
		skip, _ := body["IsGuidelineSkipped"].(bool)
		reason, _ := body["SkipReason"].(string)
		if skip && reason == "" {
			writeODataError(writer, http.StatusBadRequest, "SkipReason must not be empty.")
			return true
		}
		if guidelines.Skips[artifactId] == nil {
			guidelines.Skips[artifactId] = map[string]string{}
		}
		_, alreadySkipped := guidelines.Skips[artifactId][ruleId]
		if skip && alreadySkipped {
			writeODataError(writer, http.StatusBadRequest, "The design guideline is already skipped for the artifact.")
			return true
		}
		if !skip && !alreadySkipped {
			writeODataError(writer, http.StatusBadRequest, "You cannot revert a design guideline that is not skipped.")
			return true
		}
		if skip {
			guidelines.Skips[artifactId][ruleId] = reason
		} else {
			delete(guidelines.Skips[artifactId], ruleId)
		}
		writer.WriteHeader(http.StatusOK)

	case request.Method == http.MethodGet && strings.HasSuffix(path, "/DesignGuidelineExecutionResults"):
		execution := map[string]interface{}{
			"ExecutionId": "", "ArtifactVersion": "", "ExecutionStatus": "NOT_EXECUTED", "ExecutionTime": "0", "ReportType": nil,
		}
		if executionId := guidelines.Latest[artifactId]; executionId != "" {
			execution = map[string]interface{}{
				"ExecutionId": executionId, "ArtifactVersion": "1.0.18", "ExecutionStatus": "FAIL",
				"ExecutionTime": fmt.Sprint(guidelines.Times[artifactId]), "ReportType": nil,
			}
		}
		json.NewEncoder(writer).Encode(map[string]interface{}{"d": map[string]interface{}{"results": []interface{}{execution}}})

	case request.Method == http.MethodGet && strings.Contains(path, "/DesignGuidelineExecutionResults("):
		executionId := quotedIdFromPath(path, "DesignGuidelineExecutionResults(")
		if executionId != guidelines.Latest[artifactId] {
			writeODataError(writer, http.StatusBadRequest, "You can't perform the expand operation as the execution ID is invalid.")
			return true
		}
		results := []interface{}{}
		for _, rule := range guidelines.Rules[artifactId] {
			reason, skipped := guidelines.Skips[artifactId][rule.Id]
			var skipReason, skippedBy interface{}
			if skipped {
				skipReason, skippedBy = reason, "sb-client"
			}
			var violated interface{}
			if rule.Violated != "" {
				violated = rule.Violated
			}
			results = append(results, map[string]interface{}{
				"GuidelineId": rule.Id, "GuidelineName": strings.ToLower(rule.Id), "Category": "Category",
				"Severity": rule.Severity, "Applicability": rule.Applicability, "Compliance": rule.Compliance,
				"IsGuidelineSkipped": skipped, "SkipReason": skipReason, "SkippedBy": skippedBy,
				"ExpectedKPI": nil, "ActualKPI": nil, "ViolatedComponents": violated,
			})
		}
		json.NewEncoder(writer).Encode(map[string]interface{}{"d": map[string]interface{}{
			"ExecutionId": executionId, "ArtifactVersion": "1.0.18", "ExecutionStatus": "FAIL",
			"ExecutionTime": fmt.Sprint(guidelines.Times[artifactId]), "ReportType": nil,
			"DesignGuidelines": map[string]interface{}{"results": results},
		}})

	default:
		return false
	}

	return true
}

//clearGlobalSelectors sets the global --pkg and --artifact, which the
//guideline commands refuse, to empty
func clearGlobalSelectors(t *testing.T) {
	t.Helper()
	empty, emptyArtifact := "", ""
	previousPkg, previousArtifact := pkg, artifact
	pkg, artifact = &empty, &emptyArtifact
	t.Cleanup(func() { pkg, artifact = previousPkg, previousArtifact })
}

//newGuidelineTest wires the stub tenant with guidelines for the test artifact
func newGuidelineTest(t *testing.T) (*stubTenant, *stubGuidelines) {
	t.Helper()
	tenant := newStubTenant(t)
	guidelines := tenant.enableGuidelines()
	guidelines.Rules["Order_API_TEST_HARNESS"] = testRules()
	globalLandscape = newTestLandscape(t, tenant)
	clearGlobalSelectors(t)
	return tenant, guidelines
}

//devClient is the client of the Dev environment, pointing at the stub tenant
func devClient() guidelineClient {
	return globalLandscape.Environments["Dev"].System.Client
}

func devTarget() *guidelineTarget {
	return &guidelineTarget{
		BaseId:     "Order_API_TEST_HARNESS",
		ArtifactId: "Order_API_TEST_HARNESS",
		PackageId:  "TestHarnessPreparation",
		Skips:      globalLandscape.GetGuidelineSkips("Order_API_TEST_HARNESS"),
	}
}

func findRule(report *guidelineArtifactReport, id string) *guidelineRuleReport {
	for _, rule := range report.Rules {
		if rule.Id == id {
			return rule
		}
	}
	return nil
}

func countCalls(calls []tenantCall, method string, contains string) int {
	count := 0
	for _, call := range calls {
		if call.Method == method && strings.Contains(call.Path, contains) {
			count++
		}
	}
	return count
}

func TestGuidelineRunReportsEveryRule(t *testing.T) {
	tenant, _ := newGuidelineTest(t)

	report := checkArtifactGuidelines(devClient(), devTarget(), guidelineOptions{Execute: true, FailOn: "medium"})

	if report.Error != "" {
		t.Fatalf("unexpected error: %s", report.Error)
	}
	if report.Status != guidelineStatusNotCompliant {
		t.Errorf("Status = %q, want %q", report.Status, guidelineStatusNotCompliant)
	}
	if report.ExecutionId != "execution1" || report.Version != "1.0.18" {
		t.Errorf("unexpected execution %q, version %q", report.ExecutionId, report.Version)
	}
	if report.ExecutionTime != "2026-09-28T07:32:07Z" {
		t.Errorf("ExecutionTime = %q, want the epoch milliseconds as RFC 3339", report.ExecutionTime)
	}
	if len(report.Rules) != 4 {
		t.Fatalf("expected every rule, compliant and not applicable ones included, got %d", len(report.Rules))
	}

	want := map[string]int{ruleCompliant: 1, ruleNotCompliant: 2, ruleNotApplicable: 1}
	if !reflect.DeepEqual(report.Counts, want) {
		t.Errorf("Counts = %v, want %v", report.Counts, want)
	}

	//Only the High rule reaches --fail-on medium, the Low one does not
	if report.Violations != 1 {
		t.Errorf("Violations = %d, want 1", report.Violations)
	}

	continueRule := findRule(report, "CONTINUE_MESSAGE_PROCESSING_EVEN_AFTER_AN_EXCEPTION")
	wantComponents := []cpiclient.ViolatedComponent{{Id: "Process_54", Name: "Handle GET Order"}, {Id: "Process_70", Name: "Handle DELETE Order"}}
	if continueRule == nil || !reflect.DeepEqual(continueRule.ViolatedComponents, wantComponents) {
		t.Errorf("unexpected violated components %+v", continueRule)
	}

	//The function import answers $format=json with 501
	execute := findCall(tenant.Calls, http.MethodPost, "ExecuteIntegrationDesigntimeArtifactsGuidelines")
	if execute == nil {
		t.Fatal("the guidelines were not executed")
	}
	if got := execute.Query.Get("Id"); got != "'Order_API_TEST_HARNESS'" {
		t.Errorf("Id = %q", got)
	}
	if got := execute.Query.Get("Version"); got != "'active'" {
		t.Errorf("Version = %q, want 'active'", got)
	}
}

func TestGuidelineFailOn(t *testing.T) {

	tests := []struct {
		failOn     string
		violations int
		exitCode   int
	}{
		{"low", 2, exitGuidelineViolations},
		{"LOW", 2, exitGuidelineViolations},
		{"medium", 1, exitGuidelineViolations},
		{"high", 1, exitGuidelineViolations},
		{"none", 0, exitDeployed},
	}

	for _, test := range tests {
		t.Run(test.failOn, func(t *testing.T) {
			newGuidelineTest(t)
			report := checkArtifactGuidelines(devClient(), devTarget(), guidelineOptions{Execute: true, FailOn: test.failOn})

			if report.Violations != test.violations {
				t.Errorf("Violations = %d, want %d", report.Violations, test.violations)
			}
			if got := guidelineExitCode(summarizeGuidelines([]*guidelineArtifactReport{report})); got != test.exitCode {
				t.Errorf("exit code = %d, want %d", got, test.exitCode)
			}
		})
	}
}

func TestGuidelineRunAppliesDeclaredSkips(t *testing.T) {
	tenant, guidelines := newGuidelineTest(t)
	globalLandscape.Packages["TestHarnessPreparation"].Artifacts["Order_API_TEST_HARNESS"].GuidelineSkips = []*landscape.GuidelineSkip{
		{Rule: "CONTINUE_MESSAGE_PROCESSING_EVEN_AFTER_AN_EXCEPTION", Reason: "Errors are handled by the caller"},
	}

	report := checkArtifactGuidelines(devClient(), devTarget(), guidelineOptions{Execute: true, ApplySkips: true, FailOn: "low"})

	if report.DeclaredSkips == nil || report.DeclaredSkips.Applied != 1 || report.DeclaredSkips.Declared != 1 {
		t.Fatalf("DeclaredSkips = %+v, want 1 of 1 applied", report.DeclaredSkips)
	}

	//The report is read again after the skip, so it shows what the tenant recorded
	rule := findRule(report, "CONTINUE_MESSAGE_PROCESSING_EVEN_AFTER_AN_EXCEPTION")
	if rule.Status != ruleSkipped || rule.SkipReason != "Errors are handled by the caller" {
		t.Errorf("rule = %+v, want it skipped with the declared reason", rule)
	}
	//A skipped rule keeps Compliance at Non-Compliant but no longer counts
	if rule.Compliance != "Non-Compliant" || report.Violations != 1 {
		t.Errorf("Compliance = %q, Violations = %d, want Non-Compliant and 1", rule.Compliance, report.Violations)
	}

	//SAP's swagger spells the key GudelineId, the tenant only takes GuidelineId
	put := findCall(tenant.Calls, http.MethodPut, "/$links/DesignGuidelineExecutionResults(")
	if put == nil || put.Body["GuidelineId"] != "CONTINUE_MESSAGE_PROCESSING_EVEN_AFTER_AN_EXCEPTION" || put.Body["IsGuidelineSkipped"] != true {
		t.Errorf("unexpected skip call %+v", put)
	}
	if guidelines.Skips["Order_API_TEST_HARNESS"]["CONTINUE_MESSAGE_PROCESSING_EVEN_AFTER_AN_EXCEPTION"] == "" {
		t.Error("the tenant does not hold the skip")
	}

	//The skip survives in the tenant, so a second run files nothing
	checkArtifactGuidelines(devClient(), devTarget(), guidelineOptions{Execute: true, ApplySkips: true, FailOn: "low"})
	if got := countCalls(tenant.Calls, http.MethodPut, "/$links/DesignGuidelineExecutionResults("); got != 1 {
		t.Errorf("skip calls = %d, want the second run to leave the existing skip alone", got)
	}
}

func TestGuidelineRefusedSkipIsReportedNotFatal(t *testing.T) {
	newGuidelineTest(t)
	target := devTarget()
	target.Skips = []*landscape.GuidelineSkip{
		{Rule: "CAMEL_CLASSES_USAGE", Reason: "essential, refused"},
		{Rule: "NO_SUCH_RULE", Reason: "unknown, refused"},
		{Rule: "HANDLE_EXCEPTIONS", Reason: "accepted"},
	}

	report := checkArtifactGuidelines(devClient(), target, guidelineOptions{Execute: true, ApplySkips: true, FailOn: "low"})

	if report.Error != "" {
		t.Fatalf("a refused skip must not fail the artifact: %s", report.Error)
	}
	if report.DeclaredSkips.Applied != 1 || len(report.DeclaredSkips.Failures) != 2 {
		t.Fatalf("DeclaredSkips = %+v, want 1 applied and 2 failures", report.DeclaredSkips)
	}
	if !strings.Contains(report.DeclaredSkips.Failures[0], "essential design guidelines cannot be skipped") {
		t.Errorf("the failure should carry the tenant's message, got %q", report.DeclaredSkips.Failures[0])
	}
}

func TestGuidelineResultsOfArtifactNeverChecked(t *testing.T) {
	tenant, _ := newGuidelineTest(t)

	report := checkArtifactGuidelines(devClient(), devTarget(), guidelineOptions{FailOn: "low"})

	if report.Status != guidelineStatusNotExecuted || report.ExecutionStatus != cpiclient.GuidelineNotExecuted {
		t.Errorf("Status = %q / %q, want not-executed", report.Status, report.ExecutionStatus)
	}
	if got := guidelineExitCode(summarizeGuidelines([]*guidelineArtifactReport{report})); got != exitGuidelinesNotFinished {
		t.Errorf("exit code = %d, want %d", got, exitGuidelinesNotFinished)
	}
	if findCall(tenant.Calls, http.MethodPost, "ExecuteIntegrationDesigntimeArtifactsGuidelines") != nil {
		t.Error("results must not execute the guidelines")
	}
}

func TestGuidelineResultsReadTheLatestExecution(t *testing.T) {
	newGuidelineTest(t)
	client := devClient()

	checkArtifactGuidelines(client, devTarget(), guidelineOptions{Execute: true, FailOn: "low"})
	checkArtifactGuidelines(client, devTarget(), guidelineOptions{Execute: true, FailOn: "low"})

	report := checkArtifactGuidelines(client, devTarget(), guidelineOptions{FailOn: "low"})
	if report.ExecutionId != "execution2" || len(report.Rules) != 4 {
		t.Errorf("ExecutionId = %q with %d rules, want execution2 with every rule", report.ExecutionId, len(report.Rules))
	}

	//An older execution id is rejected by the tenant, which keeps no history
	stale := checkArtifactGuidelines(client, devTarget(), guidelineOptions{ExecutionId: "execution1", FailOn: "low"})
	if stale.Status != guidelineStatusError || !strings.Contains(stale.Error, "execution ID is invalid") {
		t.Errorf("Status = %q, Error = %q, want the tenant's refusal", stale.Status, stale.Error)
	}
}

func TestGuidelineErrorDoesNotStopBatch(t *testing.T) {
	newGuidelineTest(t)
	client := devClient()

	missing := &guidelineTarget{BaseId: "NoSuchFlow", ArtifactId: "NoSuchFlow"}
	reports := []*guidelineArtifactReport{
		checkArtifactGuidelines(client, missing, guidelineOptions{Execute: true, FailOn: "low"}),
		checkArtifactGuidelines(client, devTarget(), guidelineOptions{Execute: true, FailOn: "low"}),
	}

	//The XML error document is reduced to its message
	if reports[0].Status != guidelineStatusError || reports[0].Error != "Unable to get Data: Request: NoSuchFlow IFlow" {
		t.Errorf("Status = %q, Error = %q", reports[0].Status, reports[0].Error)
	}
	if reports[1].Status != guidelineStatusNotCompliant {
		t.Errorf("the second artifact should still be checked, got %q", reports[1].Status)
	}

	summary := summarizeGuidelines(reports)
	if summary.Errors != 1 || summary.NotCompliant != 1 || summary.Violations != 2 {
		t.Errorf("summary = %+v", summary)
	}
	if got := guidelineExitCode(summary); got != exitFailure {
		t.Errorf("exit code = %d, want an error to win over violations", got)
	}
}

func TestResolveGuidelineTargets(t *testing.T) {
	tenant, _ := newGuidelineTest(t)
	globalLandscape.Packages["TestHarnessPreparation"].Artifacts["Order_API_TEST_HARNESS"].GuidelineSkips = []*landscape.GuidelineSkip{
		{Rule: "HANDLE_EXCEPTIONS", Reason: "Handled"},
	}
	qa := globalLandscape.Environments["QA"]
	client := qa.System.Client

	//--artifacts takes base ids, the suffix of the environment is appended
	targets, err := resolveGuidelineTargets(client, qa, guidelineSelection{Artifacts: []string{"Order_API_TEST_HARNESS", "Undeclared"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 2 {
		t.Fatalf("expected two targets, got %d", len(targets))
	}
	if targets[0].ArtifactId != "Order_API_TEST_HARNESSQA" || targets[0].PackageId != "TestHarnessPreparationQA" || len(targets[0].Skips) != 1 {
		t.Errorf("unexpected target %+v", targets[0])
	}
	if targets[1].ArtifactId != "UndeclaredQA" || targets[1].PackageId != "" || len(targets[1].Skips) != 0 {
		t.Errorf("an undeclared artifact is still checked, got %+v", targets[1])
	}

	//--packages reads the package from the tenant
	tenant.Packages["TestHarnessPreparationQA"] = true
	tenant.Artifacts["Order_API_TEST_HARNESSQA"] = "1.0.1"
	tenant.Artifacts["Other_FlowQA"] = "1.0.0"
	targets, err = resolveGuidelineTargets(client, qa, guidelineSelection{Packages: []string{"TestHarnessPreparation"}})
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for _, target := range targets {
		ids = append(ids, target.ArtifactId+"/"+target.BaseId)
	}
	if !reflect.DeepEqual(ids, []string{"Order_API_TEST_HARNESSQA/Order_API_TEST_HARNESS", "Other_FlowQA/Other_FlowQA"}) {
		t.Errorf("unexpected targets %v", ids)
	}

	//--all-declared walks the landscape configuration
	targets, err = resolveGuidelineTargets(client, qa, guidelineSelection{AllDeclared: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 1 || targets[0].ArtifactId != "Order_API_TEST_HARNESSQA" {
		t.Errorf("unexpected targets %+v", targets)
	}
}

func TestValidateGuidelineSelection(t *testing.T) {
	clearGlobalSelectors(t)

	if err := validateGuidelineSelection(guidelineSelection{}); err == nil {
		t.Error("expected an error without a selector")
	}
	if err := validateGuidelineSelection(guidelineSelection{Artifacts: []string{"a"}, AllDeclared: true}); err == nil {
		t.Error("expected an error with two selectors")
	}
	if err := validateGuidelineSelection(guidelineSelection{Packages: []string{"p"}}); err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	//The global --pkg arrives suffixed with the environment, see root.go
	suffixed := "PkgQA"
	pkg = &suffixed
	if err := validateGuidelineSelection(guidelineSelection{Packages: []string{"p"}}); err == nil {
		t.Error("expected the global --pkg to be refused")
	}

	if err := validateFailOn("critical"); err == nil {
		t.Error("expected an unknown --fail-on to be refused")
	}
}

//fakeGuidelineClient replays execution statuses, for the polling rules
type fakeGuidelineClient struct {
	statuses []string
	reads    int
}

func (fake *fakeGuidelineClient) ReadIntegrationDesigntimeArtifacts(PackageId string, fetchConfig bool) ([]*cpiclient.IntegrationDesigntimeArtifact, error) {
	return nil, nil
}

func (fake *fakeGuidelineClient) ExecuteIntegrationDesigntimeArtifactGuidelines(ArtifactId string, ArtifactVersion string) (string, error) {
	return "run1", nil
}

func (fake *fakeGuidelineClient) ReadDesignGuidelineExecutions(ArtifactId string, ArtifactVersion string) ([]*cpiclient.DesignGuidelineExecution, error) {
	index := fake.reads
	if index >= len(fake.statuses) {
		index = len(fake.statuses) - 1
	}
	fake.reads++
	return []*cpiclient.DesignGuidelineExecution{{ExecutionId: "run1", ExecutionStatus: fake.statuses[index]}}, nil
}

func (fake *fakeGuidelineClient) ReadDesignGuidelineExecutionResult(ArtifactId string, ArtifactVersion string, ExecutionId string) (*cpiclient.DesignGuidelineExecution, []*cpiclient.DesignGuideline, error) {
	return &cpiclient.DesignGuidelineExecution{ExecutionId: ExecutionId, ExecutionStatus: "PASS"},
		[]*cpiclient.DesignGuideline{{GuidelineId: "RULE", Severity: "High", Compliance: "Compliant"}}, nil
}

func (fake *fakeGuidelineClient) SkipDesignGuideline(ArtifactId string, ArtifactVersion string, ExecutionId string, GuidelineId string, reason string, skip bool) error {
	return nil
}

func TestExecuteGuidelinesWaitsUntilSettled(t *testing.T) {
	fake := &fakeGuidelineClient{statuses: []string{"RUNNING", "RUNNING", "PASS"}}

	report := checkArtifactGuidelines(fake, &guidelineTarget{ArtifactId: "flow"},
		guidelineOptions{Execute: true, Wait: true, Timeout: time.Second, Interval: time.Millisecond, FailOn: "low"})

	if report.Status != guidelineStatusCompliant {
		t.Errorf("Status = %q, want compliant", report.Status)
	}
	if fake.reads != 3 {
		t.Errorf("reads = %d, want polling until the execution finished", fake.reads)
	}
}

func TestExecuteGuidelinesWithoutWaitReadsOnce(t *testing.T) {
	fake := &fakeGuidelineClient{statuses: []string{"RUNNING", "PASS"}}

	report := checkArtifactGuidelines(fake, &guidelineTarget{ArtifactId: "flow"}, guidelineOptions{Execute: true, FailOn: "low"})

	if report.Status != guidelineStatusNotFinished || fake.reads != 1 {
		t.Errorf("Status = %q after %d reads, want not-finished after one", report.Status, fake.reads)
	}
	if got := guidelineExitCode(summarizeGuidelines([]*guidelineArtifactReport{report})); got != exitGuidelinesNotFinished {
		t.Errorf("exit code = %d, want %d", got, exitGuidelinesNotFinished)
	}
}

func TestExecuteGuidelinesTimesOut(t *testing.T) {
	fake := &fakeGuidelineClient{statuses: []string{"RUNNING"}}

	report := checkArtifactGuidelines(fake, &guidelineTarget{ArtifactId: "flow"},
		guidelineOptions{Execute: true, Wait: true, Timeout: 5 * time.Millisecond, Interval: time.Millisecond, FailOn: "low"})

	if report.Status != guidelineStatusNotFinished {
		t.Errorf("Status = %q, want not-finished", report.Status)
	}
}

func TestGuidelineRuleStatus(t *testing.T) {

	tests := []struct {
		guideline cpiclient.DesignGuideline
		want      string
	}{
		{cpiclient.DesignGuideline{Compliance: "Compliant"}, ruleCompliant},
		{cpiclient.DesignGuideline{Compliance: "Non-Compliant"}, ruleNotCompliant},
		{cpiclient.DesignGuideline{Compliance: "Not Applicable"}, ruleNotApplicable},
		{cpiclient.DesignGuideline{Compliance: ""}, ruleNotApplicable},
		//The tenant keeps Non-Compliant on a skipped rule
		{cpiclient.DesignGuideline{Compliance: "Non-Compliant", IsGuidelineSkipped: true}, ruleSkipped},
		{cpiclient.DesignGuideline{Compliance: "Something New"}, "something new"},
	}

	for _, test := range tests {
		if got := guidelineRuleStatus(&test.guideline); got != test.want {
			t.Errorf("guidelineRuleStatus(%+v) = %q, want %q", test.guideline, got, test.want)
		}
	}
}

func TestGuidelineSeverity(t *testing.T) {
	for severity, want := range map[string]int{"Low": 1, "medium": 2, "HIGH": 3, "Critical": 3, "": 3} {
		if got := guidelineSeverity(severity); got != want {
			t.Errorf("guidelineSeverity(%q) = %d, want %d", severity, got, want)
		}
	}
}

func TestGuidelineExitCodePrecedence(t *testing.T) {

	tests := []struct {
		summary guidelineSummary
		want    int
	}{
		{guidelineSummary{Artifacts: 1, Compliant: 1}, exitDeployed},
		{guidelineSummary{Artifacts: 1, NotCompliant: 1}, exitDeployed},
		{guidelineSummary{Artifacts: 1, NotCompliant: 1, Violations: 3}, exitGuidelineViolations},
		{guidelineSummary{Artifacts: 2, NotFinished: 1, Violations: 3}, exitGuidelinesNotFinished},
		{guidelineSummary{Artifacts: 3, Errors: 1, NotFinished: 1, Violations: 3}, exitFailure},
	}

	for _, test := range tests {
		if got := guidelineExitCode(test.summary); got != test.want {
			t.Errorf("guidelineExitCode(%+v) = %d, want %d", test.summary, got, test.want)
		}
	}
}

func TestSkipGuidelinesRunsTheGuidelinesFirst(t *testing.T) {
	tenant, guidelines := newGuidelineTest(t)

	rows := skipGuidelines(devClient(), []*guidelineTarget{devTarget()},
		[]string{"HANDLE_EXCEPTIONS", "CAMEL_CLASSES_USAGE"}, "Handled by the caller", guidelineVersionActive, "", true)

	if len(rows) != 2 {
		t.Fatalf("expected a row per rule, got %d", len(rows))
	}
	if rows[0].Action != "skipped" || rows[0].ExecutionId != "execution1" {
		t.Errorf("first row = %+v, want skipped against the execution just made", rows[0])
	}
	if rows[1].Action != "failed" || !strings.Contains(rows[1].Error, "essential") {
		t.Errorf("second row = %+v, want the essential rule refused", rows[1])
	}
	if guidelines.Skips["Order_API_TEST_HARNESS"]["HANDLE_EXCEPTIONS"] != "Handled by the caller" {
		t.Error("the tenant does not hold the skip")
	}

	//A revert needs no reason
	rows = skipGuidelines(devClient(), []*guidelineTarget{devTarget()},
		[]string{"HANDLE_EXCEPTIONS"}, "", guidelineVersionActive, "", false)
	if rows[0].Action != "unskipped" || len(guidelines.Skips["Order_API_TEST_HARNESS"]) != 0 {
		t.Errorf("row = %+v, skips = %v, want the skip reverted", rows[0], guidelines.Skips)
	}
	if got := countCalls(tenant.Calls, http.MethodPost, "ExecuteIntegrationDesigntimeArtifactsGuidelines"); got != 1 {
		t.Errorf("executions = %d, want the existing execution reused", got)
	}
}

func TestCollectGuidelineRules(t *testing.T) {
	_, guidelines := newGuidelineTest(t)
	guidelines.Rules["Other_Flow"] = []*stubGuideline{
		{Id: "AAA_ONLY_HERE", Severity: "Medium", Compliance: "Compliant"},
		{Id: "HANDLE_EXCEPTIONS", Severity: "High", Compliance: "Compliant"},
	}

	rules, err := collectGuidelineRules(devClient(), []*guidelineTarget{
		devTarget(), {BaseId: "Other_Flow", ArtifactId: "Other_Flow"},
	}, guidelineVersionActive)
	if err != nil {
		t.Fatal(err)
	}

	ids := []string{}
	for _, rule := range rules {
		ids = append(ids, rule.Id)
	}
	want := []string{"AAA_ONLY_HERE", "CAMEL_CLASSES_USAGE", "CONTINUE_MESSAGE_PROCESSING_EVEN_AFTER_AN_EXCEPTION",
		"HANDLE_EXCEPTIONS", "USE_BYTE_ARRAY_AS_OUTPUT_TYPE"}
	if !reflect.DeepEqual(ids, want) {
		t.Errorf("rules = %v, want %v", ids, want)
	}
}

func TestUploadAppliesGuidelineSkipsBeforeDeploy(t *testing.T) {
	tenant := newStubTenant(t)
	guidelines := tenant.enableGuidelines()
	guidelines.Rules["Order_API_TEST_HARNESSQA"] = testRules()
	tenant.Packages["TestHarnessPreparationQA"] = true
	tenant.Artifacts["Order_API_TEST_HARNESSQA"] = "1.0.1"

	globalLandscape = newTestLandscape(t, tenant)
	globalLandscape.Packages["TestHarnessPreparation"].Artifacts["Order_API_TEST_HARNESS"].GuidelineSkips = []*landscape.GuidelineSkip{
		{Rule: "HANDLE_EXCEPTIONS", Reason: "Handled by the caller"},
	}

	root := t.TempDir()
	source := writeTestArtifact(t, root)
	setUploadFlags(t, "QA", filepath.Join(root, "build"))

	deploy, applySkips := true, true
	previous := uploadApplySkips
	uploadDeploy, uploadApplySkips = &deploy, &applySkips
	t.Cleanup(func() { uploadApplySkips = previous })

	row, err := uploadArtifact(source, globalLandscape.Environments["QA"])
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if row.GuidelineSkips == nil || row.GuidelineSkips.Applied != 1 {
		t.Fatalf("GuidelineSkips = %+v, want 1 applied", row.GuidelineSkips)
	}
	if describeGuidelineSkips(row.GuidelineSkips) != "1/1" {
		t.Errorf("table cell = %q", describeGuidelineSkips(row.GuidelineSkips))
	}
	if guidelines.Skips["Order_API_TEST_HARNESSQA"]["HANDLE_EXCEPTIONS"] != "Handled by the caller" {
		t.Error("the skip was not filed against the suffixed artifact")
	}

	skipIndex, deployIndex := -1, -1
	for index, call := range tenant.Calls {
		if call.Method == http.MethodPut && strings.Contains(call.Path, "/$links/DesignGuidelineExecutionResults(") {
			skipIndex = index
		}
		if strings.Contains(call.Path, "DeployIntegrationDesigntimeArtifact") {
			deployIndex = index
		}
	}
	if skipIndex < 0 || deployIndex < 0 || skipIndex > deployIndex {
		t.Errorf("skip call at %d, deploy at %d, want the skip before the deployment", skipIndex, deployIndex)
	}
}

func TestUploadWithoutGuidelineSkipsFlagMakesNoGuidelineCall(t *testing.T) {
	tenant := newStubTenant(t)
	tenant.enableGuidelines().Rules["Order_API_TEST_HARNESSQA"] = testRules()
	tenant.Packages["TestHarnessPreparationQA"] = true
	tenant.Artifacts["Order_API_TEST_HARNESSQA"] = "1.0.1"

	globalLandscape = newTestLandscape(t, tenant)
	globalLandscape.Packages["TestHarnessPreparation"].Artifacts["Order_API_TEST_HARNESS"].GuidelineSkips = []*landscape.GuidelineSkip{
		{Rule: "HANDLE_EXCEPTIONS", Reason: "Handled by the caller"},
	}

	root := t.TempDir()
	setUploadFlags(t, "QA", filepath.Join(root, "build"))
	applySkips := false
	previous := uploadApplySkips
	uploadApplySkips = &applySkips
	t.Cleanup(func() { uploadApplySkips = previous })

	row, err := uploadArtifact(writeTestArtifact(t, root), globalLandscape.Environments["QA"])
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if row.GuidelineSkips != nil {
		t.Errorf("GuidelineSkips = %+v, want nothing without the flag", row.GuidelineSkips)
	}
	if countCalls(tenant.Calls, http.MethodPost, "ExecuteIntegrationDesigntimeArtifactsGuidelines") != 0 {
		t.Error("an upload without --apply-guideline-skips must not run the guidelines")
	}
}

//The tenant refuses to skip a skipped rule and to revert one that is not
//skipped, the commands have to be safe to repeat anyway
func TestSkipGuidelinesIsIdempotent(t *testing.T) {
	_, guidelines := newGuidelineTest(t)
	skip := func(reason string, skip bool) *guidelineSkipRow {
		rows := skipGuidelines(devClient(), []*guidelineTarget{devTarget()},
			[]string{"HANDLE_EXCEPTIONS"}, reason, guidelineVersionActive, "", skip)
		if rows[0].Error != "" {
			t.Fatalf("unexpected error: %s", rows[0].Error)
		}
		return rows[0]
	}

	if row := skip("first", true); row.Action != skipActionSkipped {
		t.Errorf("Action = %q, want skipped", row.Action)
	}
	if row := skip("first", true); row.Action != skipActionUnchanged {
		t.Errorf("Action = %q, want unchanged for the same reason", row.Action)
	}
	if row := skip("second", true); row.Action != skipActionUpdated {
		t.Errorf("Action = %q, want updated for a new reason", row.Action)
	}
	if got := guidelines.Skips["Order_API_TEST_HARNESS"]["HANDLE_EXCEPTIONS"]; got != "second" {
		t.Errorf("reason in the tenant = %q, want the new one", got)
	}
	if row := skip("", false); row.Action != skipActionUnskipped {
		t.Errorf("Action = %q, want unskipped", row.Action)
	}
	if row := skip("", false); row.Action != skipActionUnchanged {
		t.Errorf("Action = %q, want unchanged for a rule that is not skipped", row.Action)
	}
}

//A skip filed by hand with another reason is replaced by the declared one, the
//landscape configuration in git is the reference
func TestDeclaredSkipReplacesAnotherReason(t *testing.T) {
	_, guidelines := newGuidelineTest(t)
	skipGuidelines(devClient(), []*guidelineTarget{devTarget()},
		[]string{"HANDLE_EXCEPTIONS"}, "filed by hand", guidelineVersionActive, "", true)

	target := devTarget()
	target.Skips = []*landscape.GuidelineSkip{{Rule: "HANDLE_EXCEPTIONS", Reason: "declared in git"}}

	report := checkArtifactGuidelines(devClient(), target, guidelineOptions{Execute: true, ApplySkips: true, FailOn: "low"})

	if report.DeclaredSkips.Applied != 1 || len(report.DeclaredSkips.Failures) != 0 {
		t.Fatalf("DeclaredSkips = %+v", report.DeclaredSkips)
	}
	if rule := findRule(report, "HANDLE_EXCEPTIONS"); rule.SkipReason != "declared in git" {
		t.Errorf("reported reason = %q, want the declared one", rule.SkipReason)
	}
	if got := guidelines.Skips["Order_API_TEST_HARNESS"]["HANDLE_EXCEPTIONS"]; got != "declared in git" {
		t.Errorf("reason in the tenant = %q", got)
	}
}
