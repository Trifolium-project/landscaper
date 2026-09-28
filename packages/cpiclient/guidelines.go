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

package cpiclient

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net/http"
	"regexp"
	"strings"
)

//Design guidelines: SAP's own static checks of an integration flow. The calls
//are not in assets/IntegrationContent.yaml of older tenants' swagger, see
//changelog/0007-design-guidelines.md for what a tenant actually answers.

//ExecutionStatus of an artifact, that has never been checked. The tenant then
//returns one placeholder execution with an empty ExecutionId.
const GuidelineNotExecuted = "NOT_EXECUTED"

//DesignGuidelineExecution is one run of the design guidelines on an artifact.
//The tenant keeps only the latest one, an older ExecutionId is rejected.
type DesignGuidelineExecution struct {
	ExecutionId     string
	ArtifactVersion string
	//PASS or FAIL once finished, NOT_EXECUTED for the placeholder
	ExecutionStatus string
	//Epoch milliseconds, as a string
	ExecutionTime string
	ReportType    string
}

//DesignGuideline is the result of one rule within an execution
type DesignGuideline struct {
	GuidelineId   string
	GuidelineName string
	Category      string
	//High, Medium or Low
	Severity string
	//Applicable or Not Applicable
	Applicability string
	//Compliant, Non-Compliant or Not Applicable. A skipped rule keeps its
	//Compliance, only IsGuidelineSkipped changes.
	Compliance         string
	IsGuidelineSkipped bool
	SkipReason         string
	SkippedBy          string
	ExpectedKPI        string
	ActualKPI          string
	//Java map notation of the offending model elements:
	//"{CallActivity_59=Build Response, MessageFlow_6=HTTPS}"
	ViolatedComponents string
}

//ViolatedComponent is one model element a rule complains about
type ViolatedComponent struct {
	Id   string `json:"id"`
	Name string `json:"name"`
}

//Element ids of the BPMN model: CallActivity_59, MessageFlow_6, Process_1
var violatedComponentKey = regexp.MustCompile(`(?:^|, )([A-Za-z][A-Za-z0-9]*_[0-9]+)=`)

//Components splits ViolatedComponents into its elements. The split anchors on
//the element id pattern, so a comma inside a display name does not break it.
//Anything unrecognised yields an empty list, the raw string stays available.
func (guideline *DesignGuideline) Components() []ViolatedComponent {

	raw := strings.TrimSpace(guideline.ViolatedComponents)
	raw = strings.TrimPrefix(raw, "{")
	raw = strings.TrimSuffix(raw, "}")

	components := []ViolatedComponent{}
	matches := violatedComponentKey.FindAllStringSubmatchIndex(raw, -1)

	for index, match := range matches {
		end := len(raw)
		if index+1 < len(matches) {
			end = matches[index+1][0]
		}
		components = append(components, ViolatedComponent{
			Id:   raw[match[2]:match[3]],
			Name: raw[match[1]:end],
		})
	}

	return components
}

//ExecuteIntegrationDesigntimeArtifactGuidelines runs the design guidelines on
//an artifact version and returns the id of the execution. The tenant answers
//synchronously with the id as text/plain.
func (s *CPIClient) ExecuteIntegrationDesigntimeArtifactGuidelines(ArtifactId string, ArtifactVersion string) (string, error) {

	//No $format=json here: the function import answers it with 501 Not implemented
	url := fmt.Sprintf("https://" + s.URL + "/api/" + apiVersion + "/" + "ExecuteIntegrationDesigntimeArtifactsGuidelines?Id='" +
		ArtifactId + "'&Version='" + ArtifactVersion + "'")

	req, err := http.NewRequestWithContext(s.traceCtx, http.MethodPost, url, nil)
	if err != nil {
		return "", err
	}

	token, err := s.getCSRFToken()
	if err != nil {
		return "", err
	}
	req.Header.Set("X-CSRF-Token", token)
	//Errors come back as XML otherwise. The execution id stays text/plain.
	req.Header.Set("Accept", "application/json")

	body, _, err := s.doRequest(req)
	if err != nil {
		return "", odataError(err)
	}

	return parseExecutionId(body)
}

//parseExecutionId accepts the plain text answer the tenant gives, and the
//OData JSON wrapping of a function import result in case a tenant switches
func parseExecutionId(body []byte) (string, error) {

	text := strings.TrimSpace(string(body))

	if strings.HasPrefix(text, "{") {
		var data map[string]interface{}
		if err := json.Unmarshal(body, &data); err != nil {
			return "", err
		}
		if root, ok := data["d"].(map[string]interface{}); ok {
			for _, key := range []string{"ExecuteIntegrationDesigntimeArtifactsGuidelines", "ExecutionId"} {
				if id := jsonString(root, key); id != "" {
					return id, nil
				}
			}
		}
		return "", fmt.Errorf("Unexpected answer to the guideline execution: %s", text)
	}

	text = strings.Trim(text, `"`)
	if text == "" {
		return "", fmt.Errorf("The tenant returned no execution id for the guideline execution")
	}

	return text, nil
}

//ReadDesignGuidelineExecutions returns the executions of an artifact version.
//In practice that is the latest one, or the NOT_EXECUTED placeholder.
func (s *CPIClient) ReadDesignGuidelineExecutions(ArtifactId string, ArtifactVersion string) ([]*DesignGuidelineExecution, error) {

	url := fmt.Sprintf("https://" + s.URL + "/api/" + apiVersion + "/" + "IntegrationDesigntimeArtifacts(Id='" +
		ArtifactId + "',Version='" + ArtifactVersion + "')/DesignGuidelineExecutionResults?$format=json")

	req, err := http.NewRequestWithContext(s.traceCtx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	body, _, err := s.doRequest(req)
	if err != nil {
		return nil, odataError(err)
	}

	var data map[string]interface{}
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, err
	}

	root, ok := data["d"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("Unexpected response while reading guideline executions of %s", ArtifactId)
	}
	rawList, ok := root["results"].([]interface{})
	if !ok {
		return nil, fmt.Errorf("Unexpected response while reading guideline executions of %s", ArtifactId)
	}

	executions := []*DesignGuidelineExecution{}
	for _, element := range rawList {
		executionJson, ok := element.(map[string]interface{})
		if !ok {
			continue
		}
		executions = append(executions, parseDesignGuidelineExecution(executionJson))
	}

	return executions, nil
}

//ReadDesignGuidelineExecutionResult returns an execution together with the
//result of every rule
func (s *CPIClient) ReadDesignGuidelineExecutionResult(ArtifactId string, ArtifactVersion string, ExecutionId string) (*DesignGuidelineExecution, []*DesignGuideline, error) {

	url := fmt.Sprintf("https://" + s.URL + "/api/" + apiVersion + "/" + "IntegrationDesigntimeArtifacts(Id='" +
		ArtifactId + "',Version='" + ArtifactVersion + "')/DesignGuidelineExecutionResults('" + ExecutionId +
		"')?$expand=DesignGuidelines&$format=json")

	req, err := http.NewRequestWithContext(s.traceCtx, http.MethodGet, url, nil)
	if err != nil {
		return nil, nil, err
	}

	body, _, err := s.doRequest(req)
	if err != nil {
		return nil, nil, odataError(err)
	}

	var data map[string]interface{}
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, nil, err
	}

	root, ok := data["d"].(map[string]interface{})
	if !ok {
		return nil, nil, fmt.Errorf("Unexpected response while reading guideline execution %s of %s", ExecutionId, ArtifactId)
	}

	execution := parseDesignGuidelineExecution(root)

	guidelines := []*DesignGuideline{}
	if expanded, ok := root["DesignGuidelines"].(map[string]interface{}); ok {
		if rawList, ok := expanded["results"].([]interface{}); ok {
			for _, element := range rawList {
				guidelineJson, ok := element.(map[string]interface{})
				if !ok {
					continue
				}
				guidelines = append(guidelines, parseDesignGuideline(guidelineJson))
			}
		}
	}

	return execution, guidelines, nil
}

//SkipDesignGuideline skips a rule, or reverts the skip when skip is false. The
//skip is filed against an execution, but the tenant carries it over into the
//next executions of the artifact. A skip needs a reason, a revert does not.
func (s *CPIClient) SkipDesignGuideline(ArtifactId string, ArtifactVersion string, ExecutionId string,
	GuidelineId string, reason string, skip bool) error {

	url := fmt.Sprintf("https://" + s.URL + "/api/" + apiVersion + "/" + "IntegrationDesigntimeArtifacts(Id='" +
		ArtifactId + "',Version='" + ArtifactVersion + "')/$links/DesignGuidelineExecutionResults('" + ExecutionId + "')")

	//SAP's swagger spells the key "GudelineId", which the tenant rejects with
	//"Illegal argument for method call with message 'GudelineId'"
	payload := map[string]interface{}{
		"GuidelineId":        GuidelineId,
		"IsGuidelineSkipped": skip,
	}
	if skip {
		payload["SkipReason"] = reason
	}

	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(s.traceCtx, http.MethodPut, url, bytes.NewReader(encoded))
	if err != nil {
		return err
	}

	token, err := s.getCSRFToken()
	if err != nil {
		return err
	}
	req.Header.Set("X-CSRF-Token", token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	if _, _, err := s.doRequest(req); err != nil {
		return odataError(err)
	}

	return nil
}

func parseDesignGuidelineExecution(executionJson map[string]interface{}) *DesignGuidelineExecution {
	return &DesignGuidelineExecution{
		ExecutionId:     jsonString(executionJson, "ExecutionId"),
		ArtifactVersion: jsonString(executionJson, "ArtifactVersion"),
		ExecutionStatus: jsonString(executionJson, "ExecutionStatus"),
		ExecutionTime:   jsonString(executionJson, "ExecutionTime"),
		ReportType:      jsonString(executionJson, "ReportType"),
	}
}

func parseDesignGuideline(guidelineJson map[string]interface{}) *DesignGuideline {
	return &DesignGuideline{
		GuidelineId:        jsonString(guidelineJson, "GuidelineId"),
		GuidelineName:      jsonString(guidelineJson, "GuidelineName"),
		Category:           jsonString(guidelineJson, "Category"),
		Severity:           jsonString(guidelineJson, "Severity"),
		Applicability:      jsonString(guidelineJson, "Applicability"),
		Compliance:         jsonString(guidelineJson, "Compliance"),
		IsGuidelineSkipped: jsonBool(guidelineJson, "IsGuidelineSkipped"),
		SkipReason:         jsonString(guidelineJson, "SkipReason"),
		SkippedBy:          jsonString(guidelineJson, "SkippedBy"),
		ExpectedKPI:        jsonString(guidelineJson, "ExpectedKPI"),
		ActualKPI:          jsonString(guidelineJson, "ActualKPI"),
		ViolatedComponents: jsonString(guidelineJson, "ViolatedComponents"),
	}
}

//odataError replaces the raw OData error document doRequest returns with
//the message inside it, e.g. "SkipReason must not be empty." Both the JSON and
//the XML form are understood.
func odataError(err error) error {

	raw := []byte(strings.TrimSpace(err.Error()))

	var document struct {
		Error struct {
			Message struct {
				Value string `json:"value"`
			} `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(raw, &document) == nil && document.Error.Message.Value != "" {
		return fmt.Errorf("%s", document.Error.Message.Value)
	}

	var xmlDocument struct {
		Message string `xml:"message"`
	}
	if xml.Unmarshal(raw, &xmlDocument) == nil && xmlDocument.Message != "" {
		return fmt.Errorf("%s", xmlDocument.Message)
	}

	return err
}
