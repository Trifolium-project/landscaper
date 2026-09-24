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
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

//RuntimeErrorMessage is one message of the tenant's deployment error report.
//The tenant nests the real cause under childInstances, often several levels
//deep, with the outermost message saying only that deployment failed.
type RuntimeErrorMessage struct {
	SubsystemName string `json:"subsystemName"`
	//SubsystemPartName is SAP's own misspelling of the field, kept verbatim
	SubsystemPartName string   `json:"subsystemPartName,omitempty"`
	MessageId         string   `json:"messageId"`
	MessageText       string   `json:"messageText"`
	Parameters        []string `json:"parameter"`
}

//RuntimeErrorInformation is the parsed answer of the ErrorInformation endpoint
type RuntimeErrorInformation struct {
	Message RuntimeErrorMessage `json:"message"`
	//Parameters sits beside message, not inside it. A real tenant puts the
	//whole diagnostic here - "Script file 'x.groovy' not found" - while
	//message.messageText is empty, so dropping it loses the actual cause.
	Parameters     []string                   `json:"parameter,omitempty"`
	ChildInstances []*RuntimeErrorInformation `json:"childInstances"`
	//Text is every message of the tree, outermost first, joined for printing.
	//Filled by the client, not by the tenant.
	Text string `json:"-"`
}

//errorInformationJSON mirrors the payload. The tenant spells the fields in
//lowerCamel and puts the parameters of a message beside its text.
type errorInformationJSON struct {
	Message struct {
		SubsystemName string `json:"subsystemName"`
		//Both spellings appear in the wild; SAP's own payload misspells it
		SubsystemPartName  string        `json:"subsystemPartName"`
		SubsytemPartName   string        `json:"subsytemPartName"`
		MessageId          string        `json:"messageId"`
		MessageText        string        `json:"messageText"`
		Parameter          []interface{} `json:"parameter"`
	} `json:"message"`
	//Sibling of message, which is where a real tenant puts the diagnostic
	Parameter      []interface{}           `json:"parameter"`
	ChildInstances []*errorInformationJSON `json:"childInstances"`
}

//ReadIntegrationRuntimeArtifactErrorInformation returns why a deployment
//failed. A deployment that did not fail has no error information, which the
//tenant reports as 204 or 404 - both yield a nil result and no error, so the
//caller can ask unconditionally.
func (s *CPIClient) ReadIntegrationRuntimeArtifactErrorInformation(ArtifactId string) (*RuntimeErrorInformation, error) {

	url := fmt.Sprintf("https://" + s.URL + "/api/" + apiVersion + "/" +
		"IntegrationRuntimeArtifacts('" + ArtifactId + "')/ErrorInformation/$value")

	req, err := http.NewRequestWithContext(s.traceCtx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	body, _, status, err := s.doRequestWithStatus(req)
	if err != nil {
		//A runtime artifact without error information is the normal case for a
		//healthy deployment, not a failure of this call
		if status == http.StatusNotFound || status == http.StatusNoContent {
			return nil, nil
		}
		return nil, fmt.Errorf("Unable to read error information of %s: %s", ArtifactId, err)
	}

	return ParseRuntimeErrorInformation(body)
}

//ParseRuntimeErrorInformation turns the payload into the tree and fills Text.
//Exported so the parsing can be tested against recorded answers without a tenant.
func ParseRuntimeErrorInformation(body []byte) (*RuntimeErrorInformation, error) {

	//An empty body is how a 204 arrives, and how some tenants answer for an
	//artifact that deployed cleanly
	if len(strings.TrimSpace(string(body))) == 0 {
		return nil, nil
	}

	parsed := &errorInformationJSON{}
	if err := json.Unmarshal(body, parsed); err != nil {
		//The endpoint is documented as JSON, but a tenant that answers with a
		//plain string still carries the information a caller needs
		text := strings.TrimSpace(string(body))
		if text == "" || text == "null" {
			return nil, nil
		}
		return &RuntimeErrorInformation{
			Message: RuntimeErrorMessage{MessageText: text},
			Text:    text,
		}, nil
	}

	information := convertErrorInformation(parsed)
	if information == nil {
		return nil, nil
	}

	information.Text = strings.Join(flattenErrorInformation(information), "\n")

	return information, nil
}

//convertErrorInformation maps the payload onto the exported tree
func convertErrorInformation(parsed *errorInformationJSON) *RuntimeErrorInformation {

	if parsed == nil {
		return nil
	}

	partName := parsed.Message.SubsystemPartName
	if partName == "" {
		partName = parsed.Message.SubsytemPartName
	}

	information := &RuntimeErrorInformation{
		Message: RuntimeErrorMessage{
			SubsystemName:     parsed.Message.SubsystemName,
			SubsystemPartName: partName,
			MessageId:         parsed.Message.MessageId,
			MessageText:       parsed.Message.MessageText,
			Parameters:        stringParameters(parsed.Message.Parameter),
		},
		Parameters: stringParameters(parsed.Parameter),
	}

	for _, child := range parsed.ChildInstances {
		converted := convertErrorInformation(child)
		if converted != nil {
			information.ChildInstances = append(information.ChildInstances, converted)
		}
	}

	//Nothing at all was reported
	if information.Message.MessageText == "" && information.Message.MessageId == "" &&
		len(information.Message.Parameters) == 0 && len(information.Parameters) == 0 &&
		len(information.ChildInstances) == 0 {
		return nil
	}

	return information
}

//stringParameters renders the parameter list. The tenant sends strings, but a
//number or a null in the array must not lose the rest of the message.
func stringParameters(values []interface{}) []string {

	parameters := []string{}
	for _, value := range values {
		if value == nil {
			continue
		}
		if text, ok := value.(string); ok {
			parameters = append(parameters, text)
			continue
		}
		parameters = append(parameters, fmt.Sprintf("%v", value))
	}

	if len(parameters) == 0 {
		return nil
	}

	return parameters
}

//flattenErrorInformation walks the tree outermost first and renders one line
//per message. The parameters are appended because the tenant frequently puts
//the offending resource name there rather than in the text.
func flattenErrorInformation(information *RuntimeErrorInformation) []string {

	if information == nil {
		return nil
	}

	lines := []string{}

	line := strings.TrimSpace(information.Message.MessageText)
	if line == "" {
		line = information.Message.MessageId
	}
	if len(information.Message.Parameters) > 0 {
		parameters := strings.Join(information.Message.Parameters, ", ")
		if line == "" {
			line = parameters
		} else {
			line = line + " [" + parameters + "]"
		}
	}
	if line != "" {
		lines = append(lines, line)
	}

	//The parameters beside the message carry the diagnostic the operator needs
	//- the tenant sends a multi line explanation here while messageText is
	//empty - so each of their lines becomes a line of its own
	for _, parameter := range information.Parameters {
		lines = append(lines, parameterLines(parameter)...)
	}

	for _, child := range information.ChildInstances {
		lines = append(lines, flattenErrorInformation(child)...)
	}

	return lines
}

//instanceMessageJSON is the second shape the tenant uses. It arrives as a JSON
//string inside a parameter rather than as the documented tree, spells the
//children childMessageInstances, and carries a plain string as the message.
type instanceMessageJSON struct {
	Message               string                 `json:"message"`
	Parameters            []interface{}          `json:"parameters"`
	ChildMessageInstances []*instanceMessageJSON `json:"childMessageInstances"`
}

//parameterLines renders one parameter. A parameter is usually a multi line
//explanation, but it can also be a whole nested error document, in which case
//printing it verbatim buries the cause in JSON punctuation.
func parameterLines(parameter string) []string {

	trimmed := strings.TrimSpace(parameter)
	if trimmed == "" {
		return nil
	}

	if strings.HasPrefix(trimmed, "{") && strings.Contains(trimmed, "childMessageInstances") {
		nested := &instanceMessageJSON{}
		if err := json.Unmarshal([]byte(trimmed), nested); err == nil {
			if lines := flattenInstanceMessage(nested); len(lines) > 0 {
				return lines
			}
		}
	}

	//A nested document that the tenant truncated itself cannot be parsed, and
	//its newlines are still escaped because the inner JSON was never decoded.
	//Unescaping them turns one unreadable blob into readable lines.
	if !strings.Contains(trimmed, "\n") && strings.Contains(trimmed, `\n`) {
		trimmed = strings.ReplaceAll(trimmed, `\n`, "\n")
	}

	lines := []string{}
	for _, line := range strings.Split(trimmed, "\n") {
		line = strings.TrimSpace(line)
		//A caret line under a syntax error carries no information on its own
		if line == "" || strings.Trim(line, "*^ ") == "" {
			continue
		}
		lines = append(lines, line)
	}

	return lines
}

//flattenInstanceMessage walks the childMessageInstances form. The innermost
//cause is the useful one, so every level is kept in order.
func flattenInstanceMessage(message *instanceMessageJSON) []string {

	if message == nil {
		return nil
	}

	lines := []string{}
	for _, parameter := range stringParameters(message.Parameters) {
		lines = append(lines, parameterLines(parameter)...)
	}

	for _, child := range message.ChildMessageInstances {
		lines = append(lines, flattenInstanceMessage(child)...)
	}

	//Deduplicate: the same cause is usually repeated at every level
	unique := []string{}
	seen := map[string]bool{}
	for _, line := range lines {
		if seen[line] {
			continue
		}
		seen[line] = true
		unique = append(unique, line)
	}

	return unique
}
