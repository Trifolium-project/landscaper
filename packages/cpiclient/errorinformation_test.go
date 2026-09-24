package cpiclient

import (
	"strings"
	"testing"
)

//The shape a tenant returns for a flow whose Groovy step references a script
//that is not in the archive. The outermost message says only that deployment
//failed; the cause is two levels down.
const nestedErrorInformation = `{
  "message": {
    "subsystemName": "Deployment",
    "messageId": "com.sap.it.nm.deployment.failed",
    "messageText": "Deployment of the artifact failed",
    "parameter": ["Order_API_TEST_HARNESS"]
  },
  "childInstances": [
    {
      "message": {
        "subsystemName": "Runtime",
        "messageId": "com.sap.it.rt.bundle.resolve.failed",
        "messageText": "Bundle could not be resolved",
        "parameter": []
      },
      "childInstances": [
        {
          "message": {
            "subsystemName": "Script",
            "messageId": "com.sap.it.script.missing",
            "messageText": "Script resource not found",
            "parameter": ["missing_script.groovy", "src/main/resources/script"]
          },
          "childInstances": []
        }
      ]
    }
  ]
}`

const flatErrorInformation = `{
  "message": {
    "subsystemName": "Deployment",
    "messageId": "com.sap.it.nm.deployment.failed",
    "messageText": "Deployment of the artifact failed",
    "parameter": []
  },
  "childInstances": []
}`

func TestParseRuntimeErrorInformationNested(t *testing.T) {
	information, err := ParseRuntimeErrorInformation([]byte(nestedErrorInformation))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if information == nil {
		t.Fatal("expected error information")
	}

	if information.Message.SubsystemName != "Deployment" {
		t.Errorf("SubsystemName = %q, want %q", information.Message.SubsystemName, "Deployment")
	}
	if information.Message.MessageId != "com.sap.it.nm.deployment.failed" {
		t.Errorf("MessageId = %q", information.Message.MessageId)
	}
	if len(information.Message.Parameters) != 1 || information.Message.Parameters[0] != "Order_API_TEST_HARNESS" {
		t.Errorf("Parameters = %v, want [Order_API_TEST_HARNESS]", information.Message.Parameters)
	}

	//The tree has to survive two levels down
	if len(information.ChildInstances) != 1 {
		t.Fatalf("expected 1 child, got %d", len(information.ChildInstances))
	}
	child := information.ChildInstances[0]
	if len(child.ChildInstances) != 1 {
		t.Fatalf("expected 1 grandchild, got %d", len(child.ChildInstances))
	}
	grandchild := child.ChildInstances[0]
	if grandchild.Message.MessageText != "Script resource not found" {
		t.Errorf("grandchild text = %q", grandchild.Message.MessageText)
	}

	//The flattened form is what a human and a log line actually read. The real
	//cause is the deepest message, so it must not be cut off.
	if !strings.Contains(information.Text, "Deployment of the artifact failed") {
		t.Errorf("Text is missing the outermost message:\n%s", information.Text)
	}
	if !strings.Contains(information.Text, "Bundle could not be resolved") {
		t.Errorf("Text is missing the intermediate message:\n%s", information.Text)
	}
	if !strings.Contains(information.Text, "Script resource not found") {
		t.Errorf("Text is missing the root cause:\n%s", information.Text)
	}
	//The offending resource is carried in the parameters, not in the text
	if !strings.Contains(information.Text, "missing_script.groovy") {
		t.Errorf("Text is missing the parameter naming the cause:\n%s", information.Text)
	}

	if lines := strings.Split(information.Text, "\n"); len(lines) != 3 {
		t.Errorf("expected 3 lines, got %d:\n%s", len(lines), information.Text)
	}
}

func TestParseRuntimeErrorInformationFlat(t *testing.T) {
	information, err := ParseRuntimeErrorInformation([]byte(flatErrorInformation))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if information == nil {
		t.Fatal("expected error information")
	}
	if information.Text != "Deployment of the artifact failed" {
		t.Errorf("Text = %q", information.Text)
	}
	if len(information.ChildInstances) != 0 {
		t.Errorf("expected no children, got %d", len(information.ChildInstances))
	}
}

//A healthy deployment has no error information, which must not look like a failure
func TestParseRuntimeErrorInformationEmpty(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"empty body, as a 204 arrives", ""},
		{"whitespace only", "   \n"},
		{"json null", "null"},
		{"empty object", "{}"},
		{"empty message", `{"message":{"subsystemName":"","messageId":"","messageText":"","parameter":[]},"childInstances":[]}`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			information, err := ParseRuntimeErrorInformation([]byte(test.body))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if information != nil {
				t.Errorf("expected no error information, got %+v", information)
			}
		})
	}
}

//Some tenants answer with a bare string rather than the documented object
func TestParseRuntimeErrorInformationPlainText(t *testing.T) {
	information, err := ParseRuntimeErrorInformation([]byte("Deployment failed: bundle not resolved"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if information == nil {
		t.Fatal("expected error information")
	}
	if information.Text != "Deployment failed: bundle not resolved" {
		t.Errorf("Text = %q", information.Text)
	}
}

//A number or a null in the parameter array must not lose the rest of the message
func TestParseRuntimeErrorInformationOddParameters(t *testing.T) {
	body := `{"message":{"messageText":"Failed","parameter":["a",7,null,true]},"childInstances":[]}`

	information, err := ParseRuntimeErrorInformation([]byte(body))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if information == nil {
		t.Fatal("expected error information")
	}

	joined := strings.Join(information.Message.Parameters, ",")
	if joined != "a,7,true" {
		t.Errorf("Parameters = %q, want %q", joined, "a,7,true")
	}
	if !strings.Contains(information.Text, "Failed [a, 7, true]") {
		t.Errorf("Text = %q", information.Text)
	}
}

//A message with no text still has to say something, or the operator is told
//only that the deployment failed
func TestParseRuntimeErrorInformationFallsBackToMessageId(t *testing.T) {
	body := `{"message":{"messageId":"com.sap.it.deploy.failed","messageText":"","parameter":[]},"childInstances":[]}`

	information, err := ParseRuntimeErrorInformation([]byte(body))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if information == nil {
		t.Fatal("expected error information")
	}
	if information.Text != "com.sap.it.deploy.failed" {
		t.Errorf("Text = %q, want the message id", information.Text)
	}
}

//Recorded verbatim from a tenant after deploying a flow whose Groovy step
//references a script that is not in the archive. Note that "parameter" sits
//BESIDE "message", not inside it, that messageText is empty, and that SAP
//misspells subsystemPartName. The diagnostic an operator needs is in that
//top level parameter array, so a parser that only reads message.parameter
//reports "GenerationFailed" and nothing else.
const realTenantErrorInformation = `{"message":{"subsystemName":"CONTENT","subsytemPartName":"CONTENT_DEPLOY","messageId":"GenerationFailed","messageText":""},"parameter":["The generation and build of the artifact were unsuccessful. Please address the issues outlined below and redeploy the artifact.\nGeneration and build failed for TEST_DEPLOY_STATUS_BROKEN as validation of resource is failed\nScript file 'script1.groovy' not found"]}`

func TestParseRuntimeErrorInformationRealTenantPayload(t *testing.T) {
	information, err := ParseRuntimeErrorInformation([]byte(realTenantErrorInformation))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if information == nil {
		t.Fatal("expected error information")
	}

	if information.Message.MessageId != "GenerationFailed" {
		t.Errorf("MessageId = %q", information.Message.MessageId)
	}
	if information.Message.SubsystemName != "CONTENT" {
		t.Errorf("SubsystemName = %q", information.Message.SubsystemName)
	}
	//Read through SAP's misspelling
	if information.Message.SubsystemPartName != "CONTENT_DEPLOY" {
		t.Errorf("SubsystemPartName = %q, want CONTENT_DEPLOY", information.Message.SubsystemPartName)
	}

	//The top level parameters must be kept
	if len(information.Parameters) != 1 {
		t.Fatalf("expected 1 top level parameter, got %d", len(information.Parameters))
	}

	//This is the line that makes the failure actionable. Losing it leaves the
	//caller with "GenerationFailed", which names no cause.
	if !strings.Contains(information.Text, "Script file 'script1.groovy' not found") {
		t.Errorf("the actual cause is missing from the flattened text:\n%s", information.Text)
	}
	if !strings.Contains(information.Text, "GenerationFailed") {
		t.Errorf("the message id is missing from the flattened text:\n%s", information.Text)
	}
	//The multi line parameter is split, so no line of the report is lost
	if lines := strings.Split(information.Text, "\n"); len(lines) != 4 {
		t.Errorf("expected 4 lines, got %d:\n%s", len(lines), information.Text)
	}
}

//The second shape a real tenant uses: a whole nested error document arrives as
//a JSON string inside a parameter, spelling the children childMessageInstances.
//Rendering it verbatim buries the cause in punctuation.
func TestParseRuntimeErrorInformationNestedInstanceMessages(t *testing.T) {
	body := `{"message":{"subsystemName":"CONTENT","messageId":"InstanceError","messageText":""},` +
		`"parameter":["{\"message\":\"EXCEPTION\",\"childMessageInstances\":[{\"message\":\"CAUSE\",` +
		`\"parameters\":[\"SimpleParserException: expected symbol functionEnd but was eol\"]}],` +
		`\"parameters\":[\"SimpleIllegalSyntaxException: expected symbol functionEnd but was eol at location 43\"]}"]}`

	information, err := ParseRuntimeErrorInformation([]byte(body))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if information == nil {
		t.Fatal("expected error information")
	}

	if !strings.Contains(information.Text, "expected symbol functionEnd but was eol") {
		t.Errorf("the cause is missing:\n%s", information.Text)
	}
	//The nested document must be flattened, not printed as JSON
	if strings.Contains(information.Text, "childMessageInstances") {
		t.Errorf("the nested document was not flattened:\n%s", information.Text)
	}
	//The same cause repeats at every level and must appear once
	if count := strings.Count(information.Text, "expected symbol functionEnd but was eol at location 43"); count != 1 {
		t.Errorf("the cause appears %d times, want 1:\n%s", count, information.Text)
	}
}

//The tenant truncates its own payload, so the nested document cannot be
//parsed. The escaped newlines must still be turned into lines.
func TestParseRuntimeErrorInformationTruncatedNestedDocument(t *testing.T) {
	body := `{"message":{"messageId":"InstanceError","messageText":""},` +
		`"parameter":["{\"message\":\"EXCEPTION\",\"childMessageInstances\":[{\"parameters\":` +
		`[\"expected symbol functionEnd but was eol at location 43\\nCamelMessageId}_${date:now\\n *\\n\"]}], ... [truncated]"]}`

	information, err := ParseRuntimeErrorInformation([]byte(body))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if information == nil {
		t.Fatal("expected error information")
	}

	if !strings.Contains(information.Text, "expected symbol functionEnd but was eol") {
		t.Errorf("the cause is missing:\n%s", information.Text)
	}
	//It has to be broken into lines rather than left as one blob
	if len(strings.Split(information.Text, "\n")) < 3 {
		t.Errorf("the truncated document was not split into lines:\n%s", information.Text)
	}
	//The caret line of a syntax error carries nothing on its own
	for _, line := range strings.Split(information.Text, "\n") {
		if strings.Trim(line, "*^ ") == "" && line != "" {
			t.Errorf("a caret only line survived: %q", line)
		}
	}
}
