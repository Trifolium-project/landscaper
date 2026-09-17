package cmd

import (
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Trifolium-project/landscaper/packages/cpiclient"
	"github.com/Trifolium-project/landscaper/packages/iflow"
	"github.com/Trifolium-project/landscaper/packages/landscape"
)

//The manifest of the sample integration flow, with CRLF terminators
const testManifest = "Manifest-Version: 1.0\r\n" +
	"Bundle-ManifestVersion: 2\r\n" +
	"Bundle-Name: Order API for Test Harness\r\n" +
	"Bundle-SymbolicName: Order_API_TEST_HARNESS; singleton:=true\r\n" +
	"Bundle-Version: 1.0.3\r\n" +
	"SAP-BundleType: IntegrationFlow\r\n" +
	"\r\n"

//tenantCall records one request made against the stub tenant
type tenantCall struct {
	Method string
	Path   string
	Query  url.Values
	Body   map[string]interface{}
}

//stubTenant is a minimal Integration Suite OData endpoint
type stubTenant struct {
	Server *httptest.Server
	//Artifact id to version, as the tenant currently holds them
	Artifacts map[string]string
	//Packages that exist in the tenant
	Packages map[string]bool
	Calls    []tenantCall
}

func newStubTenant(t *testing.T) *stubTenant {
	t.Helper()

	tenant := &stubTenant{
		Artifacts: map[string]string{},
		Packages:  map[string]bool{},
	}

	tenant.Server = httptest.NewTLSServer(http.HandlerFunc(tenant.handle))
	t.Cleanup(tenant.Server.Close)

	//The client builds its own http.Client without a Transport, so the test
	//certificate has to be trusted through the default transport
	transport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		t.Fatal("http.DefaultTransport is not an *http.Transport")
	}
	previous := transport.TLSClientConfig
	transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	t.Cleanup(func() { transport.TLSClientConfig = previous })

	return tenant
}

func (tenant *stubTenant) record(request *http.Request) {
	call := tenantCall{
		Method: request.Method,
		Path:   request.URL.Path,
		Query:  request.URL.Query(),
	}

	if body, err := io.ReadAll(request.Body); err == nil && len(body) > 0 {
		payload := map[string]interface{}{}
		if json.Unmarshal(body, &payload) == nil {
			call.Body = payload
		}
	}

	tenant.Calls = append(tenant.Calls, call)
}

func (tenant *stubTenant) handle(writer http.ResponseWriter, request *http.Request) {
	tenant.record(request)

	//Every mutating call fetches a CSRF token first
	if request.Header.Get("X-CSRF-Token") == "Fetch" {
		writer.Header().Set("X-CSRF-Token", "stub-token")
		writer.Write([]byte("{}"))
		return
	}

	path := request.URL.Path

	switch {
	case request.Method == http.MethodGet && strings.HasSuffix(path, "/IntegrationDesigntimeArtifacts"):
		packageId := packageIdFromPath(path)
		if !tenant.Packages[packageId] {
			http.Error(writer, `{"error":"package not found"}`, http.StatusNotFound)
			return
		}

		results := []map[string]string{}
		for id, version := range tenant.Artifacts {
			results = append(results, map[string]string{
				"Id": id, "Version": version, "PackageId": packageId,
				"Name": id, "Description": "", "Sender": "", "Receiver": "",
			})
		}
		json.NewEncoder(writer).Encode(map[string]interface{}{
			"d": map[string]interface{}{"results": results},
		})

	case request.Method == http.MethodGet && strings.Contains(path, "/IntegrationPackages("):
		packageId := quotedIdFromPath(path, "IntegrationPackages(")
		if !tenant.Packages[packageId] {
			http.Error(writer, `{"error":"package not found"}`, http.StatusNotFound)
			return
		}
		json.NewEncoder(writer).Encode(map[string]interface{}{
			"d": map[string]interface{}{"Id": packageId, "Name": packageId, "Version": "1.0.0"},
		})

	case request.Method == http.MethodPost && strings.HasSuffix(path, "/IntegrationPackages"):
		last := tenant.Calls[len(tenant.Calls)-1]
		if id, ok := last.Body["Id"].(string); ok {
			tenant.Packages[id] = true
		}
		writer.WriteHeader(http.StatusCreated)
		writer.Write([]byte("{}"))

	case request.Method == http.MethodPost && strings.HasSuffix(path, "/IntegrationDesigntimeArtifacts"):
		last := tenant.Calls[len(tenant.Calls)-1]
		if id, ok := last.Body["Id"].(string); ok {
			tenant.Artifacts[id] = uploadedVersion(last)
		}
		writer.WriteHeader(http.StatusCreated)
		writer.Write([]byte("{}"))

	case request.Method == http.MethodPut && strings.Contains(path, "/IntegrationDesigntimeArtifacts("):
		//A real tenant takes the new version from Bundle-Version in the archive
		last := tenant.Calls[len(tenant.Calls)-1]
		if id, ok := last.Body["Id"].(string); ok {
			tenant.Artifacts[id] = uploadedVersion(last)
		}
		writer.WriteHeader(http.StatusOK)
		writer.Write([]byte("{}"))

	case strings.Contains(path, "/$links/Configurations("):
		writer.WriteHeader(http.StatusOK)
		writer.Write([]byte("{}"))

	case strings.Contains(path, "DeployIntegrationDesigntimeArtifact"):
		writer.WriteHeader(http.StatusAccepted)
		writer.Write([]byte("taskId"))

	default:
		http.Error(writer, `{"error":"unexpected call"}`, http.StatusNotFound)
	}
}

//uploadedVersion reads Bundle-Version out of the archive a call carried
func uploadedVersion(call tenantCall) string {
	content, err := decodeArtifactContent(call)
	if err != nil {
		return "1.0.0"
	}

	version, err := iflow.ReadBundleVersionFromZip(content)
	if err != nil {
		return "1.0.0"
	}

	return version
}

//decodeArtifactContent returns the zip a POST or PUT carried
func decodeArtifactContent(call tenantCall) ([]byte, error) {
	encoded, ok := call.Body["ArtifactContent"].(string)
	if !ok {
		return nil, fmt.Errorf("call carries no ArtifactContent")
	}
	return base64.StdEncoding.DecodeString(encoded)
}

//uploadedManifestHeader reads one manifest header out of the archive that was
//actually sent to the tenant
func uploadedManifestHeader(t *testing.T, calls []tenantCall, header string) string {
	t.Helper()

	for index := len(calls) - 1; index >= 0; index-- {
		if calls[index].Body == nil {
			continue
		}
		if _, ok := calls[index].Body["ArtifactContent"]; !ok {
			continue
		}

		content, err := decodeArtifactContent(calls[index])
		if err != nil {
			t.Fatalf("unable to decode uploaded content: %v", err)
		}

		value, err := iflow.ReadManifestHeaderFromZip(content, header)
		if err != nil {
			t.Fatalf("unable to read %s from the uploaded archive: %v", header, err)
		}
		return value
	}

	t.Fatal("no artifact content was uploaded")
	return ""
}

func packageIdFromPath(path string) string {
	return quotedIdFromPath(path, "IntegrationPackages(")
}

//quotedIdFromPath extracts Foo out of a segment such as IntegrationPackages('Foo')
func quotedIdFromPath(path string, prefix string) string {
	index := strings.Index(path, prefix)
	if index < 0 {
		return ""
	}

	rest := path[index+len(prefix):]
	rest = strings.TrimPrefix(rest, "'")

	end := strings.Index(rest, "'")
	if end < 0 {
		return ""
	}

	return rest[:end]
}

//newTestLandscape wires a Dev and a QA environment onto the stub tenant
func newTestLandscape(t *testing.T, tenant *stubTenant) *landscape.Landscape {
	t.Helper()

	host := strings.TrimPrefix(tenant.Server.URL, "https://")

	system := &landscape.System{
		Id:     "dev",
		Name:   "Stub Tenant",
		Client: cpiclient.NewCPIBasicAuthClient("user", "password", "", host, false),
	}

	dev := &landscape.Environment{Id: "Dev", Name: "Development", Suffix: "", System: system}
	qa := &landscape.Environment{Id: "QA", Name: "Quality", Suffix: "QA", System: system}

	artifact := &landscape.Artifact{
		Id: "Order_API_TEST_HARNESS",
		Configurations: map[string]*landscape.Configuration{
			"QA": {
				Environment: "QA",
				Parameters: []*landscape.Parameter{
					{Key: "urlPath", Value: "/QA/erp/order", Type: "xsd:string"},
					{Key: "ds_name", Value: "orders_test_harness_QA", Type: "xsd:string"},
				},
			},
		},
	}

	return &landscape.Landscape{
		Name:    "Test",
		Systems: map[string]*landscape.System{"dev": system},
		Packages: map[string]*landscape.Package{
			"TestHarnessPreparation": {
				Id:        "TestHarnessPreparation",
				Artifacts: map[string]*landscape.Artifact{"Order_API_TEST_HARNESS": artifact},
			},
		},
		Environments:        map[string]*landscape.Environment{"Dev": dev, "QA": qa},
		OriginalEnvironment: dev,
	}
}

//writeTestArtifact builds an exploded integration flow folder
func writeTestArtifact(t *testing.T, root string) string {
	t.Helper()

	source := filepath.Join(root, "Order_API_TEST_HARNESS")
	if err := os.MkdirAll(filepath.Join(source, "META-INF"), 0755); err != nil {
		t.Fatalf("unable to create folder: %v", err)
	}
	if err := os.WriteFile(filepath.Join(source, "META-INF", "MANIFEST.MF"), []byte(testManifest), 0644); err != nil {
		t.Fatalf("unable to write manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(source, "metainfo.prop"), []byte("description="), 0644); err != nil {
		t.Fatalf("unable to write metainfo: %v", err)
	}

	return source
}

//setUploadFlags points the package level flag variables at known values
func setUploadFlags(t *testing.T, targetEnv string, outputDir string) {
	t.Helper()

	targetEnvValue := targetEnv
	outputDirValue := outputDir
	skipCheck := false
	deploy := false
	bump := ""
	setVersion := ""
	packageFlag := ""

	uploadTargetEnv = &targetEnvValue
	uploadOutputDir = &outputDirValue
	uploadSkipVersionCheck = &skipCheck
	uploadDeploy = &deploy
	uploadBump = &bump
	uploadSetVersion = &setVersion
	pkg = &packageFlag
}

func findCall(calls []tenantCall, method string, contains string) *tenantCall {
	for index := range calls {
		if calls[index].Method == method && strings.Contains(calls[index].Path, contains) {
			return &calls[index]
		}
	}
	return nil
}

func TestUploadArtifactCreatesArtifactAndPackage(t *testing.T) {
	tenant := newStubTenant(t)
	globalLandscape = newTestLandscape(t, tenant)

	root := t.TempDir()
	source := writeTestArtifact(t, root)
	setUploadFlags(t, "QA", filepath.Join(root, "build"))

	row, err := uploadArtifact(source, globalLandscape.Environments["QA"])
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if row.ArtifactId != "Order_API_TEST_HARNESSQA" {
		t.Errorf("ArtifactId = %q, want %q", row.ArtifactId, "Order_API_TEST_HARNESSQA")
	}
	if row.PackageId != "TestHarnessPreparationQA" {
		t.Errorf("PackageId = %q, want %q", row.PackageId, "TestHarnessPreparationQA")
	}
	if row.Action != "created" {
		t.Errorf("Action = %q, want %q", row.Action, "created")
	}
	if row.TenantVersion != versionNotInTenant {
		t.Errorf("TenantVersion = %q, want %q", row.TenantVersion, versionNotInTenant)
	}
	if row.Source != "folder" {
		t.Errorf("Source = %q, want %q", row.Source, "folder")
	}

	//The missing package must have been created with the suffixed id
	created := findCall(tenant.Calls, http.MethodPost, "/IntegrationPackages")
	if created == nil {
		t.Fatal("the target package was not created")
	}
	if created.Body["Id"] != "TestHarnessPreparationQA" {
		t.Errorf("created package Id = %v, want %q", created.Body["Id"], "TestHarnessPreparationQA")
	}

	//The artifact must be posted with a suffixed id and name and real content
	posted := findCall(tenant.Calls, http.MethodPost, "/IntegrationDesigntimeArtifacts")
	if posted == nil {
		t.Fatal("the artifact was not posted")
	}
	if posted.Body["Id"] != "Order_API_TEST_HARNESSQA" {
		t.Errorf("posted Id = %v, want %q", posted.Body["Id"], "Order_API_TEST_HARNESSQA")
	}
	if posted.Body["Name"] != "Order API for Test Harness QA" {
		t.Errorf("posted Name = %v, want %q", posted.Body["Name"], "Order API for Test Harness QA")
	}

	content, ok := posted.Body["ArtifactContent"].(string)
	if !ok || content == "" {
		t.Fatal("posted artifact carries no content")
	}
	decoded, err := base64.StdEncoding.DecodeString(content)
	if err != nil {
		t.Fatalf("content is not valid base64: %v", err)
	}
	if string(decoded[:2]) != "PK" {
		t.Error("posted content is not a zip archive")
	}

	//The archive must also have been written to the output folder
	if _, err := os.Stat(filepath.Join(root, "build", "Order_API_TEST_HARNESSQA.zip")); err != nil {
		t.Errorf("archive was not written: %v", err)
	}
}

func TestUploadArtifactUpdatesExistingArtifact(t *testing.T) {
	tenant := newStubTenant(t)
	tenant.Packages["TestHarnessPreparationQA"] = true
	tenant.Artifacts["Order_API_TEST_HARNESSQA"] = "1.0.1"

	globalLandscape = newTestLandscape(t, tenant)

	root := t.TempDir()
	source := writeTestArtifact(t, root)
	setUploadFlags(t, "QA", filepath.Join(root, "build"))

	row, err := uploadArtifact(source, globalLandscape.Environments["QA"])
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if row.Action != "updated" {
		t.Errorf("Action = %q, want %q", row.Action, "updated")
	}
	if row.TenantVersion != "1.0.1" {
		t.Errorf("TenantVersion = %q, want %q", row.TenantVersion, "1.0.1")
	}

	//Local 1.0.3 is ahead of tenant 1.0.1, so no bump is expected
	manifestVersion, err := os.ReadFile(filepath.Join(source, "META-INF", "MANIFEST.MF"))
	if err != nil {
		t.Fatalf("unable to read manifest: %v", err)
	}
	if !strings.Contains(string(manifestVersion), "Bundle-Version: 1.0.3") {
		t.Error("the manifest was rewritten even though the local version was already ahead")
	}

	if findCall(tenant.Calls, http.MethodPut, "/IntegrationDesigntimeArtifacts(") == nil {
		t.Error("an existing artifact must be updated with PUT, not recreated")
	}
	if findCall(tenant.Calls, http.MethodPost, "/IntegrationDesigntimeArtifacts") != nil {
		t.Error("an existing artifact must not be posted again")
	}
}

//The original environment is the only one whose version the repository owns,
//so it is the only one a bump may be written back for.
func TestUploadArtifactBumpsVersionForOriginalEnvironment(t *testing.T) {
	tenant := newStubTenant(t)
	tenant.Packages["TestHarnessPreparation"] = true
	tenant.Artifacts["Order_API_TEST_HARNESS"] = "1.0.3"

	globalLandscape = newTestLandscape(t, tenant)

	root := t.TempDir()
	source := writeTestArtifact(t, root)
	setUploadFlags(t, "Dev", filepath.Join(root, "build"))

	bump := "patch"
	uploadBump = &bump

	if _, err := uploadArtifact(source, globalLandscape.Environments["Dev"]); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	manifest, err := os.ReadFile(filepath.Join(source, "META-INF", "MANIFEST.MF"))
	if err != nil {
		t.Fatalf("unable to read manifest: %v", err)
	}
	if !strings.Contains(string(manifest), "Bundle-Version: 1.0.4") {
		t.Errorf("the manifest was not bumped to 1.0.4:\n%s", string(manifest))
	}
	if !strings.Contains(string(manifest), "Bundle-SymbolicName: Order_API_TEST_HARNESS; singleton:=true\r\n") {
		t.Error("the bump damaged the rest of the manifest")
	}
}

//Outside the original environment the version comes from the repository as it
//is, and the working copy must never be written.
func TestUploadArtifactNeverWritesRepositoryForTargetEnvironment(t *testing.T) {
	tenant := newStubTenant(t)
	tenant.Packages["TestHarnessPreparationQA"] = true
	tenant.Artifacts["Order_API_TEST_HARNESSQA"] = "2.0.0"

	globalLandscape = newTestLandscape(t, tenant)

	root := t.TempDir()
	source := writeTestArtifact(t, root)
	setUploadFlags(t, "QA", filepath.Join(root, "build"))

	//Even asking for a bump must not change the repository
	bump := "patch"
	uploadBump = &bump

	row, err := uploadArtifact(source, globalLandscape.Environments["QA"])
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	manifest, err := os.ReadFile(filepath.Join(source, "META-INF", "MANIFEST.MF"))
	if err != nil {
		t.Fatalf("unable to read manifest: %v", err)
	}
	if string(manifest) != testManifest {
		t.Errorf("the repository manifest was modified for a non original environment:\n%q", string(manifest))
	}

	//The version pushed is the one from the repository, not a bumped one
	if row.UploadVersion != "1.0.3" {
		t.Errorf("UploadVersion = %q, want %q", row.UploadVersion, "1.0.3")
	}
}

func TestUploadArtifactFailsWhenBumpIsRequiredAndNotAutomatic(t *testing.T) {
	tenant := newStubTenant(t)
	tenant.Packages["TestHarnessPreparation"] = true
	tenant.Artifacts["Order_API_TEST_HARNESS"] = "2.0.0"

	globalLandscape = newTestLandscape(t, tenant)

	root := t.TempDir()
	source := writeTestArtifact(t, root)
	setUploadFlags(t, "Dev", filepath.Join(root, "build"))

	//Tests never run on a terminal, so this exercises the CI path
	_, err := uploadArtifact(source, globalLandscape.Environments["Dev"])
	if err == nil {
		t.Fatal("expected an error when a bump is required without --bump or --set-version")
	}
	if !strings.Contains(err.Error(), "--bump") {
		t.Errorf("the error should name the flags that resolve it, got: %v", err)
	}
}

func TestUploadArtifactSkipVersionCheckStillUpdates(t *testing.T) {
	tenant := newStubTenant(t)
	tenant.Packages["TestHarnessPreparationQA"] = true
	tenant.Artifacts["Order_API_TEST_HARNESSQA"] = "2.0.0"

	globalLandscape = newTestLandscape(t, tenant)

	root := t.TempDir()
	source := writeTestArtifact(t, root)
	setUploadFlags(t, "QA", filepath.Join(root, "build"))

	skip := true
	uploadSkipVersionCheck = &skip

	row, err := uploadArtifact(source, globalLandscape.Environments["QA"])
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	//Skipping the check must not turn an update into a create
	if row.Action != "updated" {
		t.Errorf("Action = %q, want %q", row.Action, "updated")
	}
	if findCall(tenant.Calls, http.MethodPut, "/IntegrationDesigntimeArtifacts(") == nil {
		t.Error("the artifact should have been updated with PUT")
	}
}

func TestUploadArtifactAppliesConfigurationAndDeploys(t *testing.T) {
	tenant := newStubTenant(t)
	tenant.Packages["TestHarnessPreparationQA"] = true
	tenant.Artifacts["Order_API_TEST_HARNESSQA"] = "1.0.1"

	globalLandscape = newTestLandscape(t, tenant)

	root := t.TempDir()
	source := writeTestArtifact(t, root)
	setUploadFlags(t, "QA", filepath.Join(root, "build"))

	deploy := true
	uploadDeploy = &deploy

	row, err := uploadArtifact(source, globalLandscape.Environments["QA"])
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !row.Deployed {
		t.Error("the row should report the artifact as deployed")
	}

	//Both QA parameters must have been pushed
	applied := map[string]string{}
	for _, call := range tenant.Calls {
		if !strings.Contains(call.Path, "/$links/Configurations(") || call.Body == nil {
			continue
		}
		key, _ := call.Body["ParameterKey"].(string)
		value, _ := call.Body["ParameterValue"].(string)
		applied[key] = value
	}

	if applied["urlPath"] != "/QA/erp/order" {
		t.Errorf("urlPath = %q, want %q", applied["urlPath"], "/QA/erp/order")
	}
	if applied["ds_name"] != "orders_test_harness_QA" {
		t.Errorf("ds_name = %q, want %q", applied["ds_name"], "orders_test_harness_QA")
	}

	deployed := findCall(tenant.Calls, http.MethodPost, "DeployIntegrationDesigntimeArtifact")
	if deployed == nil {
		t.Fatal("the artifact was not deployed")
	}
	if got := deployed.Query.Get("Id"); got != "'Order_API_TEST_HARNESSQA'" {
		t.Errorf("deployed Id = %q, want %q", got, "'Order_API_TEST_HARNESSQA'")
	}
}

func TestUploadArtifactAcceptsZipArchive(t *testing.T) {
	tenant := newStubTenant(t)
	tenant.Packages["TestHarnessPreparationQA"] = true

	globalLandscape = newTestLandscape(t, tenant)

	root := t.TempDir()
	source := writeTestArtifact(t, root)
	setUploadFlags(t, "QA", filepath.Join(root, "build"))

	//Pack first, then upload the resulting archive
	result, err := packArtifact(packOptions{
		SourceDir: source,
		SkipCheck: true,
		OutputDir: filepath.Join(root, "build"),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	row, err := uploadArtifact(result.ZipPath, globalLandscape.Environments["QA"])
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if row.Source != "zip" {
		t.Errorf("Source = %q, want %q", row.Source, "zip")
	}
	if row.ArtifactId != "Order_API_TEST_HARNESSQA" {
		t.Errorf("ArtifactId = %q, want %q", row.ArtifactId, "Order_API_TEST_HARNESSQA")
	}
	if row.Action != "created" {
		t.Errorf("Action = %q, want %q", row.Action, "created")
	}
}

func TestUploadArtifactRejectsUnknownPath(t *testing.T) {
	tenant := newStubTenant(t)
	globalLandscape = newTestLandscape(t, tenant)

	root := t.TempDir()
	setUploadFlags(t, "QA", filepath.Join(root, "build"))

	if _, err := uploadArtifact(filepath.Join(root, "nothing_here"), globalLandscape.Environments["QA"]); err == nil {
		t.Fatal("expected an error for a path that is neither a folder nor a zip")
	}
}

//The bug this guards against: the tenant derives the symbolic name from the
//artifact id, so an archive uploaded as Order_API_TEST_HARNESSQA that still
//says Order_API_TEST_HARNESS inside is rejected on the next update with
//"Could not update artifact of the package; due to change in the
//Bundle-symbolicName."
func TestUploadArtifactSuffixesSymbolicNameInTheArchive(t *testing.T) {
	tenant := newStubTenant(t)
	tenant.Packages["TestHarnessPreparationQA"] = true

	globalLandscape = newTestLandscape(t, tenant)

	root := t.TempDir()
	source := writeTestArtifact(t, root)
	setUploadFlags(t, "QA", filepath.Join(root, "build"))

	if _, err := uploadArtifact(source, globalLandscape.Environments["QA"]); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	symbolicName := uploadedManifestHeader(t, tenant.Calls, iflow.HeaderBundleSymbolicName)
	if symbolicName != "Order_API_TEST_HARNESSQA; singleton:=true" {
		t.Errorf("uploaded Bundle-SymbolicName = %q, want %q",
			symbolicName, "Order_API_TEST_HARNESSQA; singleton:=true")
	}

	bundleName := uploadedManifestHeader(t, tenant.Calls, iflow.HeaderBundleName)
	if bundleName != "Order API for Test Harness QA" {
		t.Errorf("uploaded Bundle-Name = %q, want %q", bundleName, "Order API for Test Harness QA")
	}

	//The version must be untouched, and so must the repository
	version := uploadedManifestHeader(t, tenant.Calls, iflow.HeaderBundleVersion)
	if version != "1.0.3" {
		t.Errorf("uploaded Bundle-Version = %q, want %q", version, "1.0.3")
	}

	manifest, err := os.ReadFile(filepath.Join(source, "META-INF", "MANIFEST.MF"))
	if err != nil {
		t.Fatalf("unable to read manifest: %v", err)
	}
	if string(manifest) != testManifest {
		t.Error("the repository manifest was modified, the rename must apply to the archive only")
	}
}

//A second upload is the PUT that failed before the fix
func TestUploadArtifactTwiceKeepsSymbolicNameStable(t *testing.T) {
	tenant := newStubTenant(t)
	tenant.Packages["TestHarnessPreparationQA"] = true

	globalLandscape = newTestLandscape(t, tenant)

	root := t.TempDir()
	source := writeTestArtifact(t, root)
	setUploadFlags(t, "QA", filepath.Join(root, "build"))

	first, err := uploadArtifact(source, globalLandscape.Environments["QA"])
	if err != nil {
		t.Fatalf("unexpected error on create: %v", err)
	}
	if first.Action != "created" {
		t.Errorf("first Action = %q, want %q", first.Action, "created")
	}

	second, err := uploadArtifact(source, globalLandscape.Environments["QA"])
	if err != nil {
		t.Fatalf("unexpected error on update: %v", err)
	}
	if second.Action != "updated" {
		t.Errorf("second Action = %q, want %q", second.Action, "updated")
	}

	//Both requests must carry the same symbolic name, which is what the tenant
	//compares against the one it stored
	if name := uploadedManifestHeader(t, tenant.Calls, iflow.HeaderBundleSymbolicName); name != "Order_API_TEST_HARNESSQA; singleton:=true" {
		t.Errorf("second upload Bundle-SymbolicName = %q", name)
	}
}

//An environment without a suffix must leave the archive exactly as it is
func TestUploadArtifactLeavesArchiveAloneWithoutSuffix(t *testing.T) {
	tenant := newStubTenant(t)
	tenant.Packages["TestHarnessPreparation"] = true

	globalLandscape = newTestLandscape(t, tenant)

	root := t.TempDir()
	source := writeTestArtifact(t, root)
	setUploadFlags(t, "Dev", filepath.Join(root, "build"))

	skip := true
	uploadSkipVersionCheck = &skip

	if _, err := uploadArtifact(source, globalLandscape.Environments["Dev"]); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if name := uploadedManifestHeader(t, tenant.Calls, iflow.HeaderBundleSymbolicName); name != "Order_API_TEST_HARNESS; singleton:=true" {
		t.Errorf("Bundle-SymbolicName = %q, want it unchanged", name)
	}
	if name := uploadedManifestHeader(t, tenant.Calls, iflow.HeaderBundleName); name != "Order API for Test Harness" {
		t.Errorf("Bundle-Name = %q, want it unchanged", name)
	}
}

//A ready archive carries the identifiers of the repository and needs the same
//rename as a freshly packed folder
func TestUploadZipArchiveIsRenamedForSuffixedEnvironment(t *testing.T) {
	tenant := newStubTenant(t)
	tenant.Packages["TestHarnessPreparationQA"] = true

	globalLandscape = newTestLandscape(t, tenant)

	root := t.TempDir()
	source := writeTestArtifact(t, root)
	setUploadFlags(t, "QA", filepath.Join(root, "build"))

	//Pack for the repository, with no suffix at all
	result, err := packArtifact(packOptions{
		SourceDir: source,
		SkipCheck: true,
		OutputDir: filepath.Join(root, "build"),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	base, err := iflow.ReadManifestHeaderFromZip(result.Zip, iflow.HeaderBundleSymbolicName)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if base != "Order_API_TEST_HARNESS; singleton:=true" {
		t.Fatalf("the unsuffixed archive should keep the base name, got %q", base)
	}

	row, err := uploadArtifact(result.ZipPath, globalLandscape.Environments["QA"])
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if row.Source != "zip" {
		t.Errorf("Source = %q, want %q", row.Source, "zip")
	}

	if name := uploadedManifestHeader(t, tenant.Calls, iflow.HeaderBundleSymbolicName); name != "Order_API_TEST_HARNESSQA; singleton:=true" {
		t.Errorf("uploaded Bundle-SymbolicName = %q, want the suffixed one", name)
	}
}

//Regression: an archive packed for the target environment is named after the
//suffixed id. Uploading it must not suffix the id a second time, and must not
//suffix the symbolic name a second time either - both produced a failure
//against a live tenant.
func TestUploadAlreadySuffixedArchiveIsNotSuffixedTwice(t *testing.T) {
	tenant := newStubTenant(t)
	tenant.Packages["TestHarnessPreparationQA"] = true

	globalLandscape = newTestLandscape(t, tenant)

	root := t.TempDir()
	source := writeTestArtifact(t, root)
	setUploadFlags(t, "QA", filepath.Join(root, "build"))

	//Pack for QA: the archive is Order_API_TEST_HARNESSQA.zip and already
	//carries the suffixed identifiers
	result, err := packArtifact(packOptions{
		SourceDir: source,
		Suffix:    "QA",
		SkipCheck: true,
		OutputDir: filepath.Join(root, "build"),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if filepath.Base(result.ZipPath) != "Order_API_TEST_HARNESSQA.zip" {
		t.Fatalf("archive name = %q, want Order_API_TEST_HARNESSQA.zip", filepath.Base(result.ZipPath))
	}

	row, err := uploadArtifact(result.ZipPath, globalLandscape.Environments["QA"])
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if row.ArtifactId != "Order_API_TEST_HARNESSQA" {
		t.Errorf("ArtifactId = %q, want %q", row.ArtifactId, "Order_API_TEST_HARNESSQA")
	}
	if row.PackageId != "TestHarnessPreparationQA" {
		t.Errorf("PackageId = %q, want %q", row.PackageId, "TestHarnessPreparationQA")
	}

	if name := uploadedManifestHeader(t, tenant.Calls, iflow.HeaderBundleSymbolicName); name != "Order_API_TEST_HARNESSQA; singleton:=true" {
		t.Errorf("Bundle-SymbolicName = %q, want it suffixed exactly once", name)
	}
	if name := uploadedManifestHeader(t, tenant.Calls, iflow.HeaderBundleName); name != "Order API for Test Harness QA" {
		t.Errorf("Bundle-Name = %q, want it suffixed exactly once", name)
	}
}

func TestTrimTargetSuffix(t *testing.T) {
	tenant := newStubTenant(t)
	globalLandscape = newTestLandscape(t, tenant)

	tests := []struct {
		name       string
		artifactId string
		suffix     string
		want       string
	}{
		{name: "suffixed id of a known artifact", artifactId: "Order_API_TEST_HARNESSQA", suffix: "QA",
			want: "Order_API_TEST_HARNESS"},
		{name: "base id is left alone", artifactId: "Order_API_TEST_HARNESS", suffix: "QA",
			want: "Order_API_TEST_HARNESS"},
		{name: "empty suffix", artifactId: "Order_API_TEST_HARNESSQA", suffix: "",
			want: "Order_API_TEST_HARNESSQA"},
		{name: "id equal to the suffix", artifactId: "QA", suffix: "QA", want: "QA"},
		//Trimming would leave something the landscape does not declare, so the
		//name genuinely ends with QA and must survive
		{name: "unknown artifact ending with the suffix", artifactId: "Billing_QA", suffix: "QA",
			want: "Billing_QA"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := trimTargetSuffix(test.artifactId, test.suffix); got != test.want {
				t.Errorf("trimTargetSuffix(%q, %q) = %q, want %q",
					test.artifactId, test.suffix, got, test.want)
			}
		})
	}
}
