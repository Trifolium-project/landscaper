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
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
)

//Package level calls used by "package copy" and "package delete". What a
//tenant answers is recorded in changelog/0008-package-copy-delete.md.

//Mode of a package, that SAP delivers configure only
const PackageModeReadOnly = "READ_ONLY"

//Import modes of CopyIntegrationPackage
const (
	ImportModeOverwrite      = "OVERWRITE"
	ImportModeOverwriteMerge = "OVERWRITE_MERGE"
	ImportModeCreateCopy     = "CREATE_COPY"
)

//StatusError is a non 2xx answer, for callers that have to tell a missing
//entity or a conflict apart from any other failure
type StatusError struct {
	Code int
	//The message of the OData error document, or the raw body
	Message string
}

func (err *StatusError) Error() string {
	return err.Message
}

//HasStatus reports whether err is a StatusError with the given code
func HasStatus(err error, code int) bool {
	var statusError *StatusError
	return errors.As(err, &statusError) && statusError.Code == code
}

//PackageArtifact is one design time artifact of a package, of any type
type PackageArtifact struct {
	Id      string
	Version string
	Name    string
	//IntegrationFlow, ValueMapping, ScriptCollection or MessageMapping
	Type string
}

//Design time collections of a package and the type they hold
var packageArtifactCollections = []struct {
	Collection string
	Type       string
}{
	{"IntegrationDesigntimeArtifacts", "IntegrationFlow"},
	{"ValueMappingDesigntimeArtifacts", "ValueMapping"},
	{"ScriptCollectionDesigntimeArtifacts", "ScriptCollection"},
	{"MessageMappingDesigntimeArtifacts", "MessageMapping"},
}

//doStatusRequest sends a request and turns a non 2xx answer into a StatusError
//carrying the message of the OData error document
func (s *CPIClient) doStatusRequest(req *http.Request) ([]byte, error) {

	body, _, status, err := s.doRequestWithStatus(req)
	if err == nil {
		return body, nil
	}
	if status == 0 {
		return nil, err
	}

	return nil, &StatusError{Code: status, Message: odataError(err).Error()}
}

//CopyIntegrationPackage copies a package from Discover into Design and returns
//the package created. importMode is empty, OVERWRITE, OVERWRITE_MERGE or
//CREATE_COPY; suffix is only sent with CREATE_COPY. A tenant answers a package
//that is already in Design with 409 when no import mode is given, and an id
//unknown to Discover with 404.
func (s *CPIClient) CopyIntegrationPackage(DiscoverPackageId string, importMode string, suffix string) (*IntegrationPackage, error) {

	query := "Id='" + url.QueryEscape(DiscoverPackageId) + "'"
	if importMode != "" {
		query += "&ImportMode='" + importMode + "'"
	}
	if importMode == ImportModeCreateCopy && suffix != "" {
		query += "&Suffix='" + url.QueryEscape(suffix) + "'"
	}

	requestUrl := "https://" + s.URL + "/api/" + apiVersion + "/" + "CopyIntegrationPackage?" + query + "&$format=json"

	req, err := http.NewRequestWithContext(s.traceCtx, http.MethodPost, requestUrl, nil)
	if err != nil {
		return nil, err
	}

	token, err := s.getCSRFToken()
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-CSRF-Token", token)
	req.Header.Set("Accept", "application/json")

	body, err := s.doStatusRequest(req)
	if err != nil {
		return nil, err
	}

	var data map[string]interface{}
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, err
	}

	root, ok := data["d"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("Unexpected answer to the copy of package %s", DiscoverPackageId)
	}

	return parseIntegrationPackage(root), nil
}

//DeleteIntegrationPackage asks the tenant to delete a package. The tenant
//answers 202 and deletes asynchronously, so the package may still be readable
//for a moment. It does NOT check for deployed content: the runtime artifacts
//of a deleted package keep running.
func (s *CPIClient) DeleteIntegrationPackage(PackageId string) error {

	requestUrl := "https://" + s.URL + "/api/" + apiVersion + "/" + "IntegrationPackages('" + PackageId + "')"

	req, err := http.NewRequestWithContext(s.traceCtx, http.MethodDelete, requestUrl, nil)
	if err != nil {
		return err
	}

	token, err := s.getCSRFToken()
	if err != nil {
		return err
	}
	req.Header.Set("X-CSRF-Token", token)
	req.Header.Set("Accept", "application/json")

	_, err = s.doStatusRequest(req)
	return err
}

//ReadIntegrationPackageStatus reads a package like ReadIntegrationPackage, but
//returns a StatusError, so that a missing package can be told apart
func (s *CPIClient) ReadIntegrationPackageStatus(PackageId string) (*IntegrationPackage, error) {

	requestUrl := "https://" + s.URL + "/api/" + apiVersion + "/" + "IntegrationPackages('" + PackageId + "')?$format=json"

	req, err := http.NewRequestWithContext(s.traceCtx, http.MethodGet, requestUrl, nil)
	if err != nil {
		return nil, err
	}

	body, err := s.doStatusRequest(req)
	if err != nil {
		return nil, err
	}

	var data map[string]interface{}
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, err
	}

	root, ok := data["d"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("Unexpected answer while reading package %s", PackageId)
	}

	return parseIntegrationPackage(root), nil
}

//ReadPackageDesigntimeArtifacts returns the artifacts of every type a package
//holds. A collection the tenant does not offer is skipped.
func (s *CPIClient) ReadPackageDesigntimeArtifacts(PackageId string) ([]*PackageArtifact, error) {

	artifacts := []*PackageArtifact{}

	for _, collection := range packageArtifactCollections {

		requestUrl := "https://" + s.URL + "/api/" + apiVersion + "/" + "IntegrationPackages('" + PackageId + "')/" +
			collection.Collection + "?$format=json"

		req, err := http.NewRequestWithContext(s.traceCtx, http.MethodGet, requestUrl, nil)
		if err != nil {
			return nil, err
		}

		body, err := s.doStatusRequest(req)
		if err != nil {
			if collection.Type != "IntegrationFlow" && (HasStatus(err, http.StatusNotFound) || HasStatus(err, http.StatusNotImplemented)) {
				continue
			}
			return nil, err
		}

		results, err := odataResults(body)
		if err != nil {
			return nil, fmt.Errorf("Unexpected answer while reading %s of package %s", collection.Collection, PackageId)
		}

		for _, result := range results {
			artifacts = append(artifacts, &PackageArtifact{
				Id:      jsonString(result, "Id"),
				Version: jsonString(result, "Version"),
				Name:    jsonString(result, "Name"),
				Type:    collection.Type,
			})
		}
	}

	return artifacts, nil
}

//ReadIntegrationRuntimeArtifacts returns everything deployed on the tenant,
//following pagination
func (s *CPIClient) ReadIntegrationRuntimeArtifacts() ([]*IntegrationRuntimeArtifact, error) {

	artifacts := []*IntegrationRuntimeArtifact{}
	seen := map[string]bool{}

	for skip := 0; ; skip += collectionPageSize {

		requestUrl := "https://" + s.URL + "/api/" + apiVersion + "/" + "IntegrationRuntimeArtifacts?$format=json" +
			"&$top=" + strconv.Itoa(collectionPageSize) + "&$skip=" + strconv.Itoa(skip)

		req, err := http.NewRequestWithContext(s.traceCtx, http.MethodGet, requestUrl, nil)
		if err != nil {
			return nil, err
		}

		body, err := s.doStatusRequest(req)
		if err != nil {
			return nil, err
		}

		results, err := odataResults(body)
		if err != nil {
			return nil, fmt.Errorf("Unexpected answer while reading the runtime artifacts")
		}

		added := 0
		for _, result := range results {
			id := jsonString(result, "Id")
			//A tenant ignoring $skip would return the first page again
			if seen[id] {
				continue
			}
			seen[id] = true
			added++
			artifacts = append(artifacts, &IntegrationRuntimeArtifact{
				Id:         id,
				Version:    jsonString(result, "Version"),
				Name:       jsonString(result, "Name"),
				Type:       jsonString(result, "Type"),
				DeployedBy: jsonString(result, "DeployedBy"),
				DeployedOn: jsonString(result, "DeployedOn"),
				Status:     jsonString(result, "Status"),
			})
		}

		if len(results) < collectionPageSize || added == 0 {
			break
		}
	}

	return artifacts, nil
}

//odataResults unwraps d.results of a collection answer
func odataResults(body []byte) ([]map[string]interface{}, error) {

	var data map[string]interface{}
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, err
	}

	root, ok := data["d"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("no d in the answer")
	}
	rawList, ok := root["results"].([]interface{})
	if !ok {
		return nil, fmt.Errorf("no results in the answer")
	}

	results := []map[string]interface{}{}
	for _, element := range rawList {
		if result, ok := element.(map[string]interface{}); ok {
			results = append(results, result)
		}
	}

	return results, nil
}
