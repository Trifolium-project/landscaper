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
	"fmt"
	"sort"
	"strings"
)

const readOnlyPackageMode = "READ_ONLY"

//Parameters, that SAP CPI does not allow to change. There is no sense in
//writing them to the landscape file, and an attempt to apply them fails
var DefaultSkipParameters = []string{"SAP_ProfileId"}

//Options of the landscape discovery
type DiscoverOptions struct {
	//Base(non suffixed) package ids to gather. Empty means every package of every system
	Packages []string
	//Include packages delivered by SAP, that cannot be changed
	IncludeReadOnly bool
	//Write complete parameter list for every environment instead of
	//the difference against the original environment
	AllParameters bool
	//Parameter keys, that are not written to the landscape file. A trailing
	//asterisk matches a prefix. Empty list writes all parameters
	SkipParameters []string
}

//Binding of one physical package of one tenant to one logical environment
type pkgBinding struct {
	PhysicalId  string
	Environment *Environment
}

//Connect to every system of the landscape and gather packages, artifacts and
//their configuration parameters. Returns packages, keyed by base(non suffixed)
//id, and a list of non fatal warnings.
func (landscape *Landscape) Discover(opts DiscoverOptions) (map[string]*Package, []string, error) {

	var warnings []string

	if err := landscape.validateForDiscovery(); err != nil {
		return nil, warnings, err
	}

	selectedPackages := map[string]bool{}
	for _, packageId := range opts.Packages {
		packageId = strings.TrimSpace(packageId)
		if packageId != "" {
			selectedPackages[packageId] = true
		}
	}

	packages := map[string]*Package{}
	foundPackages := map[string]bool{}

	for _, system := range landscape.sortedSystems() {

		environmentsOnSystem := landscape.environmentsOfSystem(system)
		if len(environmentsOnSystem) == 0 {
			continue
		}

		baseEnvironment, warning := pickBaseEnvironment(environmentsOnSystem, landscape.OriginalEnvironment)
		if warning != "" {
			warnings = append(warnings, fmt.Sprintf("System %s: %s", system.Id, warning))
		}

		integrationPackages, err := system.Client.ReadAllIntegrationPackages()
		if err != nil {
			return nil, warnings, fmt.Errorf("Unable to read packages of system %s: %s", system.Id, err)
		}

		packageIds := []string{}
		packageModes := map[string]string{}
		for _, integrationPackage := range integrationPackages {
			packageIds = append(packageIds, integrationPackage.Id)
			packageModes[integrationPackage.Id] = integrationPackage.Mode
		}

		bindings, classifyWarnings := classifyPackages(packageIds, environmentsOnSystem, baseEnvironment)
		for _, classifyWarning := range classifyWarnings {
			warnings = append(warnings, fmt.Sprintf("System %s: %s", system.Id, classifyWarning))
		}

		baseIds := []string{}
		for baseId := range bindings {
			baseIds = append(baseIds, baseId)
		}
		sort.Strings(baseIds)

		for _, baseId := range baseIds {
			explicitlySelected := selectedPackages[baseId]
			if len(selectedPackages) > 0 && !explicitlySelected {
				continue
			}

			for _, binding := range bindings[baseId] {
				//Packages delivered by SAP cannot be configured or transported,
				//unless the user asked for them explicitly
				if packageModes[binding.PhysicalId] == readOnlyPackageMode && !opts.IncludeReadOnly && !explicitlySelected {
					continue
				}

				foundPackages[baseId] = true

				gatherWarnings := landscape.gatherPackage(system, packages, baseId, binding, opts)
				warnings = append(warnings, gatherWarnings...)
			}
		}
	}

	for packageId := range selectedPackages {
		if !foundPackages[packageId] {
			warnings = append(warnings, fmt.Sprintf("Package %s is not found in any system of the landscape", packageId))
		}
	}

	warnings = append(warnings, landscape.reduceConfigurations(packages, opts.AllParameters)...)
	dropEmptyConfigurations(packages)

	return packages, warnings, nil
}

//Read artifacts and configurations of one physical package and merge them into
//the base package under the environment of the binding
func (landscape *Landscape) gatherPackage(system *System, packages map[string]*Package, baseId string, binding *pkgBinding, opts DiscoverOptions) []string {

	var warnings []string

	integrationArtifacts, err := system.Client.ReadIntegrationDesigntimeArtifacts(binding.PhysicalId, false)
	if err != nil {
		return append(warnings, fmt.Sprintf("Unable to read artifacts of package %s on system %s: %s", binding.PhysicalId, system.Id, err))
	}

	package_ := packages[baseId]
	if package_ == nil {
		package_ = &Package{
			Id:        baseId,
			Artifacts: map[string]*Artifact{},
		}
		packages[baseId] = package_
	}

	for _, integrationArtifact := range integrationArtifacts {

		baseArtifactId := trimEnvironmentSuffix(integrationArtifact.Id, binding.Environment.Suffix)

		artifact := package_.Artifacts[baseArtifactId]
		if artifact == nil {
			artifact = &Artifact{
				Id:             baseArtifactId,
				Configurations: map[string]*Configuration{},
			}
			package_.Artifacts[baseArtifactId] = artifact
		}

		configurations, err := system.Client.ReadIntegrationDesigntimeArtifactConfigurations(integrationArtifact.Id, integrationArtifact.Version)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("Unable to read configuration of artifact %s(version %s) of package %s on system %s: %s",
				integrationArtifact.Id, integrationArtifact.Version, binding.PhysicalId, system.Id, err))
			continue
		}

		parameters := []*Parameter{}
		for _, configuration := range configurations {
			//Parameters, that cannot be changed, are not a part of the landscape
			if isSkippedParameter(configuration.ParameterKey, opts.SkipParameters) {
				continue
			}

			parameterType := configuration.DataType
			if parameterType == "" {
				parameterType = "xsd:string"
			}
			parameters = append(parameters, &Parameter{
				Key:   configuration.ParameterKey,
				Value: configuration.ParameterValue,
				Type:  parameterType,
			})
		}

		artifact.Configurations[binding.Environment.Id] = &Configuration{
			Environment: binding.Environment.Id,
			Parameters:  parameters,
		}
	}

	return warnings
}

//Keep the complete parameter list of the original environment as a baseline and
//reduce every other environment to the parameters, that differ from it
func (landscape *Landscape) reduceConfigurations(packages map[string]*Package, allParameters bool) []string {

	var warnings []string

	originalEnvironmentId := landscape.OriginalEnvironment.Id

	for _, package_ := range packages {
		for _, artifact := range package_.Artifacts {

			baseline := artifact.Configurations[originalEnvironmentId]
			if baseline == nil {
				//Artifacts without any parameter are dropped anyway, there is
				//nothing to warn about
				if artifactHasParameters(artifact) {
					warnings = append(warnings, fmt.Sprintf("Artifact %s of package %s does not exist in the original environment %s, complete configuration is written for every environment",
						artifact.Id, package_.Id, originalEnvironmentId))
				}
				continue
			}

			if allParameters {
				continue
			}

			baselineValues := map[string]string{}
			for _, parameter := range baseline.Parameters {
				baselineValues[parameter.Key] = parameter.Value
			}

			for environmentId, configuration := range artifact.Configurations {
				if environmentId == originalEnvironmentId {
					continue
				}

				changedParameters := []*Parameter{}
				for _, parameter := range configuration.Parameters {
					baselineValue, exists := baselineValues[parameter.Key]
					if !exists || baselineValue != parameter.Value {
						changedParameters = append(changedParameters, parameter)
					}
				}

				//Nothing to override in this environment
				if len(changedParameters) == 0 {
					delete(artifact.Configurations, environmentId)
					continue
				}

				configuration.Parameters = changedParameters
			}
		}
	}

	return warnings
}

//Check, that the landscape file contains everything, that is necessary to connect
func (landscape *Landscape) validateForDiscovery() error {

	if len(landscape.Systems) == 0 {
		return fmt.Errorf("No systems are declared in the landscape file. See conf/landscape-minimal-example.yaml")
	}

	if len(landscape.Environments) == 0 {
		return fmt.Errorf("No environments are declared in the landscape file. See conf/landscape-minimal-example.yaml")
	}

	if landscape.OriginalEnvironment == nil {
		return fmt.Errorf("Original environment is not declared or does not match any environment id. See conf/landscape-minimal-example.yaml")
	}

	for _, environment := range landscape.Environments {
		if environment.System == nil {
			return fmt.Errorf("Environment %s refers to a system, that is not declared in the landscape file", environment.Id)
		}
	}

	return nil
}

//Systems of the landscape in a stable order
func (landscape *Landscape) sortedSystems() []*System {

	systemIds := []string{}
	for systemId := range landscape.Systems {
		systemIds = append(systemIds, systemId)
	}
	sort.Strings(systemIds)

	systems := []*System{}
	for _, systemId := range systemIds {
		systems = append(systems, landscape.Systems[systemId])
	}

	return systems
}

//Environments, that are hosted on the given system, in a stable order
func (landscape *Landscape) environmentsOfSystem(system *System) []*Environment {

	environmentIds := []string{}
	for environmentId, environment := range landscape.Environments {
		if environment.System != nil && environment.System.Id == system.Id {
			environmentIds = append(environmentIds, environmentId)
		}
	}
	sort.Strings(environmentIds)

	environments := []*Environment{}
	for _, environmentId := range environmentIds {
		environments = append(environments, landscape.Environments[environmentId])
	}

	return environments
}

//Environment without suffix of one system. Every system may host its own one -
//Dev on the development tenant, Prod on the production tenant
func pickBaseEnvironment(environmentsOnSystem []*Environment, originalEnvironment *Environment) (*Environment, string) {

	candidates := []*Environment{}
	for _, environment := range environmentsOnSystem {
		if environment.Suffix == "" {
			candidates = append(candidates, environment)
		}
	}

	if len(candidates) == 0 {
		return nil, "No environment without suffix is declared, packages without suffix are skipped"
	}

	if len(candidates) == 1 {
		return candidates[0], ""
	}

	chosen := candidates[0]
	for _, candidate := range candidates {
		if originalEnvironment != nil && candidate.Id == originalEnvironment.Id {
			chosen = candidate
		}
	}

	environmentIds := []string{}
	for _, candidate := range candidates {
		environmentIds = append(environmentIds, candidate.Id)
	}

	return chosen, fmt.Sprintf("Environments %s do not have a suffix and cannot be distinguished, %s is used for packages without suffix",
		strings.Join(environmentIds, ", "), chosen.Id)
}

//Correlate physical package ids of one tenant with the environments, that are
//hosted on it. A suffixed package is treated as a copy of the base package only
//if the base package exists on the same tenant, otherwise its name just happens
//to end with the suffix and it is a base package of its own.
func classifyPackages(packageIds []string, environmentsOnSystem []*Environment, baseEnvironment *Environment) (map[string][]*pkgBinding, []string) {

	var warnings []string
	bindings := map[string][]*pkgBinding{}

	existingPackages := map[string]bool{}
	for _, packageId := range packageIds {
		existingPackages[packageId] = true
	}

	//Longest suffix wins, so that a suffix is not shadowed by its own prefix
	suffixedEnvironments := []*Environment{}
	for _, environment := range environmentsOnSystem {
		if environment.Suffix != "" {
			suffixedEnvironments = append(suffixedEnvironments, environment)
		}
	}
	sort.SliceStable(suffixedEnvironments, func(i, j int) bool {
		return len(suffixedEnvironments[i].Suffix) > len(suffixedEnvironments[j].Suffix)
	})

	for _, packageId := range packageIds {

		matched := false

		for _, environment := range suffixedEnvironments {
			if packageId == environment.Suffix || !strings.HasSuffix(packageId, environment.Suffix) {
				continue
			}

			baseId := strings.TrimSuffix(packageId, environment.Suffix)
			if !existingPackages[baseId] {
				continue
			}

			bindings[baseId] = append(bindings[baseId], &pkgBinding{
				PhysicalId:  packageId,
				Environment: environment,
			})
			matched = true
			break
		}

		if matched {
			continue
		}

		if baseEnvironment == nil {
			warnings = append(warnings, fmt.Sprintf("Package %s is skipped, because no environment without suffix is declared", packageId))
			continue
		}

		bindings[packageId] = append(bindings[packageId], &pkgBinding{
			PhysicalId:  packageId,
			Environment: baseEnvironment,
		})
	}

	return bindings, warnings
}

//Whether a parameter key is not written to the landscape file. A pattern with a
//trailing asterisk matches a prefix, every other pattern matches the whole key
func isSkippedParameter(key string, patterns []string) bool {

	for _, pattern := range patterns {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" {
			continue
		}

		if strings.HasSuffix(pattern, "*") {
			if strings.HasPrefix(key, strings.TrimSuffix(pattern, "*")) {
				return true
			}
			continue
		}

		if key == pattern {
			return true
		}
	}

	return false
}

//Whether an artifact has at least one parameter in at least one environment
func artifactHasParameters(artifact *Artifact) bool {

	for _, configuration := range artifact.Configurations {
		if len(configuration.Parameters) > 0 {
			return true
		}
	}

	return false
}

//Remove configurations without parameters and artifacts without configurations.
//An artifact, whose parameters are all non changeable, has nothing to declare.
//Packages are kept, because a package without artifacts is still transported.
func dropEmptyConfigurations(packages map[string]*Package) {

	for _, package_ := range packages {
		for artifactId, artifact := range package_.Artifacts {

			for environmentId, configuration := range artifact.Configurations {
				if len(configuration.Parameters) == 0 {
					delete(artifact.Configurations, environmentId)
				}
			}

			if len(artifact.Configurations) == 0 {
				delete(package_.Artifacts, artifactId)
			}
		}
	}
}

//Remove the environment suffix from an artifact id
func trimEnvironmentSuffix(id string, suffix string) string {

	if suffix == "" || id == suffix || !strings.HasSuffix(id, suffix) {
		return id
	}

	return strings.TrimSuffix(id, suffix)
}
