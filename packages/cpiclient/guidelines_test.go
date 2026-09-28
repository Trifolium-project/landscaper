package cpiclient

import (
	"fmt"
	"reflect"
	"testing"
)

func TestViolatedComponents(t *testing.T) {

	tests := []struct {
		name string
		raw  string
		want []ViolatedComponent
	}{
		{"empty", "", []ViolatedComponent{}},
		{"one", "{MessageFlow_6=HTTPS}", []ViolatedComponent{{"MessageFlow_6", "HTTPS"}}},
		{"several, as the tenant answers", "{CallActivity_59=Build Response, CallActivity_75=Build Delete Response}",
			[]ViolatedComponent{{"CallActivity_59", "Build Response"}, {"CallActivity_75", "Build Delete Response"}}},
		{"comma inside a name", "{CallActivity_1=Map, then route, Process_2=Main}",
			[]ViolatedComponent{{"CallActivity_1", "Map, then route"}, {"Process_2", "Main"}}},
		{"unrecognised", "something else", []ViolatedComponent{}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			guideline := &DesignGuideline{ViolatedComponents: test.raw}
			if got := guideline.Components(); !reflect.DeepEqual(got, test.want) {
				t.Errorf("Components(%q) = %v, want %v", test.raw, got, test.want)
			}
		})
	}
}

func TestParseExecutionId(t *testing.T) {

	tests := []struct {
		name    string
		body    string
		want    string
		wantErr bool
	}{
		{"text/plain, as the tenant answers", "48141d135ce840ffb9ba3d610d563249\n", "48141d135ce840ffb9ba3d610d563249", false},
		{"quoted", `"abc"`, "abc", false},
		{"OData function import", `{"d":{"ExecuteIntegrationDesigntimeArtifactsGuidelines":"abc"}}`, "abc", false},
		{"empty", "  ", "", true},
		{"unexpected JSON", `{"d":{}}`, "", true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := parseExecutionId([]byte(test.body))
			if (err != nil) != test.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, test.wantErr)
			}
			if got != test.want {
				t.Errorf("got %q, want %q", got, test.want)
			}
		})
	}
}

func TestODataError(t *testing.T) {

	tests := []struct {
		name string
		body string
		want string
	}{
		{"JSON", `{"error":{"code":"Bad Request","message":{"lang":"en","value":"SkipReason must not be empty."}}}`,
			"SkipReason must not be empty."},
		{"XML", `<?xml version='1.0' encoding='UTF-8'?><error xmlns="http://schemas.microsoft.com/ado/2007/08/dataservices/metadata"><code>Internal Server Error</code><message xml:lang="en">Unable to get Data: Request: NoSuchFlow IFlow</message></error>`,
			"Unable to get Data: Request: NoSuchFlow IFlow"},
		{"anything else is kept", "connection reset", "connection reset"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := odataError(fmt.Errorf("%s", test.body)).Error(); got != test.want {
				t.Errorf("got %q, want %q", got, test.want)
			}
		})
	}
}
