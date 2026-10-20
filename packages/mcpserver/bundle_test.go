package mcpserver

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

//A flow with the elements an exported server has: the collaboration, the MCP
//sender channel, the HTTP receiver channel and the authorization policy
const testFlow = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn2:definitions xmlns:bpmn2="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:ifl="http:///com.sap.ifl.model/Ifl.xsd" id="Definitions_1">
    <bpmn2:collaboration id="Collaboration_1" name="Default Collaboration">
        <bpmn2:extensionElements>
            <ifl:property><key>log</key><value>All events</value></ifl:property>
        </bpmn2:extensionElements>
        <bpmn2:messageFlow id="MessageFlow_18_a1" name="MCP">
            <bpmn2:extensionElements>
                <ifl:property><key>ComponentType</key><value>MCP</value></ifl:property>
                <ifl:property><key>urlPath</key><value>/mcp-server</value></ifl:property>
            </bpmn2:extensionElements>
        </bpmn2:messageFlow>
        <bpmn2:messageFlow id="MessageFlow_6_a1" name="HTTP">
            <bpmn2:extensionElements>
                <ifl:property><key>ComponentType</key><value>HTTP</value></ifl:property>
                <ifl:property><key>httpAddressWithoutQuery</key><value>https://petstore.example/api/v3/</value></ifl:property>
                <ifl:property><key>httpRequestTimeout</key><value>30000</value></ifl:property>
            </bpmn2:extensionElements>
        </bpmn2:messageFlow>
    </bpmn2:collaboration>
    <bpmn2:process id="Process_1_a1" name="Policy Model">
        <bpmn2:callActivity id="CallActivity_16_a1" name="Authorization 1">
            <bpmn2:extensionElements>
                <ifl:property><key>values</key><value>API.invoke,API.read</value></ifl:property>
                <ifl:property><key>empty</key><value/></ifl:property>
            </bpmn2:extensionElements>
        </bpmn2:callActivity>
    </bpmn2:process>
</bpmn2:definitions>`

//writeTestBundle builds the exploded bundle of an MCP server with one tool,
//one resource, one resource template and one prompt
func writeTestBundle(t *testing.T) string {
	t.Helper()

	root := filepath.Join(t.TempDir(), "petstore")
	files := map[string]string{
		"META-INF/MANIFEST.MF": "Manifest-Version: 1.0\r\nBundle-Name: Pet Store\r\nBundle-SymbolicName: petstore\r\n" +
			"Bundle-Version: 1.0.2\r\nSAP-BundleType: MCPSERVER\r\nSAP-NodeType: IFLMAP\r\nSAP-RuntimeProfile: integrationcell\r\n" +
			"mcpartifacttype: HTTP_ENDPOINT\r\ntargetURL: https://petstore.example/api/v3/\r\nDT-VirtualHostNames: default\r\n\r\n",
		apiDefinitionPath: `{"openapi":"3.0.4","paths":{}}`,
		specificationPath: `{"name":"petstore","title":"Short text","description":"<p>Long text</p>",` +
			`"tools":[{"name":"get_pet","title":"Find pet","description":"Returns a pet",` +
			`"inputSchema":{"type":"object","properties":{"petId":{"type":"integer"}}}}],` +
			`"resources":[{"uri":"mcpresource:///store/inventory","name":"get_inventory","title":"Inventory","description":"","mimeType":"application/json"}],` +
			`"resourceTemplates":[{"uriTemplate":"mcpresource:///pet/{petId}","name":"get_pet_res","title":"Pet","description":"","mimeType":"application/json"}],` +
			`"prompts":[{"name":"ask","title":"","description":"Ask","arguments":[{"name":"arg1","required":true}]}]}`,
		sourceMappingPath: `{"version":"1.0",` +
			`"tools":{"get_pet":{"name":"get_pet","metadata":{"method":"get","path":"/pet/{petId}","responseContentType":"application/json"}}},` +
			`"resources":{"mcpresource:///store/inventory":{"name":"get_inventory","metadata":{"method":"get","path":"/store/inventory","responseContentType":"application/json"}}},` +
			`"resourceTemplates":{"mcpresource:///pet/{petId}":{"name":"get_pet_res","metadata":{"method":"get","path":"/pet/{petId}","responseContentType":"application/json"}}},` +
			`"prompts":{"ask":{"messages":[{"role":"user","content":{"type":"Text","text":"hello"}}]}}}`,
		integrationFlowDir + "/mcpartifact_default_a1.iflw": testFlow,
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

func TestReadBundle(t *testing.T) {
	bundle, err := ReadBundle(writeTestBundle(t))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	checks := map[string][2]string{
		"Id":          {bundle.Id, "petstore"},
		"Name":        {bundle.Name, "Pet Store"},
		"Version":     {bundle.Version, "1.0.2"},
		"ShortText":   {bundle.ShortText, "Short text"},
		"Description": {bundle.Description, "<p>Long text</p>"},
		"TargetURL":   {bundle.TargetURL, "https://petstore.example/api/v3/"},
		"McpPath":     {bundle.McpPath, "/mcp-server"},
		"SourceType":  {bundle.SourceType, "HTTP_ENDPOINT"},
		"Profile":     {bundle.Profile, "integrationcell"},
		"VirtualHost": {bundle.VirtualHost, "default"},
		"OpenAPI":     {bundle.OpenAPI, `{"openapi":"3.0.4","paths":{}}`},
	}
	for field, check := range checks {
		if check[0] != check[1] {
			t.Errorf("%s = %q, want %q", field, check[0], check[1])
		}
	}

	if len(bundle.Tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(bundle.Tools))
	}
	tool := bundle.Tools[0]
	if tool["method"] != "get" || tool["path"] != "/pet/{petId}" || tool["title"] != "Find pet" {
		t.Errorf("unexpected tool %v", tool)
	}
	//The model holds schemas as JSON text
	if schema, ok := tool["inputSchema"].(string); !ok || !strings.Contains(schema, `"petId"`) {
		t.Errorf("inputSchema = %v, want the schema as JSON text", tool["inputSchema"])
	}

	//Templates first, both with uriTemplate, as the editor lists them
	if len(bundle.Resources) != 2 || bundle.Resources[0]["uriTemplate"] != "mcpresource:///pet/{petId}" ||
		bundle.Resources[1]["uriTemplate"] != "mcpresource:///store/inventory" || bundle.Resources[1]["path"] != "/store/inventory" {
		t.Errorf("unexpected resources %v", bundle.Resources)
	}

	if len(bundle.Prompts) != 1 {
		t.Fatalf("expected 1 prompt, got %d", len(bundle.Prompts))
	}
	if messages, ok := bundle.Prompts[0]["messages"].([]interface{}); !ok || len(messages) != 1 {
		t.Errorf("prompt messages = %v, want the one message of the source mapping", bundle.Prompts[0]["messages"])
	}
}

func TestReadBundleRefusesIntegrationFlow(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "META-INF"), 0o755)
	os.WriteFile(filepath.Join(root, "META-INF", "MANIFEST.MF"), []byte("Bundle-SymbolicName: flow\r\nBundle-Version: 1.0.0\r\n\r\n"), 0o644)

	if IsBundleDir(root) {
		t.Error("an integration flow folder was taken for an MCP server")
	}
	if _, err := ReadBundle(root); err == nil {
		t.Error("expected an error for an integration flow folder")
	}
}

//Properties belong to the innermost element with an id, empty values included
func TestParseFlowProperties(t *testing.T) {
	properties, err := parseFlowProperties(strings.NewReader(testFlow))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if properties["Collaboration_1"]["log"] != "All events" {
		t.Errorf("collaboration log = %q", properties["Collaboration_1"]["log"])
	}
	if _, ok := properties["Collaboration_1"]["urlPath"]; ok {
		t.Error("a property of a channel was attached to the collaboration")
	}
	if properties["MessageFlow_18_a1"]["urlPath"] != "/mcp-server" {
		t.Errorf("urlPath = %q", properties["MessageFlow_18_a1"]["urlPath"])
	}
	if value, ok := properties["CallActivity_16_a1"]["empty"]; !ok || value != "" {
		t.Errorf("empty = %q (%t), want an empty value", value, ok)
	}
}

func TestSetMcpPathAndTargetURL(t *testing.T) {
	bundle, err := ReadBundle(writeTestBundle(t))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	bundle.SetMcpPath("/mcp-server-qa")
	bundle.SetTargetURL("https://qa.example/api/v3/")

	if bundle.McpPath != "/mcp-server-qa" || bundle.Properties["MessageFlow_18_a1"]["urlPath"] != "/mcp-server-qa" {
		t.Errorf("mcpPath not replaced: %q, %q", bundle.McpPath, bundle.Properties["MessageFlow_18_a1"]["urlPath"])
	}
	if bundle.TargetURL != "https://qa.example/api/v3/" ||
		bundle.Properties["MessageFlow_6_a1"]["httpAddressWithoutQuery"] != "https://qa.example/api/v3/" {
		t.Errorf("targetURL not replaced: %q, %q", bundle.TargetURL, bundle.Properties["MessageFlow_6_a1"]["httpAddressWithoutQuery"])
	}
}

//A trimmed editor model in the shape the tenant answers
const testModel = `{"name":"old name","id":"petstore","mcpState":"Active",
"iflow":{"propertyViewModel":{
  "defaultIntegrationFlowModel":{"id":"Collobaration_1","allAttributes":{
    "log":{"value":"None","defaultValue":"","key":"","isPersisted":true},
    "name":{"value":"old name","defaultValue":"","key":""}}},
  "listOfDefaultChannelModel":[
    {"id":"MessageFlow_18_a1","allAttributes":{"urlPath":{"value":"/old","defaultValue":"","key":"","isPersisted":true}}},
    {"id":"MessageFlow_6_a1","allAttributes":{"httpAddressWithoutQuery":{"value":"https://old.example","defaultValue":"","key":"","isPersisted":true},
      "httpRequestTimeout":{"value":"60000","defaultValue":"","key":"","isPersisted":true}}}],
  "listOfDefaultFlowElementModel":[
    {"id":"CallActivity_16_a1","allAttributes":{"values":{"value":"API.invoke","values":["API.invoke"],"defaultValue":"","key":"","isPersisted":true}}}],
  "listOfDefaultProcessModel":[],
  "manifestContent":{"Bundle-Name":"old name","Bundle-Version":"1.0.0","targetURL":"https://old.example","Bundle-SymbolicName":"petstore"}}},
"tools":[{"name":"get_pet","inputApiDefinitionSchema":"{\"parameters\":[]}","outputApiDefinitionSchema":"{\"responses\":{}}"},{"name":"removed_tool"}],
"resources":[],"prompts":[]}`

func TestApplyBundle(t *testing.T) {
	bundle, err := ReadBundle(writeTestBundle(t))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	model := map[string]interface{}{}
	if err := json.Unmarshal([]byte(testModel), &model); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := ApplyBundle(model, bundle); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	//Through JSON, as it is sent
	content, _ := json.Marshal(model)
	applied := map[string]interface{}{}
	json.Unmarshal(content, &applied)

	if applied["name"] != "Pet Store" || applied["mcpPath"] != "/mcp-server" || applied["shortText"] != "Short text" ||
		applied["openAPISpec"] != `{"openapi":"3.0.4","paths":{}}` || applied["targetURL"] != "https://petstore.example/api/v3/" {
		t.Errorf("top level fields not applied: %v %v %v", applied["name"], applied["mcpPath"], applied["targetURL"])
	}

	view := applied["iflow"].(map[string]interface{})["propertyViewModel"].(map[string]interface{})
	attribute := func(element map[string]interface{}, key string) map[string]interface{} {
		return element["allAttributes"].(map[string]interface{})[key].(map[string]interface{})
	}
	collaboration := view["defaultIntegrationFlowModel"].(map[string]interface{})
	if attribute(collaboration, "log")["value"] != "All events" || attribute(collaboration, "name")["value"] != "Pet Store" {
		t.Errorf("collaboration attributes not applied: %v", collaboration["allAttributes"])
	}
	channels := view["listOfDefaultChannelModel"].([]interface{})
	if attribute(channels[0].(map[string]interface{}), "urlPath")["value"] != "/mcp-server" ||
		attribute(channels[1].(map[string]interface{}), "httpRequestTimeout")["value"] != "30000" {
		t.Errorf("channel attributes not applied: %v", channels)
	}
	//A property the model has no attribute for is not introduced
	if _, ok := channels[1].(map[string]interface{})["allAttributes"].(map[string]interface{})["ComponentType"]; ok {
		t.Error("ComponentType was added to the model")
	}
	policy := view["listOfDefaultFlowElementModel"].([]interface{})[0].(map[string]interface{})
	if values := attribute(policy, "values")["values"].([]interface{}); len(values) != 2 || values[1] != "API.read" {
		t.Errorf("multi value attribute = %v, want [API.invoke API.read]", values)
	}

	manifest := view["manifestContent"].(map[string]interface{})
	if manifest["Bundle-Name"] != "Pet Store" || manifest["Bundle-Version"] != "1.0.2" || manifest["Bundle-SymbolicName"] != "petstore" {
		t.Errorf("manifest = %v", manifest)
	}

	//The bundle decides the tools, the tenant's OpenAPI fragments are kept
	tools := applied["tools"].([]interface{})
	if len(tools) != 1 {
		t.Fatalf("expected the 1 tool of the bundle, got %d", len(tools))
	}
	tool := tools[0].(map[string]interface{})
	if tool["inputApiDefinitionSchema"] != `{"parameters":[]}` || tool["method"] != "get" {
		t.Errorf("tool = %v", tool)
	}
	if len(applied["resources"].([]interface{})) != 2 || len(applied["prompts"].([]interface{})) != 1 {
		t.Errorf("resources %v, prompts %v", applied["resources"], applied["prompts"])
	}
}

func TestCreatePayload(t *testing.T) {
	bundle, err := ReadBundle(writeTestBundle(t))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content, _ := json.Marshal(CreatePayload(bundle))
	payload := map[string]interface{}{}
	json.Unmarshal(content, &payload)

	if payload["type"] != "MCP" || payload["id"] != "petstore" || payload["name"] != "Pet Store" ||
		payload["mcpPath"] != "/mcp-server" || payload["sourceType"] != "HTTP_ENDPOINT" ||
		payload["openApiSpec"] != `{"openapi":"3.0.4","paths":{}}` || payload["httpSourceType"] != "URL" {
		t.Errorf("unexpected payload %v", payload)
	}
	tool := payload["tools"].([]interface{})[0].(map[string]interface{})
	if _, ok := tool["inputSchema"]; ok {
		t.Error("the create payload carries schemas, the tenant derives them")
	}
	attributes := payload["additionalAttrs"].(map[string]interface{})
	if profile := attributes["productProfile"].([]interface{}); profile[0] != "integrationcell" {
		t.Errorf("productProfile = %v", profile)
	}
}
