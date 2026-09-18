package cmd

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Trifolium-project/landscaper/packages/iflow"
)

//setDownloadFlags points the package level flag variables at known values.
//The global --pkg and --artifact are cleared, because the upload tests set
//them and the download command refuses to run while they carry a value.
func setDownloadFlags(t *testing.T, outputDir string) {
	t.Helper()

	packages := []string{}
	artifacts := []string{}
	all := false
	outputDirValue := outputDir
	force := false
	packageFlag := ""
	artifactFlag := ""

	downloadPackages = &packages
	downloadArtifacts = &artifacts
	downloadAll = &all
	downloadOutputDir = &outputDirValue
	downloadForce = &force
	pkg = &packageFlag
	artifact = &artifactFlag
}

//stubArtifact registers an artifact in the tenant, together with the archive
//the download call answers with
func stubArtifact(t *testing.T, tenant *stubTenant, packageId string, artifactId string, version string) {
	t.Helper()

	source := writeTestArtifact(t, t.TempDir())
	content, err := iflow.ZipDir(source)
	if err != nil {
		t.Fatalf("unable to build the archive: %v", err)
	}

	tenant.Packages[packageId] = true
	tenant.Artifacts[artifactId] = version
	tenant.ArtifactPackages[artifactId] = packageId
	tenant.ArtifactZips[artifactId] = content
}

//assertArtifactDir fails unless the folder holds the extracted integration flow
func assertArtifactDir(t *testing.T, path string) {
	t.Helper()

	if !iflow.IsArtifactDir(path) {
		t.Fatalf("%s is not an extracted integration flow folder", path)
	}

	version, err := iflow.ReadBundleVersion(path)
	if err != nil {
		t.Fatalf("unable to read the extracted manifest: %v", err)
	}
	if version != "1.0.3" {
		t.Errorf("Bundle-Version = %q, want %q", version, "1.0.3")
	}
}

func statusesOf(rows []*downloadRow) string {
	statuses := []string{}
	for _, row := range rows {
		statuses = append(statuses, row.ArtifactId+"="+row.Status)
	}
	return strings.Join(statuses, " ")
}

//A package of the original environment lands under its own id, with no suffix
func TestDownloadByPackage(t *testing.T) {
	tenant := newStubTenant(t)
	stubArtifact(t, tenant, "TestHarnessPreparation", "Order_API_TEST_HARNESS", "1.0.3")

	globalLandscape = newTestLandscape(t, tenant)
	output := t.TempDir()
	setDownloadFlags(t, output)

	environment := globalLandscape.Environments["Dev"]
	target := downloadTarget{PackageId: "TestHarnessPreparation"}

	artifacts, err := environment.System.Client.ReadIntegrationDesigntimeArtifacts(target.PackageId, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	rows := downloadPackageArtifacts(environment, target, artifacts, output)

	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d: %s", len(rows), statusesOf(rows))
	}
	if rows[0].Status != "downloaded" {
		t.Errorf("Status = %q, want %q", rows[0].Status, "downloaded")
	}
	if rows[0].Version != "1.0.3" {
		t.Errorf("Version = %q, want %q", rows[0].Version, "1.0.3")
	}

	assertArtifactDir(t, filepath.Join(output, "TestHarnessPreparation", "Order_API_TEST_HARNESS"))
}

//--artifacts takes base ids, and the suffix of the environment is appended to
//both the artifact and the package it is declared in
func TestDownloadByArtifactResolvesSuffixedIds(t *testing.T) {
	tenant := newStubTenant(t)
	stubArtifact(t, tenant, "TestHarnessPreparationQA", "Order_API_TEST_HARNESSQA", "2.1.0")

	globalLandscape = newTestLandscape(t, tenant)
	output := t.TempDir()
	setDownloadFlags(t, output)

	artifacts := []string{"Order_API_TEST_HARNESS"}
	downloadArtifacts = &artifacts

	environment := globalLandscape.Environments["QA"]

	targets, err := resolveDownloadTargets(environment)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(targets) != 1 {
		t.Fatalf("expected 1 target, got %d", len(targets))
	}
	if targets[0].PackageId != "TestHarnessPreparationQA" {
		t.Errorf("PackageId = %q, want %q", targets[0].PackageId, "TestHarnessPreparationQA")
	}
	if strings.Join(targets[0].ArtifactIds, ",") != "Order_API_TEST_HARNESSQA" {
		t.Errorf("ArtifactIds = %v, want [Order_API_TEST_HARNESSQA]", targets[0].ArtifactIds)
	}

	tenantArtifacts, err := environment.System.Client.ReadIntegrationDesigntimeArtifacts(targets[0].PackageId, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	rows := downloadPackageArtifacts(environment, targets[0], tenantArtifacts, output)
	if len(rows) != 1 || rows[0].Status != "downloaded" {
		t.Fatalf("unexpected rows: %s", statusesOf(rows))
	}

	//Ids are written verbatim, so the suffix is part of both folder names
	assertArtifactDir(t, filepath.Join(output, "TestHarnessPreparationQA", "Order_API_TEST_HARNESSQA"))

	call := findCall(tenant.Calls, http.MethodGet, "/$value")
	if call == nil {
		t.Fatal("no download call was made")
	}
	if !strings.Contains(call.Path, "Id='Order_API_TEST_HARNESSQA'") {
		t.Errorf("download path = %q, want it to carry the suffixed id", call.Path)
	}
}

//An existing folder is reported and left alone, and the artifact is not even
//requested from the tenant
func TestDownloadSkipsExistingFolder(t *testing.T) {
	tenant := newStubTenant(t)
	stubArtifact(t, tenant, "TestHarnessPreparation", "Order_API_TEST_HARNESS", "1.0.3")

	globalLandscape = newTestLandscape(t, tenant)
	output := t.TempDir()
	setDownloadFlags(t, output)

	destination := filepath.Join(output, "TestHarnessPreparation", "Order_API_TEST_HARNESS")
	if err := os.MkdirAll(destination, 0755); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	marker := filepath.Join(destination, "local-change.txt")
	if err := os.WriteFile(marker, []byte("mine"), 0644); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	environment := globalLandscape.Environments["Dev"]
	target := downloadTarget{PackageId: "TestHarnessPreparation"}

	artifacts, err := environment.System.Client.ReadIntegrationDesigntimeArtifacts(target.PackageId, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	rows := downloadPackageArtifacts(environment, target, artifacts, output)

	if len(rows) != 1 || !strings.HasPrefix(rows[0].Status, "skipped") {
		t.Fatalf("unexpected rows: %s", statusesOf(rows))
	}
	if rows[0].Failed {
		t.Error("a skipped artifact must not count as a failure")
	}
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("the existing folder was changed: %v", err)
	}
	if findCall(tenant.Calls, http.MethodGet, "/$value") != nil {
		t.Error("a skipped artifact must not be downloaded")
	}
}

func TestDownloadForceReplacesExistingFolder(t *testing.T) {
	tenant := newStubTenant(t)
	stubArtifact(t, tenant, "TestHarnessPreparation", "Order_API_TEST_HARNESS", "1.0.3")

	globalLandscape = newTestLandscape(t, tenant)
	output := t.TempDir()
	setDownloadFlags(t, output)

	force := true
	downloadForce = &force

	destination := filepath.Join(output, "TestHarnessPreparation", "Order_API_TEST_HARNESS")
	if err := os.MkdirAll(destination, 0755); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	marker := filepath.Join(destination, "local-change.txt")
	if err := os.WriteFile(marker, []byte("mine"), 0644); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	environment := globalLandscape.Environments["Dev"]
	target := downloadTarget{PackageId: "TestHarnessPreparation"}

	artifacts, err := environment.System.Client.ReadIntegrationDesigntimeArtifacts(target.PackageId, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	rows := downloadPackageArtifacts(environment, target, artifacts, output)

	if len(rows) != 1 || rows[0].Status != "downloaded" {
		t.Fatalf("unexpected rows: %s", statusesOf(rows))
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Error("--force must replace the folder completely")
	}
	assertArtifactDir(t, destination)
}

//A draft is downloadable, and the report says so
func TestDownloadMarksDraft(t *testing.T) {
	tenant := newStubTenant(t)
	stubArtifact(t, tenant, "TestHarnessPreparation", "Order_API_TEST_HARNESS", "Active")

	globalLandscape = newTestLandscape(t, tenant)
	output := t.TempDir()
	setDownloadFlags(t, output)

	environment := globalLandscape.Environments["Dev"]
	target := downloadTarget{PackageId: "TestHarnessPreparation"}

	artifacts, err := environment.System.Client.ReadIntegrationDesigntimeArtifacts(target.PackageId, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	rows := downloadPackageArtifacts(environment, target, artifacts, output)

	if len(rows) != 1 || rows[0].Status != "downloaded (draft)" {
		t.Fatalf("unexpected rows: %s", statusesOf(rows))
	}
}

//One unreadable artifact must not stop the others
func TestDownloadReportsFailureWithoutAborting(t *testing.T) {
	tenant := newStubTenant(t)
	stubArtifact(t, tenant, "TestHarnessPreparation", "Order_API_TEST_HARNESS", "1.0.3")
	stubArtifact(t, tenant, "TestHarnessPreparation", "Broken_Flow", "1.0.0")
	tenant.FailingDownloads["Broken_Flow"] = true

	globalLandscape = newTestLandscape(t, tenant)
	output := t.TempDir()
	setDownloadFlags(t, output)

	environment := globalLandscape.Environments["Dev"]
	target := downloadTarget{PackageId: "TestHarnessPreparation"}

	artifacts, err := environment.System.Client.ReadIntegrationDesigntimeArtifacts(target.PackageId, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	rows := downloadPackageArtifacts(environment, target, artifacts, output)

	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d: %s", len(rows), statusesOf(rows))
	}

	failed := 0
	for _, row := range rows {
		if row.Failed {
			failed++
			if !strings.HasPrefix(row.Status, "failed:") {
				t.Errorf("Status = %q, want it to start with failed:", row.Status)
			}
			if strings.Contains(row.Status, "\n") {
				t.Errorf("Status = %q, want a single line", row.Status)
			}
		}
	}
	if failed != 1 {
		t.Errorf("expected 1 failure, got %d: %s", failed, statusesOf(rows))
	}

	//The healthy artifact still landed on disk
	assertArtifactDir(t, filepath.Join(output, "TestHarnessPreparation", "Order_API_TEST_HARNESS"))
}

//An artifact, that the package does not hold, has to be visible as a failure
func TestDownloadReportsMissingArtifact(t *testing.T) {
	tenant := newStubTenant(t)
	stubArtifact(t, tenant, "TestHarnessPreparation", "Order_API_TEST_HARNESS", "1.0.3")

	globalLandscape = newTestLandscape(t, tenant)
	output := t.TempDir()
	setDownloadFlags(t, output)

	environment := globalLandscape.Environments["Dev"]
	target := downloadTarget{PackageId: "TestHarnessPreparation", ArtifactIds: []string{"Unknown_Flow"}}

	artifacts, err := environment.System.Client.ReadIntegrationDesigntimeArtifacts(target.PackageId, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	rows := downloadPackageArtifacts(environment, target, artifacts, output)

	if len(rows) != 1 || !rows[0].Failed {
		t.Fatalf("unexpected rows: %s", statusesOf(rows))
	}
}

//--download-all keeps only the packages of the selected environment and drops
//the ones SAP delivers read only
func TestDownloadAllKeepsOnlyEnvironmentPackages(t *testing.T) {
	tenant := newStubTenant(t)
	stubArtifact(t, tenant, "TestHarnessPreparation", "Order_API_TEST_HARNESS", "1.0.3")
	stubArtifact(t, tenant, "TestHarnessPreparationQA", "Order_API_TEST_HARNESSQA", "1.0.3")
	tenant.Packages["SAPDeliveredPackage"] = true
	tenant.PackageModes["SAPDeliveredPackage"] = "READ_ONLY"

	globalLandscape = newTestLandscape(t, tenant)
	output := t.TempDir()
	setDownloadFlags(t, output)

	all := true
	downloadAll = &all

	tests := []struct {
		environment string
		expected    string
	}{
		{environment: "QA", expected: "TestHarnessPreparationQA"},
		{environment: "Dev", expected: "TestHarnessPreparation"},
	}

	for _, test := range tests {
		t.Run(test.environment, func(t *testing.T) {
			targets, err := resolveDownloadTargets(globalLandscape.Environments[test.environment])
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			packageIds := []string{}
			for _, target := range targets {
				packageIds = append(packageIds, target.PackageId)
			}

			if strings.Join(packageIds, " ") != test.expected {
				t.Errorf("packages = %v, want %q", packageIds, test.expected)
			}
		})
	}
}

func TestValidateDownloadFlags(t *testing.T) {
	tests := []struct {
		name      string
		packages  []string
		artifacts []string
		all       bool
		pkgFlag   string
		expectErr bool
	}{
		{name: "packages only", packages: []string{"Foo"}},
		{name: "artifacts only", artifacts: []string{"Foo"}},
		{name: "download all", all: true},
		{name: "nothing selected", expectErr: true},
		{name: "two selectors", packages: []string{"Foo"}, all: true, expectErr: true},
		{name: "global pkg flag", packages: []string{"Foo"}, pkgFlag: "FooQA", expectErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			setDownloadFlags(t, t.TempDir())

			packages := test.packages
			artifacts := test.artifacts
			all := test.all
			pkgFlag := test.pkgFlag

			downloadPackages = &packages
			downloadArtifacts = &artifacts
			downloadAll = &all
			pkg = &pkgFlag

			err := validateDownloadFlags()
			if test.expectErr && err == nil {
				t.Error("expected an error, got none")
			}
			if !test.expectErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

//A tenant id, that is not a plain folder name, must never build a path
func TestResolveDownloadTargetsRejectsTraversal(t *testing.T) {
	tenant := newStubTenant(t)
	globalLandscape = newTestLandscape(t, tenant)
	setDownloadFlags(t, t.TempDir())

	packages := []string{"../escape"}
	downloadPackages = &packages

	if _, err := resolveDownloadTargets(globalLandscape.Environments["Dev"]); err == nil {
		t.Fatal("expected an error for a package id, that escapes the output folder")
	}
}

//The same package named twice is read once
func TestResolveDownloadTargetsDeduplicates(t *testing.T) {
	tenant := newStubTenant(t)
	globalLandscape = newTestLandscape(t, tenant)
	setDownloadFlags(t, t.TempDir())

	packages := []string{"TestHarnessPreparation", "TestHarnessPreparation"}
	downloadPackages = &packages

	targets, err := resolveDownloadTargets(globalLandscape.Environments["Dev"])
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(targets) != 1 {
		t.Errorf("expected 1 target, got %d", len(targets))
	}
}

//An answer, that is not an integration flow archive, must not be written to
//disk. doRequest only looks at the leading digit of the status code.
func TestDownloadRejectsNonArchiveAnswer(t *testing.T) {
	tenant := newStubTenant(t)
	stubArtifact(t, tenant, "TestHarnessPreparation", "Order_API_TEST_HARNESS", "1.0.3")
	tenant.ArtifactZips["Order_API_TEST_HARNESS"] = []byte("<html>session expired</html>")

	globalLandscape = newTestLandscape(t, tenant)
	output := t.TempDir()
	setDownloadFlags(t, output)

	environment := globalLandscape.Environments["Dev"]
	target := downloadTarget{PackageId: "TestHarnessPreparation"}

	artifacts, err := environment.System.Client.ReadIntegrationDesigntimeArtifacts(target.PackageId, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	rows := downloadPackageArtifacts(environment, target, artifacts, output)

	if len(rows) != 1 || !rows[0].Failed {
		t.Fatalf("unexpected rows: %s", statusesOf(rows))
	}
	if _, err := os.Stat(filepath.Join(output, "TestHarnessPreparation")); !os.IsNotExist(err) {
		t.Error("nothing must be written for an answer, that is not an archive")
	}
}
