package auditlog

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

//readRecords returns every line of the log file, parsed
func readRecords(t *testing.T, path string) []map[string]interface{} {
	t.Helper()

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("unable to read the log: %v", err)
	}

	records := []map[string]interface{}{}
	for _, line := range strings.Split(strings.TrimRight(string(content), "\n"), "\n") {
		if line == "" {
			continue
		}
		record := map[string]interface{}{}
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("line is not valid JSON: %v\n%s", err, line)
		}
		records = append(records, record)
	}

	return records
}

func newTestLogger(t *testing.T) *Logger {
	t.Helper()

	logger, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	t.Cleanup(func() { logger.Close() })

	return logger
}

//A disabled logger is a nil pointer, so every method has to tolerate one.
//This is what lets the call sites drop the "if logging is on" guard.
func TestNilLoggerIsANoOp(t *testing.T) {
	var logger *Logger

	logger.RunStart("artifact list", "Dev", map[string]string{"pkg": "X"})
	logger.Item(map[string]interface{}{"artifact": "X"})
	logger.Message("info", "hello")
	logger.RunEnd("ok", "")

	if path := logger.Path(); path != "" {
		t.Errorf("Path() = %q, want empty", path)
	}
	if err := logger.Close(); err != nil {
		t.Errorf("Close() = %v, want nil", err)
	}

	request, _ := http.NewRequest(http.MethodGet, "https://example.invalid/", nil)
	call := logger.StartHTTP(request)
	call.SetResponse(200, http.Header{}, []byte("body"))
	call.SetError(nil)
	call.Flush()
}

func TestEveryRecordIsOneJSONLine(t *testing.T) {
	logger := newTestLogger(t)

	logger.RunStart("artifact download", "QA", map[string]string{"packages": "A"})
	logger.Item(map[string]interface{}{"artifact": "A", "status": "downloaded"})
	//A message containing newlines must not break the stream
	logger.Message("info", "first line\nsecond line")
	logger.RunEnd("ok", "")

	records := readRecords(t, logger.Path())
	if len(records) != 4 {
		t.Fatalf("expected 4 records, got %d", len(records))
	}

	types := []string{}
	for _, record := range records {
		types = append(types, record["type"].(string))
	}
	if got := strings.Join(types, ","); got != "run,item,log,run" {
		t.Errorf("types = %q, want %q", got, "run,item,log,run")
	}
}

func TestRunEndIsWrittenOnlyOnce(t *testing.T) {
	logger := newTestLogger(t)

	logger.RunEnd("failed", "first")
	logger.RunEnd("ok", "second")

	records := readRecords(t, logger.Path())
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}
	if records[0]["status"] != "failed" {
		t.Errorf("status = %v, want %q", records[0]["status"], "failed")
	}
}

//The whole point of the feature: a credential must never reach the file
func TestRedactHeaders(t *testing.T) {
	headers := http.Header{}
	headers.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("user:hunter2")))
	headers.Set("X-CSRF-Token", "csrf-secret-value")
	headers.Set("Cookie", "__Host-csrf-client-id=cookie-secret")
	headers.Set("Set-Cookie", "__Host-csrf-client-id=cookie-secret")
	headers.Set("Content-Type", "application/json")

	redactedHeaders := RedactHeaders(headers)

	for _, key := range []string{"Authorization", "X-Csrf-Token", "Cookie", "Set-Cookie"} {
		value, present := redactedHeaders[key]
		if !present {
			t.Errorf("header %s was dropped, it must be present so the record shows auth was sent", key)
			continue
		}
		if value != redacted {
			t.Errorf("header %s = %q, want %q", key, value, redacted)
		}
	}

	if redactedHeaders["Content-Type"] != "application/json" {
		t.Errorf("Content-Type = %q, want it passed through", redactedHeaders["Content-Type"])
	}

	for _, secret := range []string{"hunter2", "csrf-secret-value", "cookie-secret"} {
		for _, value := range redactedHeaders {
			if strings.Contains(value, secret) {
				t.Errorf("secret %q survived redaction", secret)
			}
		}
	}
}

func TestRedactFlags(t *testing.T) {
	flags := RedactFlags(map[string]string{
		"pkg":      "TestHarnessPreparation",
		"password": "hunter2",
		"Token":    "abc",
	})

	if flags["pkg"] != "TestHarnessPreparation" {
		t.Errorf("pkg = %q, want it passed through", flags["pkg"])
	}
	if flags["password"] != redacted {
		t.Errorf("password = %q, want %q", flags["password"], redacted)
	}
	if flags["Token"] != redacted {
		t.Errorf("Token = %q, want %q (matching is case insensitive)", flags["Token"], redacted)
	}
}

//An uploaded artifact is megabytes of base64. It is the payload, not
//information about the operation, and must never be written.
func TestCaptureRequestBodyOmitsArtifactContent(t *testing.T) {
	//The field order matters and must match cpiclient: a struct marshals in
	//declaration order, so ArtifactContent is last, after four short strings.
	//A map would sort the keys and put it first, which is not what is sent.
	payload := struct {
		Id              string
		PackageId       string
		Name            string
		Description     string
		ArtifactContent string
	}{
		Id:              "Order_API_TEST_HARNESS",
		PackageId:       "TestHarnessPreparation",
		Name:            "Order API",
		Description:     "",
		ArtifactContent: strings.Repeat("QUJD", 500000), //~2 MB of base64
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	request, err := http.NewRequest(http.MethodPost, "https://example.invalid/", bytes.NewBuffer(encoded))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	body := CaptureRequestBody(request)
	if body == nil {
		t.Fatal("expected a body record")
	}

	if !strings.Contains(body.Text, `"ArtifactContent":"`+redacted+`"`) {
		t.Errorf("ArtifactContent was not omitted: %.200s", body.Text)
	}
	if !strings.Contains(body.Text, "Order_API_TEST_HARNESS") {
		t.Error("the useful fields of the request were lost")
	}
	if len(body.Text) > requestScanBytes {
		t.Errorf("recorded %d bytes, want the multi megabyte tail left unread", len(body.Text))
	}
	if body.Bytes < int64(len(encoded)) {
		t.Errorf("Bytes = %d, want the real size %d recorded", body.Bytes, len(encoded))
	}
}

//Even if the payload were to arrive before the other fields, the megabytes
//must still not be written. The trailing fields are lost in that case, which
//is an acceptable trade for never recording an artifact.
func TestCaptureRequestBodyOmitsArtifactContentWhateverItsPosition(t *testing.T) {
	encoded := `{"ArtifactContent":"` + strings.Repeat("QUJD", 500000) + `","Id":"Order_API_TEST_HARNESS"}`

	request, err := http.NewRequest(http.MethodPost, "https://example.invalid/", bytes.NewBufferString(encoded))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	body := CaptureRequestBody(request)
	if strings.Contains(body.Text, "QUJDQUJD") {
		t.Error("the artifact payload was written to the log")
	}
	if len(body.Text) > requestScanBytes {
		t.Errorf("recorded %d bytes, want the payload left unread", len(body.Text))
	}
}

func TestCaptureRequestBodyKeepsOrdinaryJSON(t *testing.T) {
	request, err := http.NewRequest(http.MethodPost, "https://example.invalid/",
		bytes.NewBufferString(`{"Id":"CRMPackage","Name":"CRM"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	body := CaptureRequestBody(request)
	if body == nil || body.Text != `{"Id":"CRMPackage","Name":"CRM"}` {
		t.Errorf("body = %+v, want the request recorded in full", body)
	}
}

func TestIsTextual(t *testing.T) {
	//A zip, as the $value download returns
	archive := append([]byte("PK\x03\x04"), make([]byte, 64)...)

	tests := []struct {
		name        string
		contentType string
		content     []byte
		want        bool
	}{
		{"json", "application/json", []byte(`{"d":{}}`), true},
		{"json with charset", "application/json; charset=utf-8", []byte(`{}`), true},
		{"atom xml", "application/atom+xml", []byte("<entry/>"), true},
		{"plain text", "text/plain", []byte("hello"), true},
		{"declared zip", "application/zip", archive, false},
		{"octet stream", "application/octet-stream", archive, false},
		{"undeclared zip is sniffed", "", archive, false},
		{"undeclared json is sniffed", "", []byte(`{"d":{}}`), true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := isTextual(test.contentType, test.content); got != test.want {
				t.Errorf("isTextual(%q) = %t, want %t", test.contentType, got, test.want)
			}
		})
	}
}

//The full text of a tenant answer is recorded, which is what makes an OData
//error message readable afterwards
func TestResponseBodyRecordsTextInFullAndOmitsBinary(t *testing.T) {
	odataError := `{"error":{"code":"Bad Request","message":{"value":"due to change in the Bundle-symbolicName."}}}`

	body := responseBody("application/json", []byte(odataError))
	if body.Text != odataError {
		t.Errorf("text = %q, want the whole error body", body.Text)
	}
	if body.Binary {
		t.Error("a JSON error body must not be marked binary")
	}

	archive := append([]byte("PK\x03\x04"), make([]byte, 1024)...)
	body = responseBody("application/zip", archive)
	if !body.Binary || body.Text != "" {
		t.Errorf("body = %+v, want the archive omitted", body)
	}
	if body.Bytes != int64(len(archive)) {
		t.Errorf("Bytes = %d, want %d", body.Bytes, len(archive))
	}
}

//The file is opened append only, because the name has second granularity and
//two runs in the same second must not truncate each other
func TestSecondLoggerDoesNotTruncateTheFirst(t *testing.T) {
	directory := t.TempDir()

	first, err := New(directory)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	first.Message("info", "from the first run")

	second, err := New(directory)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	second.Message("info", "from the second run")

	first.Close()
	second.Close()

	//Same second means the same name; either way nothing may be lost
	if first.Path() == second.Path() {
		records := readRecords(t, first.Path())
		if len(records) != 2 {
			t.Fatalf("expected both runs in %s, got %d records", first.Path(), len(records))
		}
		return
	}

	for _, path := range []string{first.Path(), second.Path()} {
		if len(readRecords(t, path)) != 1 {
			t.Errorf("expected 1 record in %s", path)
		}
	}
}

func TestNewRejectsAnUnusableDirectory(t *testing.T) {
	//A file where the directory should be
	blocker := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blocker, []byte("x"), 0600); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, err := New(blocker); err == nil {
		t.Fatal("expected an error when the log directory cannot be created")
	}

	if _, err := New(""); err == nil {
		t.Fatal("expected an error for an empty directory")
	}
}

//The log file holds complete tenant responses, so it must not be world readable
func TestLogFileIsNotWorldReadable(t *testing.T) {
	logger := newTestLogger(t)

	info, err := os.Stat(logger.Path())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if permissions := info.Mode().Perm(); permissions != 0600 {
		t.Errorf("mode = %v, want %v", permissions, os.FileMode(0600))
	}
}

func TestHTTPCallRecordsStatusAndRedactsHeaders(t *testing.T) {
	logger := newTestLogger(t)

	request, err := http.NewRequest(http.MethodGet, "https://tenant.invalid/api/v1/IntegrationPackages", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	request.SetBasicAuth("user", "hunter2")

	responseHeaders := http.Header{}
	responseHeaders.Set("Content-Type", "application/json")
	responseHeaders.Set("Set-Cookie", "__Host-csrf-client-id=secret")

	call := logger.StartHTTP(request)
	call.SetResponse(200, responseHeaders, []byte(`{"d":{"results":[]}}`))
	call.Flush()

	records := readRecords(t, logger.Path())
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}

	record := records[0]
	if record["type"] != "http" {
		t.Errorf("type = %v, want http", record["type"])
	}
	//The status code is lost by the client's error, so the record is the only
	//place it survives
	if status, ok := record["status"].(float64); !ok || int(status) != 200 {
		t.Errorf("status = %v, want 200", record["status"])
	}

	content, err := os.ReadFile(logger.Path())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, secret := range []string{"hunter2", base64.StdEncoding.EncodeToString([]byte("user:hunter2")), "__Host-csrf-client-id=secret"} {
		if bytes.Contains(content, []byte(secret)) {
			t.Errorf("secret %q leaked into the audit log", secret)
		}
	}
}
