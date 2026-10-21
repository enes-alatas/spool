package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/enes-alatas/spool/internal/store"
)

// fixedEnvironments is an account's list of environments, or the error
// listing it fails with.
type fixedEnvironments struct {
	listed []CloudEnvironment
	err    error
}

func (environments fixedEnvironments) Environments(context.Context) ([]CloudEnvironment, error) {
	return environments.listed, environments.err
}

// cloudCall runs one Settings → Cloud request against server and decodes
// the answer into out, unless it is nil. It returns the status.
func cloudCall(t *testing.T, server *Server, method, path, body string, out any) int {
	t.Helper()
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	handlers := map[string]http.HandlerFunc{
		"GET /api/settings/cloud":                      server.handleGetCloudSettings,
		"GET /api/settings/cloud/environments":         server.handleListCloudEnvironments,
		"PUT /api/settings/cloud/environment":          server.handleChooseCloudEnvironment,
		"DELETE /api/settings/cloud/environment":       server.handleClearCloudEnvironment,
		"POST /api/settings/cloud/environment/confirm": server.handleConfirmCloudAllowlist,
		"POST /api/settings/egress/hosts":              server.handleAddEgressHost,
	}
	handlers[method+" "+path](recorder, req)
	if out != nil && recorder.Code < 300 {
		if err := json.Unmarshal(recorder.Body.Bytes(), out); err != nil {
			t.Fatalf("%s %s answered %s: %v", method, path, recorder.Body, err)
		}
	}
	return recorder.Code
}

// The list keeps only IDs of an environment's shape, with names capped
// and without backticks, and a list that fails says why, so the page can
// offer the paste instead.
func TestTheEnvironmentListOffersOnlyIDsTheHubTakes(t *testing.T) {
	server := cloudCheckServer(t, "https://hub.example.test")
	server.CloudEnvironments = fixedEnvironments{listed: []CloudEnvironment{
		{ID: "env_01Spool", Name: "`spool`"}, {ID: `env_01"}`, Name: "odd"},
		{ID: "env_01Long", Name: strings.Repeat("é", 300)},
	}}
	var answer struct{ Environments []CloudEnvironment }
	if code := cloudCall(t, server, "GET", "/api/settings/cloud/environments", "", &answer); code != 200 {
		t.Fatalf("list: %d", code)
	}
	if len(answer.Environments) != 2 || answer.Environments[0].ID != "env_01Spool" || answer.Environments[1].ID != "env_01Long" {
		t.Fatalf("listed %+v, want env_01Spool and env_01Long", answer.Environments)
	}
	if answer.Environments[0].Name != "'spool'" || answer.Environments[1].Name != strings.Repeat("é", maxCloudEnvironmentName) {
		t.Errorf("names %q, %q: want the backticks out and the long one capped", answer.Environments[0].Name, answer.Environments[1].Name)
	}
	server.CloudEnvironments = fixedEnvironments{err: errors.New("listing environments: answered 401")}
	if code := cloudCall(t, server, "GET", "/api/settings/cloud/environments", "", nil); code != http.StatusBadGateway {
		t.Errorf("a failed list: %d, want 502", code)
	}
}

// A chosen ID is stored with the name the account's list gives it, never
// the page's. One without an environment ID's shape is refused, and so is
// one a list that was read doesn't show: a bare paste is taken only when
// the list fails.
func TestChoosingAnEnvironmentStoresItsListedName(t *testing.T) {
	server := cloudCheckServer(t, "https://hub.example.test")
	server.CloudEnvironments = fixedEnvironments{listed: []CloudEnvironment{{ID: "env_01Spool", Name: "spool"}}}
	var view cloudSettingsView
	if code := cloudCall(t, server, "PUT", "/api/settings/cloud/environment", `{"id":"env_01Spool","name":"not this"}`, &view); code != 200 {
		t.Fatalf("choose: %d", code)
	}
	if view.Environment.ID != "env_01Spool" || view.Environment.Name != "spool" || view.Environment.Confirmed {
		t.Errorf("chosen: %+v, want env_01Spool named spool, unconfirmed", view.Environment)
	}
	if code := cloudCall(t, server, "PUT", "/api/settings/cloud/environment", `{"id":"env_01Laptop"}`, &view); code != 400 {
		t.Errorf("an ID the list doesn't show: %d, want 400", code)
	}
	cloudCall(t, server, "GET", "/api/settings/cloud", "", &view)
	if view.Environment.ID != "env_01Spool" {
		t.Errorf("after the refusal: %+v, want env_01Spool kept", view.Environment)
	}
	server.CloudEnvironments = fixedEnvironments{err: errors.New("listing environments: answered 503")}
	if code := cloudCall(t, server, "PUT", "/api/settings/cloud/environment", `{"id":"env_01Spool"}`, &view); code != 200 ||
		view.Environment.Name != "spool" {
		t.Errorf("the same ID again while the list fails: %d %+v, want its name kept", code, view.Environment)
	}
	if code := cloudCall(t, server, "PUT", "/api/settings/cloud/environment", `{"id":"env_01Pasted"}`, &view); code != 200 ||
		view.Environment.ID != "env_01Pasted" || view.Environment.Name != "" {
		t.Errorf("a paste while the list fails: %d %+v, want it stored with no name", code, view.Environment)
	}
	for _, id := range []string{"", "env_", "01Spool", `env_01","x":"y`, "env_01 Spool"} {
		if code := cloudCall(t, server, "PUT", "/api/settings/cloud/environment", `{"id":`+jsonString(id)+`}`, nil); code != 400 {
			t.Errorf("id %q: %d, want 400", id, code)
		}
	}
	if code := cloudCall(t, server, "DELETE", "/api/settings/cloud/environment", "", &view); code != 200 || view.Environment.ID != "" {
		t.Errorf("cleared: %d %+v", code, view.Environment)
	}
}

// The list to paste is built from the allowlist as it is now: a confirm
// names the text it confirmed, one of a list that moved is refused, and
// an edit to the allowlist or another environment drops the confirm
// (ADR-0059).
func TestTheAllowlistConfirmFollowsTheList(t *testing.T) {
	server := cloudCheckServer(t, "https://hub.example.test")
	var view cloudSettingsView
	if code := cloudCall(t, server, "POST", "/api/settings/cloud/environment/confirm", `{"digest":"x"}`, nil); code != 409 {
		t.Errorf("a confirm with no environment chosen: %d, want 409", code)
	}
	cloudCall(t, server, "PUT", "/api/settings/cloud/environment", `{"id":"env_01Spool"}`, &view)
	lines := strings.Split(view.Environment.Allowlist, "\n")
	if lines[0] != "hub.example.test" || !strings.Contains(view.Environment.Allowlist, "\napi.anthropic.com\n") ||
		!strings.Contains(view.Environment.Allowlist, "\n*.githubusercontent.com\ngithubusercontent.com\n") {
		t.Errorf("the list to paste:\n%s\nwant the public host first, then the built-in hosts as an environment spells them", view.Environment.Allowlist)
	}
	shown := view.Environment.Digest
	if code := cloudCall(t, server, "POST", "/api/settings/cloud/environment/confirm", `{"digest":"`+shown+`"}`, &view); code != 200 ||
		!view.Environment.Confirmed || view.Environment.ConfirmedAt == 0 {
		t.Fatalf("confirm: %d %+v", code, view.Environment)
	}
	if server.cloudAllowlistUnconfirmed(context.Background()) {
		t.Error("a confirmed list still warns on the loops")
	}

	cloudCall(t, server, "POST", "/api/settings/egress/hosts", `{"host":"tools.example.test"}`, nil)
	cloudCall(t, server, "GET", "/api/settings/cloud", "", &view)
	if view.Environment.Confirmed || !strings.HasSuffix(view.Environment.Allowlist, "\ntools.example.test") {
		t.Errorf("after an allowlist edit: %+v, want the new host listed and the confirm dropped", view.Environment)
	}
	if !server.cloudAllowlistUnconfirmed(context.Background()) {
		t.Error("an allowlist edit left the loops without the warning")
	}
	if code := cloudCall(t, server, "POST", "/api/settings/cloud/environment/confirm", `{"digest":"`+shown+`"}`, nil); code != 409 {
		t.Errorf("a confirm of the list before the edit: %d, want 409", code)
	}
	cloudCall(t, server, "POST", "/api/settings/cloud/environment/confirm", `{"digest":"`+view.Environment.Digest+`"}`, &view)
	cloudCall(t, server, "PUT", "/api/settings/cloud/environment", `{"id":"env_01Spool"}`, &view)
	if !view.Environment.Confirmed {
		t.Error("choosing the same environment again dropped the confirm")
	}
	cloudCall(t, server, "PUT", "/api/settings/cloud/environment", `{"id":"env_01Other"}`, &view)
	if view.Environment.Confirmed {
		t.Error("another environment kept the confirm")
	}
}

// An entry an environment can't carry is left off the list to paste and
// named beside it.
func TestTheAllowlistNamesWhatAnEnvironmentCantCarry(t *testing.T) {
	server := cloudCheckServer(t, "https://hub.example.test:8443")
	if err := storeEgressHosts(context.Background(), server.Store.Settings(), store.EgressHosts{Hosts: []string{"tools.example.test:8443", ".example.test"}}); err != nil {
		t.Fatal(err)
	}
	var view cloudSettingsView
	cloudCall(t, server, "GET", "/api/settings/cloud", "", &view)
	if !strings.HasPrefix(view.Environment.Allowlist, "hub.example.test\n") ||
		!strings.HasSuffix(view.Environment.Allowlist, "\n*.example.test\nexample.test") {
		t.Errorf("the list to paste:\n%s", view.Environment.Allowlist)
	}
	if len(view.Environment.NotApplied) != 1 || view.Environment.NotApplied[0] != "tools.example.test:8443" {
		t.Errorf("not applied = %q, want the entry with a port of its own", view.Environment.NotApplied)
	}
}

func jsonString(value string) string {
	raw, _ := json.Marshal(value)
	return string(raw)
}
