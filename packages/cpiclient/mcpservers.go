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
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"net/url"
	"strings"
)

//MCP Server artifacts (Integration Suite, runtime profile Integration Cell).
//The OData API of /api/v1 does not know this artifact type: its $metadata has
//no entity set for it and IntegrationDesigntimeArtifacts of a package holding
//only MCP servers is empty. The Integration Suite web UI reaches them through
//its own, undocumented API on the design time host, which accepts a browser
//session only - an OAuth client credentials token is answered with the login
//page. See changelog/0009-mcp-server-artifacts.md.

//Artifact type of an MCP server in the UI API
const ArtifactTypeMCPServer = "MCPSERVER"

//Version the UI API reports for an MCP server that has unsaved changes
const MCPServerVersionDraft = "Draft"

//ErrNoUISession is returned by every MCP server call of a system without
//uiHost and uiCookie
var ErrNoUISession = errors.New("MCP Server artifacts are not available through the OData API. " +
	"Set uiHost and uiCookie of the system to reach them through the Integration Suite UI, see the README")

//ErrUISessionExpired: the UI answers an expired or invalid session with its
//login page and status 200, not with 401
var ErrUISessionExpired = errors.New("the Integration Suite UI answered with its login page: " +
	"the session in uiCookie is not valid any more, copy a fresh cookie from the browser")

//MCPServer is one MCP server of a package, as the UI API lists it
type MCPServer struct {
	//Technical id, equal to Bundle-SymbolicName of the archive
	Id string
	//Display name, Bundle-Name of the archive
	Name        string
	Version     string
	Description string
	PackageId   string
	//Id of the server in the UI registry, needed by every call on the server
	RegistryId string
}

//SetUISession enables the MCP server calls. host may be given as a URL copied
//from the browser, only the host name is kept.
func (s *CPIClient) SetUISession(host string, cookie string) {
	host = strings.TrimSpace(host)
	host = strings.TrimPrefix(strings.TrimPrefix(host, "https://"), "http://")
	if index := strings.Index(host, "/"); index >= 0 {
		host = host[:index]
	}
	s.UIHost = host
	s.UICookie = strings.TrimSpace(cookie)
}

//HasUISession reports whether the MCP server calls can be made
func (s *CPIClient) HasUISession() bool {
	return s.UIHost != "" && s.UICookie != ""
}

//doUIRequest sends a request to the UI API with the session cookie instead of
//the OAuth token, and turns the login page into ErrUISessionExpired
func (s *CPIClient) doUIRequest(req *http.Request) ([]byte, error) {
	body, _, err := s.doUIRequestWithHeaders(req)
	return body, err
}

func (s *CPIClient) doUIRequestWithHeaders(req *http.Request) ([]byte, http.Header, error) {

	if !s.HasUISession() {
		return nil, nil, ErrNoUISession
	}

	req.Header.Set("Cookie", s.UICookie)
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	//Without it the artifact list of a package is empty
	req.Header.Set("x-navcontext-header", "Integration")
	if req.Header.Get("Accept") == "" {
		req.Header.Set("Accept", "application/json")
	}

	//Recorded with the cookie redacted, see auditlog.RedactHeaders
	call := s.Logger.StartHTTP(req)
	defer call.Flush()

	//A client of its own and without a cookie jar: the session is the cookie
	//header as given, the jar of s.Client would add a second JSESSIONID to it
	if s.uiClient == nil {
		s.uiClient = &http.Client{}
	}

	resp, err := s.uiClient.Do(req)
	if err != nil {
		call.SetError(err)
		log.Printf("HTTP request error: %s", err)
		return nil, nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		call.SetError(err)
		return nil, nil, err
	}
	call.SetResponse(resp.StatusCode, resp.Header, body)

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden || isHTMLPage(resp.Header) {
		return nil, nil, ErrUISessionExpired
	}

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, nil, &StatusError{Code: resp.StatusCode, Message: odataError(fmt.Errorf("%s", body)).Error()}
	}

	return body, resp.Header, nil
}

//uiCSRFToken fetches a token for a mutating UI call. Any GET of the UI answers
//X-CSRF-Token: Fetch, /api/1.0/user is the smallest.
func (s *CPIClient) uiCSRFToken() (string, error) {

	req, err := http.NewRequestWithContext(s.traceCtx, http.MethodGet, s.uiURL("/api/1.0/user"), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("X-CSRF-Token", "Fetch")

	_, headers, err := s.doUIRequestWithHeaders(req)
	if err != nil {
		return "", err
	}
	token := headers.Get("X-CSRF-Token")
	if token == "" {
		return "", fmt.Errorf("the Integration Suite UI did not hand out a CSRF token")
	}

	return token, nil
}

//doUIWrite sends a mutating UI request with a fresh CSRF token
func (s *CPIClient) doUIWrite(method string, path string, contentType string, body []byte, accept string) ([]byte, error) {

	token, err := s.uiCSRFToken()
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(s.traceCtx, method, s.uiURL(path), bytes.NewBuffer(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-CSRF-Token", token)
	req.Header.Set("Content-Type", contentType)
	if accept != "" {
		req.Header.Set("Accept", accept)
	}

	return s.doUIRequest(req)
}

//isHTMLPage: no call of the UI API answers with HTML, only the login redirect does
func isHTMLPage(headers http.Header) bool {
	mediaType, _, err := mime.ParseMediaType(headers.Get("Content-Type"))
	return err == nil && mediaType == "text/html"
}

//uiURL builds an URL on the design time host of the UI
func (s *CPIClient) uiURL(path string) string {
	return "https://" + s.UIHost + path
}

//ReadMCPServers lists the MCP servers of a package. A package that does not
//exist is a StatusError with code 404.
func (s *CPIClient) ReadMCPServers(PackageId string) ([]*MCPServer, error) {

	requestUrl := s.uiURL("/odata/1.0/workspace.svc/ContentEntities.ContentPackages('" + PackageId + "')/Artifacts?$format=json")

	req, err := http.NewRequestWithContext(s.traceCtx, http.MethodGet, requestUrl, nil)
	if err != nil {
		return nil, err
	}

	body, err := s.doUIRequest(req)
	if err != nil {
		return nil, err
	}

	return parseMCPServers(body, PackageId)
}

//parseMCPServers keeps the MCP servers of the artifact list of a package. The
//list holds artifacts of every type, iFlows included.
func parseMCPServers(body []byte, PackageId string) ([]*MCPServer, error) {

	results, err := odataResults(body)
	if err != nil {
		return nil, fmt.Errorf("Unexpected answer to the artifact list of package %s: %s", PackageId, err)
	}

	servers := []*MCPServer{}
	for _, result := range results {
		if jsonString(result, "Type") != ArtifactTypeMCPServer {
			continue
		}
		servers = append(servers, &MCPServer{
			Id:          jsonString(result, "Name"),
			Name:        jsonString(result, "DisplayName"),
			Version:     jsonString(result, "Version"),
			Description: jsonString(result, "Description"),
			PackageId:   PackageId,
			RegistryId:  jsonString(result, "reg_id"),
		})
	}

	return servers, nil
}

//ReadPackageRegistryId reads the id of a package in the UI registry, which the
//calls on single servers take instead of the package id
func (s *CPIClient) ReadPackageRegistryId(PackageId string) (string, error) {

	requestUrl := s.uiURL("/odata/1.0/workspace.svc/ContentEntities.ContentPackages('" + PackageId + "')?$format=json")

	req, err := http.NewRequestWithContext(s.traceCtx, http.MethodGet, requestUrl, nil)
	if err != nil {
		return "", err
	}

	body, err := s.doUIRequest(req)
	if err != nil {
		return "", err
	}

	var data map[string]interface{}
	if err := json.Unmarshal(body, &data); err != nil {
		return "", err
	}
	root, ok := data["d"].(map[string]interface{})
	if !ok || jsonString(root, "reg_id") == "" {
		return "", fmt.Errorf("Unexpected answer to the read of package %s", PackageId)
	}

	return jsonString(root, "reg_id"), nil
}

//DownloadMCPServer exports the bundle of an MCP server, the same zip the
//Download action of the UI hands out
func (s *CPIClient) DownloadMCPServer(PackageRegistryId string, server *MCPServer) ([]byte, error) {

	requestUrl := s.uiURL("/api/1.0/workspace/" + PackageRegistryId + "/artifacts/" + server.RegistryId +
		"/entities/" + server.RegistryId + "?export=true")

	req, err := http.NewRequestWithContext(s.traceCtx, http.MethodGet, requestUrl, nil)
	if err != nil {
		return nil, err
	}
	//The tenant answers Accept application/octet-stream or application/zip
	//with an empty 500, although the zip comes back as application/octet-stream
	req.Header.Set("Accept", "*/*")

	return s.doUIRequest(req)
}

//mcpArtifactPath is the editor model of a server. The read answers under the
//id and the display name alike; the UI saves and deploys under the id.
func mcpArtifactPath(PackageRegistryId string, server *MCPServer) string {
	return "/api/1.0/workspace/" + PackageRegistryId + "/artifacts/" + server.RegistryId +
		"/entities/" + server.RegistryId + "/mcpartifacts/" + url.PathEscape(server.Id)
}

//LockMCPServer takes the edit lock, what Edit in the UI does. Without it the
//tenant refuses every save with 500 "Update artifact in content store failed".
func (s *CPIClient) LockMCPServer(PackageRegistryId string, server *MCPServer) error {
	_, err := s.doUIWrite(http.MethodPut, "/api/1.0/workspace/"+PackageRegistryId+"/artifacts/"+server.RegistryId+"?webdav=LOCK",
		"application/json", nil, "")
	return err
}

//UnlockMCPServer releases the edit lock, what Cancel or leaving the editor does
func (s *CPIClient) UnlockMCPServer(PackageRegistryId string, server *MCPServer) error {
	_, err := s.doUIWrite(http.MethodPut, "/api/1.0/workspace/"+PackageRegistryId+"/artifacts/"+server.RegistryId+"?webdav=UNLOCK",
		"application/json", nil, "")
	return err
}

//ReadMCPServerModel reads the editor model of a server, the document the UI
//edits and saves back as a whole
func (s *CPIClient) ReadMCPServerModel(PackageRegistryId string, server *MCPServer) (map[string]interface{}, error) {

	req, err := http.NewRequestWithContext(s.traceCtx, http.MethodGet, s.uiURL(mcpArtifactPath(PackageRegistryId, server)), nil)
	if err != nil {
		return nil, err
	}

	body, err := s.doUIRequest(req)
	if err != nil {
		return nil, err
	}

	model := map[string]interface{}{}
	if err := json.Unmarshal(body, &model); err != nil {
		return nil, fmt.Errorf("Unexpected answer to the read of MCP server %s: %s", server.Id, err)
	}

	return model, nil
}

//UpdateMCPServerModel saves the editor model, which is what Save in the UI does.
//The server becomes a draft until it is deployed. It has to be locked first.
func (s *CPIClient) UpdateMCPServerModel(PackageRegistryId string, server *MCPServer, model map[string]interface{}) error {

	body, err := json.Marshal(model)
	if err != nil {
		return err
	}

	_, err = s.doUIWrite(http.MethodPut, mcpArtifactPath(PackageRegistryId, server), "application/json", body, "")
	return err
}

//CreateMCPServer creates a server in a package from the body of
//mcpserver.CreatePayload and returns the registry id of the new server
func (s *CPIClient) CreateMCPServer(PackageRegistryId string, payload map[string]interface{}) (string, error) {

	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	answer, err := s.doUIWrite(http.MethodPost, "/api/1.0/workspace/"+PackageRegistryId+"/mcpartifacts/", "application/json", body, "")
	if err != nil {
		return "", err
	}

	var created map[string]interface{}
	if err := json.Unmarshal(answer, &created); err != nil || jsonString(created, "id") == "" {
		return "", fmt.Errorf("Unexpected answer to the creation of an MCP server")
	}

	return jsonString(created, "id"), nil
}

//DeployMCPServer deploys the saved state of a server and returns the task id
func (s *CPIClient) DeployMCPServer(PackageRegistryId string, server *MCPServer, runtimeLocation string, virtualHost string) (string, error) {

	path := mcpArtifactPath(PackageRegistryId, server) + "?action=DEPLOY&runtimeLocation=" +
		url.QueryEscape(runtimeLocation) + "&virtualHost=" + url.QueryEscape(virtualHost)

	answer, err := s.doUIWrite(http.MethodPut, path, "application/json", []byte("{}"), "")
	if err != nil {
		return "", err
	}

	var started map[string]interface{}
	if err := json.Unmarshal(answer, &started); err != nil || jsonString(started, "taskId") == "" {
		return "", fmt.Errorf("Unexpected answer to the deployment of MCP server %s", server.Id)
	}

	return jsonString(started, "taskId"), nil
}

//IntegrationCellArtifact is a deployed artifact of an Integration Cell runtime,
//as Monitor -> Manage Integration Content lists it
type IntegrationCellArtifact struct {
	//Runtime id, changes with every deployment
	Id           string `xml:"id"`
	SymbolicName string `xml:"symbolicName"`
	Name         string `xml:"name"`
	Version      string `xml:"version"`
	//INTEGRATION_FLOW, MCP_SERVER, ...
	Type string `xml:"type"`
	//DEPLOYED, UNDEPLOYING, DELETED, ...
	DeployState string `xml:"deployState"`
	//STARTED, STARTING, STOPPING, ERROR, ...
	SemanticState     string `xml:"semanticState"`
	DeployedBy        string `xml:"deployedBy"`
	DeployedOn        string `xml:"deployedOn"`
	TenantId          string `xml:"tenantId"`
	RuntimeLocationId string `xml:"runtimeLocationId"`
}

//Artifact type of an MCP server in the runtime
const IntegrationCellTypeMCPServer = "MCP_SERVER"

//ReadIntegrationCellArtifacts lists what is deployed on an Integration Cell
//runtime. The v1 runtime API does not list these artifacts.
func (s *CPIClient) ReadIntegrationCellArtifacts(runtimeLocation string) ([]*IntegrationCellArtifact, error) {

	requestUrl := s.uiURL("/Operations/com.sap.it.op.tmn.commands.dashboard.webui.IntegrationComponentsListCommand?runtimeLocationId=" +
		url.QueryEscape(runtimeLocation))

	req, err := http.NewRequestWithContext(s.traceCtx, http.MethodGet, requestUrl, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/xml")

	body, err := s.doUIRequest(req)
	if err != nil {
		return nil, err
	}

	artifacts, err := parseIntegrationCellArtifacts(body)
	if err != nil {
		return nil, err
	}
	for _, artifact := range artifacts {
		if artifact.RuntimeLocationId == "" {
			artifact.RuntimeLocationId = runtimeLocation
		}
	}

	return artifacts, nil
}

func parseIntegrationCellArtifacts(body []byte) ([]*IntegrationCellArtifact, error) {

	var list struct {
		Artifacts []*IntegrationCellArtifact `xml:"artifactInformations"`
	}
	if err := xml.Unmarshal(body, &list); err != nil {
		return nil, fmt.Errorf("Unexpected answer to the list of the Integration Cell runtime: %s", err)
	}
	if list.Artifacts == nil {
		list.Artifacts = []*IntegrationCellArtifact{}
	}

	return list.Artifacts, nil
}

//UndeployIntegrationCellArtifact removes a deployed artifact from an
//Integration Cell runtime. The runtime takes it down in the background.
func (s *CPIClient) UndeployIntegrationCellArtifact(artifact *IntegrationCellArtifact) error {

	form := url.Values{}
	form.Set("artifactIds", artifact.Id)
	form.Set("tenantId", artifact.TenantId)
	form.Set("runtimeLocationId", artifact.RuntimeLocationId)

	_, err := s.doUIWrite(http.MethodPost, "/Operations/com.sap.it.nm.commands.deploy.DeleteContentCommand",
		"application/x-www-form-urlencoded; charset=UTF-8", []byte(form.Encode()), "application/xml")
	return err
}

//MCPDeployTask is the state of a deployment task, as the UI polls it after Deploy
type MCPDeployTask struct {
	//DEPLOYING, SUCCESS or FAIL
	Status        string
	Version       string
	DeployState   string
	SemanticState string
	DeployedBy    string
	DeployedOn    string
}

//States of a deployment task
const (
	MCPDeployTaskRunning = "DEPLOYING"
	MCPDeployTaskSuccess = "SUCCESS"
	MCPDeployTaskFailed  = "FAIL"
)

//ReadMCPDeployTask reads a task of DeployMCPServer. Only the task tells that a
//deployment failed: the Integration Cell list then keeps the server STARTING.
func (s *CPIClient) ReadMCPDeployTask(TaskId string, server *MCPServer) (*MCPDeployTask, error) {

	requestUrl := s.uiURL("/api/1.0/deploystatus/" + url.PathEscape(TaskId) + "?bundleType=" + ArtifactTypeMCPServer +
		"&symbolicName=" + url.QueryEscape(server.RegistryId))

	req, err := http.NewRequestWithContext(s.traceCtx, http.MethodGet, requestUrl, nil)
	if err != nil {
		return nil, err
	}

	body, err := s.doUIRequest(req)
	if err != nil {
		return nil, err
	}

	var task map[string]interface{}
	if err := json.Unmarshal(body, &task); err != nil {
		return nil, fmt.Errorf("Unexpected answer to the deploy status of MCP server %s: %s", server.Id, err)
	}

	return &MCPDeployTask{
		Status:        jsonString(task, "status"),
		Version:       jsonString(task, "version"),
		DeployState:   jsonString(task, "deployState"),
		SemanticState: jsonString(task, "semanticState"),
		DeployedBy:    jsonString(task, "deployedBy"),
		DeployedOn:    jsonString(task, "deployedOn"),
	}, nil
}
