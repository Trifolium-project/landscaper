package landscape

import (
	"sort"
	"strings"
	"testing"
)

func environment(id string, suffix string, system *System) *Environment {
	return &Environment{Id: id, Name: id, Suffix: suffix, System: system}
}

//Rendered as "baseId=physicalId:environmentId", sorted, for easy comparison
func formatBindings(bindings map[string][]*pkgBinding) string {

	lines := []string{}
	for baseId, baseBindings := range bindings {
		for _, binding := range baseBindings {
			lines = append(lines, baseId+"="+binding.PhysicalId+":"+binding.Environment.Id)
		}
	}
	sort.Strings(lines)

	return strings.Join(lines, " ")
}

func TestClassifyPackages(t *testing.T) {

	dev := &System{Id: "dev"}

	devEnvironment := environment("Dev", "", dev)
	qaEnvironment := environment("QA", "QA", dev)
	shortEnvironment := environment("Q", "Q", dev)

	tests := []struct {
		name            string
		packageIds      []string
		environments    []*Environment
		baseEnvironment *Environment
		expected        string
		expectWarning   bool
	}{
		{
			name:            "Suffixed package is grouped under the base package",
			packageIds:      []string{"Foo", "FooQA"},
			environments:    []*Environment{devEnvironment, qaEnvironment},
			baseEnvironment: devEnvironment,
			expected:        "Foo=Foo:Dev Foo=FooQA:QA",
		},
		{
			name:            "Suffixed package without base package stays on its own",
			packageIds:      []string{"FooQA"},
			environments:    []*Environment{devEnvironment, qaEnvironment},
			baseEnvironment: devEnvironment,
			expected:        "FooQA=FooQA:Dev",
		},
		{
			name:            "Longest suffix wins",
			packageIds:      []string{"Foo", "FooQ", "FooQA"},
			environments:    []*Environment{devEnvironment, shortEnvironment, qaEnvironment},
			baseEnvironment: devEnvironment,
			expected:        "Foo=Foo:Dev Foo=FooQ:Q Foo=FooQA:QA",
		},
		{
			name:            "Package, that is named as the suffix, is not stripped",
			packageIds:      []string{"QA"},
			environments:    []*Environment{devEnvironment, qaEnvironment},
			baseEnvironment: devEnvironment,
			expected:        "QA=QA:Dev",
		},
		{
			name:            "Packages without suffix are skipped, if no environment matches them",
			packageIds:      []string{"Foo", "FooQA"},
			environments:    []*Environment{qaEnvironment},
			baseEnvironment: nil,
			expected:        "Foo=FooQA:QA",
			expectWarning:   true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			bindings, warnings := classifyPackages(test.packageIds, test.environments, test.baseEnvironment)

			actual := formatBindings(bindings)
			if actual != test.expected {
				t.Errorf("expected bindings %q, got %q", test.expected, actual)
			}

			if test.expectWarning && len(warnings) == 0 {
				t.Errorf("expected a warning, got none")
			}
			if !test.expectWarning && len(warnings) != 0 {
				t.Errorf("expected no warnings, got %v", warnings)
			}
		})
	}
}

func TestSelectPackagesForEnvironment(t *testing.T) {

	dev := &System{Id: "dev"}

	devEnvironment := environment("Dev", "", dev)
	qaEnvironment := environment("QA", "QA", dev)
	stagingEnvironment := environment("Staging", "", dev)

	packageIds := []string{"Foo", "FooQA", "Bar", "SAPDelivered"}
	packageModes := map[string]string{"SAPDelivered": readOnlyPackageMode}

	tests := []struct {
		name            string
		environments    []*Environment
		baseEnvironment *Environment
		environment     *Environment
		expected        string
		expectWarning   bool
	}{
		{
			name:            "Suffixed environment gets only the suffixed packages",
			environments:    []*Environment{devEnvironment, qaEnvironment},
			baseEnvironment: devEnvironment,
			environment:     qaEnvironment,
			expected:        "FooQA",
		},
		{
			name:            "Base environment gets the packages without suffix",
			environments:    []*Environment{devEnvironment, qaEnvironment},
			baseEnvironment: devEnvironment,
			environment:     devEnvironment,
			expected:        "Bar Foo",
			expectWarning:   true, //SAPDelivered is read only
		},
		{
			name:            "Packages delivered by SAP are skipped",
			environments:    []*Environment{devEnvironment},
			baseEnvironment: devEnvironment,
			environment:     devEnvironment,
			//FooQA has no environment with suffix QA here, so it is a base package of its own
			expected:      "Bar Foo FooQA",
			expectWarning: true,
		},
		{
			name:            "Environment without suffix, that is not the base one, gets nothing",
			environments:    []*Environment{devEnvironment, stagingEnvironment, qaEnvironment},
			baseEnvironment: devEnvironment,
			environment:     stagingEnvironment,
			expected:        "",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			packages, warnings := selectPackagesForEnvironment(packageIds, packageModes,
				test.environments, test.baseEnvironment, test.environment)

			actual := strings.Join(packages, " ")
			if actual != test.expected {
				t.Errorf("expected packages %q, got %q", test.expected, actual)
			}

			if test.expectWarning && len(warnings) == 0 {
				t.Errorf("expected a warning, got none")
			}
		})
	}
}

//Two tenants, each one hosting an environment without suffix and a suffixed one.
//One package has to end up with four bindings under one base id.
func TestClassifyPackagesTwoSystems(t *testing.T) {

	dev := &System{Id: "dev"}
	prod := &System{Id: "prod"}

	devEnvironment := environment("Dev", "", dev)
	qaEnvironment := environment("QA", "QA", dev)
	prodEnvironment := environment("Prod", "", prod)
	preProdEnvironment := environment("PreProd", "PreProd", prod)

	devBindings, _ := classifyPackages(
		[]string{"Salesforce2S4", "Salesforce2S4QA"},
		[]*Environment{devEnvironment, qaEnvironment},
		devEnvironment,
	)
	prodBindings, _ := classifyPackages(
		[]string{"Salesforce2S4", "Salesforce2S4PreProd"},
		[]*Environment{prodEnvironment, preProdEnvironment},
		prodEnvironment,
	)

	merged := map[string][]*pkgBinding{}
	for baseId, bindings := range devBindings {
		merged[baseId] = append(merged[baseId], bindings...)
	}
	for baseId, bindings := range prodBindings {
		merged[baseId] = append(merged[baseId], bindings...)
	}

	if len(merged) != 1 {
		t.Fatalf("expected one base package, got %d: %s", len(merged), formatBindings(merged))
	}

	expected := "Salesforce2S4=Salesforce2S4:Dev Salesforce2S4=Salesforce2S4:Prod " +
		"Salesforce2S4=Salesforce2S4PreProd:PreProd Salesforce2S4=Salesforce2S4QA:QA"
	actual := formatBindings(merged)
	if actual != expected {
		t.Errorf("expected bindings %q, got %q", expected, actual)
	}
}

func TestPickBaseEnvironment(t *testing.T) {

	dev := &System{Id: "dev"}
	devEnvironment := environment("Dev", "", dev)
	qaEnvironment := environment("QA", "QA", dev)
	otherEnvironment := environment("Other", "", dev)

	chosen, warning := pickBaseEnvironment([]*Environment{devEnvironment, qaEnvironment}, devEnvironment)
	if chosen != devEnvironment || warning != "" {
		t.Errorf("expected Dev without warning, got %v, %q", chosen, warning)
	}

	chosen, warning = pickBaseEnvironment([]*Environment{qaEnvironment}, devEnvironment)
	if chosen != nil || warning == "" {
		t.Errorf("expected no environment and a warning, got %v, %q", chosen, warning)
	}

	//Original environment is preferred, if several environments have no suffix
	chosen, warning = pickBaseEnvironment([]*Environment{otherEnvironment, devEnvironment}, devEnvironment)
	if chosen != devEnvironment || warning == "" {
		t.Errorf("expected Dev and a warning, got %v, %q", chosen, warning)
	}
}

func TestTrimEnvironmentSuffix(t *testing.T) {

	tests := []struct {
		id       string
		suffix   string
		expected string
	}{
		{"com.sap.flowQA", "QA", "com.sap.flow"},
		{"com.sap.flow", "QA", "com.sap.flow"},
		{"com.sap.flow", "", "com.sap.flow"},
		{"QA", "QA", "QA"},
	}

	for _, test := range tests {
		actual := trimEnvironmentSuffix(test.id, test.suffix)
		if actual != test.expected {
			t.Errorf("trimEnvironmentSuffix(%q, %q) = %q, expected %q", test.id, test.suffix, actual, test.expected)
		}
	}
}

//Original environment keeps all its parameters, the others keep only the differences
func TestReduceConfigurations(t *testing.T) {

	dev := &System{Id: "dev"}
	devEnvironment := environment("Dev", "", dev)

	testLandscape := &Landscape{
		Systems:             map[string]*System{"dev": dev},
		Environments:        map[string]*Environment{"Dev": devEnvironment},
		OriginalEnvironment: devEnvironment,
	}

	artifact := &Artifact{
		Id: "flow",
		Configurations: map[string]*Configuration{
			"Dev": {Environment: "Dev", Parameters: []*Parameter{
				{Key: "Host", Value: "dev.example.com", Type: "xsd:string"},
				{Key: "Retry", Value: "3", Type: "xsd:integer"},
			}},
			"QA": {Environment: "QA", Parameters: []*Parameter{
				{Key: "Host", Value: "qa.example.com", Type: "xsd:string"},
				{Key: "Retry", Value: "3", Type: "xsd:integer"},
				{Key: "QAOnly", Value: "true", Type: "xsd:string"},
			}},
			"Prod": {Environment: "Prod", Parameters: []*Parameter{
				{Key: "Host", Value: "dev.example.com", Type: "xsd:string"},
				{Key: "Retry", Value: "3", Type: "xsd:integer"},
			}},
		},
	}

	packages := map[string]*Package{
		"Pkg": {Id: "Pkg", Artifacts: map[string]*Artifact{"flow": artifact}},
	}

	warnings := testLandscape.reduceConfigurations(packages, false)
	if len(warnings) != 0 {
		t.Errorf("expected no warnings, got %v", warnings)
	}

	if len(artifact.Configurations["Dev"].Parameters) != 2 {
		t.Errorf("expected the original environment to keep all parameters, got %d", len(artifact.Configurations["Dev"].Parameters))
	}

	qaParameters := artifact.Configurations["QA"].Parameters
	if len(qaParameters) != 2 {
		t.Fatalf("expected two changed parameters in QA, got %d", len(qaParameters))
	}
	for _, parameter := range qaParameters {
		if parameter.Key == "Retry" {
			t.Errorf("unchanged parameter Retry is written for QA")
		}
	}

	//Prod is identical to the original environment, so there is nothing to declare
	if _, exists := artifact.Configurations["Prod"]; exists {
		t.Errorf("expected the Prod configuration to be dropped, because it does not differ from Dev")
	}
}

//An artifact, that does not exist in the original environment, has no baseline
func TestReduceConfigurationsWithoutBaseline(t *testing.T) {

	dev := &System{Id: "dev"}
	devEnvironment := environment("Dev", "", dev)

	testLandscape := &Landscape{
		Environments:        map[string]*Environment{"Dev": devEnvironment},
		OriginalEnvironment: devEnvironment,
	}

	artifact := &Artifact{
		Id: "flow",
		Configurations: map[string]*Configuration{
			"QA": {Environment: "QA", Parameters: []*Parameter{{Key: "Host", Value: "qa.example.com"}}},
		},
	}

	warnings := testLandscape.reduceConfigurations(
		map[string]*Package{"Pkg": {Id: "Pkg", Artifacts: map[string]*Artifact{"flow": artifact}}}, false)

	if len(warnings) != 1 {
		t.Errorf("expected one warning, got %v", warnings)
	}
	if len(artifact.Configurations["QA"].Parameters) != 1 {
		t.Errorf("expected the QA parameters to be kept")
	}
}

func TestIsSkippedParameter(t *testing.T) {

	tests := []struct {
		name     string
		key      string
		patterns []string
		expected bool
	}{
		{"Default list skips the profile id", "SAP_ProfileId", DefaultSkipParameters, true},
		{"Default list keeps a user parameter, that starts with SAP_", "SAP_Client", DefaultSkipParameters, false},
		{"Default list keeps a regular parameter", "Host", DefaultSkipParameters, false},
		{"Prefix pattern skips the whole namespace", "SAP_Client", []string{"SAP_*"}, true},
		{"Prefix pattern does not match another prefix", "Host", []string{"SAP_*"}, false},
		{"Empty list skips nothing", "SAP_ProfileId", []string{}, false},
		{"Disabled flag skips nothing", "SAP_ProfileId", []string{""}, false},
		{"Blank patterns are ignored", "SAP_ProfileId", []string{"  ", "SAP_ProfileId"}, true},
		{"Asterisk skips everything", "Host", []string{"*"}, true},
		{"Keys are case sensitive", "sap_profileid", DefaultSkipParameters, false},
		{"Pattern is trimmed", "SAP_ProfileId", []string{" SAP_ProfileId "}, true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			actual := isSkippedParameter(test.key, test.patterns)
			if actual != test.expected {
				t.Errorf("isSkippedParameter(%q, %v) = %t, expected %t", test.key, test.patterns, actual, test.expected)
			}
		})
	}
}

//An artifact, whose parameters are all non changeable, has nothing to declare
func TestDropEmptyConfigurations(t *testing.T) {

	emptyArtifact := &Artifact{
		Id: "onlyNonChangeable",
		Configurations: map[string]*Configuration{
			"Dev": {Environment: "Dev", Parameters: []*Parameter{}},
		},
	}
	filledArtifact := &Artifact{
		Id: "flow",
		Configurations: map[string]*Configuration{
			"Dev": {Environment: "Dev", Parameters: []*Parameter{{Key: "Host", Value: "dev.example.com"}}},
			"QA":  {Environment: "QA", Parameters: []*Parameter{}},
		},
	}

	packages := map[string]*Package{
		"Pkg": {Id: "Pkg", Artifacts: map[string]*Artifact{
			"onlyNonChangeable": emptyArtifact,
			"flow":              filledArtifact,
		}},
	}

	dropEmptyConfigurations(packages)

	if _, exists := packages["Pkg"].Artifacts["onlyNonChangeable"]; exists {
		t.Errorf("expected an artifact without parameters to be dropped")
	}

	//The package itself is kept, a package without artifacts is still transported
	if packages["Pkg"] == nil {
		t.Fatalf("expected the package to be kept")
	}

	if len(filledArtifact.Configurations) != 1 {
		t.Errorf("expected the empty QA configuration to be dropped, got %v", filledArtifact.Configurations)
	}
	if filledArtifact.Configurations["Dev"] == nil {
		t.Errorf("expected the Dev configuration to be kept")
	}
}

//No warning is produced for artifacts, that are dropped anyway
func TestReduceConfigurationsWithoutBaselineAndWithoutParameters(t *testing.T) {

	dev := &System{Id: "dev"}
	devEnvironment := environment("Dev", "", dev)

	testLandscape := &Landscape{
		Environments:        map[string]*Environment{"Dev": devEnvironment},
		OriginalEnvironment: devEnvironment,
	}

	artifact := &Artifact{
		Id: "flow",
		Configurations: map[string]*Configuration{
			"QA": {Environment: "QA", Parameters: []*Parameter{}},
		},
	}

	warnings := testLandscape.reduceConfigurations(
		map[string]*Package{"Pkg": {Id: "Pkg", Artifacts: map[string]*Artifact{"flow": artifact}}}, false)

	if len(warnings) != 0 {
		t.Errorf("expected no warnings, got %v", warnings)
	}
}
