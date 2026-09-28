package landscape

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeLandscapeWithPackages(t *testing.T, packagesSection string) string {
	t.Helper()

	file := filepath.Join(t.TempDir(), "landscape.yaml")
	document := strings.Replace(minimalLandscapeFile, "  environments:", packagesSection+"  environments:", 1)
	if err := os.WriteFile(file, []byte(document), 0644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("DEV_LOGIN_ENV_VAR", "login")
	t.Setenv("DEV_PASSWORD_ENV_VAR", "password")

	return file
}

func TestGuidelineSkipsAreLoaded(t *testing.T) {

	file := writeLandscapeWithPackages(t, `  packages:
    - id: Pkg
      artifacts:
        - id: flow
          guidelineSkips:
            - rule: HANDLE_EXCEPTIONS
              reason: "  Errors are handled by the caller  "
            - rule: USE_CSRF_PROTECTION
              reason: Called by a system that cannot fetch tokens
`)

	loaded, err := NewLandscape(file)
	if err != nil {
		t.Fatal(err)
	}

	skips := loaded.GetGuidelineSkips("flow")
	if len(skips) != 2 {
		t.Fatalf("expected two skips, got %v", skips)
	}
	if skips[0].Rule != "HANDLE_EXCEPTIONS" || skips[0].Reason != "Errors are handled by the caller" {
		t.Errorf("unexpected first skip %+v, the reason should be trimmed", skips[0])
	}

	if other := loaded.GetGuidelineSkips("unknown"); len(other) != 0 {
		t.Errorf("an undeclared artifact should have no skips, got %v", other)
	}
}

//The tenant refuses a skip without a reason, so the file is rejected up front
//rather than failing half way through an upload
func TestGuidelineSkipWithoutReasonIsRejected(t *testing.T) {

	file := writeLandscapeWithPackages(t, `  packages:
    - id: Pkg
      artifacts:
        - id: flow
          guidelineSkips:
            - rule: HANDLE_EXCEPTIONS
`)

	_, err := NewLandscape(file)
	if err == nil || !strings.Contains(err.Error(), "HANDLE_EXCEPTIONS") {
		t.Fatalf("expected an error naming the rule, got %v", err)
	}
}

func TestGuidelineSkipsSurviveWritePackages(t *testing.T) {

	directory := t.TempDir()
	sourceFile := filepath.Join(directory, "landscape.yaml")
	targetFile := filepath.Join(directory, "landscape-generated.yaml")
	if err := os.WriteFile(sourceFile, []byte(minimalLandscapeFile), 0644); err != nil {
		t.Fatal(err)
	}

	testLandscape, packages := testLandscapeWithPackages()
	packages["Pkg"].Artifacts["flow"].GuidelineSkips = []*GuidelineSkip{
		{Rule: "HANDLE_EXCEPTIONS", Reason: "Handled by the caller"},
	}

	if err := testLandscape.WritePackages(sourceFile, targetFile, packages); err != nil {
		t.Fatal(err)
	}

	t.Setenv("DEV_LOGIN_ENV_VAR", "login")
	t.Setenv("DEV_PASSWORD_ENV_VAR", "password")

	reloaded, err := NewLandscape(targetFile)
	if err != nil {
		t.Fatal(err)
	}

	skips := reloaded.GetGuidelineSkips("flow")
	if len(skips) != 1 || skips[0].Rule != "HANDLE_EXCEPTIONS" || skips[0].Reason != "Handled by the caller" {
		t.Errorf("expected the skip to survive the round trip, got %v", skips)
	}
}

//"init" regenerates the packages from the tenant, which does not know the
//declared skips
func TestCarryOverGuidelineSkips(t *testing.T) {

	declared, _ := testLandscapeWithPackages()
	declared.Packages = map[string]*Package{
		"Pkg": {Id: "Pkg", Artifacts: map[string]*Artifact{
			"flow": {Id: "flow", GuidelineSkips: []*GuidelineSkip{{Rule: "HANDLE_EXCEPTIONS", Reason: "Handled"}}},
		}},
	}

	_, discovered := testLandscapeWithPackages()
	discovered["Pkg"].Artifacts["newFlow"] = &Artifact{Id: "newFlow"}

	declared.CarryOverGuidelineSkips(discovered)

	if skips := discovered["Pkg"].Artifacts["flow"].GuidelineSkips; len(skips) != 1 {
		t.Errorf("expected the declared skip to be carried over, got %v", skips)
	}
	if skips := discovered["Pkg"].Artifacts["newFlow"].GuidelineSkips; len(skips) != 0 {
		t.Errorf("an artifact without declaration should get no skips, got %v", skips)
	}
}
