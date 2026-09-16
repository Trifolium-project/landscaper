package landscape

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const minimalLandscapeFile = `#Minimal example of landscape declaration
landscape:
  name: Test env
  systems:
    - id: dev
      name: Development Tenant
      host: xxxxxxx-tmn.hci.ru1.hana.ondemand.com
      login: DEV_LOGIN_ENV_VAR
      password: DEV_PASSWORD_ENV_VAR
  environments:
    - id: Dev
      name: Development Environment
      suffix: null
      system: dev
    - id: QA
      name: QA Environment
      suffix: QA
      system: dev
  originalEnvironment: Dev
`

func testLandscapeWithPackages() (*Landscape, map[string]*Package) {

	dev := &System{Id: "dev"}
	devEnvironment := environment("Dev", "", dev)
	qaEnvironment := environment("QA", "QA", dev)

	testLandscape := &Landscape{
		Name:                "Test env",
		Systems:             map[string]*System{"dev": dev},
		Environments:        map[string]*Environment{"Dev": devEnvironment, "QA": qaEnvironment},
		OriginalEnvironment: devEnvironment,
	}

	packages := map[string]*Package{
		"Pkg": {Id: "Pkg", Artifacts: map[string]*Artifact{
			"flow": {Id: "flow", Configurations: map[string]*Configuration{
				"QA": {Environment: "QA", Parameters: []*Parameter{
					{Key: "Retry", Value: "5", Type: "xsd:integer"},
					{Key: "Host", Value: "qa.example.com", Type: "xsd:string"},
				}},
				"Dev": {Environment: "Dev", Parameters: []*Parameter{
					{Key: "Host", Value: "dev.example.com", Type: "xsd:string"},
				}},
			}},
		}},
	}

	return testLandscape, packages
}

func TestRenderPackagesKeepsOtherSections(t *testing.T) {

	directory := t.TempDir()
	sourceFile := filepath.Join(directory, "landscape.yaml")
	if err := os.WriteFile(sourceFile, []byte(minimalLandscapeFile), 0644); err != nil {
		t.Fatal(err)
	}

	testLandscape, packages := testLandscapeWithPackages()

	document, err := testLandscape.RenderPackages(sourceFile, packages)
	if err != nil {
		t.Fatal(err)
	}
	rendered := string(document)

	//Comments and the sections, that are not generated, are kept
	for _, expected := range []string{
		"#Minimal example of landscape declaration",
		"host: xxxxxxx-tmn.hci.ru1.hana.ondemand.com",
		"originalEnvironment: Dev",
		"packages:",
		"- id: Pkg",
		"- id: flow",
		"- environment: Dev",
		"- environment: QA",
		"key: Host",
		"type: xsd:integer",
	} {
		if !strings.Contains(rendered, expected) {
			t.Errorf("expected the generated file to contain %q:\n%s", expected, rendered)
		}
	}

	//The default parameter type is not written down
	if strings.Contains(rendered, "xsd:string") {
		t.Errorf("expected the default parameter type to be omitted:\n%s", rendered)
	}

	//Packages are written before the environments, as in the example files
	if strings.Index(rendered, "packages:") > strings.Index(rendered, "environments:") {
		t.Errorf("expected the packages section before the environments section:\n%s", rendered)
	}

	//The original environment comes first, because it is the baseline
	if strings.Index(rendered, "- environment: Dev") > strings.Index(rendered, "- environment: QA") {
		t.Errorf("expected the original environment first:\n%s", rendered)
	}

	//Parameters are sorted, so that two runs produce the same file
	if strings.Index(rendered, "key: Host") > strings.Index(rendered, "key: Retry") {
		t.Errorf("expected sorted parameters:\n%s", rendered)
	}
}

//The generated file has to be readable by the landscape loader again
func TestWritePackagesRoundTrip(t *testing.T) {

	directory := t.TempDir()
	sourceFile := filepath.Join(directory, "landscape.yaml")
	targetFile := filepath.Join(directory, "landscape-generated.yaml")
	if err := os.WriteFile(sourceFile, []byte(minimalLandscapeFile), 0644); err != nil {
		t.Fatal(err)
	}

	testLandscape, packages := testLandscapeWithPackages()

	if err := testLandscape.WritePackages(sourceFile, targetFile, packages); err != nil {
		t.Fatal(err)
	}

	t.Setenv("DEV_LOGIN_ENV_VAR", "login")
	t.Setenv("DEV_PASSWORD_ENV_VAR", "password")

	reloaded, err := NewLandscape(targetFile)
	if err != nil {
		t.Fatal(err)
	}

	artifact := reloaded.Packages["Pkg"].Artifacts["flow"]
	if artifact == nil {
		t.Fatalf("expected artifact flow of package Pkg, got %v", reloaded.Packages)
	}

	parameters, err := reloaded.GetArtifactConfiguration("QA", "Pkg", "flow")
	if err != nil {
		t.Fatal(err)
	}
	if len(parameters) != 2 {
		t.Fatalf("expected two QA parameters, got %v", parameters)
	}

	//The omitted default type is restored by the loader
	for _, parameter := range parameters {
		if parameter.Key == "Host" && parameter.Type != "xsd:string" {
			t.Errorf("expected the default type xsd:string for Host, got %q", parameter.Type)
		}
	}

	//Writing in place keeps the file readable as well
	if err := testLandscape.WritePackages(targetFile, targetFile, packages); err != nil {
		t.Fatal(err)
	}
	if _, err := NewLandscape(targetFile); err != nil {
		t.Fatal(err)
	}
}
