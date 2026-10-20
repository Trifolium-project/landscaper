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

//Package mcpserver reads the exploded bundle of an MCP server and turns it into
//the editor model of the Integration Suite UI. The tenant has no import for
//MCP servers, so an upload replays the bundle onto that model. Pure local
//handling like packages/iflow: no HTTP, no cobra.
package mcpserver

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Trifolium-project/landscaper/packages/iflow"
)

//Value of SAP-BundleType in the manifest of an MCP server
const BundleType = "MCPSERVER"

//Locations inside the bundle
const (
	specificationPath = "src/main/resources/mcpspecification/mcpspecification.json"
	sourceMappingPath = "src/main/resources/mcpsourcemapping/mcpsourcemapping.json"
	apiDefinitionPath = "src/main/resources/apidefinition/apidefinition.json"
	integrationFlowDir = "src/main/resources/scenarioflows/integrationflow"
)

//Manifest headers of an MCP server, besides those of packages/iflow
const (
	headerTargetURL   = "targetURL"
	headerSourceType  = "mcpartifacttype"
	headerProfile     = "SAP-RuntimeProfile"
	headerNodeType    = "SAP-NodeType"
	headerVirtualHost = "DT-VirtualHostNames"
)

//Property keys of the integration flow that carry the two settings an
//environment may override
const (
	propertyURLPath    = "urlPath"
	propertyTargetURL  = "httpAddressWithoutQuery"
	propertyComponent  = "ComponentType"
	componentMCPSender = "MCP"
)

//Bundle is everything the bundle folder says about an MCP server
type Bundle struct {
	Id          string
	Name        string
	Version     string
	ShortText   string
	Description string
	TargetURL   string
	McpPath     string
	SourceType  string
	Profile     string
	NodeType    string
	VirtualHost string
	//apidefinition.json as it is, the model carries it as a string
	OpenAPI string
	//Tools, resources and prompts in the shape of the editor model
	Tools     []map[string]interface{}
	Resources []map[string]interface{}
	Prompts   []map[string]interface{}
	//ifl:property values of the integration flow, by element id and key
	Properties map[string]map[string]string
}

//IsBundleDir reports whether the folder holds an MCP server rather than an
//integration flow
func IsBundleDir(path string) bool {
	if !iflow.IsArtifactDir(path) {
		return false
	}
	bundleType, err := iflow.ReadManifestHeader(path, iflow.HeaderSAPBundleType)
	return err == nil && bundleType == BundleType
}

//ReadBundle reads an exploded MCP server bundle
func ReadBundle(dir string) (*Bundle, error) {

	if !IsBundleDir(dir) {
		return nil, fmt.Errorf("%s is not an MCP server folder (SAP-BundleType: %s)", dir, BundleType)
	}

	bundle := &Bundle{}
	headers := map[string]*string{
		iflow.HeaderBundleSymbolicName: &bundle.Id,
		iflow.HeaderBundleName:         &bundle.Name,
		iflow.HeaderBundleVersion:      &bundle.Version,
		headerTargetURL:                &bundle.TargetURL,
		headerSourceType:               &bundle.SourceType,
		headerProfile:                  &bundle.Profile,
		headerNodeType:                 &bundle.NodeType,
		headerVirtualHost:              &bundle.VirtualHost,
	}
	for header, target := range headers {
		//Absent headers stay empty, the defaults below cover the ones that matter
		value, _ := iflow.ReadManifestHeader(dir, header)
		*target = value
	}
	if bundle.Id == "" {
		return nil, fmt.Errorf("%s has no Bundle-SymbolicName", dir)
	}
	if bundle.Name == "" {
		bundle.Name = bundle.Id
	}
	if bundle.VirtualHost == "" {
		bundle.VirtualHost = "default"
	}

	openAPI, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(apiDefinitionPath)))
	if err != nil {
		return nil, err
	}
	bundle.OpenAPI = string(openAPI)

	if err := bundle.readSpecification(dir); err != nil {
		return nil, err
	}

	properties, err := readFlowProperties(dir)
	if err != nil {
		return nil, err
	}
	bundle.Properties = properties
	bundle.McpPath = bundle.findMcpPath()

	return bundle, nil
}

//specification is the MCP server card of mcpspecification.json
type specification struct {
	Title             string                   `json:"title"`
	Description       string                   `json:"description"`
	Tools             []map[string]interface{} `json:"tools"`
	Resources         []map[string]interface{} `json:"resources"`
	ResourceTemplates []map[string]interface{} `json:"resourceTemplates"`
	Prompts           []map[string]interface{} `json:"prompts"`
}

//sourceMapping of mcpsourcemapping.json: which operation serves a tool or a
//resource, and the messages of a prompt
type sourceMapping struct {
	Tools             map[string]mappingEntry `json:"tools"`
	Resources         map[string]mappingEntry `json:"resources"`
	ResourceTemplates map[string]mappingEntry `json:"resourceTemplates"`
	Prompts           map[string]struct {
		Messages []interface{} `json:"messages"`
	} `json:"prompts"`
}

type mappingEntry struct {
	OriginalToolName string `json:"originalToolName"`
	Metadata         struct {
		Method              string `json:"method"`
		Path                string `json:"path"`
		RequestContentType  string `json:"requestContentType"`
		ResponseContentType string `json:"responseContentType"`
	} `json:"metadata"`
}

//readSpecification combines the server card and the source mapping into the
//tool, resource and prompt entries of the editor model
func (bundle *Bundle) readSpecification(dir string) error {

	var spec specification
	if err := readJSON(filepath.Join(dir, filepath.FromSlash(specificationPath)), &spec); err != nil {
		return err
	}
	var mapping sourceMapping
	if err := readJSON(filepath.Join(dir, filepath.FromSlash(sourceMappingPath)), &mapping); err != nil {
		return err
	}

	bundle.ShortText = spec.Title
	bundle.Description = spec.Description

	bundle.Tools = []map[string]interface{}{}
	for _, tool := range spec.Tools {
		name := stringOf(tool["name"])
		source := mapping.Tools[name]
		entry := map[string]interface{}{
			"name":                name,
			"title":               stringOf(tool["title"]),
			"description":         stringOf(tool["description"]),
			"jouleEnabled":        false,
			"method":              source.Metadata.Method,
			"path":                source.Metadata.Path,
			"source":              source.Metadata.Path,
			"originalToolName":    "",
			"requestContentType":  source.Metadata.RequestContentType,
			"responseContentType": "",
		}
		//The model holds the schemas as JSON text, not as objects
		for _, key := range []string{"inputSchema", "outputSchema"} {
			if schema, ok := tool[key]; ok && schema != nil {
				text, err := json.Marshal(schema)
				if err != nil {
					return err
				}
				entry[key] = string(text)
			}
		}
		bundle.Tools = append(bundle.Tools, entry)
	}

	//The model lists templates and plain resources together, both with uriTemplate
	bundle.Resources = []map[string]interface{}{}
	appendResources := func(resources []map[string]interface{}, uriKey string, sources map[string]mappingEntry) {
		for _, resource := range resources {
			uri := stringOf(resource[uriKey])
			source := sources[uri]
			bundle.Resources = append(bundle.Resources, map[string]interface{}{
				"name":                stringOf(resource["name"]),
				"title":               stringOf(resource["title"]),
				"description":         stringOf(resource["description"]),
				"method":              source.Metadata.Method,
				"path":                source.Metadata.Path,
				"source":              source.Metadata.Path,
				"originalName":        "",
				"uriTemplate":         uri,
				"mimeType":            stringOf(resource["mimeType"]),
				"requestContentType":  "",
				"responseContentType": source.Metadata.ResponseContentType,
			})
		}
	}
	appendResources(spec.ResourceTemplates, "uriTemplate", mapping.ResourceTemplates)
	appendResources(spec.Resources, "uri", mapping.Resources)

	bundle.Prompts = []map[string]interface{}{}
	for _, prompt := range spec.Prompts {
		name := stringOf(prompt["name"])
		arguments := prompt["arguments"]
		if arguments == nil {
			arguments = []interface{}{}
		}
		messages := mapping.Prompts[name].Messages
		if messages == nil {
			messages = []interface{}{}
		}
		bundle.Prompts = append(bundle.Prompts, map[string]interface{}{
			"name":        name,
			"title":       stringOf(prompt["title"]),
			"description": stringOf(prompt["description"]),
			"arguments":   arguments,
			"messages":    messages,
		})
	}

	return nil
}

//findMcpPath reads the path of the MCP sender channel
func (bundle *Bundle) findMcpPath() string {
	for _, properties := range bundle.Properties {
		if properties[propertyComponent] == componentMCPSender {
			return properties[propertyURLPath]
		}
	}
	return ""
}

//SetMcpPath changes the endpoint path, in the flow properties as well
func (bundle *Bundle) SetMcpPath(path string) {
	bundle.replaceProperty(propertyURLPath, bundle.McpPath, path)
	bundle.McpPath = path
}

//SetTargetURL changes the URL of the source API, in the flow properties as well
func (bundle *Bundle) SetTargetURL(targetURL string) {
	bundle.replaceProperty(propertyTargetURL, bundle.TargetURL, targetURL)
	bundle.TargetURL = targetURL
}

func (bundle *Bundle) replaceProperty(key string, old string, value string) {
	for _, properties := range bundle.Properties {
		if current, ok := properties[key]; ok && current == old {
			properties[key] = value
		}
	}
}

//readFlowProperties collects the ifl:property key/value pairs of the
//integration flow, keyed by the id of the element they configure
func readFlowProperties(dir string) (map[string]map[string]string, error) {

	flows, err := filepath.Glob(filepath.Join(dir, filepath.FromSlash(integrationFlowDir), "*.iflw"))
	if err != nil {
		return nil, err
	}
	if len(flows) != 1 {
		return nil, fmt.Errorf("%s: expected one integration flow in %s, found %d", dir, integrationFlowDir, len(flows))
	}

	file, err := os.Open(flows[0])
	if err != nil {
		return nil, err
	}
	defer file.Close()

	return parseFlowProperties(file)
}

//parseFlowProperties attaches each property to the innermost enclosing element
//with an id, which is how the BPMN of SAP nests extensionElements
func parseFlowProperties(reader io.Reader) (map[string]map[string]string, error) {

	properties := map[string]map[string]string{}
	decoder := xml.NewDecoder(reader)

	//Ids of the open elements, "" for an element without one
	ids := []string{}
	var key, value, text string
	inProperty := false

	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}

		switch element := token.(type) {

		case xml.StartElement:
			id := ""
			for _, attribute := range element.Attr {
				if attribute.Name.Local == "id" && attribute.Name.Space == "" {
					id = attribute.Value
				}
			}
			ids = append(ids, id)
			text = ""
			if element.Name.Local == "property" {
				inProperty, key, value = true, "", ""
			}

		case xml.CharData:
			text += string(element)

		case xml.EndElement:
			switch {
			case inProperty && element.Name.Local == "key":
				key = strings.TrimSpace(text)
			case inProperty && element.Name.Local == "value":
				value = text
			case element.Name.Local == "property":
				inProperty = false
				if owner := innermostId(ids[:len(ids)-1]); owner != "" && key != "" {
					if properties[owner] == nil {
						properties[owner] = map[string]string{}
					}
					properties[owner][key] = value
				}
			}
			ids = ids[:len(ids)-1]
			text = ""
		}
	}

	return properties, nil
}

func innermostId(ids []string) string {
	for index := len(ids) - 1; index >= 0; index-- {
		if ids[index] != "" {
			return ids[index]
		}
	}
	return ""
}

func readJSON(path string, target interface{}) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(content, target); err != nil {
		return fmt.Errorf("%s: %s", path, err)
	}
	return nil
}

func stringOf(value interface{}) string {
	if text, ok := value.(string); ok {
		return text
	}
	return ""
}
