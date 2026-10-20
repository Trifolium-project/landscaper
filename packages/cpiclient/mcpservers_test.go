package cpiclient

import (
	"net/http"
	"testing"
)

//The artifact list of a package holds every artifact type, only MCP servers are kept
func TestParseMCPServers(t *testing.T) {
	body := []byte(`{"d":{"results":[` +
		`{"Name":"petstore","Type":"MCPSERVER","DisplayName":"Pet Store","reg_id":"29b34e8e","Version":"Draft","Description":null},` +
		`{"Name":"Order_API","Type":"IFlow","DisplayName":"Order API","reg_id":"0000000a","Version":"1.0.3"}]}}`)

	servers, err := parseMCPServers(body, "MCPservers")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(servers) != 1 {
		t.Fatalf("expected 1 server, got %d", len(servers))
	}

	server := servers[0]
	if server.Id != "petstore" || server.Name != "Pet Store" || server.Version != MCPServerVersionDraft ||
		server.RegistryId != "29b34e8e" || server.PackageId != "MCPservers" || server.Description != "" {
		t.Errorf("unexpected server %+v", server)
	}
}

func TestParseMCPServersRejectsOtherShapes(t *testing.T) {
	if _, err := parseMCPServers([]byte(`[]`), "MCPservers"); err == nil {
		t.Error("expected an error for a body without d.results")
	}
}

//uiHost is copied from the browser address bar more often than not
func TestSetUISessionKeepsHostOnly(t *testing.T) {
	cases := map[string]string{
		"tenant.example":                                       "tenant.example",
		"https://tenant.example/shell/design/contentpackage/X": "tenant.example",
		" http://tenant.example/ ":                             "tenant.example",
	}
	for input, want := range cases {
		client := NewCPIBasicAuthClient("user", "password", "", "api.example", false)
		client.SetUISession(input, " cookie ")
		if client.UIHost != want {
			t.Errorf("SetUISession(%q) host = %q, want %q", input, client.UIHost, want)
		}
		if client.UICookie != "cookie" || !client.HasUISession() {
			t.Errorf("SetUISession(%q) cookie = %q, session = %t", input, client.UICookie, client.HasUISession())
		}
	}

	client := NewCPIBasicAuthClient("user", "password", "", "api.example", false)
	client.SetUISession("tenant.example", "")
	if client.HasUISession() {
		t.Error("a host without a cookie is not a session")
	}
}

func TestIsHTMLPage(t *testing.T) {
	cases := map[string]bool{
		"text/html; charset=utf-8":       true,
		"application/json;charset=UTF-8": false,
		"application/octet-stream":       false,
		"":                               false,
	}
	for contentType, want := range cases {
		headers := http.Header{}
		headers.Set("Content-Type", contentType)
		if got := isHTMLPage(headers); got != want {
			t.Errorf("isHTMLPage(%q) = %t, want %t", contentType, got, want)
		}
	}
}

//Artifact list of a package as the UI answers it, recorded from a trial tenant
//with host and user replaced
const recordedMCPArtifactList = `{"d":{"results":[` +
	`{"__metadata":{"id":"https://tenant.example/odata/1.0/catalog.svc/Artifacts(Name='Northwind',Type='MCPSERVER')","type":"ContentPackageModel.Artifact"},` +
	`"Name":"Northwind","Type":"MCPSERVER","DisplayName":"Northwind","SubType":null,"reg_id":"f20602754c6a4ab1b036b10ba72623d8","Version":"1.0.0",` +
	`"Description":"This is Northwind MCP Server","CreatedAt":"\/Date(1792439712804)\/","CreatedBy":"developer@example.com","ContentType":null,"URI":null,"State":null},` +
	`{"__metadata":{"id":"https://tenant.example/odata/1.0/catalog.svc/Artifacts(Name='id',Type='MCPSERVER')","type":"ContentPackageModel.Artifact"},` +
	`"Name":"id","Type":"MCPSERVER","DisplayName":"name","SubType":null,"reg_id":"9484a2300b094d159636006402ae9f30","Version":"1.0.0",` +
	`"Description":"This is name MCP Server","CreatedAt":"\/Date(1792444830848)\/","CreatedBy":"developer@example.com","ContentType":null,"URI":null,"State":null}` +
	`]}}`

//Id and display name differ for a server created with different values
func TestParseRecordedMCPArtifactList(t *testing.T) {
	servers, err := parseMCPServers([]byte(recordedMCPArtifactList), "MCPservers")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(servers) != 2 || servers[1].Id != "id" || servers[1].Name != "name" || servers[1].RegistryId != "9484a2300b094d159636006402ae9f30" {
		t.Errorf("unexpected servers %+v %+v", servers[0], servers[1])
	}
}

//The Integration Cell list as the Operations command answers it, recorded with
//tenant, host and user replaced and the tags shortened
const recordedIntegrationCellList = `<?xml version="1.0" ?><com.sap.it.op.tmn.commands.dashboard.webui.IntegrationComponentsListResponse>` +
	`<artifactInformations><taskId>04532112</taskId><deployState>DEPLOYED</deployState><deployedBy>sb-client</deployedBy>` +
	`<deployedOn>2026-10-19T18:59:02.025Z</deployedOn><id>a87f0ab0-a96a-4f9f-b616-cccc68554b93</id><name>GenAI Gdansk Weather</name>` +
	`<nodeType>IFLMAP</nodeType><semanticState>STARTED</semanticState><symbolicName>GENAI_GdanskWeather</symbolicName>` +
	`<runtimeLocationId>integrationcell</runtimeLocationId><tags name="SAP-BundleType" value="IntegrationFlow"/>` +
	`<tenantId>tenant1</tenantId><type>INTEGRATION_FLOW</type><version>1.0.0</version></artifactInformations>` +
	`<artifactInformations><taskId>ceff4082</taskId><deployState>UNDEPLOYING</deployState><deployedBy>Developer</deployedBy>` +
	`<deployedOn>2026-10-19T21:52:54.103Z</deployedOn><id>45be60de-8b6c-415d-8762-81aacdd2a1ec</id><name>petstore1</name>` +
	`<nodeType>IFLMAP</nodeType><semanticState>STOPPING</semanticState><symbolicName>petstore</symbolicName>` +
	`<runtimeLocationId>integrationcell</runtimeLocationId><tags name="SAP-MCPEndpoint" value="https://runtime.example/mcp-server"/>` +
	`<tenantId>tenant1</tenantId><type>MCP_SERVER</type><version>1.0.0</version></artifactInformations>` +
	`</com.sap.it.op.tmn.commands.dashboard.webui.IntegrationComponentsListResponse>`

func TestParseIntegrationCellArtifacts(t *testing.T) {
	artifacts, err := parseIntegrationCellArtifacts([]byte(recordedIntegrationCellList))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(artifacts) != 2 {
		t.Fatalf("expected 2 artifacts, got %d", len(artifacts))
	}

	server := artifacts[1]
	if server.Id != "45be60de-8b6c-415d-8762-81aacdd2a1ec" || server.SymbolicName != "petstore" || server.Name != "petstore1" ||
		server.Type != IntegrationCellTypeMCPServer || server.DeployState != "UNDEPLOYING" || server.SemanticState != "STOPPING" ||
		server.TenantId != "tenant1" || server.RuntimeLocationId != "integrationcell" || server.Version != "1.0.0" {
		t.Errorf("unexpected artifact %+v", server)
	}

	empty, err := parseIntegrationCellArtifacts([]byte(`<?xml version="1.0" ?><com.sap.it.op.tmn.commands.dashboard.webui.IntegrationComponentsListResponse/>`))
	if err != nil || len(empty) != 0 {
		t.Errorf("empty list = %v, %v", empty, err)
	}
}
