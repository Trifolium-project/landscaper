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
	"fmt"
	"log"
	"strings"

	"github.com/Trifolium-project/landscaper/packages/cpiclient"
	"github.com/Trifolium-project/landscaper/packages/landscape"
	"github.com/Trifolium-project/landscaper/packages/mcpserver"
)

//MCP servers through the UI session of a system, shared by artifact list,
//upload, deploy and undeploy. See changelog/0009-mcp-server-artifacts.md.

//The only runtime an MCP server runs on
const mcpRuntimeLocation = "integrationcell"

//Configuration keys an MCP server accepts in the landscape file. The bundle has
//no externalized parameters, these two are what differs between environments.
const (
	mcpParameterPath      = "mcpPath"
	mcpParameterTargetURL = "targetURL"
)

//findMCPServer returns the server of a package, nil when the package does not
//hold it or does not exist
func findMCPServer(client *cpiclient.CPIClient, packageId string, serverId string) (*cpiclient.MCPServer, error) {

	servers, err := client.ReadMCPServers(packageId)
	if cpiclient.HasStatus(err, 404) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	for _, server := range servers {
		if server.Id == serverId {
			return server, nil
		}
	}

	return nil, nil
}

//uploadMCPServer replays an MCP server folder onto the tenant. There is no
//import for this type, so a missing server is created from its OpenAPI
//definition and every server then gets the editor model rewritten from the
//bundle and saved.
func uploadMCPServer(path string, targetEnvironment *landscape.Environment) (*uploadRow, error) {

	client := targetEnvironment.System.Client
	if !client.HasUISession() {
		return nil, cpiclient.ErrNoUISession
	}

	bundle, err := mcpserver.ReadBundle(path)
	if err != nil {
		return nil, err
	}

	if *uploadBump != "" || *uploadSetVersion != "" {
		log.Printf("--bump and --set-version are not applied to MCP server %s, the version of its manifest is used", bundle.Id)
	}

	//Ids follow the iFlow rules: the folder name without the suffix is the base
	baseArtifactId := trimTargetSuffix(artifactIdFromPath(path), targetEnvironment.Suffix)
	basePackageId, err := resolveBasePackageId(baseArtifactId)
	if err != nil {
		return nil, err
	}
	targetPackageId := basePackageId + targetEnvironment.Suffix

	bundle.Id = baseArtifactId + targetEnvironment.Suffix
	//Unlike an iFlow name, an MCP server name may hold letters, digits and
	//underscores only, so the suffix is appended as it is to the id
	bundle.Name = mcpServerName(bundle.Name, targetEnvironment.Suffix)

	if err := applyMCPConfiguration(bundle, basePackageId, baseArtifactId); err != nil {
		return nil, err
	}

	if err := ensureTargetPackage(targetEnvironment, targetPackageId, basePackageId); err != nil {
		return nil, err
	}

	packageRegistryId, err := client.ReadPackageRegistryId(targetPackageId)
	if err != nil {
		return nil, err
	}

	server, err := findMCPServer(client, targetPackageId, bundle.Id)
	if err != nil {
		return nil, err
	}

	row := &uploadRow{
		ArtifactId:    bundle.Id,
		Source:        "folder",
		PackageId:     targetPackageId,
		TenantVersion: versionNotInTenant,
		UploadVersion: bundle.Version,
	}

	if server == nil {
		registryId, err := client.CreateMCPServer(packageRegistryId, mcpserver.CreatePayload(bundle))
		if err != nil {
			return nil, err
		}
		server = &cpiclient.MCPServer{Id: bundle.Id, Name: bundle.Name, PackageId: targetPackageId, RegistryId: registryId}
		row.Action = "created"
	} else {
		row.TenantVersion = server.Version
		row.Action = "updated"
	}

	model, err := client.ReadMCPServerModel(packageRegistryId, server)
	if err != nil {
		return nil, err
	}
	if err := mcpserver.ApplyBundle(model, bundle); err != nil {
		return nil, err
	}
	if err := saveMCPServerModel(client, packageRegistryId, server, model); err != nil {
		return nil, err
	}
	server.Name = bundle.Name

	if *uploadDeploy {
		taskId, err := client.DeployMCPServer(packageRegistryId, server, bundle.Profile, bundle.VirtualHost)
		if err != nil {
			return nil, err
		}
		row.Deployed = true

		if uploadWait != nil && *uploadWait {
			row.Status = waitForDeployment(&mcpDeployTaskReader{client: client, taskId: taskId, server: server},
				bundle.Id, bundle.Version, *uploadTimeout, *uploadInterval)
		}
	}

	return row, nil
}

//saveMCPServerModel saves the model under the edit lock, as Edit, Save and
//leaving the editor do in the UI. The lock is released also when the save fails,
//so that the server can still be edited in the UI.
func saveMCPServerModel(client *cpiclient.CPIClient, packageRegistryId string, server *cpiclient.MCPServer, model map[string]interface{}) (err error) {

	if err := client.LockMCPServer(packageRegistryId, server); err != nil {
		return fmt.Errorf("MCP server %s cannot be locked for editing: %s", server.Id, err)
	}
	defer func() {
		if unlockErr := client.UnlockMCPServer(packageRegistryId, server); unlockErr != nil {
			log.Printf("MCP server %s stays locked, release it in the UI: %s", server.Id, unlockErr)
		}
	}()

	return client.UpdateMCPServerModel(packageRegistryId, server, model)
}

//applyMCPConfiguration takes mcpPath and targetURL of the target environment
//from the landscape file. Any other key is refused rather than ignored.
func applyMCPConfiguration(bundle *mcpserver.Bundle, basePackageId string, baseArtifactId string) error {

	parameters, err := globalLandscape.GetArtifactConfiguration(*uploadTargetEnv, basePackageId, baseArtifactId)
	if err != nil {
		return err
	}

	for _, parameter := range parameters {
		switch parameter.Key {
		case mcpParameterPath:
			bundle.SetMcpPath(parameter.Value)
		case mcpParameterTargetURL:
			bundle.SetTargetURL(parameter.Value)
		default:
			return fmt.Errorf("MCP server %s has no configuration parameter %s, only %s and %s can be set",
				baseArtifactId, parameter.Key, mcpParameterPath, mcpParameterTargetURL)
		}
	}

	return nil
}

//deployedMCPServer is what the deploy command reports
type deployedMCPServer struct {
	Server  *cpiclient.MCPServer
	Version string
	TaskId  string
}

//deployMCPServerById deploys the saved state of a server of a package, with
//the runtime and virtual host its manifest names
func deployMCPServerById(client *cpiclient.CPIClient, packageId string, serverId string) (*deployedMCPServer, error) {

	server, err := findMCPServer(client, packageId, serverId)
	if err != nil {
		return nil, err
	}
	if server == nil {
		return nil, fmt.Errorf("Package %s holds no MCP server %s", packageId, serverId)
	}

	packageRegistryId, err := client.ReadPackageRegistryId(packageId)
	if err != nil {
		return nil, err
	}
	model, err := client.ReadMCPServerModel(packageRegistryId, server)
	if err != nil {
		return nil, err
	}

	manifest := modelManifest(model)
	runtimeLocation := manifest["SAP-RuntimeProfile"]
	if runtimeLocation == "" {
		runtimeLocation = mcpRuntimeLocation
	}
	virtualHost := manifest["DT-VirtualHostNames"]
	if virtualHost == "" {
		virtualHost = "default"
	}

	taskId, err := client.DeployMCPServer(packageRegistryId, server, runtimeLocation, virtualHost)
	if err != nil {
		return nil, err
	}

	return &deployedMCPServer{Server: server, Version: manifest["Bundle-Version"], TaskId: taskId}, nil
}

//modelManifest reads the manifest headers of an editor model
func modelManifest(model map[string]interface{}) map[string]string {

	headers := map[string]string{}
	flow, _ := model["iflow"].(map[string]interface{})
	view, _ := flow["propertyViewModel"].(map[string]interface{})
	manifest, _ := view["manifestContent"].(map[string]interface{})
	for key, value := range manifest {
		if text, ok := value.(string); ok {
			headers[key] = text
		}
	}

	return headers
}

//deployedMCPServerArtifact finds the deployment of a server on the Integration
//Cell, nil when it is not deployed or is being removed
func deployedMCPServerArtifact(client *cpiclient.CPIClient, serverId string) (*cpiclient.IntegrationCellArtifact, error) {

	artifacts, err := client.ReadIntegrationCellArtifacts(mcpRuntimeLocation)
	if err != nil {
		return nil, err
	}

	return findMCPServerArtifact(artifacts, serverId), nil
}

func findMCPServerArtifact(artifacts []*cpiclient.IntegrationCellArtifact, serverId string) *cpiclient.IntegrationCellArtifact {
	for _, artifact := range artifacts {
		if artifact.Type != cpiclient.IntegrationCellTypeMCPServer || artifact.SymbolicName != serverId {
			continue
		}
		if artifact.DeployState == "UNDEPLOYING" || artifact.DeployState == "DELETED" {
			continue
		}
		return artifact
	}
	return nil
}

//mcpDeployTaskReader lets waitForDeployment poll an MCP server like an iFlow.
//It reads the deployment task, not the Integration Cell list: a deployment
//that fails stays STARTING in the list for good, only the task says FAIL.
type mcpDeployTaskReader struct {
	client *cpiclient.CPIClient
	taskId string
	server *cpiclient.MCPServer
}

func (reader *mcpDeployTaskReader) ReadIntegrationRuntimeArtifact(ArtifactId string) (*cpiclient.IntegrationRuntimeArtifact, error) {

	task, err := reader.client.ReadMCPDeployTask(reader.taskId, reader.server)
	if err != nil {
		return nil, err
	}

	status := runtimeStatusStarting
	switch task.Status {
	case cpiclient.MCPDeployTaskSuccess:
		status = runtimeStatusStarted
	case cpiclient.MCPDeployTaskFailed:
		status = runtimeStatusError
	}

	return &cpiclient.IntegrationRuntimeArtifact{
		Id:         ArtifactId,
		Version:    task.Version,
		Name:       reader.server.Name,
		Type:       cpiclient.IntegrationCellTypeMCPServer,
		DeployedBy: task.DeployedBy,
		DeployedOn: task.DeployedOn,
		Status:     status,
	}, nil
}

//The task carries no error text. The UI shows none either, Monitor has the log.
func (reader *mcpDeployTaskReader) ReadIntegrationRuntimeArtifactErrorInformation(ArtifactId string) (*cpiclient.RuntimeErrorInformation, error) {
	return &cpiclient.RuntimeErrorInformation{
		Text: "the deployment task of the MCP server failed, see Monitor -> Manage Integration Content of the Integration Cell",
	}, nil
}

//mcpServerName appends the suffix of the environment to a server name, taking
//off one it may already have been downloaded with
func mcpServerName(name string, suffix string) string {
	if suffix == "" {
		return name
	}
	if trimmed := strings.TrimSuffix(trimNameSuffix(name, suffix), suffix); trimmed != "" {
		name = trimmed
	}
	return name + suffix
}
