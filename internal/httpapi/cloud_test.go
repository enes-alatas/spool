package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// An address-check value answers once, within its minute, and nothing
// else answers at all: the route is no oracle for a scanner (ADR-0056).
func TestCloudCheckValueAnswersOnce(t *testing.T) {
	var checks cloudChecks
	now := time.Unix(1_700_000_000, 0)

	value := checks.issue(now)
	if len(value) < 26 {
		t.Fatalf("value %q is shorter than 128 bits of base32", value)
	}
	if checks.redeem("not-"+value, now) {
		t.Fatal("a value the hub never issued answered")
	}
	if !checks.redeem(value, now.Add(cloudCheckTTL-time.Second)) {
		t.Fatal("an issued value did not answer within its minute")
	}
	if checks.redeem(value, now) {
		t.Fatal("a value answered twice")
	}

	late := checks.issue(now)
	if checks.redeem(late, now.Add(cloudCheckTTL)) {
		t.Fatal("a value answered after its minute")
	}
}

// The set of outstanding values never grows past its cap; the oldest gives
// way, and the newest still answers.
func TestCloudCheckValuesAreCapped(t *testing.T) {
	var checks cloudChecks
	now := time.Unix(1_700_000_000, 0)

	first := checks.issue(now)
	var last string
	for i := 1; i <= cloudCheckCap; i++ {
		last = checks.issue(now.Add(time.Duration(i) * time.Millisecond))
	}
	if got := len(checks.issued); got != cloudCheckCap {
		t.Fatalf("%d values outstanding, want the cap %d", got, cloudCheckCap)
	}
	if checks.redeem(first, now) {
		t.Fatal("the oldest value survived past the cap")
	}
	if !checks.redeem(last, now) {
		t.Fatal("the newest value did not answer")
	}
}

// The cloud listener serves /mcp and the check, and 404 for every other
// path and method: the brokered servers and the API are not on it.
func TestCloudHandlerServesNothingElse(t *testing.T) {
	server := &Server{}
	handler := server.CloudHandler(noRedaction)
	status := func(method, path string) int {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(method, path, nil))
		return recorder.Code
	}

	value := server.IssueCloudCheck()
	if got := status(http.MethodPost, "/mcp/check/"+value); got != http.StatusNotFound {
		t.Fatalf("POST to the check: %d, want 404", got)
	}
	if got := status(http.MethodGet, "/mcp/check/"+value); got != http.StatusNoContent {
		t.Fatalf("an issued value: %d, want 204", got)
	}
	if got := status(http.MethodGet, "/mcp/check/"+value); got != http.StatusNotFound {
		t.Fatalf("the same value again: %d, want 404", got)
	}
	for _, path := range []string{
		"/", "/mcp/", "/mcp/check/", "/mcp/connections/github/", "/api/loops", "/api/version", "/healthz",
	} {
		if got := status(http.MethodGet, path); got != http.StatusNotFound {
			t.Errorf("GET %s: %d, want 404", path, got)
		}
	}
}
