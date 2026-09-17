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

package landscape

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"gopkg.in/yaml.v3"
)

//Indentation of the generated landscape file
const yamlIndent = 2

//Separate structures are used for writing, so that empty fields of the
//internal model do not end up in the generated file
type packageExport struct {
	Id        string           `yaml:"id"`
	Artifacts []artifactExport `yaml:"artifacts,omitempty"`
}

type artifactExport struct {
	Id             string                `yaml:"id"`
	Template       string                `yaml:"template,omitempty"`
	Configurations []configurationExport `yaml:"configurations,omitempty"`
}

type configurationExport struct {
	Environment string            `yaml:"environment"`
	Parameters  []parameterExport `yaml:"parameters,omitempty"`
}

type parameterExport struct {
	Key   string `yaml:"key"`
	Value string `yaml:"value"`
	Type  string `yaml:"type,omitempty"`
}

//Write the landscape file with the gathered packages. Systems, environments and
//the comments of the source file are kept as they are, only the packages section
//is replaced. Target and source may be the same file.
func (landscape *Landscape) WritePackages(sourceFilePath string, targetFilePath string, packages map[string]*Package) error {

	document, err := landscape.RenderPackages(sourceFilePath, packages)
	if err != nil {
		return err
	}

	//Write through a temporary file, so that the source file is not damaged
	//if writing fails halfway
	directory := filepath.Dir(targetFilePath)
	temporaryFile, err := os.CreateTemp(directory, filepath.Base(targetFilePath)+".tmp")
	if err != nil {
		return err
	}
	temporaryFileName := temporaryFile.Name()

	_, err = temporaryFile.Write(document)
	if err != nil {
		temporaryFile.Close()
		os.Remove(temporaryFileName)
		return err
	}

	err = temporaryFile.Close()
	if err != nil {
		os.Remove(temporaryFileName)
		return err
	}

	return os.Rename(temporaryFileName, targetFilePath)
}

//Build the content of a landscape file out of the source file and the gathered packages
func (landscape *Landscape) RenderPackages(sourceFilePath string, packages map[string]*Package) ([]byte, error) {

	source, err := os.ReadFile(sourceFilePath)
	if err != nil {
		return nil, err
	}

	var document yaml.Node
	err = yaml.Unmarshal(source, &document)
	if err != nil {
		return nil, err
	}

	if len(document.Content) == 0 || document.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("File %s does not contain a landscape declaration", sourceFilePath)
	}

	landscapeNode := childNode(document.Content[0], "landscape")
	if landscapeNode == nil || landscapeNode.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("File %s does not contain a landscape declaration", sourceFilePath)
	}

	var packagesNode yaml.Node
	err = packagesNode.Encode(landscape.buildPackageExport(packages))
	if err != nil {
		return nil, err
	}

	setChildNode(landscapeNode, "packages", &packagesNode, "environments")

	var buffer []byte
	buffer, err = marshalDocument(&document)
	if err != nil {
		return nil, err
	}

	return buffer, nil
}

//Convert the internal model into the structures, that are written to the file.
//Everything is sorted, so that two runs of the command produce the same file.
func (landscape *Landscape) buildPackageExport(packages map[string]*Package) []packageExport {

	packageIds := []string{}
	for packageId := range packages {
		packageIds = append(packageIds, packageId)
	}
	sort.Strings(packageIds)

	packageExports := []packageExport{}

	for _, packageId := range packageIds {
		package_ := packages[packageId]

		artifactIds := []string{}
		for artifactId := range package_.Artifacts {
			artifactIds = append(artifactIds, artifactId)
		}
		sort.Strings(artifactIds)

		artifactExports := []artifactExport{}

		for _, artifactId := range artifactIds {
			artifact := package_.Artifacts[artifactId]

			configurationExports := []configurationExport{}

			for _, environmentId := range landscape.sortedEnvironmentIds(artifact.Configurations) {
				configuration := artifact.Configurations[environmentId]

				parameterExports := []parameterExport{}
				parameters := append([]*Parameter{}, configuration.Parameters...)
				sort.SliceStable(parameters, func(i, j int) bool {
					return parameters[i].Key < parameters[j].Key
				})

				for _, parameter := range parameters {
					parameterType := parameter.Type
					//Default type of the landscape file, no need to write it down
					if parameterType == "xsd:string" {
						parameterType = ""
					}

					parameterExports = append(parameterExports, parameterExport{
						Key:   parameter.Key,
						Value: parameter.Value,
						Type:  parameterType,
					})
				}

				configurationExports = append(configurationExports, configurationExport{
					Environment: environmentId,
					Parameters:  parameterExports,
				})
			}

			artifactExports = append(artifactExports, artifactExport{
				Id:             artifact.Id,
				Template:       artifact.Template,
				Configurations: configurationExports,
			})
		}

		packageExports = append(packageExports, packageExport{
			Id:        package_.Id,
			Artifacts: artifactExports,
		})
	}

	return packageExports
}

//Original environment comes first, because it is the baseline of all the others
func (landscape *Landscape) sortedEnvironmentIds(configurations map[string]*Configuration) []string {

	environmentIds := []string{}
	for environmentId := range configurations {
		environmentIds = append(environmentIds, environmentId)
	}
	sort.Strings(environmentIds)

	if landscape.OriginalEnvironment == nil {
		return environmentIds
	}

	sortedEnvironmentIds := []string{}
	if configurations[landscape.OriginalEnvironment.Id] != nil {
		sortedEnvironmentIds = append(sortedEnvironmentIds, landscape.OriginalEnvironment.Id)
	}
	for _, environmentId := range environmentIds {
		if environmentId != landscape.OriginalEnvironment.Id {
			sortedEnvironmentIds = append(sortedEnvironmentIds, environmentId)
		}
	}

	return sortedEnvironmentIds
}

//Value node of a key of a mapping node
func childNode(mappingNode *yaml.Node, key string) *yaml.Node {

	for index := 0; index+1 < len(mappingNode.Content); index += 2 {
		if mappingNode.Content[index].Value == key {
			return mappingNode.Content[index+1]
		}
	}

	return nil
}

//Replace the value of a key of a mapping node, or add the key. A new key is
//placed before beforeKey, to keep the usual order of the landscape file.
func setChildNode(mappingNode *yaml.Node, key string, value *yaml.Node, beforeKey string) {

	for index := 0; index+1 < len(mappingNode.Content); index += 2 {
		if mappingNode.Content[index].Value == key {
			//Keep the comments of the existing key
			value.HeadComment = mappingNode.Content[index+1].HeadComment
			value.LineComment = mappingNode.Content[index+1].LineComment
			value.FootComment = mappingNode.Content[index+1].FootComment
			mappingNode.Content[index+1] = value
			return
		}
	}

	keyNode := &yaml.Node{
		Kind:  yaml.ScalarNode,
		Tag:   "!!str",
		Value: key,
	}

	for index := 0; index+1 < len(mappingNode.Content); index += 2 {
		if mappingNode.Content[index].Value == beforeKey {
			content := []*yaml.Node{}
			content = append(content, mappingNode.Content[:index]...)
			content = append(content, keyNode, value)
			content = append(content, mappingNode.Content[index:]...)
			mappingNode.Content = content
			return
		}
	}

	mappingNode.Content = append(mappingNode.Content, keyNode, value)
}

//Marshal a document node with the indentation of the landscape files
func marshalDocument(document *yaml.Node) ([]byte, error) {

	buffer := &bytes.Buffer{}
	encoder := yaml.NewEncoder(buffer)
	encoder.SetIndent(yamlIndent)

	err := encoder.Encode(document)
	if err != nil {
		encoder.Close()
		return nil, err
	}

	err = encoder.Close()
	if err != nil {
		return nil, err
	}

	return buffer.Bytes(), nil
}
