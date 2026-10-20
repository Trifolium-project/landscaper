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

package mcpserver

import (
	"fmt"
	"strings"
)

//The editor model names the collaboration "Collobaration_1", the flow it is
//saved to names it "Collaboration_1"
const (
	modelCollaborationId = "Collobaration_1"
	flowCollaborationId  = "Collaboration_1"
)

//Lists of the property view model whose elements carry allAttributes keyed by
//the id of a flow element
var modelElementLists = []string{
	"listOfDefaultChannelModel",
	"listOfDefaultFlowElementModel",
	"listOfDefaultProcessModel",
}

//CreatePayload is the body that creates an MCP server, the one the wizard of
//the UI sends. The tenant derives tool schemas from the OpenAPI definition;
//everything else is set by ApplyBundle afterwards.
func CreatePayload(bundle *Bundle) map[string]interface{} {

	tools := []map[string]interface{}{}
	for _, tool := range bundle.Tools {
		tools = append(tools, map[string]interface{}{
			"name":               tool["name"],
			"method":             tool["method"],
			"path":               tool["path"],
			"title":              tool["title"],
			"description":        tool["description"],
			"jouleEnabled":       false,
			"requestContentType": tool["requestContentType"],
		})
	}

	return map[string]interface{}{
		"name":        bundle.Name,
		"type":        "MCP",
		"id":          bundle.Id,
		"sourceType":  bundle.SourceType,
		"mcpPath":     bundle.McpPath,
		"virtualHost": bundle.VirtualHost,
		"additionalAttrs": map[string]interface{}{
			"productProfile": []string{bundle.Profile},
			"nodeType":       []string{bundle.NodeType},
			"virtualHost":    []string{bundle.VirtualHost},
			"sourceType":     []string{bundle.SourceType},
		},
		"tools":          tools,
		"openApiSpec":    bundle.OpenAPI,
		"shortText":      bundle.ShortText,
		"description":    bundle.Description,
		"httpSourceType": "URL",
		"targetURL":      bundle.TargetURL,
	}
}

//ApplyBundle writes the bundle onto the editor model read from the tenant, so
//that the model can be saved back. Settings the bundle does not carry, and
//attributes the flow has no property for, stay as the tenant has them.
func ApplyBundle(model map[string]interface{}, bundle *Bundle) error {

	model["name"] = bundle.Name
	model["shortText"] = bundle.ShortText
	model["description"] = bundle.Description
	model["openAPISpec"] = bundle.OpenAPI
	model["targetURL"] = bundle.TargetURL
	model["mcpPath"] = bundle.McpPath
	model["resources"] = bundle.Resources
	model["prompts"] = bundle.Prompts
	model["tools"] = mergeTools(model["tools"], bundle.Tools)

	flow, ok := model["iflow"].(map[string]interface{})
	if !ok {
		return fmt.Errorf("the model of %s has no iflow", bundle.Id)
	}
	view, ok := flow["propertyViewModel"].(map[string]interface{})
	if !ok {
		return fmt.Errorf("the model of %s has no propertyViewModel", bundle.Id)
	}

	if collaboration, ok := view["defaultIntegrationFlowModel"].(map[string]interface{}); ok {
		applyProperties(collaboration, bundle.Properties[flowCollaborationId])
		setAttribute(collaboration, "name", bundle.Name)
	}

	for _, listName := range modelElementLists {
		elements, _ := view[listName].([]interface{})
		for _, raw := range elements {
			element, ok := raw.(map[string]interface{})
			if !ok {
				continue
			}
			id, _ := element["id"].(string)
			applyProperties(element, bundle.Properties[id])
		}
	}

	if manifest, ok := view["manifestContent"].(map[string]interface{}); ok {
		manifest["Bundle-Name"] = bundle.Name
		manifest["targetURL"] = bundle.TargetURL
		if bundle.Version != "" {
			manifest["Bundle-Version"] = bundle.Version
		}
	}

	return nil
}

//mergeTools takes the tools of the bundle and keeps the OpenAPI fragments the
//tenant derived for a tool of the same name, which the bundle does not carry
func mergeTools(current interface{}, tools []map[string]interface{}) []map[string]interface{} {

	derived := map[string]map[string]interface{}{}
	list, _ := current.([]interface{})
	for _, raw := range list {
		if tool, ok := raw.(map[string]interface{}); ok {
			if name, ok := tool["name"].(string); ok {
				derived[name] = tool
			}
		}
	}

	merged := []map[string]interface{}{}
	for _, tool := range tools {
		entry := map[string]interface{}{}
		for key, value := range tool {
			entry[key] = value
		}
		if existing, ok := derived[stringOf(tool["name"])]; ok {
			for _, key := range []string{"inputApiDefinitionSchema", "outputApiDefinitionSchema"} {
				if value, ok := existing[key]; ok {
					entry[key] = value
				}
			}
		}
		merged = append(merged, entry)
	}

	return merged
}

//applyProperties sets the attributes of a model element from the flow
//properties of the same element. Only attributes the model already has are
//touched, so nothing unknown to the editor is introduced.
func applyProperties(element map[string]interface{}, properties map[string]string) {
	for key, value := range properties {
		setAttribute(element, key, value)
	}
}

func setAttribute(element map[string]interface{}, key string, value string) {

	attributes, ok := element["allAttributes"].(map[string]interface{})
	if !ok {
		return
	}
	attribute, ok := attributes[key].(map[string]interface{})
	if !ok {
		return
	}

	attribute["value"] = value
	//Multi valued attributes keep the single value and the list side by side
	if _, ok := attribute["values"]; ok {
		values := []string{}
		for _, part := range strings.Split(value, ",") {
			if part = strings.TrimSpace(part); part != "" {
				values = append(values, part)
			}
		}
		attribute["values"] = values
	}
}
