package cpiclient

import (
	"fmt"
	"net/http"
	"testing"
)

func TestHasStatus(t *testing.T) {
	notFound := &StatusError{Code: http.StatusNotFound, Message: "Requested entity could not be found."}

	if !HasStatus(notFound, http.StatusNotFound) {
		t.Error("expected 404")
	}
	if HasStatus(notFound, http.StatusConflict) {
		t.Error("404 is not 409")
	}
	if !HasStatus(fmt.Errorf("wrapped: %w", notFound), http.StatusNotFound) {
		t.Error("a wrapped StatusError should still match")
	}
	if HasStatus(fmt.Errorf("connection reset"), http.StatusNotFound) || HasStatus(nil, http.StatusNotFound) {
		t.Error("a plain error has no status")
	}
	if notFound.Error() != "Requested entity could not be found." {
		t.Errorf("Error() = %q", notFound.Error())
	}
}

func TestODataResults(t *testing.T) {
	results, err := odataResults([]byte(`{"d":{"results":[{"Id":"A","Version":null},"junk",{"Id":"B"}]}}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 || jsonString(results[0], "Id") != "A" || jsonString(results[0], "Version") != "" {
		t.Errorf("unexpected results %v", results)
	}

	for _, body := range []string{`{}`, `{"d":{}}`, `not json`} {
		if _, err := odataResults([]byte(body)); err == nil {
			t.Errorf("expected an error for %s", body)
		}
	}
}
