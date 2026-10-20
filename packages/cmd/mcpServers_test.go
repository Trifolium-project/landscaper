package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Trifolium-project/landscaper/packages/cpiclient"
	"github.com/Trifolium-project/landscaper/packages/iflow"
	"github.com/Trifolium-project/landscaper/packages/landscape"
)

//Session cookie the stub UI accepts. Anything else is answered with the login
//page, as the real UI does.
const stubUICookie = "JSESSIONID=s%3Astub-session; __VCAP_ID__=stub-instance"

//Registry id of the package of the recording
const recordedPackageRegistryId = "7b80cd2e19d746588d857652150d17a5"

//stubMCPServer is one MCP server the stub UI holds
type stubMCPServer struct {
	Id         string
	Name       string
	Version    string
	RegistryId string
	PackageId  string
	//Answer of the export
	Export []byte
	//Editor model, as the last save left it
	Model map[string]interface{}
}

//stubMCPDeployment is an MCP server on the stub Integration Cell
type stubMCPDeployment struct {
	RuntimeId     string
	SymbolicName  string
	Name          string
	Version       string
	SemanticState string
}

//stubMCP imitates the UI API: artifact lists, package reads, export, editor
//model, create, deploy and the Integration Cell runtime
type stubMCP struct {
	Servers []*stubMCPServer
	//Package id to registry id. A package without one gets "reg-<id>".
	PackageRegistryIds map[string]string
	Deployments        []*stubMCPDeployment
	//Answer every call with the login page, as for an expired session
	SessionExpired bool
	//Headers of every UI call, for the assertions on what was sent
	Headers []http.Header
	//Bodies of the create and save calls, and the forms of undeploy calls
	Created []map[string]interface{}
	Saved   []map[string]interface{}
	Forms   []url.Values
	//Every mutating call carried the CSRF token handed out
	MissingToken bool
	//The deploy task answers FAIL, while the runtime keeps the server STARTING
	FailDeployment bool
	//Registry ids of the servers under an edit lock, and every lock call
	Locked    map[string]bool
	LockCalls []string
}

func (mcp *stubMCP) packageRegistryId(packageId string) string {
	if id, ok := mcp.PackageRegistryIds[packageId]; ok {
		return id
	}
	return "reg-" + packageId
}

func (mcp *stubMCP) serverByRegistryId(registryId string) *stubMCPServer {
	for _, server := range mcp.Servers {
		if server.RegistryId == registryId {
			return server
		}
	}
	return nil
}

func (tenant *stubTenant) handleMCP(writer http.ResponseWriter, request *http.Request) bool {

	mcp := tenant.MCP
	path := request.URL.Path
	if !strings.HasPrefix(path, "/odata/1.0/workspace.svc/") && !strings.HasPrefix(path, "/api/1.0/") &&
		!strings.HasPrefix(path, "/Operations/") {
		return false
	}
	if mcp == nil {
		writer.WriteHeader(http.StatusNotFound)
		return true
	}

	mcp.Headers = append(mcp.Headers, request.Header.Clone())

	if mcp.SessionExpired || request.Header.Get("Cookie") != stubUICookie {
		writer.Header().Set("Content-Type", "text/html; charset=utf-8")
		writer.Write([]byte("<html><head></head><body>login</body></html>"))
		return true
	}
	if request.Method != http.MethodGet && request.Header.Get("X-CSRF-Token") != "stub-token" {
		mcp.MissingToken = true
		writer.WriteHeader(http.StatusForbidden)
		return true
	}

	body, _ := io.ReadAll(request.Body)
	query := request.URL.Query()

	switch {

	case strings.HasPrefix(path, "/odata/1.0/workspace.svc/ContentEntities.ContentPackages("):
		packageId := strings.TrimSuffix(strings.TrimPrefix(path, "/odata/1.0/workspace.svc/ContentEntities.ContentPackages('"), "')/Artifacts")
		packageId = strings.TrimSuffix(packageId, "')")
		if !tenant.Packages[packageId] {
			writer.Header().Set("Content-Type", "application/json")
			writer.WriteHeader(http.StatusNotFound)
			writer.Write([]byte(`{"error":{"code":null,"message":{"lang":"en","value":"Entity not found"}}}`))
			return true
		}
		writer.Header().Set("Content-Type", "application/json")
		if !strings.HasSuffix(path, "/Artifacts") {
			fmt.Fprintf(writer, `{"d":{"TechnicalName":"%s","reg_id":"%s","Version":"1.0.0"}}`, packageId, mcp.packageRegistryId(packageId))
			return true
		}
		results := []map[string]interface{}{}
		for _, server := range mcp.Servers {
			if server.PackageId == packageId {
				results = append(results, map[string]interface{}{"Name": server.Id, "Type": "MCPSERVER",
					"DisplayName": server.Name, "reg_id": server.RegistryId, "Version": server.Version})
			}
		}
		//The list holds the iFlows of the package as well
		results = append(results, map[string]interface{}{"Name": "Order_API_TEST_HARNESS", "Type": "IFlow", "reg_id": "0000000a", "Version": "1.0.3"})
		json.NewEncoder(writer).Encode(map[string]interface{}{"d": map[string]interface{}{"results": results}})

	case path == "/api/1.0/user":
		writer.Header().Set("Content-Type", "application/json")
		writer.Write([]byte(`{}`))

	case strings.HasPrefix(path, "/api/1.0/workspace/") && query.Get("export") == "true":
		//The tenant answers these Accept values with an empty 500
		accept := request.Header.Get("Accept")
		if accept == "" || accept == "application/octet-stream" || accept == "application/zip" {
			writer.WriteHeader(http.StatusInternalServerError)
			return true
		}
		segments := strings.Split(path, "/")
		server := mcp.serverByRegistryId(segments[len(segments)-1])
		if server == nil || server.Export == nil {
			writer.WriteHeader(http.StatusInternalServerError)
			return true
		}
		writer.Header().Set("Content-Type", "application/octet-stream")
		writer.Write(server.Export)

	case request.Method == http.MethodPost && strings.HasSuffix(path, "/mcpartifacts/"):
		payload := map[string]interface{}{}
		json.Unmarshal(body, &payload)
		mcp.Created = append(mcp.Created, payload)
		packageRegistryId := strings.Split(path, "/")[4]
		packageId := ""
		for id := range tenant.Packages {
			if mcp.packageRegistryId(id) == packageRegistryId {
				packageId = id
			}
		}
		server := &stubMCPServer{
			Id: fmt.Sprint(payload["id"]), Name: fmt.Sprint(payload["name"]), Version: "1.0.0",
			RegistryId: fmt.Sprintf("new%d", len(mcp.Servers)), PackageId: packageId,
			Model: newStubMCPModel(fmt.Sprint(payload["id"]), fmt.Sprint(payload["name"])),
		}
		mcp.Servers = append(mcp.Servers, server)
		writer.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(writer, `{"id":"%s","name":"%s","type":"MCPSERVER"}`, server.RegistryId, server.Name)

	case request.Method == http.MethodPut && query.Get("webdav") != "":
		registryId := path[strings.LastIndex(path, "/")+1:]
		if mcp.serverByRegistryId(registryId) == nil {
			writer.WriteHeader(http.StatusNotFound)
			return true
		}
		if mcp.Locked == nil {
			mcp.Locked = map[string]bool{}
		}
		mcp.LockCalls = append(mcp.LockCalls, query.Get("webdav")+" "+registryId)
		mcp.Locked[registryId] = query.Get("webdav") == "LOCK"
		writer.Header().Set("Content-Type", "application/json")
		writer.Write([]byte(`{}`))

	case strings.Contains(path, "/mcpartifacts/"):
		segments := strings.Split(path, "/")
		server := mcp.serverByRegistryId(segments[len(segments)-3])
		//The UI saves and deploys under the id
		if server == nil || segments[len(segments)-1] != server.Id {
			writer.WriteHeader(http.StatusNotFound)
			return true
		}
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodGet:
			json.NewEncoder(writer).Encode(server.Model)
		case request.Method == http.MethodPut && query.Get("action") == "DEPLOY":
			manifest := server.Model["iflow"].(map[string]interface{})["propertyViewModel"].(map[string]interface{})["manifestContent"].(map[string]interface{})
			state := "STARTED"
			if mcp.FailDeployment {
				state = "STARTING"
			}
			mcp.Deployments = append(mcp.Deployments, &stubMCPDeployment{
				RuntimeId: fmt.Sprintf("runtime-%d", len(mcp.Deployments)), SymbolicName: server.Id,
				Name: server.Name, Version: fmt.Sprint(manifest["Bundle-Version"]), SemanticState: state,
			})
			fmt.Fprintf(writer, `{"taskId":"task-%d","iFlowModelTO":{}}`, len(mcp.Deployments)-1)
		case request.Method == http.MethodPut && !mcp.Locked[server.RegistryId]:
			//What the tenant answers a save without the edit lock
			writer.WriteHeader(http.StatusInternalServerError)
			writer.Write([]byte(`{"errorCode":"ARTIFACT_UPDATE_FAILED","errorMessage":"MCP Artifact update failed: Update artifact in content store failed"}`))
		case request.Method == http.MethodPut:
			model := map[string]interface{}{}
			json.Unmarshal(body, &model)
			mcp.Saved = append(mcp.Saved, model)
			server.Model = model
			server.Name = fmt.Sprint(model["name"])
			server.Version = "Draft"
			writer.Write(body)
		}

	case strings.HasPrefix(path, "/api/1.0/deploystatus/task-"):
		var index int
		fmt.Sscanf(strings.TrimPrefix(path, "/api/1.0/deploystatus/task-"), "%d", &index)
		if index >= len(mcp.Deployments) || query.Get("bundleType") != "MCPSERVER" {
			writer.WriteHeader(http.StatusNotFound)
			return true
		}
		deployment := mcp.Deployments[index]
		status, deployState := "SUCCESS", "DEPLOYED"
		if mcp.FailDeployment {
			status, deployState = "FAIL", "STORED"
		}
		writer.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(writer, `{"status":"%s","version":"%s","deployedBy":"Developer","deployedOn":"Oct 20, 2026, 7:22:37 AM","deployState":"%s","semanticState":"%s"}`,
			status, deployment.Version, deployState, deployment.SemanticState)

	case strings.HasSuffix(path, ".IntegrationComponentsListCommand"):
		writer.Header().Set("Content-Type", "application/xml;charset=UTF-8")
		fmt.Fprint(writer, `<?xml version="1.0" ?><com.sap.it.op.tmn.commands.dashboard.webui.IntegrationComponentsListResponse>`)
		fmt.Fprint(writer, `<artifactInformations><deployState>DEPLOYED</deployState><id>a87f0ab0</id><name>GenAI Gdansk Weather</name>`+
			`<semanticState>STARTED</semanticState><symbolicName>GENAI_GdanskWeather</symbolicName><runtimeLocationId>integrationcell</runtimeLocationId>`+
			`<tenantId>tenant1</tenantId><type>INTEGRATION_FLOW</type><version>1.0.0</version></artifactInformations>`)
		for _, deployment := range mcp.Deployments {
			fmt.Fprintf(writer, `<artifactInformations><deployState>DEPLOYED</deployState><id>%s</id><name>%s</name>`+
				`<semanticState>%s</semanticState><symbolicName>%s</symbolicName><runtimeLocationId>integrationcell</runtimeLocationId>`+
				`<tags name="SAP-BundleType" value="MCPSERVER"/><tenantId>tenant1</tenantId><type>MCP_SERVER</type><version>%s</version></artifactInformations>`,
				deployment.RuntimeId, deployment.Name, deployment.SemanticState, deployment.SymbolicName, deployment.Version)
		}
		fmt.Fprint(writer, `</com.sap.it.op.tmn.commands.dashboard.webui.IntegrationComponentsListResponse>`)

	case strings.HasSuffix(path, ".DeleteContentCommand"):
		form, _ := url.ParseQuery(string(body))
		mcp.Forms = append(mcp.Forms, form)
		remaining := []*stubMCPDeployment{}
		for _, deployment := range mcp.Deployments {
			if deployment.RuntimeId != form.Get("artifactIds") {
				remaining = append(remaining, deployment)
			}
		}
		mcp.Deployments = remaining
		writer.Header().Set("Content-Type", "application/xml")
		writer.Write([]byte(`<?xml version="1.0" ?><java.lang.String>task-2</java.lang.String>`))

	default:
		writer.WriteHeader(http.StatusNotFound)
	}

	return true
}

//newStubMCPModel is a trimmed editor model in the shape the tenant answers
func newStubMCPModel(id string, name string) map[string]interface{} {
	model := map[string]interface{}{}
	json.Unmarshal([]byte(`{"name":"`+name+`","id":"`+id+`","mcpState":"Active",
"iflow":{"propertyViewModel":{
  "defaultIntegrationFlowModel":{"id":"Collobaration_1","allAttributes":{"name":{"value":"`+name+`"}}},
  "listOfDefaultChannelModel":[{"id":"MessageFlow_18_a1","allAttributes":{"urlPath":{"value":"/created"}}}],
  "listOfDefaultFlowElementModel":[],"listOfDefaultProcessModel":[],
  "manifestContent":{"Bundle-Name":"`+name+`","Bundle-SymbolicName":"`+id+`","Bundle-Version":"1.0.0",
    "SAP-RuntimeProfile":"integrationcell","DT-VirtualHostNames":"default"}}},
"tools":[],"resources":[],"prompts":[]}`), &model)
	return model
}

//writeTestMCPServer builds the exploded bundle of an MCP server, with the
//files of a real export
func writeTestMCPServer(t *testing.T, id string) string {
	t.Helper()

	root := filepath.Join(t.TempDir(), id)
	files := map[string]string{
		"META-INF/MANIFEST.MF": "Manifest-Version: 1.0\r\nBundle-ManifestVersion: 2\r\nBundle-Name: " + id +
			"\r\nBundle-SymbolicName: " + id + "\r\nBundle-Version: 1.0.0\r\nSAP-BundleType: MCPSERVER\r\n" +
			"SAP-RuntimeProfile: integrationcell\r\nmcpartifacttype: HTTP_ENDPOINT\r\nDT-VirtualHostNames: default\r\n" +
			"targetURL: https://petstore.example/api/v3/\r\n" +
			"Provide-Capability: mcpserver." + id + ";version:Version=\"1.0.0\"\r\n\r\n",
		".project":      "<projectDescription><name>" + id + "</name></projectDescription>",
		"metainfo.prop": "description=This is " + id + " MCP Server\n",
		"src/main/resources/mcpspecification/mcpspecification.json": `{"name":"` + id + `","title":"` + id + ` server",` +
			`"tools":[{"name":"get_pet","title":"Find pet","description":"","inputSchema":{"type":"object"}}]}`,
		"src/main/resources/mcpsourcemapping/mcpsourcemapping.json": `{"version":"1.0","tools":{"get_pet":{"metadata":{"method":"get","path":"/pet/{petId}"}}}}`,
		"src/main/resources/apidefinition/apidefinition.json":                           `{"openapi":"3.0.4"}`,
		"src/main/resources/scenarioflows/integrationflow/mcpartifact_default_a1.iflw": testMCPFlow,
	}
	for name, content := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}

	return root
}

//The flow of a server, with the MCP sender and its path
const testMCPFlow = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn2:definitions xmlns:bpmn2="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:ifl="http:///com.sap.ifl.model/Ifl.xsd" id="Definitions_1">
    <bpmn2:collaboration id="Collaboration_1" name="Default Collaboration">
        <bpmn2:messageFlow id="MessageFlow_18_a1" name="MCP">
            <bpmn2:extensionElements>
                <ifl:property><key>ComponentType</key><value>MCP</value></ifl:property>
                <ifl:property><key>urlPath</key><value>/mcp-server</value></ifl:property>
            </bpmn2:extensionElements>
        </bpmn2:messageFlow>
    </bpmn2:collaboration>
</bpmn2:definitions>`

//stubMCPPackage gives the tenant a package with one iFlow, the MCP servers of
//the recording and their exports, and gives the system a UI session
func stubMCPPackage(t *testing.T, tenant *stubTenant) {
	t.Helper()

	stubArtifact(t, tenant, "MCPservers", "Order_API_TEST_HARNESS", "1.0.3")

	tenant.MCP = &stubMCP{PackageRegistryIds: map[string]string{"MCPservers": recordedPackageRegistryId}}
	for _, server := range []struct{ id, registryId, version string }{
		{"Northwind", "f20602754c6a4ab1b036b10ba72623d8", "1.0.0"},
		{"petstore", "29b34e8e536b427ba3ee2098ac34ac6a", "Draft"},
	} {
		content, err := iflow.ZipDir(writeTestMCPServer(t, server.id))
		if err != nil {
			t.Fatalf("unable to build the archive: %v", err)
		}
		tenant.MCP.Servers = append(tenant.MCP.Servers, &stubMCPServer{
			Id: server.id, Name: server.id, Version: server.version, RegistryId: server.registryId,
			PackageId: "MCPservers", Export: content, Model: newStubMCPModel(server.id, server.id),
		})
	}

	useStubUISession(tenant)
}

//useStubUISession gives every system the session the stub accepts
func useStubUISession(tenant *stubTenant) {
	host := strings.TrimPrefix(tenant.Server.URL, "https://")
	for _, system := range globalLandscape.Systems {
		//Given the way it is copied from the browser
		system.Client.SetUISession("https://"+host+"/shell/design", stubUICookie)
	}
}

//downloadMCPTestPackage runs the download of the stub package, as the command does
func downloadMCPTestPackage(t *testing.T, target downloadTarget, output string) []*downloadRow {
	t.Helper()

	environment := globalLandscape.Environments["Dev"]
	return downloadTargets(environment, []downloadTarget{target}, output)
}

//An MCP server lands next to the iFlows of its package, in the exploded layout
//of the export
func TestDownloadMCPServers(t *testing.T) {
	tenant := newStubTenant(t)
	globalLandscape = newTestLandscape(t, tenant)
	stubMCPPackage(t, tenant)
	output := t.TempDir()
	setDownloadFlags(t, output)

	rows := downloadMCPTestPackage(t, downloadTarget{PackageId: "MCPservers"}, output)

	if got, want := statusesOf(rows), "Order_API_TEST_HARNESS=downloaded Northwind=downloaded petstore=downloaded (draft)"; got != want {
		t.Fatalf("statuses = %q, want %q", got, want)
	}
	if downloadExitCode(rows) != exitDeployed {
		t.Errorf("exit code = %d, want %d", downloadExitCode(rows), exitDeployed)
	}

	folder := filepath.Join(output, "MCPservers", "petstore")
	bundleType, err := iflow.ReadManifestHeader(folder, iflow.HeaderSAPBundleType)
	if err != nil || bundleType != "MCPSERVER" {
		t.Errorf("SAP-BundleType = %q (%v), want MCPSERVER", bundleType, err)
	}
	for _, name := range []string{
		"mcpspecification/mcpspecification.json",
		"mcpsourcemapping/mcpsourcemapping.json",
		"apidefinition/apidefinition.json",
		"scenarioflows/integrationflow/mcpartifact_default_a1.iflw",
	} {
		if _, err := os.Stat(filepath.Join(folder, "src", "main", "resources", filepath.FromSlash(name))); err != nil {
			t.Errorf("%s was not extracted: %v", name, err)
		}
	}
}

//The UI host gets the session and nothing else: the tenant password of Basic
//authentication must not travel to a second host
func TestMCPCallsSendSessionCookieOnly(t *testing.T) {
	tenant := newStubTenant(t)
	globalLandscape = newTestLandscape(t, tenant)
	stubMCPPackage(t, tenant)
	output := t.TempDir()
	setDownloadFlags(t, output)

	downloadMCPTestPackage(t, downloadTarget{PackageId: "MCPservers"}, output)

	if len(tenant.MCP.Headers) == 0 {
		t.Fatal("no UI call was made")
	}
	for _, headers := range tenant.MCP.Headers {
		if headers.Get("Authorization") != "" {
			t.Errorf("a UI call carried an Authorization header")
		}
		if strings.Count(headers.Get("Cookie"), "JSESSIONID") != 1 {
			t.Errorf("Cookie = %q, want the session exactly once", headers.Get("Cookie"))
		}
	}
}

//--artifacts selects an MCP server like an iFlow
func TestDownloadRequestedMCPServer(t *testing.T) {
	tenant := newStubTenant(t)
	globalLandscape = newTestLandscape(t, tenant)
	stubMCPPackage(t, tenant)
	output := t.TempDir()
	setDownloadFlags(t, output)

	rows := downloadMCPTestPackage(t, downloadTarget{PackageId: "MCPservers", ArtifactIds: []string{"petstore", "Missing"}}, output)

	if got, want := statusesOf(rows), "petstore=downloaded (draft) Missing=failed: artifact is not in the package"; got != want {
		t.Fatalf("statuses = %q, want %q", got, want)
	}
}

//An expired session is reported as such and does not hide the iFlows
func TestDownloadMCPServersWithExpiredSession(t *testing.T) {
	tenant := newStubTenant(t)
	globalLandscape = newTestLandscape(t, tenant)
	stubMCPPackage(t, tenant)
	tenant.MCP.SessionExpired = true
	output := t.TempDir()
	setDownloadFlags(t, output)

	rows := downloadMCPTestPackage(t, downloadTarget{PackageId: "MCPservers"}, output)

	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d: %s", len(rows), statusesOf(rows))
	}
	if !rows[0].Failed || !strings.Contains(rows[0].Status, "session in uiCookie is not valid") {
		t.Errorf("first row = %q, want a failed row naming the session", rows[0].Status)
	}
	if rows[1].ArtifactId != "Order_API_TEST_HARNESS" || rows[1].Status != "downloaded" {
		t.Errorf("second row = %s=%s, want the iFlow downloaded", rows[1].ArtifactId, rows[1].Status)
	}
	if downloadExitCode(rows) != exitFailure {
		t.Errorf("exit code = %d, want %d", downloadExitCode(rows), exitFailure)
	}
}

//Without a UI session the download is what it was before MCP servers existed
func TestDownloadWithoutUISessionReadsIFlowsOnly(t *testing.T) {
	tenant := newStubTenant(t)
	globalLandscape = newTestLandscape(t, tenant)
	stubMCPPackage(t, tenant)
	for _, system := range globalLandscape.Systems {
		system.Client.SetUISession("", "")
	}
	output := t.TempDir()
	setDownloadFlags(t, output)

	rows := downloadMCPTestPackage(t, downloadTarget{PackageId: "MCPservers"}, output)

	if got, want := statusesOf(rows), "Order_API_TEST_HARNESS=downloaded"; got != want {
		t.Fatalf("statuses = %q, want %q", got, want)
	}
	if len(tenant.MCP.Headers) != 0 {
		t.Errorf("%d UI calls were made without a session", len(tenant.MCP.Headers))
	}
}

//The session cookie never reaches the audit log
func TestAuditLogNeverContainsUICookie(t *testing.T) {
	tenant := newStubTenant(t)
	globalLandscape = newTestLandscape(t, tenant)
	stubMCPPackage(t, tenant)
	output := t.TempDir()
	setDownloadFlags(t, output)

	logger := startTestAuditLog(t)

	downloadMCPTestPackage(t, downloadTarget{PackageId: "MCPservers"}, output)

	content, err := os.ReadFile(logger.Path())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(string(content), "export=true") {
		t.Fatal("the export call is not in the audit log")
	}
	if strings.Contains(string(content), "stub-session") {
		t.Error("the audit log contains the session cookie")
	}
}

//setMCPUploadFlags is setUploadFlags plus the wait flags, which the MCP tests use
func setMCPUploadFlags(t *testing.T, targetEnv string, packageId string, deploy bool, wait bool) {
	t.Helper()

	setUploadFlags(t, targetEnv, t.TempDir())
	*uploadDeploy = deploy
	*pkg = packageId
	waitValue := wait
	timeout := 2 * time.Second
	interval := 10 * time.Millisecond
	uploadWait = &waitValue
	uploadTimeout = &timeout
	uploadInterval = &interval
}

//declareMCPServer adds the server to the landscape, with the configuration of QA
func declareMCPServer(parameters ...*landscape.Parameter) {
	globalLandscape.Packages["MCPservers"] = &landscape.Package{
		Id: "MCPservers",
		Artifacts: map[string]*landscape.Artifact{"petstore": {
			Id: "petstore",
			Configurations: map[string]*landscape.Configuration{
				"QA": {Environment: "QA", Parameters: parameters},
			},
		}},
	}
}

//A server missing in the package is created from its OpenAPI definition and
//then saved with the content of the folder
func TestUploadMCPServerCreates(t *testing.T) {
	tenant := newStubTenant(t)
	globalLandscape = newTestLandscape(t, tenant)
	tenant.Packages["MCPservers"] = true
	tenant.MCP = &stubMCP{}
	useStubUISession(tenant)
	setMCPUploadFlags(t, "Dev", "MCPservers", false, false)

	row, err := uploadArtifact(writeTestMCPServer(t, "petstore"), globalLandscape.Environments["Dev"])
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if row.Action != "created" || row.ArtifactId != "petstore" || row.PackageId != "MCPservers" || row.UploadVersion != "1.0.0" {
		t.Errorf("unexpected row %+v", row)
	}
	if len(tenant.MCP.Created) != 1 {
		t.Fatalf("expected 1 create call, got %d", len(tenant.MCP.Created))
	}
	created := tenant.MCP.Created[0]
	if created["id"] != "petstore" || created["mcpPath"] != "/mcp-server" || created["targetURL"] != "https://petstore.example/api/v3/" {
		t.Errorf("unexpected create payload %v", created)
	}

	if len(tenant.MCP.Saved) != 1 {
		t.Fatalf("expected 1 save, got %d", len(tenant.MCP.Saved))
	}
	saved := tenant.MCP.Saved[0]
	if saved["mcpPath"] != "/mcp-server" || len(saved["tools"].([]interface{})) != 1 {
		t.Errorf("the save did not carry the folder: %v %v", saved["mcpPath"], saved["tools"])
	}
	if tenant.MCP.MissingToken {
		t.Error("a mutating UI call was sent without the CSRF token")
	}
	//Saved under the edit lock, which is released again
	if got := strings.Join(tenant.MCP.LockCalls, ","); got != "LOCK new0,UNLOCK new0" {
		t.Errorf("lock calls = %q, want LOCK then UNLOCK", got)
	}
	//The OData upload of an iFlow is not used for an MCP server
	if call := findCall(tenant.Calls, http.MethodPost, "IntegrationDesigntimeArtifacts"); call != nil {
		t.Error("the MCP server was posted as an integration flow")
	}
}

//An existing server of a suffixed environment is saved under the suffixed id
//and name, with mcpPath and targetURL of that environment
func TestUploadMCPServerUpdatesSuffixedEnvironment(t *testing.T) {
	tenant := newStubTenant(t)
	globalLandscape = newTestLandscape(t, tenant)
	declareMCPServer(
		&landscape.Parameter{Key: "mcpPath", Value: "/mcp-server-qa"},
		&landscape.Parameter{Key: "targetURL", Value: "https://qa.example/api/v3/"},
	)
	tenant.Packages["MCPserversQA"] = true
	tenant.MCP = &stubMCP{Servers: []*stubMCPServer{{
		Id: "petstoreQA", Name: "petstore QA", Version: "1.0.0", RegistryId: "qa1", PackageId: "MCPserversQA",
		Model: newStubMCPModel("petstoreQA", "petstore QA"),
	}}}
	useStubUISession(tenant)
	setMCPUploadFlags(t, "QA", "", false, false)

	row, err := uploadArtifact(writeTestMCPServer(t, "petstore"), globalLandscape.Environments["QA"])
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if row.Action != "updated" || row.ArtifactId != "petstoreQA" || row.PackageId != "MCPserversQA" || row.TenantVersion != "1.0.0" {
		t.Errorf("unexpected row %+v", row)
	}
	if len(tenant.MCP.Created) != 0 {
		t.Errorf("an existing server was created again")
	}
	if len(tenant.MCP.Saved) != 1 {
		t.Fatalf("expected 1 save, got %d", len(tenant.MCP.Saved))
	}
	saved := tenant.MCP.Saved[0]
	//No space: the tenant refuses anything but letters, digits and underscores
	if saved["name"] != "petstoreQA" || saved["mcpPath"] != "/mcp-server-qa" || saved["targetURL"] != "https://qa.example/api/v3/" {
		t.Errorf("unexpected save: name %v, mcpPath %v, targetURL %v", saved["name"], saved["mcpPath"], saved["targetURL"])
	}
	channel := saved["iflow"].(map[string]interface{})["propertyViewModel"].(map[string]interface{})["listOfDefaultChannelModel"].([]interface{})[0].(map[string]interface{})
	if path := channel["allAttributes"].(map[string]interface{})["urlPath"].(map[string]interface{})["value"]; path != "/mcp-server-qa" {
		t.Errorf("urlPath of the MCP channel = %v, want /mcp-server-qa", path)
	}
}

//Only mcpPath and targetURL can be configured, anything else is refused
func TestUploadMCPServerRefusesUnknownParameter(t *testing.T) {
	tenant := newStubTenant(t)
	globalLandscape = newTestLandscape(t, tenant)
	declareMCPServer(&landscape.Parameter{Key: "urlPath", Value: "/x"})
	tenant.MCP = &stubMCP{}
	useStubUISession(tenant)
	setMCPUploadFlags(t, "QA", "", false, false)

	_, err := uploadArtifact(writeTestMCPServer(t, "petstore"), globalLandscape.Environments["QA"])
	if err == nil || !strings.Contains(err.Error(), "only mcpPath and targetURL") {
		t.Fatalf("expected the parameter to be refused, got %v", err)
	}
	if len(tenant.MCP.Saved) != 0 || len(tenant.MCP.Created) != 0 {
		t.Error("the tenant was written although the configuration is wrong")
	}
}

//--deploy --wait deploys the saved server and reports the Integration Cell status
func TestUploadMCPServerDeploysAndWaits(t *testing.T) {
	tenant := newStubTenant(t)
	globalLandscape = newTestLandscape(t, tenant)
	stubMCPPackage(t, tenant)
	setMCPUploadFlags(t, "Dev", "MCPservers", true, true)

	row, err := uploadArtifact(writeTestMCPServer(t, "petstore"), globalLandscape.Environments["Dev"])
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !row.Deployed || row.Status == nil || row.Status.Summary() != "STARTED" || row.Status.ExitCode() != exitDeployed {
		t.Fatalf("unexpected deployment result: deployed %t, status %s", row.Deployed, row.Status.Summary())
	}
	if len(tenant.MCP.Deployments) != 1 || tenant.MCP.Deployments[0].SymbolicName != "petstore" {
		t.Errorf("unexpected deployments %v", tenant.MCP.Deployments)
	}
}

//An MCP server is found on the Integration Cell by its id and removed there
func TestUndeployMCPServer(t *testing.T) {
	tenant := newStubTenant(t)
	globalLandscape = newTestLandscape(t, tenant)
	stubMCPPackage(t, tenant)
	tenant.MCP.Deployments = []*stubMCPDeployment{{RuntimeId: "runtime-7", SymbolicName: "petstore", Name: "petstore", Version: "1.0.0", SemanticState: "STARTED"}}
	client := globalLandscape.Systems["dev"].Client

	deployed, err := deployedMCPServerArtifact(client, "petstore")
	if err != nil || deployed == nil {
		t.Fatalf("the deployment was not found: %v", err)
	}
	if err := client.UndeployIntegrationCellArtifact(deployed); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(tenant.MCP.Forms) != 1 {
		t.Fatalf("expected 1 undeploy call, got %d", len(tenant.MCP.Forms))
	}
	form := tenant.MCP.Forms[0]
	if form.Get("artifactIds") != "runtime-7" || form.Get("tenantId") != "tenant1" || form.Get("runtimeLocationId") != "integrationcell" {
		t.Errorf("unexpected undeploy form %v", form)
	}

	//An iFlow of the same Integration Cell is never taken for the server
	if artifact, _ := deployedMCPServerArtifact(client, "GENAI_GdanskWeather"); artifact != nil {
		t.Error("an integration flow was taken for an MCP server")
	}
}

//Deploy by id reads runtime, virtual host and version from the saved model
func TestDeployMCPServerById(t *testing.T) {
	tenant := newStubTenant(t)
	globalLandscape = newTestLandscape(t, tenant)
	stubMCPPackage(t, tenant)
	client := globalLandscape.Systems["dev"].Client

	deployed, err := deployMCPServerById(client, "MCPservers", "Northwind")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if deployed.Version != "1.0.0" || deployed.Server.RegistryId != "f20602754c6a4ab1b036b10ba72623d8" {
		t.Errorf("unexpected result %+v", deployed)
	}

	status := waitForDeployment(&mcpDeployTaskReader{client: client, taskId: deployed.TaskId, server: deployed.Server},
		"Northwind", deployed.Version, 2*time.Second, 10*time.Millisecond)
	if status.ExitCode() != exitDeployed {
		t.Errorf("status = %s, want STARTED", status.Summary())
	}

	if _, err := deployMCPServerById(client, "MCPservers", "Missing"); err == nil {
		t.Error("expected an error for a server the package does not hold")
	}
}

//Without a UI session an MCP folder is refused before the tenant is touched
func TestUploadMCPServerWithoutUISession(t *testing.T) {
	tenant := newStubTenant(t)
	globalLandscape = newTestLandscape(t, tenant)
	setMCPUploadFlags(t, "Dev", "MCPservers", false, false)

	_, err := uploadArtifact(writeTestMCPServer(t, "petstore"), globalLandscape.Environments["Dev"])
	if err != cpiclient.ErrNoUISession {
		t.Fatalf("err = %v, want ErrNoUISession", err)
	}
	if len(tenant.Calls) != 0 {
		t.Errorf("%d calls were made without a session", len(tenant.Calls))
	}
}

func TestMCPServerName(t *testing.T) {
	cases := []struct{ name, suffix, want string }{
		{"petstore", "", "petstore"},
		{"petstore", "QA", "petstoreQA"},
		{"petstoreQA", "QA", "petstoreQA"},
		{"petstore QA", "QA", "petstoreQA"},
		{"QA", "QA", "QAQA"},
	}
	for _, c := range cases {
		if got := mcpServerName(c.name, c.suffix); got != c.want {
			t.Errorf("mcpServerName(%q, %q) = %q, want %q", c.name, c.suffix, got, c.want)
		}
	}
}

//A failed deployment is ERROR at once, not STARTING until the wait times out:
//the runtime list keeps such a server STARTING, the task says FAIL
func TestUploadMCPServerReportsFailedDeployment(t *testing.T) {
	tenant := newStubTenant(t)
	globalLandscape = newTestLandscape(t, tenant)
	stubMCPPackage(t, tenant)
	tenant.MCP.FailDeployment = true
	setMCPUploadFlags(t, "Dev", "MCPservers", true, true)

	row, err := uploadArtifact(writeTestMCPServer(t, "petstore"), globalLandscape.Environments["Dev"])
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if row.Status == nil || row.Status.Summary() != "ERROR" || row.Status.ExitCode() != exitDeployError || row.Status.TimedOut {
		t.Fatalf("status = %s, want ERROR without a timeout", row.Status.Summary())
	}
	if !strings.Contains(row.Status.ErrorText, "Manage Integration Content") {
		t.Errorf("ErrorText = %q", row.Status.ErrorText)
	}
}

//The tenant refuses a save without the edit lock, saveMCPServerModel takes it and releases it again
func TestSaveMCPServerModelLocks(t *testing.T) {
	tenant := newStubTenant(t)
	globalLandscape = newTestLandscape(t, tenant)
	stubMCPPackage(t, tenant)
	setMCPUploadFlags(t, "Dev", "MCPservers", false, false)
	client := globalLandscape.Systems["dev"].Client
	server := &cpiclient.MCPServer{Id: "Northwind", RegistryId: "f20602754c6a4ab1b036b10ba72623d8"}

	//A model the stub refuses: the save answers 500 once the lock is taken away
	tenant.MCP.Locked = map[string]bool{}
	err := client.UpdateMCPServerModel(recordedPackageRegistryId, server, map[string]interface{}{"name": "x"})
	if err == nil || !strings.Contains(err.Error(), "content store") {
		t.Fatalf("a save without lock = %v, want the content store error", err)
	}

	if err := saveMCPServerModel(client, recordedPackageRegistryId, server, map[string]interface{}{"name": "Northwind"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tenant.MCP.Locked[server.RegistryId] {
		t.Error("the server is still locked after the save")
	}
}
