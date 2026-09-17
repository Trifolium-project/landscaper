package iflow

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

//A manifest in the shape SAP exports: CRLF terminators and a 72 column
//wrapped Import-Package block.
const sampleManifest = "Manifest-Version: 1.0\r\n" +
	"Bundle-ManifestVersion: 2\r\n" +
	"Bundle-Name: Order API for Test Harness\r\n" +
	"Bundle-SymbolicName: Order_API_TEST_HARNESS; singleton:=true\r\n" +
	"Bundle-Version: 1.0.3\r\n" +
	"SAP-BundleType: IntegrationFlow\r\n" +
	"Import-Package: com.sap.esb.application.services.cxf.interceptor,com.sap\r\n" +
	" .esb.security,com.sap.it.op.agent.api,org.osgi.framework,org.slf4j,org.\r\n" +
	" apache.camel\r\n" +
	"Origin-Bundle-Name: Order API for Test Harness\r\n" +
	"Origin-Bundle-SymbolicName: Order_API_TEST_HARNESS\r\n" +
	"\r\n"

//writeArtifactDir builds a minimal exploded iflow project in a temp directory
func writeArtifactDir(t *testing.T, manifest string) string {
	t.Helper()

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "META-INF"), 0755); err != nil {
		t.Fatalf("unable to create META-INF: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "META-INF", "MANIFEST.MF"), []byte(manifest), 0644); err != nil {
		t.Fatalf("unable to write manifest: %v", err)
	}

	return root
}

func TestReadBundleVersion(t *testing.T) {
	root := writeArtifactDir(t, sampleManifest)

	version, err := ReadBundleVersion(root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if version != "1.0.3" {
		t.Errorf("ReadBundleVersion = %q, want %q", version, "1.0.3")
	}
}

func TestReadBundleSymbolicNameDropsDirectives(t *testing.T) {
	root := writeArtifactDir(t, sampleManifest)

	name, err := ReadBundleSymbolicName(root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name != "Order_API_TEST_HARNESS" {
		t.Errorf("ReadBundleSymbolicName = %q, want %q", name, "Order_API_TEST_HARNESS")
	}
}

func TestReadBundleName(t *testing.T) {
	root := writeArtifactDir(t, sampleManifest)

	name, err := ReadBundleName(root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name != "Order API for Test Harness" {
		t.Errorf("ReadBundleName = %q, want %q", name, "Order API for Test Harness")
	}
}

func TestReadHeaderJoinsContinuationLines(t *testing.T) {
	value, err := readHeader([]byte(sampleManifest), "Import-Package")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := "com.sap.esb.application.services.cxf.interceptor,com.sap.esb.security," +
		"com.sap.it.op.agent.api,org.osgi.framework,org.slf4j,org.apache.camel"
	if value != want {
		t.Errorf("readHeader(Import-Package) = %q, want %q", value, want)
	}
}

func TestReadHeaderMissing(t *testing.T) {
	if _, err := readHeader([]byte(sampleManifest), "Bundle-Activator"); err == nil {
		t.Fatal("expected an error for a missing header")
	}
}

//The Import-Package block must survive a version bump byte for byte, and the
//CRLF terminators must not be rewritten to LF.
func TestSetBundleVersionPreservesEverythingElse(t *testing.T) {
	root := writeArtifactDir(t, sampleManifest)

	if err := SetBundleVersion(root, "1.0.4"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(root, "META-INF", "MANIFEST.MF"))
	if err != nil {
		t.Fatalf("unable to read manifest: %v", err)
	}

	got := string(data)
	want := strings.Replace(sampleManifest, "Bundle-Version: 1.0.3", "Bundle-Version: 1.0.4", 1)
	if got != want {
		t.Errorf("manifest after bump:\n%q\nwant:\n%q", got, want)
	}

	if strings.Count(got, "\r\n") != strings.Count(sampleManifest, "\r\n") {
		t.Errorf("CRLF terminators changed: got %d, want %d",
			strings.Count(got, "\r\n"), strings.Count(sampleManifest, "\r\n"))
	}
}

func TestSetBundleVersionKeepsLFManifest(t *testing.T) {
	manifest := strings.ReplaceAll(sampleManifest, "\r\n", "\n")
	root := writeArtifactDir(t, manifest)

	if err := SetBundleVersion(root, "2.0.0"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(root, "META-INF", "MANIFEST.MF"))
	if err != nil {
		t.Fatalf("unable to read manifest: %v", err)
	}
	if strings.Contains(string(data), "\r\n") {
		t.Error("SetBundleVersion introduced CRLF into an LF manifest")
	}
	if !strings.Contains(string(data), "Bundle-Version: 2.0.0\n") {
		t.Error("Bundle-Version was not updated")
	}
}

func TestSetBundleVersionRoundTrip(t *testing.T) {
	root := writeArtifactDir(t, sampleManifest)

	if err := SetBundleVersion(root, "1.2.3"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	version, err := ReadBundleVersion(root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if version != "1.2.3" {
		t.Errorf("ReadBundleVersion after bump = %q, want %q", version, "1.2.3")
	}
}

func TestSetBundleVersionMissingHeader(t *testing.T) {
	root := writeArtifactDir(t, "Manifest-Version: 1.0\r\n\r\n")

	if err := SetBundleVersion(root, "1.0.4"); err == nil {
		t.Fatal("expected an error when Bundle-Version is absent")
	}
}

func TestApplySuffixToSymbolicName(t *testing.T) {
	tests := []struct {
		name   string
		value  string
		suffix string
		want   string
	}{
		{name: "with directive", value: "Order_API_TEST_HARNESS; singleton:=true", suffix: "QA",
			want: "Order_API_TEST_HARNESSQA; singleton:=true"},
		{name: "without directive", value: "Order_API_TEST_HARNESS", suffix: "QA",
			want: "Order_API_TEST_HARNESSQA"},
		{name: "no space before directive", value: "Order_API;singleton:=true", suffix: "QA",
			want: "Order_APIQA;singleton:=true"},
		{name: "several directives", value: "Order_API; singleton:=true; foo:=bar", suffix: "PRD",
			want: "Order_APIPRD; singleton:=true; foo:=bar"},
		{name: "empty suffix is a no-op", value: "Order_API; singleton:=true", suffix: "",
			want: "Order_API; singleton:=true"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := ApplySuffixToSymbolicName(test.value, test.suffix); got != test.want {
				t.Errorf("ApplySuffixToSymbolicName(%q, %q) = %q, want %q",
					test.value, test.suffix, got, test.want)
			}
		})
	}
}

func TestRewriteManifestHeaders(t *testing.T) {
	updated, err := RewriteManifestHeaders([]byte(sampleManifest), map[string]string{
		HeaderBundleSymbolicName: "Order_API_TEST_HARNESSQA; singleton:=true",
		HeaderBundleName:         "Order API for Test Harness QA",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := string(updated)

	want := strings.Replace(sampleManifest,
		"Bundle-SymbolicName: Order_API_TEST_HARNESS; singleton:=true",
		"Bundle-SymbolicName: Order_API_TEST_HARNESSQA; singleton:=true", 1)
	want = strings.Replace(want,
		"Bundle-Name: Order API for Test Harness",
		"Bundle-Name: Order API for Test Harness QA", 1)

	if got != want {
		t.Errorf("rewritten manifest:\n%q\nwant:\n%q", got, want)
	}

	//The wrapped Import-Package block and the CRLF terminators must survive
	if !strings.Contains(got, " .esb.security,com.sap.it.op.agent.api,org.osgi.framework,org.slf4j,org.\r\n") {
		t.Error("the wrapped Import-Package block was damaged")
	}
	if strings.Count(got, "\r\n") != strings.Count(sampleManifest, "\r\n") {
		t.Error("line terminators changed")
	}

	//Origin-Bundle-* are deliberately left alone
	if !strings.Contains(got, "Origin-Bundle-SymbolicName: Order_API_TEST_HARNESS\r\n") {
		t.Error("Origin-Bundle-SymbolicName should not be rewritten")
	}
}

func TestRewriteManifestHeadersMissingHeader(t *testing.T) {
	_, err := RewriteManifestHeaders([]byte(sampleManifest), map[string]string{"Bundle-Activator": "x"})
	if err == nil {
		t.Fatal("expected an error for a header that is not in the manifest")
	}
}
