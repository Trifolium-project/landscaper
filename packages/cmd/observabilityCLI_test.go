package cmd

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Trifolium-project/landscaper/packages/auditlog"
)

//A fresh process keeps Cobra flags and global state isolated and exercises os.Exit.
func TestObservabilityCLIProcess(t *testing.T) {
	if os.Getenv("LANDSCAPER_CLI_TEST_PROCESS") != "1" {
		return
	}
	index := 0
	for index < len(os.Args) && os.Args[index] != "--" {
		index++
	}
	if index == len(os.Args) {
		os.Exit(99)
	}
	os.Args = append([]string{"landscaper"}, os.Args[index+1:]...)
	//Only the child running against the parent's localhost TLS fixture trusts its certificate.
	http.DefaultTransport.(*http.Transport).TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	Execute()
	os.Exit(0)
}

type cliEvidence struct {
	stdout string
	stderr string
	records []map[string]interface{}
	progress []map[string]interface{}
	code int
}

func runObservabilityCLI(t *testing.T, tenant *stubTenant, args []string, logging bool) cliEvidence {
	t.Helper()
	//The older shared stub only supports collection reads. Add the single-artifact
	//and configuration endpoints needed by the real get/deploy command here.
	original := tenant.Server.Config.Handler
	tenant.Server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if r.Method == http.MethodGet && strings.Contains(path, "/IntegrationDesigntimeArtifacts(") {
			id := quotedIdFromPath(path, "IntegrationDesigntimeArtifacts(Id=")
			if strings.HasSuffix(path, "/Configurations") {
				fmt.Fprint(w, `{"d":{"results":[]}}`)
				return
			}
			if strings.Index(path, ")") == len(path)-1 {
				if version, exists := tenant.Artifacts[id]; exists {
					fmt.Fprintf(w, "<entry><properties><Id>%s</Id><Version>%s</Version><PackageId>%s</PackageId><Name>%s</Name></properties></entry>",
						id, version, tenant.ArtifactPackages[id], id)
					return
				}
			}
		}
		if r.Method == http.MethodGet && strings.Contains(path, "/IntegrationRuntimeArtifacts(") &&
			!strings.Contains(path, "/ErrorInformation") {
			id := quotedIdFromPath(path, "IntegrationRuntimeArtifacts(")
			if tenant.PackageOps != nil && tenant.PackageOps.Runtime[id] {
				json.NewEncoder(w).Encode(map[string]interface{}{"d": map[string]string{
					"Id": id, "Version": tenant.Artifacts[id], "Status": "STARTED",
				}})
				return
			}
		}
		original.ServeHTTP(w, r)
	})
	dir := t.TempDir()
	config := filepath.Join(dir, "landscape.yaml")
	manifest := "landscape:\n  name: CLI fixture\n  originalEnvironment: Dev\n" +
		"  systems:\n    - id: test\n      host: CLI_TEST_HOST\n      login: CLI_TEST_LOGIN\n      password: CLI_TEST_PASSWORD\n" +
		"  environments:\n    - {id: Dev, system: test}\n" +
		"  packages:\n    - id: TestHarnessPreparation\n      artifacts:\n        - {id: Order_API_TEST_HARNESS}\n"
	if err := os.WriteFile(config, []byte(manifest), 0600); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(dir, "nested evidence", "exact.jsonl")
	cmdArgs := []string{"-test.run=^TestObservabilityCLIProcess$", "--"}
	cmdArgs = append(cmdArgs, args...)
	help := false
	for _, arg := range args {
		help = help || arg == "--help"
	}
	if !help {
		cmdArgs = append(cmdArgs, "--landscape-file", config, "--config", config, "--env", "Dev")
	}
	if logging {
		//The exact file itself enables audit logging, without --log.
		cmdArgs = append(cmdArgs, "--log-file", logPath, "--log-dir", filepath.Join(dir, "unused"))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[0], cmdArgs...)
	command.Dir = dir //never read the developer's .env or landscape
	overrides := map[string]string{
		"LANDSCAPER_CLI_TEST_PROCESS": "1", "CLI_TEST_HOST": strings.TrimPrefix(tenant.Server.URL, "https://"),
		"CLI_TEST_LOGIN": "fixture-user", "CLI_TEST_PASSWORD": "fixture-password",
		"LANDSCAPER_RUN_ID": "env-run", "TRACEPARENT": "00-" + strings.Repeat("1", 32) + "-" + strings.Repeat("2", 16) + "-01",
	}
	for _, value := range os.Environ() {
		key := strings.SplitN(value, "=", 2)[0]
		if _, replaced := overrides[key]; !replaced {
			command.Env = append(command.Env, value)
		}
	}
	for key, value := range overrides {
		command.Env = append(command.Env, key+"="+value)
	}
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	err := command.Run()
	result := cliEvidence{stdout: stdout.String(), stderr: stderr.String()}
	if ctx.Err() != nil {
		t.Fatalf("CLI timed out: %s", result.stderr)
	}
	if err != nil {
		if failure, ok := err.(*exec.ExitError); ok {
			result.code = failure.ExitCode()
		} else {
			t.Fatal(err)
		}
	}
	for _, line := range strings.Split(result.stderr, "\n") {
		var row map[string]interface{}
		if json.Unmarshal([]byte(line), &row) == nil && row["type"] == "progress" {
			result.progress = append(result.progress, row)
		}
	}
	if logging {
		data, err := os.ReadFile(logPath)
		if err != nil {
			t.Fatalf("exact audit file not written: %v; stderr=%s", err, result.stderr)
		}
		for _, line := range bytes.Split(bytes.TrimSpace(data), []byte("\n")) {
			var row map[string]interface{}
			if err := json.Unmarshal(line, &row); err != nil {
				t.Fatalf("invalid audit record: %v", err)
			}
			result.records = append(result.records, row)
		}
		if _, err := os.Stat(filepath.Join(dir, "unused")); !os.IsNotExist(err) {
			t.Fatal("--log-file also created the unused log directory")
		}
	}
	return result
}

func assertCLIJournal(t *testing.T, result cliEvidence, run string) map[string]interface{} {
	t.Helper()
	rows := result.records
	if len(rows) < 3 || rows[0]["phase"] != "start" || rows[0]["schema"] != float64(1) {
		t.Fatalf("missing schema/start: %v", rows)
	}
	summary, end := rows[len(rows)-2], rows[len(rows)-1]
	if summary["type"] != "summary" || summary["exit_code"] != float64(result.code) || end["phase"] != "end" {
		t.Fatalf("missing or incorrect summary/end: %v %v", summary, end)
	}
	for _, row := range rows {
		if row["run_id"] != run || row["trace_id"] != strings.Repeat("1", 32) || row["parent_span_id"] != strings.Repeat("2", 16) {
			t.Fatalf("uncorrelated record: %v", row)
		}
		if row["type"] == "http" {
			if _, ok := row["duration_ms"].(float64); !ok {
				t.Fatalf("missing HTTP duration: %v", row)
			}
			if row["attempt"] != float64(1) {
				t.Fatalf("missing HTTP attempt: %v", row)
			}
		}
	}
	itemCounts := map[string]int{"ok": 0, "failed": 0, "skipped": 0}
	httpCalls := 0
	httpFailed := 0
	httpDuration := float64(0)
	for _, row := range rows {
		if row["type"] == "item" {
			itemCounts[auditlog.ItemOutcome(row)]++
		}
		if row["type"] == "http" {
			httpCalls++
			httpDuration += row["duration_ms"].(float64)
			status, _ := row["status"].(float64)
			if status >= 400 || row["error"] != nil && row["error"] != "" {
				httpFailed++
			}
		}
	}
	for status, count := range itemCounts {
		if summary["items"].(map[string]interface{})[status] != float64(count) {
			t.Fatalf("summary disagrees with item records: %v", summary)
		}
	}
	if summary["http"].(map[string]interface{})["calls"] != float64(httpCalls) {
		t.Fatalf("summary disagrees with HTTP records: %v", summary)
	}
	httpSummary := summary["http"].(map[string]interface{})
	if httpSummary["failed"] != float64(httpFailed) || httpSummary["total_ms"] != httpDuration {
		t.Fatalf("summary HTTP outcomes/durations disagree: %v", summary)
	}
	progressCounts := map[string]int{"ok": 0, "failed": 0, "skipped": 0}
	for index, row := range result.progress {
		if row["done"] != float64(index+1) || row["run_id"] != run {
			t.Fatalf("invalid progress sequence: %v", row)
		}
		if row["status"] != "ok" && row["status"] != "failed" && row["status"] != "skipped" {
			t.Fatalf("invalid progress outcome: %v", row)
		}
		progressCounts[row["status"].(string)]++
		encoded, _ := json.Marshal(row)
		if bytes.Contains(encoded, []byte("fixture-password")) || bytes.Contains(encoded, []byte("stub-token")) ||
			bytes.Contains(encoded, []byte("response_body")) {
			t.Fatalf("progress leaked response/credentials: %s", encoded)
		}
	}
	if len(result.progress) > 0 {
		for status, count := range progressCounts {
			if count != itemCounts[status] {
				t.Fatalf("progress/summary outcome mismatch: %v %v", progressCounts, itemCounts)
			}
		}
	}
	return summary
}

func TestCLIJSONAuditAndProgress(t *testing.T) {
	tests := []struct {
		name string
		args []string
		code int
		progress int
	}{
		{"artifact get equals", []string{"artifact", "get", "--artifact", "Order_API_TEST_HARNESS", "--output=json"}, 4, 0},
		{"artifact get separate", []string{"artifact", "get", "--artifact", "Order_API_TEST_HARNESS", "--output", "json"}, 4, 0},
		{"package copy", []string{"package", "copy", "--id", "DiscoverPackage", "--output=json"}, 0, 1},
		{"package exists", []string{"package", "copy", "--id", "TestHarnessPreparation", "--output", "json"}, 5, 1},
		{"package missing", []string{"package", "copy", "--id", "Missing", "--output=json"}, 6, 1},
		{"package delete dry run", []string{"package", "delete", "--pkg", "TestHarnessPreparation", "--force", "--dry-run", "--output=json"}, 0, 2},
		{"guidelines violations", []string{"artifact", "guidelines", "run", "--artifacts", "Order_API_TEST_HARNESS", "--wait", "--interval=1ms", "--output=json"}, 7, 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tenant := newStubTenant(t)
			stubArtifact(t, tenant, "TestHarnessPreparation", "Order_API_TEST_HARNESS", "1.0.3")
			ops := tenant.enablePackageOps()
			ops.Discover["DiscoverPackage"] = &stubDiscoverPackage{Mode: "EDIT_ALLOWED", Flows: map[string]string{"CopiedFlow": "1.0.0"}}
			ops.Discover["TestHarnessPreparation"] = &stubDiscoverPackage{Mode: "EDIT_ALLOWED"}
			tenant.enableGuidelines().Rules["Order_API_TEST_HARNESS"] = testRules()
			args := append(append([]string{}, test.args...), "--progress=json", "--run-id", "flag-run")
			result := runObservabilityCLI(t, tenant, args, true)
			if result.code != test.code || !json.Valid([]byte(result.stdout)) {
				t.Fatalf("exit=%d want=%d; stdout=%s stderr=%s", result.code, test.code, result.stdout, result.stderr)
			}
			if strings.Contains(result.stdout, "Writing the audit log") || !strings.Contains(result.stderr, "Writing the audit log") {
				t.Fatal("audit path notice is not stderr-only")
			}
			assertCLIJournal(t, result, "flag-run")
			if len(result.progress) != test.progress {
				t.Fatalf("progress records=%d want=%d: %s", len(result.progress), test.progress, result.stderr)
			}
		})
	}
}

func TestCLIBulkDownloadProgress(t *testing.T) {
	tenant := newStubTenant(t)
	for _, id := range []string{"Healthy", "Broken", "Existing"} {
		stubArtifact(t, tenant, "Package", id, "1.0.3")
	}
	tenant.FailingDownloads["Broken"] = true
	output := t.TempDir()
	if err := os.MkdirAll(filepath.Join(output, "Package", "Existing"), 0755); err != nil {
		t.Fatal(err)
	}
	result := runObservabilityCLI(t, tenant, []string{"artifact", "download", "--download-all",
		"--output", output, "--progress=json"}, true)
	if result.code != 1 || len(result.progress) != 3 {
		t.Fatalf("exit=%d progress=%v stderr=%s", result.code, result.progress, result.stderr)
	}
	summary := assertCLIJournal(t, result, "env-run")
	counts := summary["items"].(map[string]interface{})
	for _, status := range []string{"ok", "failed", "skipped"} {
		if counts[status] != float64(1) {
			t.Fatalf("wrong item summary: %v", counts)
		}
	}
	for _, row := range result.progress {
		if row["total"] != float64(3) || row["command"] != "artifact download" {
			t.Fatalf("unstable total/command: %v", row)
		}
	}
	if !strings.Contains(result.stdout, "Downloading artifacts") {
		t.Fatal("human download output was lost")
	}
}

func TestCLIProgressWithoutAuditAndDefaultOutput(t *testing.T) {
	for _, progress := range []bool{false, true} {
		t.Run(fmt.Sprint(progress), func(t *testing.T) {
			tenant := newStubTenant(t)
			tenant.Packages["Existing"] = true
			tenant.enablePackageOps().Discover["Existing"] = &stubDiscoverPackage{Mode: "EDIT_ALLOWED"}
			args := []string{"package", "copy", "--id", "Existing"}
			if progress {
				args = append(args, "--progress=json")
			}
			result := runObservabilityCLI(t, tenant, args, false)
			if result.code != 5 || json.Valid([]byte(result.stdout)) {
				t.Fatalf("default output/exit changed: %+v", result)
			}
			if (len(result.progress) == 1) != progress || strings.Contains(result.stdout+result.stderr, "Writing the audit log") {
				t.Fatalf("unexpected progress/audit output: %+v", result)
			}
		})
	}
}

func TestCLIDeployWaitProgress(t *testing.T) {
	for _, upload := range []bool{false, true} {
		for _, status := range []string{"STARTED", "ERROR", "STARTING"} {
			t.Run(fmt.Sprintf("upload=%t/%s", upload, status), func(t *testing.T) {
				tenant := newStubTenant(t)
				stubArtifact(t, tenant, "TestHarnessPreparation", "Order_API_TEST_HARNESS", "1.0.3")
				tenant.enablePackageOps()
				original := tenant.Server.Config.Handler
				tenant.Server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/IntegrationRuntimeArtifacts(") &&
						!strings.Contains(r.URL.Path, "/ErrorInformation") {
						json.NewEncoder(w).Encode(map[string]interface{}{"d": map[string]string{
							"Id": "Order_API_TEST_HARNESS", "Version": tenant.Artifacts["Order_API_TEST_HARNESS"], "Status": status,
						}})
						return
					}
					original.ServeHTTP(w, r)
				})
				args := []string{"artifact", "deploy", "--artifact", "Order_API_TEST_HARNESS"}
				if upload {
					source := writeTestArtifact(t, t.TempDir())
					args = []string{"artifact", "upload", source, "--target-env=Dev", "--deploy",
						"--skip-version-check", "--output", t.TempDir()}
				}
				args = append(args, "--wait", "--timeout=5ms", "--interval=1ms", "--progress=json")
				result := runObservabilityCLI(t, tenant, args, true)
				code := map[string]int{"STARTED": 0, "ERROR": 2, "STARTING": 3}[status]
				if result.code != code || len(result.progress) != 1 {
					t.Fatalf("exit=%d want=%d progress=%v; stdout=%s stderr=%s",
						result.code, code, result.progress, result.stdout, result.stderr)
				}
				summary := assertCLIJournal(t, result, "env-run")
				outcome := "ok"
				if code != 0 {
					outcome = "failed"
				}
				if result.progress[0]["status"] != outcome ||
					summary["items"].(map[string]interface{})[outcome] != float64(1) {
					t.Fatalf("deployment outcome not propagated: %v", result.progress)
				}
			})
		}
	}
}

func TestCLIHelpAdvertisesObservabilityFlags(t *testing.T) {
	for _, selection := range [][]string{
		{}, {"artifact", "get"}, {"artifact", "download"}, {"artifact", "deploy"},
		{"package", "copy"}, {"package", "delete"}, {"artifact", "guidelines", "run"},
	} {
		t.Run(strings.Join(selection, "-"), func(t *testing.T) {
			tenant := newStubTenant(t)
			result := runObservabilityCLI(t, tenant, append(selection, "--help"), false)
			if result.code != 0 {
				t.Fatalf("help failed: %s", result.stderr)
			}
			for _, flag := range []string{"--log-file", "--run-id", "--progress", "--log-dir"} {
				if !strings.Contains(result.stdout, flag) {
					t.Fatalf("help omits %s: %s", flag, result.stdout)
				}
			}
			if len(tenant.Calls) != 0 {
				t.Fatal("help made a tenant call")
			}
		})
	}
}
