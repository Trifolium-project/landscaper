package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Trifolium-project/landscaper/packages/iflow"
	"github.com/Trifolium-project/landscaper/packages/landscape"
)

func TestArtifactIdFromPath(t *testing.T) {
	tests := []struct {
		name string
		path string
		want string
	}{
		{name: "folder", path: "artifacts/Order_API_TEST_HARNESS", want: "Order_API_TEST_HARNESS"},
		{name: "folder with trailing slash", path: "artifacts/Order_API_TEST_HARNESS/", want: "Order_API_TEST_HARNESS"},
		{name: "archive", path: "build/Order_API_TEST_HARNESS.zip", want: "Order_API_TEST_HARNESS"},
		{name: "bare name", path: "Sample_API", want: "Sample_API"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := artifactIdFromPath(test.path); got != test.want {
				t.Errorf("artifactIdFromPath(%q) = %q, want %q", test.path, got, test.want)
			}
		})
	}
}

//resolvePackedVersion holds the rule the user asked for: the packed version has
//to be strictly higher than the one already present in the tenant.
func TestResolvePackedVersion(t *testing.T) {
	environment := &landscape.Environment{Id: "QA", Suffix: "QA"}

	tests := []struct {
		name          string
		tenantVersion string
		localVersion  string
		bump          string
		setVersion    string
		want          string
		wantBumped    bool
		wantErr       bool
	}{
		{
			name: "local ahead is packed as it is", tenantVersion: "1.0.3", localVersion: "1.0.4",
			want: "1.0.4", wantBumped: false,
		},
		{
			name: "artifact is new in the tenant", tenantVersion: versionNotInTenant, localVersion: "1.0.3",
			want: "1.0.3", wantBumped: false,
		},
		{
			name: "draft in the tenant cannot be compared", tenantVersion: "Active", localVersion: "1.0.3",
			want: "1.0.3", wantBumped: false,
		},
		{
			name: "equal versions bump", tenantVersion: "1.0.3", localVersion: "1.0.3",
			bump: iflow.BumpPatch, want: "1.0.4", wantBumped: true,
		},
		{
			name: "local behind bumps from the tenant version", tenantVersion: "2.5.0", localVersion: "1.0.3",
			bump: iflow.BumpPatch, want: "2.5.1", wantBumped: true,
		},
		{
			name: "minor bump", tenantVersion: "1.0.3", localVersion: "1.0.3",
			bump: iflow.BumpMinor, want: "1.1.0", wantBumped: true,
		},
		{
			name: "major bump", tenantVersion: "1.0.3", localVersion: "1.0.3",
			bump: iflow.BumpMajor, want: "2.0.0", wantBumped: true,
		},
		{
			name: "explicit version", tenantVersion: "1.0.3", localVersion: "1.0.3",
			setVersion: "3.1.4", want: "3.1.4", wantBumped: true,
		},
		{
			name: "explicit version must be ahead of the tenant", tenantVersion: "1.0.3", localVersion: "1.0.3",
			setVersion: "1.0.3", wantErr: true,
		},
		{
			name: "unknown bump level", tenantVersion: "1.0.3", localVersion: "1.0.3",
			bump: "build", wantErr: true,
		},
		{
			//Tests never run on a terminal, so this is the CI path
			name: "no flags and no terminal", tenantVersion: "1.0.3", localVersion: "1.0.3",
			wantErr: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			localVersion, err := iflow.ParseVersion(test.localVersion)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			options := packOptions{
				CheckEnvironment: environment,
				TenantArtId:      "Order_API_TEST_HARNESSQA",
				Bump:             test.bump,
				SetVersion:       test.setVersion,
			}

			got, bumped, err := resolvePackedVersion(test.tenantVersion, localVersion, options)
			if test.wantErr {
				if err == nil {
					t.Fatalf("expected an error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != test.want {
				t.Errorf("version = %q, want %q", got, test.want)
			}
			if bumped != test.wantBumped {
				t.Errorf("bumped = %t, want %t", bumped, test.wantBumped)
			}
		})
	}
}

func TestPackArtifactWritesArchiveAndLeavesManifestAlone(t *testing.T) {
	root := t.TempDir()
	source := writeTestArtifact(t, root)

	result, err := packArtifact(packOptions{
		SourceDir: source,
		SkipCheck: true,
		OutputDir: filepath.Join(root, "build"),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.ArtifactId != "Order_API_TEST_HARNESS" {
		t.Errorf("ArtifactId = %q, want %q", result.ArtifactId, "Order_API_TEST_HARNESS")
	}
	if result.LocalVersion != "1.0.3" || result.PackedVersion != "1.0.3" {
		t.Errorf("versions = %q/%q, want 1.0.3/1.0.3", result.LocalVersion, result.PackedVersion)
	}
	if result.TenantVersion != versionNotInTenant {
		t.Errorf("TenantVersion = %q, want %q", result.TenantVersion, versionNotInTenant)
	}
	if result.Bumped {
		t.Error("nothing should have been bumped with --skip-version-check")
	}

	want := filepath.Join(root, "build", "Order_API_TEST_HARNESS.zip")
	if result.ZipPath != want {
		t.Errorf("ZipPath = %q, want %q", result.ZipPath, want)
	}

	written, err := os.ReadFile(want)
	if err != nil {
		t.Fatalf("archive was not written: %v", err)
	}
	if string(written[:2]) != "PK" {
		t.Error("the written file is not a zip archive")
	}

	version, err := iflow.ReadBundleVersionFromZip(written)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if version != "1.0.3" {
		t.Errorf("packed Bundle-Version = %q, want %q", version, "1.0.3")
	}
}

func TestPackArtifactRejectsFolderWithoutManifest(t *testing.T) {
	root := t.TempDir()

	_, err := packArtifact(packOptions{SourceDir: root, SkipCheck: true, OutputDir: root})
	if err == nil {
		t.Fatal("expected an error for a folder without a manifest")
	}
	if !strings.Contains(err.Error(), iflow.ManifestPath) {
		t.Errorf("the error should mention %s, got: %v", iflow.ManifestPath, err)
	}
}

func TestResolvePackageIdUsesLandscapeAndSuffix(t *testing.T) {
	tenant := newStubTenant(t)
	globalLandscape = newTestLandscape(t, tenant)

	empty := ""
	pkg = &empty

	qa := globalLandscape.Environments["QA"]

	packageId, err := resolvePackageId("Order_API_TEST_HARNESS", qa)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if packageId != "TestHarnessPreparationQA" {
		t.Errorf("packageId = %q, want %q", packageId, "TestHarnessPreparationQA")
	}

	//An unknown artifact must point the user at --pkg
	if _, err := resolvePackageId("Unknown_API", qa); err == nil {
		t.Fatal("expected an error for an artifact that is not in the landscape")
	}

	//The explicit flag wins and is used verbatim
	override := "ExplicitPackage"
	pkg = &override
	packageId, err = resolvePackageId("Order_API_TEST_HARNESS", qa)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if packageId != "ExplicitPackage" {
		t.Errorf("packageId = %q, want %q", packageId, "ExplicitPackage")
	}
}
