package cmd

import (
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Trifolium-project/landscaper/packages/cpiclient"
	"github.com/Trifolium-project/landscaper/packages/landscape"
)

//stubDiscoverPackage is a package of the SAP Business Accelerator Hub
type stubDiscoverPackage struct {
	Mode string
	//Integration flow ids and their versions
	Flows map[string]string
}

//stubPackageOps imitates what a real tenant answered, see
//changelog/0008-package-copy-delete.md
type stubPackageOps struct {
	Discover map[string]*stubDiscoverPackage
	//Package id to artifacts of the other collections, keyed by collection
	OtherArtifacts map[string]map[string][]string
	//Deployed runtime artifact ids
	Runtime map[string]bool
	//Package id to the number of reads it survives after a DELETE, which the
	//tenant runs in the background
	PendingDeletes map[string]int
	//Undeployed ids to the number of runtime reads they survive
	PendingUndeploys map[string]int
}

func (tenant *stubTenant) enablePackageOps() *stubPackageOps {
	tenant.PackageOps = &stubPackageOps{
		Discover:         map[string]*stubDiscoverPackage{},
		OtherArtifacts:   map[string]map[string][]string{},
		Runtime:          map[string]bool{},
		PendingDeletes:   map[string]int{},
		PendingUndeploys: map[string]int{},
	}
	return tenant.PackageOps
}

func (tenant *stubTenant) deletePackage(packageId string) {
	delete(tenant.Packages, packageId)
	delete(tenant.PackageModes, packageId)
	for id, owner := range tenant.ArtifactPackages {
		if owner == packageId {
			delete(tenant.Artifacts, id)
			delete(tenant.ArtifactPackages, id)
		}
	}
}

//handlePackageOps answers the copy, delete and runtime calls and reports
//whether the request was one of them
func (tenant *stubTenant) handlePackageOps(writer http.ResponseWriter, request *http.Request) bool {

	ops := tenant.PackageOps
	if ops == nil {
		return false
	}

	path := request.URL.Path
	query := request.URL.Query()

	switch {

	case request.Method == http.MethodPost && strings.HasSuffix(path, "/CopyIntegrationPackage"):
		id := strings.Trim(query.Get("Id"), "'")
		mode := strings.Trim(query.Get("ImportMode"), "'")
		suffix := strings.Trim(query.Get("Suffix"), "'")
		source := ops.Discover[id]
		if source == nil {
			writeODataError(writer, http.StatusNotFound, "Copy not successful from content hub to workpace. An error occurred while fetching the integration package "+id+" from the Integration Content Catalog.")
			return true
		}
		target, artifactSuffix := id, ""
		if mode == cpiclient.ImportModeCreateCopy {
			target, artifactSuffix = id+"."+suffix, "."+suffix
		} else if tenant.Packages[id] && mode == "" {
			writeODataError(writer, http.StatusConflict, "Copy not successful from content hub to workpace due to conflict.")
			return true
		}
		tenant.Packages[target] = true
		tenant.PackageModes[target] = source.Mode
		for flow, version := range source.Flows {
			tenant.Artifacts[flow+artifactSuffix] = version
			tenant.ArtifactPackages[flow+artifactSuffix] = target
		}
		writer.WriteHeader(http.StatusCreated)
		fmt.Fprintf(writer, `{"d":{"Id":%q,"Name":%q,"Version":"1.0.0","Vendor":"SAP","Mode":%q,"Description":null}}`,
			target, "Name of "+target, source.Mode)

	case request.Method == http.MethodDelete && strings.Contains(path, "/IntegrationPackages("):
		packageId := packageIdFromPath(path)
		if !tenant.Packages[packageId] {
			writeODataError(writer, http.StatusNotFound, "Requested entity could not be found.")
			return true
		}
		ops.PendingDeletes[packageId] = 1
		writer.WriteHeader(http.StatusAccepted)

	case request.Method == http.MethodGet && strings.HasSuffix(path, "')") && strings.Contains(path, "/IntegrationPackages("):
		//A pending deletion still shows the package for a while
		packageId := packageIdFromPath(path)
		if remaining, pending := ops.PendingDeletes[packageId]; pending {
			if remaining <= 0 {
				delete(ops.PendingDeletes, packageId)
				tenant.deletePackage(packageId)
			} else {
				ops.PendingDeletes[packageId] = remaining - 1
			}
		}
		if !tenant.Packages[packageId] {
			writeODataError(writer, http.StatusNotFound, "Requested entity could not be found.")
			return true
		}
		return false

	case request.Method == http.MethodGet && (strings.HasSuffix(path, "/ValueMappingDesigntimeArtifacts") ||
		strings.HasSuffix(path, "/ScriptCollectionDesigntimeArtifacts") ||
		strings.HasSuffix(path, "/MessageMappingDesigntimeArtifacts")):
		packageId := packageIdFromPath(path)
		collection := path[strings.LastIndex(path, "/")+1:]
		results := []map[string]string{}
		for _, id := range ops.OtherArtifacts[packageId][collection] {
			results = append(results, map[string]string{"Id": id, "Version": "1.0.0", "Name": id})
		}
		writeJSONResults(writer, results)

	case request.Method == http.MethodGet && strings.HasSuffix(path, "/IntegrationRuntimeArtifacts"):
		results := []map[string]string{}
		for id := range ops.Runtime {
			if remaining, pending := ops.PendingUndeploys[id]; pending {
				if remaining <= 0 {
					delete(ops.PendingUndeploys, id)
					delete(ops.Runtime, id)
					continue
				}
				ops.PendingUndeploys[id] = remaining - 1
			}
			results = append(results, map[string]string{"Id": id, "Version": "1.0.0", "Type": "INTEGRATION_FLOW", "Status": "STARTED"})
		}
		writeJSONResults(writer, results)

	case request.Method == http.MethodDelete && strings.Contains(path, "/IntegrationRuntimeArtifacts("):
		id := quotedIdFromPath(path, "IntegrationRuntimeArtifacts(Id=")
		ops.PendingUndeploys[id] = 1
		writer.WriteHeader(http.StatusAccepted)

	default:
		return false
	}

	return true
}

func writeJSONResults(writer http.ResponseWriter, results []map[string]string) {
	parts := []string{}
	for _, result := range results {
		fields := []string{}
		for key, value := range result {
			fields = append(fields, fmt.Sprintf("%q:%q", key, value))
		}
		parts = append(parts, "{"+strings.Join(fields, ",")+"}")
	}
	fmt.Fprintf(writer, `{"d":{"results":[%s]}}`, strings.Join(parts, ","))
}

//newPackageTest wires the stub tenant with one editable and one configure only
//Hub package in Discover
func newPackageTest(t *testing.T) (*stubTenant, *stubPackageOps) {
	t.Helper()
	tenant := newStubTenant(t)
	ops := tenant.enablePackageOps()
	ops.Discover["PrivateLinkProxy"] = &stubDiscoverPackage{Mode: "EDIT_ALLOWED",
		Flows: map[string]string{"AzureBlobConnectivityPrivateLinkServiceSample": "1.0.0"}}
	ops.Discover["SAPIBPReusableIntegrationFlowsExamples"] = &stubDiscoverPackage{Mode: cpiclient.PackageModeReadOnly,
		Flows: map[string]string{"SAP_IBP_Read_-_Example": "1.0.2", "SAP_IBP_Write_-_Example": "1.0.2"}}
	globalLandscape = newTestLandscape(t, tenant)
	return tenant, ops
}

//devCPIClient is the client of the Dev environment, pointing at the stub tenant
func devCPIClient() *cpiclient.CPIClient {
	return globalLandscape.Environments["Dev"].System.Client
}

func writeCalls(calls []tenantCall) []string {
	writes := []string{}
	for _, call := range calls {
		if call.Method != http.MethodGet {
			writes = append(writes, call.Method+" "+call.Path)
		}
	}
	return writes
}

func TestPackageCopyCreatesThePackage(t *testing.T) {
	tenant, _ := newPackageTest(t)

	report := copyPackage(devCPIClient(), "PrivateLinkProxy", "", "")

	if report.Status != copyStatusCopied || packageCopyExitCode(report.Status) != exitDeployed {
		t.Fatalf("Status = %q, Error = %q", report.Status, report.Error)
	}
	if report.Id != "PrivateLinkProxy" || report.Mode != "EDIT_ALLOWED" || report.Vendor != "SAP" {
		t.Errorf("unexpected report %+v", report)
	}
	if len(report.Artifacts) != 1 || report.Artifacts[0].Id != "AzureBlobConnectivityPrivateLinkServiceSample" ||
		report.Artifacts[0].Type != "IntegrationFlow" {
		t.Errorf("unexpected artifacts %+v", report.Artifacts)
	}

	copied := findCall(tenant.Calls, http.MethodPost, "/CopyIntegrationPackage")
	if copied.Query.Get("Id") != "'PrivateLinkProxy'" || copied.Query.Get("ImportMode") != "" {
		t.Errorf("unexpected copy call %+v", copied.Query)
	}
}

func TestPackageCopyOfAnExistingPackage(t *testing.T) {
	tenant, _ := newPackageTest(t)
	tenant.Packages["PrivateLinkProxy"] = true

	report := copyPackage(devCPIClient(), "PrivateLinkProxy", "", "")

	if report.Status != copyStatusExists || packageCopyExitCode(report.Status) != exitPackageExists {
		t.Errorf("Status = %q, want exists and exit 5", report.Status)
	}
	if !strings.Contains(report.Error, "conflict") {
		t.Errorf("the tenant's message should be kept, got %q", report.Error)
	}
}

func TestPackageCopyNotInDiscover(t *testing.T) {
	newPackageTest(t)

	report := copyPackage(devCPIClient(), "NoSuchHubPackage", "", "")

	if report.Status != copyStatusNotFound || packageCopyExitCode(report.Status) != exitPackageNotInDiscover {
		t.Errorf("Status = %q, want not-found and exit 6", report.Status)
	}
}

func TestPackageCopyCreateCopy(t *testing.T) {
	tenant, _ := newPackageTest(t)
	tenant.Packages["PrivateLinkProxy"] = true

	report := copyPackage(devCPIClient(), "PrivateLinkProxy", cpiclient.ImportModeCreateCopy, "LSC")

	//The tenant separates the suffix with a dot, in the package id and the artifact ids
	if report.Status != copyStatusCopied || report.Id != "PrivateLinkProxy.LSC" || report.Source != "PrivateLinkProxy" {
		t.Fatalf("unexpected report %+v", report)
	}
	if len(report.Artifacts) != 1 || report.Artifacts[0].Id != "AzureBlobConnectivityPrivateLinkServiceSample.LSC" {
		t.Errorf("unexpected artifacts %+v", report.Artifacts)
	}
	copied := findCall(tenant.Calls, http.MethodPost, "/CopyIntegrationPackage")
	if copied.Query.Get("ImportMode") != "'CREATE_COPY'" || copied.Query.Get("Suffix") != "'LSC'" {
		t.Errorf("unexpected copy call %+v", copied.Query)
	}
}

func TestValidateCopyFlags(t *testing.T) {

	tests := []struct {
		mode, suffix, output string
		want                 string
		wantErr              bool
	}{
		{"", "", "text", "", false},
		{"overwrite", "", "json", cpiclient.ImportModeOverwrite, false},
		{"Overwrite-Merge", "", "text", cpiclient.ImportModeOverwriteMerge, false},
		{"create-copy", "X", "text", cpiclient.ImportModeCreateCopy, false},
		{"create-copy", "", "text", "", true},
		{"overwrite", "X", "text", "", true},
		{"", "X", "text", "", true},
		{"replace", "", "text", "", true},
		{"", "", "yaml", "", true},
	}

	for _, test := range tests {
		got, err := validateCopyFlags(test.mode, test.suffix, test.output)
		if (err != nil) != test.wantErr || got != test.want {
			t.Errorf("validateCopyFlags(%q, %q, %q) = %q, %v", test.mode, test.suffix, test.output, got, err)
		}
	}
}

func TestRawPackageFlagUndoesTheSuffix(t *testing.T) {
	previous := pkg
	t.Cleanup(func() { pkg = previous })

	//root.go has already turned "PrivateLinkProxy" into "PrivateLinkProxyQA"
	value := "PrivateLinkProxyQA"
	pkg = &value
	if got := rawPackageFlag(&landscape.Environment{Suffix: "QA"}); got != "PrivateLinkProxy" {
		t.Errorf("got %q", got)
	}
	if got := rawPackageFlag(&landscape.Environment{}); got != "PrivateLinkProxyQA" {
		t.Errorf("without a suffix the value is kept, got %q", got)
	}
}

func deleteRequest(packageId string) packageDeleteRequest {
	return packageDeleteRequest{
		PackageId: packageId,
		Env:       "Dev",
		Timeout:   time.Second,
		Interval:  time.Millisecond,
	}
}

func TestPackageDeleteDeletes(t *testing.T) {
	tenant, ops := newPackageTest(t)
	copyPackage(devCPIClient(), "PrivateLinkProxy", "", "")
	ops.OtherArtifacts["PrivateLinkProxy"] = map[string][]string{"ValueMappingDesigntimeArtifacts": {"Regions"}}

	report := deletePackage(devCPIClient(), deleteRequest("PrivateLinkProxy"))

	if report.Status != deleteStatusDeleted || !report.Deleted || packageDeleteExitCode(report.Status) != exitDeployed {
		t.Fatalf("Status = %q, Error = %q", report.Status, report.Error)
	}
	if len(report.Artifacts) != 2 || report.Artifacts[1].Type != "ValueMapping" {
		t.Errorf("expected artifacts of every type, got %+v", report.Artifacts)
	}
	if tenant.Packages["PrivateLinkProxy"] {
		t.Error("the package is still there")
	}
	//The tenant deletes in the background, the command waits for the 404
	if got := countCalls(tenant.Calls, http.MethodGet, "/IntegrationPackages('PrivateLinkProxy')"); got < 3 {
		t.Errorf("package reads = %d, want polling until it is gone", got)
	}
}

func TestPackageDeleteNotFound(t *testing.T) {
	tenant, _ := newPackageTest(t)

	report := deletePackage(devCPIClient(), deleteRequest("NoSuchPackage"))

	if report.Status != deleteStatusNotFound || packageDeleteExitCode(report.Status) != exitPackageNotFound {
		t.Errorf("Status = %q, want not-found and exit 4", report.Status)
	}
	if writes := writeCalls(tenant.Calls); len(writes) != 0 {
		t.Errorf("unexpected writes %v", writes)
	}
}

func TestPackageDeleteRefusesDeclaredPackage(t *testing.T) {
	tenant, _ := newPackageTest(t)
	tenant.Packages["TestHarnessPreparation"] = true

	request := deleteRequest("TestHarnessPreparation")
	request.Declared = true

	report := deletePackage(devCPIClient(), request)
	if report.Status != deleteStatusDeclared || packageDeleteExitCode(report.Status) != exitPackageDeclared {
		t.Errorf("Status = %q, want declared and exit 6", report.Status)
	}
	if writes := writeCalls(tenant.Calls); len(writes) != 0 {
		t.Errorf("unexpected writes %v", writes)
	}

	request.Force = true
	if report := deletePackage(devCPIClient(), request); report.Status != deleteStatusDeleted {
		t.Errorf("with --force Status = %q, Error = %q", report.Status, report.Error)
	}
}

//The tenant deletes a package with deployed content and leaves the content
//running, so the command has to refuse
func TestPackageDeleteRefusesDeployedContent(t *testing.T) {
	tenant, ops := newPackageTest(t)
	copyPackage(devCPIClient(), "PrivateLinkProxy", "", "")
	ops.Runtime["AzureBlobConnectivityPrivateLinkServiceSample"] = true
	ops.Runtime["SomethingElse"] = true

	report := deletePackage(devCPIClient(), deleteRequest("PrivateLinkProxy"))

	if report.Status != deleteStatusDeployed || packageDeleteExitCode(report.Status) != exitPackageDeployed {
		t.Fatalf("Status = %q, want deployed and exit 5", report.Status)
	}
	if !report.Artifacts[0].Deployed || !strings.Contains(report.Error, "AzureBlobConnectivityPrivateLinkServiceSample") {
		t.Errorf("the deployed artifact should be listed, got %+v / %q", report.Artifacts, report.Error)
	}
	if strings.Contains(report.Error, "SomethingElse") {
		t.Error("content of other packages is not this package's business")
	}
	if !tenant.Packages["PrivateLinkProxy"] || len(writeCalls(tenant.Calls)) != 1 {
		t.Errorf("nothing but the copy may have been written: %v", writeCalls(tenant.Calls))
	}
}

func TestPackageDeleteUndeploysFirst(t *testing.T) {
	tenant, ops := newPackageTest(t)
	copyPackage(devCPIClient(), "PrivateLinkProxy", "", "")
	ops.Runtime["AzureBlobConnectivityPrivateLinkServiceSample"] = true

	request := deleteRequest("PrivateLinkProxy")
	request.Undeploy = true

	report := deletePackage(devCPIClient(), request)

	if report.Status != deleteStatusDeleted {
		t.Fatalf("Status = %q, Error = %q", report.Status, report.Error)
	}
	if len(report.Undeployed) != 1 || ops.Runtime["AzureBlobConnectivityPrivateLinkServiceSample"] {
		t.Errorf("Undeployed = %v, runtime = %v", report.Undeployed, ops.Runtime)
	}

	//Undeployed and gone from the runtime before the package is deleted
	writes := writeCalls(tenant.Calls)
	if len(writes) != 3 || !strings.Contains(writes[1], "IntegrationRuntimeArtifacts") || !strings.Contains(writes[2], "IntegrationPackages") {
		t.Errorf("unexpected write order %v", writes)
	}
}

func TestPackageDeleteDryRunWritesNothing(t *testing.T) {
	tenant, ops := newPackageTest(t)
	copyPackage(devCPIClient(), "PrivateLinkProxy", "", "")
	ops.Runtime["AzureBlobConnectivityPrivateLinkServiceSample"] = true
	before := len(writeCalls(tenant.Calls))

	request := deleteRequest("PrivateLinkProxy")
	request.DryRun = true
	request.Undeploy = true
	request.Confirm = func() (bool, error) {
		t.Error("a dry run must not ask")
		return false, nil
	}

	report := deletePackage(devCPIClient(), request)

	if report.Status != deleteStatusDryRun || packageDeleteExitCode(report.Status) != exitDeployed || report.Deleted {
		t.Errorf("Status = %q, want dry-run and exit 0", report.Status)
	}
	if writes := writeCalls(tenant.Calls); len(writes) != before {
		t.Errorf("a dry run wrote %v", writes[before:])
	}
	if !report.Artifacts[0].Deployed {
		t.Error("the plan should show the deployed state")
	}
}

func TestPackageDeleteDeclinedConfirmation(t *testing.T) {
	tenant, _ := newPackageTest(t)
	copyPackage(devCPIClient(), "PrivateLinkProxy", "", "")

	request := deleteRequest("PrivateLinkProxy")
	request.Confirm = func() (bool, error) { return false, nil }

	report := deletePackage(devCPIClient(), request)

	if report.Status != deleteStatusCancelled || packageDeleteExitCode(report.Status) != exitFailure {
		t.Errorf("Status = %q, want cancelled and exit 1", report.Status)
	}
	if !tenant.Packages["PrivateLinkProxy"] {
		t.Error("the package was deleted without confirmation")
	}
}

func TestDeleteConfirmation(t *testing.T) {
	previousTerminal, previousInput := stdinIsTerminal, confirmInput
	t.Cleanup(func() { stdinIsTerminal, confirmInput = previousTerminal, previousInput })

	//--yes needs no question
	if confirm, err := deleteConfirmation(true, "P", "Dev"); err != nil || confirm != nil {
		t.Errorf("--yes: confirm = %v, err = %v", confirm != nil, err)
	}

	//Without a terminal nobody can answer
	stdinIsTerminal = func() bool { return false }
	if _, err := deleteConfirmation(false, "P", "Dev"); err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Errorf("expected a refusal naming --yes, got %v", err)
	}

	stdinIsTerminal = func() bool { return true }
	for answer, want := range map[string]bool{"y\n": true, "YES\n": true, "n\n": false, "\n": false, "": false} {
		confirmInput = strings.NewReader(answer)
		confirm, err := deleteConfirmation(false, "P", "Dev")
		if err != nil {
			t.Fatal(err)
		}
		if got, _ := confirm(); got != want {
			t.Errorf("answer %q = %v, want %v", answer, got, want)
		}
	}
}

func TestDownloadOfConfigureOnlyPackage(t *testing.T) {
	tenant, _ := newPackageTest(t)
	copyPackage(devCPIClient(), "SAPIBPReusableIntegrationFlowsExamples", "", "")
	copyPackage(devCPIClient(), "PrivateLinkProxy", "", "")
	stubArtifact(t, tenant, "PrivateLinkProxy", "AzureBlobConnectivityPrivateLinkServiceSample", "1.0.0")

	rows := downloadTargets(globalLandscape.Environments["Dev"], []downloadTarget{
		{PackageId: "SAPIBPReusableIntegrationFlowsExamples"},
		{PackageId: "PrivateLinkProxy"},
	}, filepath.Join(t.TempDir(), "artifacts"))

	notDownloadable := 0
	for _, row := range rows {
		if row.NotDownloadable {
			notDownloadable++
			if row.Status != "not downloadable (configure-only SAP package)" || row.Failed {
				t.Errorf("unexpected row %+v", row)
			}
		}
	}
	if notDownloadable != 2 || len(rows) != 3 {
		t.Fatalf("rows = %d, not downloadable = %d", len(rows), notDownloadable)
	}
	if got := downloadExitCode(rows); got != exitNotDownloadable {
		t.Errorf("exit code = %d, want 7", got)
	}

	//SAP answers 400 for the content of a configure only package, so none is asked for
	if got := countCalls(tenant.Calls, http.MethodGet, "SAP_IBP_"); got != 0 {
		t.Errorf("content of a configure only package was requested %d times", got)
	}
}

func TestDownloadExitCode(t *testing.T) {
	ok := &downloadRow{}
	readOnly := &downloadRow{NotDownloadable: true}
	failed := &downloadRow{Failed: true}

	for _, test := range []struct {
		rows []*downloadRow
		want int
	}{
		{[]*downloadRow{ok}, exitDeployed},
		{[]*downloadRow{ok, readOnly}, exitNotDownloadable},
		{[]*downloadRow{readOnly, failed}, exitFailure},
		{[]*downloadRow{}, exitDeployed},
	} {
		if got := downloadExitCode(test.rows); got != test.want {
			t.Errorf("downloadExitCode = %d, want %d", got, test.want)
		}
	}
}
