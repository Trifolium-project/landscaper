package cmd

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Trifolium-project/landscaper/packages/auditlog"
	"github.com/Trifolium-project/landscaper/packages/cpiclient"
)

//A password that could not plausibly occur anywhere else in the log
const sentinelPassword = "s3nt1nel-not-a-real-password"

//startTestAuditLog points the package level logger at a temporary directory and
//attaches it to every client of the test landscape, the way initConfig does
func startTestAuditLog(t *testing.T) *auditlog.Logger {
	t.Helper()

	logger, err := auditlog.New(t.TempDir())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	previous := auditLogger
	auditLogger = logger
	t.Cleanup(func() {
		logger.Close()
		auditLogger = previous
	})

	globalLandscape.SetLogger(logger)

	return logger
}

//useSentinelCredentials replaces the client of every system with one that
//authenticates using a password we can search the log for
func useSentinelCredentials(t *testing.T, tenant *stubTenant) {
	t.Helper()

	host := strings.TrimPrefix(tenant.Server.URL, "https://")
	for _, system := range globalLandscape.Systems {
		system.Client = cpiclient.NewCPIBasicAuthClient("user", sentinelPassword, "", host, false)
	}
}

func readAuditRecords(t *testing.T, logger *auditlog.Logger) []map[string]interface{} {
	t.Helper()

	content, err := os.ReadFile(logger.Path())
	if err != nil {
		t.Fatalf("unable to read the audit log: %v", err)
	}

	records := []map[string]interface{}{}
	for _, line := range strings.Split(strings.TrimRight(string(content), "\n"), "\n") {
		if line == "" {
			continue
		}
		record := map[string]interface{}{}
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("audit log line is not valid JSON: %v\n%s", err, line)
		}
		records = append(records, record)
	}

	return records
}

//The assertion this whole feature stands or falls on. It reads the raw file
//rather than parsed records, so that a credential arriving through a path
//nobody modelled is still caught.
func TestAuditLogNeverContainsCredentials(t *testing.T) {
	tenant := newStubTenant(t)
	stubArtifact(t, tenant, "TestHarnessPreparation", "Order_API_TEST_HARNESS", "1.0.3")

	globalLandscape = newTestLandscape(t, tenant)
	useSentinelCredentials(t, tenant)
	output := t.TempDir()
	setDownloadFlags(t, output)

	logger := startTestAuditLog(t)

	environment := globalLandscape.Environments["Dev"]
	target := downloadTarget{PackageId: "TestHarnessPreparation"}

	artifacts, err := environment.System.Client.ReadIntegrationDesigntimeArtifacts(target.PackageId, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	downloadPackageArtifacts(environment, target, artifacts, output)

	content, err := os.ReadFile(logger.Path())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	secrets := map[string]string{
		"the password itself":     sentinelPassword,
		"the Basic header value":  base64.StdEncoding.EncodeToString([]byte("user:" + sentinelPassword)),
		"the CSRF token":          "stub-token",
	}
	for name, secret := range secrets {
		if bytes.Contains(content, []byte(secret)) {
			t.Errorf("%s leaked into the audit log", name)
		}
	}

	//The Authorization header must still be visible as a redacted key, so an
	//auditor can see that authentication was sent
	if !bytes.Contains(content, []byte("Authorization")) {
		t.Error("the Authorization header is missing entirely, it should be present but redacted")
	}
}

func TestAuditLogRecordsTenantCalls(t *testing.T) {
	tenant := newStubTenant(t)
	stubArtifact(t, tenant, "TestHarnessPreparation", "Order_API_TEST_HARNESS", "1.0.3")

	globalLandscape = newTestLandscape(t, tenant)
	output := t.TempDir()
	setDownloadFlags(t, output)

	logger := startTestAuditLog(t)

	environment := globalLandscape.Environments["Dev"]
	target := downloadTarget{PackageId: "TestHarnessPreparation"}

	artifacts, err := environment.System.Client.ReadIntegrationDesigntimeArtifacts(target.PackageId, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	rows := downloadPackageArtifacts(environment, target, artifacts, output)
	if len(rows) != 1 || rows[0].Status != "downloaded" {
		t.Fatalf("unexpected rows: %+v", rows)
	}

	records := readAuditRecords(t, logger)

	httpRecords := 0
	itemRecords := 0
	sawStatus := false
	for _, record := range records {
		switch record["type"] {
		case "http":
			httpRecords++
			if status, ok := record["status"].(float64); ok && int(status) == 200 {
				sawStatus = true
			}
			if record["url"] == nil || record["method"] == nil {
				t.Errorf("http record is missing method or url: %v", record)
			}
		case "item":
			itemRecords++
		}
	}

	if httpRecords == 0 {
		t.Error("no http record was written, SetLogger did not reach the client")
	}
	if !sawStatus {
		t.Error("no http record carried a status code, which the client's error discards")
	}
	//Item records come from the command's flush closure, which this test does
	//not reach; downloadPackageArtifacts only produces the rows
	if itemRecords != 0 {
		t.Errorf("expected no item records from this helper, got %d", itemRecords)
	}
}

//The multi megabyte archive must never be written, whatever the response rules
func TestAuditLogOmitsDownloadedArchive(t *testing.T) {
	tenant := newStubTenant(t)
	stubArtifact(t, tenant, "TestHarnessPreparation", "Order_API_TEST_HARNESS", "1.0.3")

	globalLandscape = newTestLandscape(t, tenant)
	output := t.TempDir()
	setDownloadFlags(t, output)

	logger := startTestAuditLog(t)

	environment := globalLandscape.Environments["Dev"]
	target := downloadTarget{PackageId: "TestHarnessPreparation"}

	artifacts, err := environment.System.Client.ReadIntegrationDesigntimeArtifacts(target.PackageId, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	downloadPackageArtifacts(environment, target, artifacts, output)

	content, err := os.ReadFile(logger.Path())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	//The local PK header of a zip entry. Its presence would mean the archive
	//was written into the log.
	if bytes.Contains(content, []byte("PK\x03\x04")) {
		t.Error("the downloaded archive was written into the audit log")
	}

	records := readAuditRecords(t, logger)
	sawBinary := false
	for _, record := range records {
		body, ok := record["response_body"].(map[string]interface{})
		if !ok {
			continue
		}
		if binary, _ := body["binary"].(bool); binary {
			sawBinary = true
			if body["text"] != nil {
				t.Error("a binary body was recorded with its text")
			}
			if size, ok := body["bytes"].(float64); !ok || size <= 0 {
				t.Errorf("binary body recorded without a size: %v", body)
			}
		}
	}
	if !sawBinary {
		t.Error("the archive download was not recorded as a binary body")
	}
}

//Logging is off by default, and nothing in the command path may assume
//otherwise. Tests never run initConfig, so the flag pointers are nil here.
func TestNoAuditLogWhenDisabled(t *testing.T) {
	tenant := newStubTenant(t)
	stubArtifact(t, tenant, "TestHarnessPreparation", "Order_API_TEST_HARNESS", "1.0.3")

	globalLandscape = newTestLandscape(t, tenant)
	output := t.TempDir()
	setDownloadFlags(t, output)

	//No logger at all, as on any run without --log
	previous := auditLogger
	auditLogger = nil
	t.Cleanup(func() { auditLogger = previous })

	logDirectory := t.TempDir()

	environment := globalLandscape.Environments["Dev"]
	target := downloadTarget{PackageId: "TestHarnessPreparation"}

	artifacts, err := environment.System.Client.ReadIntegrationDesigntimeArtifacts(target.PackageId, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	//Must not panic on the nil logger, and must write nothing
	rows := downloadPackageArtifacts(environment, target, artifacts, output)
	auditItem(map[string]interface{}{"artifact": "X", "status": "downloaded"})

	if len(rows) != 1 {
		t.Fatalf("expected the download to still work, got %+v", rows)
	}

	entries, err := os.ReadDir(logDirectory)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected no log files, found %d", len(entries))
	}
}

//startAuditLog must fail loudly rather than run a tenant operation unrecorded
func TestAuditLogDirectoryCannotBeAFile(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blocker, []byte("x"), 0600); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, err := auditlog.New(blocker); err == nil {
		t.Fatal("expected an error when the log directory cannot be created")
	}
}
