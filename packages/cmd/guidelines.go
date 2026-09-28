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
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/Trifolium-project/landscaper/packages/cpiclient"
	"github.com/Trifolium-project/landscaper/packages/landscape"
)

//Shared logic of the "artifact guidelines" commands. Everything that talks to
//the tenant goes through guidelineClient, so the rules are testable without one.

//Exit codes of the guideline commands, next to the ones in deployStatus.go
const (
	//an execution did not finish in time, or the artifact was never checked
	exitGuidelinesNotFinished = 3
	//at least one rule at or above --fail-on is not compliant
	exitGuidelineViolations = 7
)

//Version the tenant resolves to the current design time version
const guidelineVersionActive = "active"

//Normalised status of a rule
const (
	ruleCompliant     = "compliant"
	ruleNotCompliant  = "not-compliant"
	ruleNotApplicable = "not-applicable"
	ruleSkipped       = "skipped"
)

//Status of an artifact in the report
const (
	guidelineStatusCompliant    = "compliant"
	guidelineStatusNotCompliant = "not-compliant"
	guidelineStatusNotFinished  = "not-finished"
	guidelineStatusNotExecuted  = "not-executed"
	guidelineStatusError        = "error"
)

//Accepted values of --fail-on, "none" never fails
var guidelineSeverityRank = map[string]int{
	"none":   0,
	"low":    1,
	"medium": 2,
	"high":   3,
}

//Execution statuses that mean the tenant is not done yet
var guidelinePendingStatuses = map[string]bool{
	"":                             true,
	cpiclient.GuidelineNotExecuted: true,
	"RUNNING":                      true,
	"PENDING":                      true,
	"IN_PROGRESS":                  true,
	"STARTED":                      true,
}

//guidelineClient is the part of CPIClient the guideline commands need
type guidelineClient interface {
	ReadIntegrationDesigntimeArtifacts(PackageId string, fetchConfig bool) ([]*cpiclient.IntegrationDesigntimeArtifact, error)
	ExecuteIntegrationDesigntimeArtifactGuidelines(ArtifactId string, ArtifactVersion string) (string, error)
	ReadDesignGuidelineExecutions(ArtifactId string, ArtifactVersion string) ([]*cpiclient.DesignGuidelineExecution, error)
	ReadDesignGuidelineExecutionResult(ArtifactId string, ArtifactVersion string, ExecutionId string) (*cpiclient.DesignGuidelineExecution, []*cpiclient.DesignGuideline, error)
	SkipDesignGuideline(ArtifactId string, ArtifactVersion string, ExecutionId string, GuidelineId string, reason string, skip bool) error
}

//guidelineSelection holds the selector flags every guideline command shares
type guidelineSelection struct {
	Artifacts   []string
	Packages    []string
	AllDeclared bool
}

//guidelineTarget is one artifact of the environment to check
type guidelineTarget struct {
	//Id without environment suffix, as in the landscape configuration
	BaseId string
	//Id in the tenant
	ArtifactId string
	//Package in the tenant, empty when unknown
	PackageId string
	//Declared in the landscape configuration
	Skips []*landscape.GuidelineSkip
}

//guidelineOptions steer checkArtifactGuidelines
type guidelineOptions struct {
	//Execute runs the guidelines, otherwise an existing execution is read
	Execute     bool
	Version     string
	ExecutionId string
	Wait        bool
	Timeout     time.Duration
	Interval    time.Duration
	//ApplySkips applies the skips declared in the landscape configuration
	ApplySkips bool
	FailOn     string
}

//guidelineReport is the JSON document of "run" and "results"
type guidelineReport struct {
	Environment string                     `json:"environment"`
	FailOn      string                     `json:"failOn"`
	Summary     guidelineSummary           `json:"summary"`
	Artifacts   []*guidelineArtifactReport `json:"artifacts"`
}

type guidelineSummary struct {
	Artifacts    int `json:"artifacts"`
	Compliant    int `json:"compliant"`
	NotCompliant int `json:"notCompliant"`
	NotFinished  int `json:"notFinished"`
	Errors       int `json:"errors"`
	//Rules at or above --fail-on, that are not compliant and not skipped
	Violations int `json:"violations"`
}

type guidelineArtifactReport struct {
	Id              string `json:"id"`
	Package         string `json:"package"`
	Version         string `json:"version"`
	ExecutionId     string `json:"executionId"`
	ExecutionStatus string `json:"executionStatus"`
	//RFC 3339, empty when the artifact was never checked
	ExecutionTime string `json:"executionTime"`
	Status        string `json:"status"`
	Violations    int    `json:"violations"`
	//Rule counts by normalised status
	Counts        map[string]int         `json:"counts"`
	DeclaredSkips *declaredSkipsReport   `json:"declaredSkips,omitempty"`
	Error         string                 `json:"error,omitempty"`
	Rules         []*guidelineRuleReport `json:"rules"`
}

type declaredSkipsReport struct {
	Declared int      `json:"declared"`
	Applied  int      `json:"applied"`
	Failures []string `json:"failures"`
}

type guidelineRuleReport struct {
	Id            string `json:"id"`
	Name          string `json:"name"`
	Category      string `json:"category"`
	Severity      string `json:"severity"`
	Applicability string `json:"applicability"`
	Compliance    string `json:"compliance"`
	//compliant, not-compliant, not-applicable or skipped
	Status     string `json:"status"`
	Skipped    bool   `json:"skipped"`
	SkipReason string `json:"skipReason"`
	SkippedBy  string `json:"skippedBy"`
	Expected   string `json:"expected"`
	Actual     string `json:"actual"`
	//Model elements the rule complains about, parsed out of ViolatedComponentsRaw
	ViolatedComponents    []cpiclient.ViolatedComponent `json:"violatedComponents"`
	ViolatedComponentsRaw string                        `json:"violatedComponentsRaw"`
}

//validateGuidelineSelection checks that exactly one selector is given
func validateGuidelineSelection(selection guidelineSelection) error {

	selectors := 0
	if len(selection.Artifacts) > 0 {
		selectors++
	}
	if len(selection.Packages) > 0 {
		selectors++
	}
	if selection.AllDeclared {
		selectors++
	}

	if selectors == 0 {
		return fmt.Errorf("Nothing to check, pass --artifacts, --packages or --all-declared")
	}
	if selectors > 1 {
		return fmt.Errorf("Pass only one of --artifacts, --packages and --all-declared")
	}

	//The global flags already carry the suffix of --env, see root.go
	if *pkg != "" || *artifact != "" {
		return fmt.Errorf("Use --packages and --artifacts instead of the global --pkg and --artifact, which already carry the environment suffix")
	}

	return nil
}

//validateFailOn checks the value of --fail-on
func validateFailOn(failOn string) error {
	if _, found := guidelineSeverityRank[strings.ToLower(failOn)]; !found {
		return fmt.Errorf("Unknown --fail-on %q, use none, low, medium or high", failOn)
	}
	return nil
}

//resolveGuidelineTargets turns the selector flags into the artifacts of the
//environment. --packages reads the package from the tenant, so artifacts that
//are not declared in the landscape configuration are checked as well.
func resolveGuidelineTargets(client guidelineClient, environment *landscape.Environment,
	selection guidelineSelection) ([]*guidelineTarget, error) {

	targets := []*guidelineTarget{}
	seen := map[string]bool{}

	add := func(baseId string, artifactId string, packageId string) {
		if seen[artifactId] {
			return
		}
		seen[artifactId] = true
		targets = append(targets, &guidelineTarget{
			BaseId:     baseId,
			ArtifactId: artifactId,
			PackageId:  packageId,
			Skips:      globalLandscape.GetGuidelineSkips(baseId),
		})
	}

	switch {

	case len(selection.Artifacts) > 0:
		for _, baseId := range selection.Artifacts {
			packageId := ""
			//An artifact that is not declared can still be checked
			if basePackageId, err := globalLandscape.FindPackageForArtifact(baseId); err == nil {
				packageId = basePackageId + environment.Suffix
			}
			add(baseId, baseId+environment.Suffix, packageId)
		}

	case len(selection.Packages) > 0:
		for _, basePackageId := range selection.Packages {
			packageId := basePackageId + environment.Suffix
			artifacts, err := client.ReadIntegrationDesigntimeArtifacts(packageId, false)
			if err != nil {
				return nil, fmt.Errorf("Cannot read package %s: %s", packageId, err)
			}
			sort.Slice(artifacts, func(i, j int) bool { return artifacts[i].Id < artifacts[j].Id })
			//The tenant id is taken as it is. The base id only finds the declared
			//skips, and stays suffixed for an artifact that is not declared.
			for _, artifact_ := range artifacts {
				add(trimTargetSuffix(artifact_.Id, environment.Suffix), artifact_.Id, packageId)
			}
		}

	case selection.AllDeclared:
		packageIds := []string{}
		for packageId := range globalLandscape.Packages {
			packageIds = append(packageIds, packageId)
		}
		sort.Strings(packageIds)

		for _, basePackageId := range packageIds {
			artifactIds := []string{}
			for artifactId := range globalLandscape.Packages[basePackageId].Artifacts {
				artifactIds = append(artifactIds, artifactId)
			}
			sort.Strings(artifactIds)
			for _, baseId := range artifactIds {
				add(baseId, baseId+environment.Suffix, basePackageId+environment.Suffix)
			}
		}
	}

	return targets, nil
}

//checkArtifactGuidelines runs or reads the guidelines of one artifact. Failures
//end up in the report instead of stopping a batch.
func checkArtifactGuidelines(client guidelineClient, target *guidelineTarget, options guidelineOptions) *guidelineArtifactReport {

	report := &guidelineArtifactReport{
		Id:      target.ArtifactId,
		Package: target.PackageId,
		Counts:  map[string]int{},
		Rules:   []*guidelineRuleReport{},
	}

	fail := func(err error) *guidelineArtifactReport {
		report.Status = guidelineStatusError
		report.Error = err.Error()
		return report
	}

	version := options.Version
	if version == "" {
		version = guidelineVersionActive
	}

	var execution *cpiclient.DesignGuidelineExecution
	var err error

	switch {
	case options.Execute:
		execution, err = executeGuidelines(client, target.ArtifactId, version, options)
	case options.ExecutionId != "":
		//Taken as finished, the results read below carry the real status, or
		//the tenant's refusal of an id that is no longer the latest
		execution = &cpiclient.DesignGuidelineExecution{ExecutionId: options.ExecutionId, ExecutionStatus: "PASS"}
	default:
		execution, err = latestGuidelineExecution(client, target.ArtifactId, version)
	}
	if err != nil {
		return fail(err)
	}

	if execution == nil || execution.ExecutionId == "" {
		report.Status = guidelineStatusNotExecuted
		report.ExecutionStatus = cpiclient.GuidelineNotExecuted
		return report
	}

	report.ExecutionId = execution.ExecutionId
	report.ExecutionStatus = execution.ExecutionStatus
	report.Version = execution.ArtifactVersion

	if !isGuidelineExecutionSettled(execution.ExecutionStatus) {
		report.Status = guidelineStatusNotFinished
		return report
	}

	execution, guidelines, err := client.ReadDesignGuidelineExecutionResult(target.ArtifactId, version, execution.ExecutionId)
	if err != nil {
		return fail(err)
	}

	if options.ApplySkips && len(target.Skips) > 0 {
		skips, changed := applyDeclaredGuidelineSkips(client, target, version, execution.ExecutionId, guidelines)
		report.DeclaredSkips = skips

		//Read again, so the report shows the skips as the tenant recorded them
		if changed {
			execution, guidelines, err = client.ReadDesignGuidelineExecutionResult(target.ArtifactId, version, execution.ExecutionId)
			if err != nil {
				return fail(err)
			}
		}
	}

	fillGuidelineReport(report, execution, guidelines, options.FailOn)

	return report
}

//executeGuidelines starts an execution and, with --wait, polls until it is done.
//The tenants seen so far answer synchronously, so the first read usually settles.
func executeGuidelines(client guidelineClient, artifactId string, version string,
	options guidelineOptions) (*cpiclient.DesignGuidelineExecution, error) {

	executionId, err := client.ExecuteIntegrationDesigntimeArtifactGuidelines(artifactId, version)
	if err != nil {
		return nil, err
	}

	interval := options.Interval
	if interval <= 0 {
		interval = defaultDeployInterval
	}

	deadline := time.Now().Add(options.Timeout)

	for {
		execution, err := findGuidelineExecution(client, artifactId, version, executionId)
		if err != nil {
			return nil, err
		}

		if execution != nil && isGuidelineExecutionSettled(execution.ExecutionStatus) {
			return execution, nil
		}

		if !options.Wait || !time.Now().Before(deadline) {
			if execution == nil {
				execution = &cpiclient.DesignGuidelineExecution{ExecutionId: executionId}
			}
			return execution, nil
		}

		time.Sleep(interval)
	}
}

//findGuidelineExecution returns the execution with the given id, nil while the
//tenant does not list it yet
func findGuidelineExecution(client guidelineClient, artifactId string, version string,
	executionId string) (*cpiclient.DesignGuidelineExecution, error) {

	executions, err := client.ReadDesignGuidelineExecutions(artifactId, version)
	if err != nil {
		return nil, err
	}

	for _, execution := range executions {
		if execution.ExecutionId == executionId {
			return execution, nil
		}
	}

	return nil, nil
}

//latestGuidelineExecution returns the newest real execution, nil when the
//artifact was never checked
func latestGuidelineExecution(client guidelineClient, artifactId string, version string) (*cpiclient.DesignGuidelineExecution, error) {

	executions, err := client.ReadDesignGuidelineExecutions(artifactId, version)
	if err != nil {
		return nil, err
	}

	var latest *cpiclient.DesignGuidelineExecution
	var latestTime int64 = -1

	for _, execution := range executions {
		if execution.ExecutionId == "" {
			continue
		}
		executionTime, _ := strconv.ParseInt(execution.ExecutionTime, 10, 64)
		if executionTime > latestTime {
			latest = execution
			latestTime = executionTime
		}
	}

	return latest, nil
}

//ensureGuidelineExecution returns the latest execution, and runs the
//guidelines first when there is none. Skips and the rule list need one.
func ensureGuidelineExecution(client guidelineClient, artifactId string, version string) (*cpiclient.DesignGuidelineExecution, error) {

	execution, err := latestGuidelineExecution(client, artifactId, version)
	if err != nil {
		return nil, err
	}
	if execution != nil {
		return execution, nil
	}

	log.Printf("%s was never checked against the design guidelines, running them first", artifactId)

	execution, err = executeGuidelines(client, artifactId, version, guidelineOptions{
		Wait:     true,
		Timeout:  defaultDeployTimeout,
		Interval: defaultDeployInterval,
	})
	if err != nil {
		return nil, err
	}
	if !isGuidelineExecutionSettled(execution.ExecutionStatus) {
		return nil, fmt.Errorf("The guideline execution of %s did not finish", artifactId)
	}

	return execution, nil
}

func isGuidelineExecutionSettled(status string) bool {
	return !guidelinePendingStatuses[strings.ToUpper(status)]
}

//Outcome of filing one skip or revert
const (
	skipActionSkipped   = "skipped"
	skipActionUnskipped = "unskipped"
	//the reason of an existing skip was replaced
	skipActionUpdated = "updated"
	//the tenant already held the requested state
	skipActionUnchanged = "unchanged"
	skipActionFailed    = "failed"
)

//fileGuidelineSkip skips or reverts one rule, idempotently. The tenant itself
//is not: it refuses to skip a skipped rule, even to change the reason, and to
//revert one that is not skipped. current is the rule as the latest execution
//reports it, nil when unknown, in which case the call is made as asked and the
//tenant decides.
func fileGuidelineSkip(client guidelineClient, artifactId string, version string, executionId string,
	rule string, reason string, skip bool, current *cpiclient.DesignGuideline) (string, error) {

	if !skip {
		if current != nil && !current.IsGuidelineSkipped {
			return skipActionUnchanged, nil
		}
		if err := client.SkipDesignGuideline(artifactId, version, executionId, rule, "", false); err != nil {
			return skipActionFailed, err
		}
		return skipActionUnskipped, nil
	}

	action := skipActionSkipped
	if current != nil && current.IsGuidelineSkipped {
		if current.SkipReason == reason {
			return skipActionUnchanged, nil
		}
		if err := client.SkipDesignGuideline(artifactId, version, executionId, rule, "", false); err != nil {
			return skipActionFailed, err
		}
		action = skipActionUpdated
	}

	if err := client.SkipDesignGuideline(artifactId, version, executionId, rule, reason, true); err != nil {
		return skipActionFailed, err
	}

	return action, nil
}

//guidelinesById indexes the rules of an execution
func guidelinesById(guidelines []*cpiclient.DesignGuideline) map[string]*cpiclient.DesignGuideline {
	byId := map[string]*cpiclient.DesignGuideline{}
	for _, guideline := range guidelines {
		byId[guideline.GuidelineId] = guideline
	}
	return byId
}

//applyDeclaredGuidelineSkips files the skips of the landscape configuration
//against an execution. The declared reason wins over one filed by hand.
//Failures, such as an unknown rule or an essential rule that cannot be
//skipped, are reported rather than fatal.
func applyDeclaredGuidelineSkips(client guidelineClient, target *guidelineTarget, version string,
	executionId string, guidelines []*cpiclient.DesignGuideline) (*declaredSkipsReport, bool) {

	report := &declaredSkipsReport{Declared: len(target.Skips), Failures: []string{}}
	changed := false
	current := guidelinesById(guidelines)

	for _, skip := range target.Skips {

		action, err := fileGuidelineSkip(client, target.ArtifactId, version, executionId,
			skip.Rule, skip.Reason, true, current[skip.Rule])
		if err != nil {
			message := fmt.Sprintf("%s: %s", skip.Rule, err)
			report.Failures = append(report.Failures, message)
			log.Printf("Guideline skip of %s not applied, %s", target.ArtifactId, message)
			//A revert that went through before the skip failed is a change too
			changed = true
			continue
		}

		report.Applied++
		if action != skipActionUnchanged {
			changed = true
		}
	}

	return report, changed
}

//fillGuidelineReport copies the rules into the report and counts them
func fillGuidelineReport(report *guidelineArtifactReport, execution *cpiclient.DesignGuidelineExecution,
	guidelines []*cpiclient.DesignGuideline, failOn string) {

	if execution != nil {
		report.ExecutionStatus = execution.ExecutionStatus
		report.ExecutionTime = formatExecutionTime(execution.ExecutionTime)
		if execution.ArtifactVersion != "" {
			report.Version = execution.ArtifactVersion
		}
	}

	threshold := guidelineSeverityRank[strings.ToLower(failOn)]
	notCompliant := 0

	for _, guideline := range guidelines {
		status := guidelineRuleStatus(guideline)
		report.Counts[status]++

		if status == ruleNotCompliant {
			notCompliant++
			if threshold > 0 && guidelineSeverity(guideline.Severity) >= threshold {
				report.Violations++
			}
		}

		report.Rules = append(report.Rules, &guidelineRuleReport{
			Id:                    guideline.GuidelineId,
			Name:                  guideline.GuidelineName,
			Category:              guideline.Category,
			Severity:              guideline.Severity,
			Applicability:         guideline.Applicability,
			Compliance:            guideline.Compliance,
			Status:                status,
			Skipped:               guideline.IsGuidelineSkipped,
			SkipReason:            guideline.SkipReason,
			SkippedBy:             guideline.SkippedBy,
			Expected:              guideline.ExpectedKPI,
			Actual:                guideline.ActualKPI,
			ViolatedComponents:    guideline.Components(),
			ViolatedComponentsRaw: guideline.ViolatedComponents,
		})
	}

	if notCompliant > 0 {
		report.Status = guidelineStatusNotCompliant
	} else {
		report.Status = guidelineStatusCompliant
	}
}

//guidelineRuleStatus normalises a rule. Skipped wins, because the tenant keeps
//Compliance at Non-Compliant for a skipped rule.
func guidelineRuleStatus(guideline *cpiclient.DesignGuideline) string {

	if guideline.IsGuidelineSkipped {
		return ruleSkipped
	}

	switch strings.ToLower(strings.TrimSpace(guideline.Compliance)) {
	case "compliant":
		return ruleCompliant
	case "non-compliant", "not compliant", "noncompliant":
		return ruleNotCompliant
	case "not applicable", "not-applicable", "":
		return ruleNotApplicable
	default:
		return strings.ToLower(guideline.Compliance)
	}
}

//guidelineSeverity ranks a severity. An unknown one counts as high, so that a
//new severity name cannot slip through --fail-on.
func guidelineSeverity(severity string) int {
	if rank, found := guidelineSeverityRank[strings.ToLower(strings.TrimSpace(severity))]; found && rank > 0 {
		return rank
	}
	return guidelineSeverityRank["high"]
}

//formatExecutionTime turns the epoch milliseconds of the tenant into RFC 3339
func formatExecutionTime(executionTime string) string {
	milliseconds, err := strconv.ParseInt(executionTime, 10, 64)
	if err != nil || milliseconds <= 0 {
		return ""
	}
	return time.Unix(0, milliseconds*int64(time.Millisecond)).UTC().Format(time.RFC3339)
}

//summarizeGuidelines counts the artifacts of a report
func summarizeGuidelines(artifacts []*guidelineArtifactReport) guidelineSummary {

	summary := guidelineSummary{Artifacts: len(artifacts)}

	for _, artifactReport := range artifacts {
		switch artifactReport.Status {
		case guidelineStatusCompliant:
			summary.Compliant++
		case guidelineStatusNotCompliant:
			summary.NotCompliant++
		case guidelineStatusNotFinished, guidelineStatusNotExecuted:
			summary.NotFinished++
		case guidelineStatusError:
			summary.Errors++
		}
		summary.Violations += artifactReport.Violations
	}

	return summary
}

//guidelineExitCode: an error beats an unfinished check, which beats a violation
func guidelineExitCode(summary guidelineSummary) int {
	switch {
	case summary.Errors > 0:
		return exitFailure
	case summary.NotFinished > 0:
		return exitGuidelinesNotFinished
	case summary.Violations > 0:
		return exitGuidelineViolations
	default:
		return exitDeployed
	}
}

//runGuidelineCommand is the body shared by "run" and "results"
func runGuidelineCommand(selection guidelineSelection, options guidelineOptions, output string, operation string) {

	if err := validateGuidelineSelection(selection); err != nil {
		log.Fatalln(err)
	}
	if err := validateFailOn(options.FailOn); err != nil {
		log.Fatalln(err)
	}
	if output != outputText && output != outputJSON {
		log.Fatalf("Unknown output format %q, use %s or %s", output, outputText, outputJSON)
	}

	environment_, err := globalLandscape.GetEnvironment(*environment)
	if err != nil {
		log.Fatalln(err)
	}
	client := environment_.System.Client

	targets, err := resolveGuidelineTargets(client, environment_, selection)
	if err != nil {
		log.Fatalln(err)
	}

	report := &guidelineReport{
		Environment: environment_.Id,
		FailOn:      strings.ToLower(options.FailOn),
		Artifacts:   []*guidelineArtifactReport{},
	}

	for _, target := range targets {
		artifactReport := checkArtifactGuidelines(client, target, options)
		report.Artifacts = append(report.Artifacts, artifactReport)

		auditItem(map[string]interface{}{
			"operation":    operation,
			"artifact":     artifactReport.Id,
			"package":      artifactReport.Package,
			"version":      artifactReport.Version,
			"execution_id": artifactReport.ExecutionId,
			"status":       artifactReport.Status,
			"violations":   artifactReport.Violations,
			"error":        artifactReport.Error,
		})
	}

	report.Summary = summarizeGuidelines(report.Artifacts)

	if output == outputJSON {
		printGuidelineJSON(report)
	} else {
		printGuidelineText(report)
	}

	exitWith(guidelineExitCode(report.Summary))
}

func printGuidelineJSON(report *guidelineReport) {
	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		log.Fatalln(err)
	}
	fmt.Println(string(encoded))
}

//printGuidelineText prints one line per artifact, then the rules that need
//attention, then a summary. Compliant and not applicable rules are left to
//--output json.
func printGuidelineText(report *guidelineReport) {

	writer := tabwriter.NewWriter(os.Stdout, 0, 8, 1, '\t', tabwriter.AlignRight)

	fmt.Fprintf(writer, "#\tArtefactId\tPackage\tVersion\tStatus\tNot compliant\tSkipped\tViolations\tDeclared skips\tError\n")
	for index, artifactReport := range report.Artifacts {
		declared := ""
		if artifactReport.DeclaredSkips != nil {
			declared = fmt.Sprintf("%d/%d", artifactReport.DeclaredSkips.Applied, artifactReport.DeclaredSkips.Declared)
		}
		fmt.Fprintf(writer, "%d\t%s\t%s\t%s\t%s\t%d\t%d\t%d\t%s\t%s\n",
			index+1, artifactReport.Id, artifactReport.Package, artifactReport.Version, artifactReport.Status,
			artifactReport.Counts[ruleNotCompliant], artifactReport.Counts[ruleSkipped],
			artifactReport.Violations, declared, artifactReport.Error)
	}
	writer.Flush()

	for _, artifactReport := range report.Artifacts {
		attention := []*guidelineRuleReport{}
		for _, rule := range artifactReport.Rules {
			if rule.Status == ruleNotCompliant || rule.Status == ruleSkipped {
				attention = append(attention, rule)
			}
		}
		if len(attention) == 0 {
			continue
		}

		fmt.Printf("\n===%s===\n", artifactReport.Id)
		writer = tabwriter.NewWriter(os.Stdout, 0, 8, 1, '\t', 0)
		fmt.Fprintf(writer, "#\tRule\tSeverity\tStatus\tElements\tDetail\n")
		for index, rule := range attention {
			elements := []string{}
			for _, component := range rule.ViolatedComponents {
				elements = append(elements, component.Id)
			}
			detail := rule.Actual
			if rule.Status == ruleSkipped {
				detail = "Skipped: " + rule.SkipReason
			}
			fmt.Fprintf(writer, "%d\t%s\t%s\t%s\t%s\t%s\n",
				index+1, rule.Id, rule.Severity, rule.Status, strings.Join(elements, ","), detail)
		}
		writer.Flush()
	}

	fmt.Printf("\n===Summary===\n")
	writer = tabwriter.NewWriter(os.Stdout, 0, 8, 1, '\t', 0)
	fmt.Fprintf(writer, "Environment:\t%s\n", report.Environment)
	fmt.Fprintf(writer, "Artifacts:\t%d\n", report.Summary.Artifacts)
	fmt.Fprintf(writer, "Compliant:\t%d\n", report.Summary.Compliant)
	fmt.Fprintf(writer, "Not compliant:\t%d\n", report.Summary.NotCompliant)
	fmt.Fprintf(writer, "Not finished:\t%d\n", report.Summary.NotFinished)
	fmt.Fprintf(writer, "Errors:\t%d\n", report.Summary.Errors)
	fmt.Fprintf(writer, "Violations (--fail-on %s):\t%d\n", report.FailOn, report.Summary.Violations)
	writer.Flush()
}

//applyUploadGuidelineSkips runs the guidelines on a just uploaded artifact and
//files its declared skips. Nothing declared means nothing to do and no call.
//Every failure is reported in the result, the upload goes on.
func applyUploadGuidelineSkips(client guidelineClient, target *guidelineTarget) *declaredSkipsReport {

	if len(target.Skips) == 0 {
		return nil
	}

	execution, err := executeGuidelines(client, target.ArtifactId, guidelineVersionActive, guidelineOptions{
		Wait:     true,
		Timeout:  defaultDeployTimeout,
		Interval: defaultDeployInterval,
	})
	if err == nil && !isGuidelineExecutionSettled(execution.ExecutionStatus) {
		err = fmt.Errorf("the guideline execution did not finish")
	}

	var guidelines []*cpiclient.DesignGuideline
	if err == nil {
		_, guidelines, err = client.ReadDesignGuidelineExecutionResult(target.ArtifactId, guidelineVersionActive, execution.ExecutionId)
	}

	if err != nil {
		log.Printf("Guideline skips of %s not applied: %s", target.ArtifactId, err)
		return &declaredSkipsReport{
			Declared: len(target.Skips),
			Failures: []string{err.Error()},
		}
	}

	report, _ := applyDeclaredGuidelineSkips(client, target, guidelineVersionActive, execution.ExecutionId, guidelines)
	return report
}

//describeGuidelineSkips renders a skip result for a table cell: "2/2", or
//"1/2 failed" when the tenant refused one
func describeGuidelineSkips(report *declaredSkipsReport) string {
	if report == nil {
		return "-"
	}
	text := fmt.Sprintf("%d/%d", report.Applied, report.Declared)
	if len(report.Failures) > 0 {
		text += " failed"
	}
	return text
}
