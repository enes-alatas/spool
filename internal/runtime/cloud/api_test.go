package cloud

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A synthetic account: never a real token.
var testAccount = Account{Token: "sk-ant-oat01-synthetic", OrgUUID: "00000000-0000-4000-8000-000000000001"}

type seen struct {
	method, path, query string
	header              http.Header
	body                map[string]any
}

// fakeAPI answers every request with status and body, and records what it
// was sent.
func fakeAPI(t *testing.T, status int, body string) (*Client, *[]seen) {
	t.Helper()
	var requests []seen
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var decoded map[string]any
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &decoded); err != nil {
				t.Errorf("%s %s sent a body that isn't JSON: %s", r.Method, r.URL.Path, raw)
			}
		}
		requests = append(requests, seen{r.Method, r.URL.Path, r.URL.RawQuery, r.Header.Clone(), decoded})
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(server.Close)
	client := &Client{
		BaseURL: server.URL,
		Account: func(context.Context) (Account, error) { return testAccount, nil },
	}
	return client, &requests
}

func TestCreateSessionSendsWhatTheAPICheckFound(t *testing.T) {
	client, requests := fakeAPI(t, 200,
		`{"id":"session_01synthetic","session_status":"pending","session_url":"https://claude.ai/code/x","environment_id":"env_01synthetic"}`)

	session, err := client.CreateSession(context.Background(), SessionSpec{
		EnvironmentID:      "env_01synthetic",
		Title:              "spool: cirrus",
		Model:              "claude-haiku-5-5",
		AppendSystemPrompt: "You are cirrus.",
		Env:                map[string]string{"SPOOL_HUB_TOKEN": "synthetic-hub-token"},
		FirstMessage:       "wake up",
		MessageID:          "11111111-1111-4111-8111-111111111111",
	})
	if err != nil {
		t.Fatal(err)
	}
	if session.ID != "session_01synthetic" || session.Status != StatusPending || session.URL == "" {
		t.Fatalf("session read back as %+v", session)
	}

	request := (*requests)[0]
	if request.method != http.MethodPost || request.path != "/v1/sessions" {
		t.Fatalf("created with %s %s", request.method, request.path)
	}
	for header, want := range map[string]string{
		"Authorization":       "Bearer " + testAccount.Token,
		"Anthropic-Beta":      sessionsBeta,
		"X-Organization-Uuid": testAccount.OrgUUID,
		"Anthropic-Version":   apiVersion,
	} {
		if got := request.header.Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}

	sessionContext := request.body["session_context"].(map[string]any)
	if servers := sessionContext["mcp_config"].(map[string]any)["mcpServers"].(map[string]any); len(servers) != 0 {
		t.Errorf("mcp_config names servers %v; it must be explicit and empty, which keeps the session tools out", servers)
	}
	if sessionContext["append_system_prompt"] != "You are cirrus." || sessionContext["model"] != "claude-haiku-5-5" {
		t.Errorf("session_context = %v", sessionContext)
	}
	if env := sessionContext["environment_variables"].(map[string]any); env["SPOOL_HUB_TOKEN"] != "synthetic-hub-token" {
		t.Errorf("the hub token is not a per-session variable: %v", env)
	}

	events := request.body["events"].([]any)
	if len(events) != 2 {
		t.Fatalf("%d events, want the permission mode then the message", len(events))
	}
	mode := events[0].(map[string]any)["data"].(map[string]any)["request"].(map[string]any)
	if mode["subtype"] != "set_permission_mode" || mode["mode"] != "bypassPermissions" {
		t.Errorf("first event is %v, want bypassPermissions", mode)
	}
	message := events[1].(map[string]any)["data"].(map[string]any)
	if message["type"] != "user" || message["message"].(map[string]any)["content"] != "wake up" ||
		message["uuid"] != "11111111-1111-4111-8111-111111111111" {
		t.Errorf("second event is %v, want the first message", message)
	}
}

func TestSessionEventsAndControl(t *testing.T) {
	client, requests := fakeAPI(t, 200,
		`{"data":[{"type":"system","subtype":"init"},{"type":"result","total_cost_usd":0.01}],"first_id":"a","last_id":"b","has_more":true}`)
	ctx := context.Background()

	page, err := client.Events(ctx, "session_01synthetic", "evt-9", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Events) != 2 || page.LastID != "b" || !page.HasMore {
		t.Fatalf("page read back as %+v", page)
	}
	if got := (*requests)[0]; got.path != "/v1/sessions/session_01synthetic/events" || got.query != "after_id=evt-9" {
		t.Errorf("events read from %s?%s", got.path, got.query)
	}

	if err := client.SendMessage(ctx, "session_01synthetic", "hello", "22222222-2222-4222-8222-222222222222"); err != nil {
		t.Fatal(err)
	}
	sent := (*requests)[1].body["events"].([]any)[0].(map[string]any)
	if sent["type"] != "user" || sent["message"].(map[string]any)["content"] != "hello" {
		t.Errorf("message sent as %v", sent)
	}

	if err := client.Interrupt(ctx, "session_01synthetic"); err != nil {
		t.Fatal(err)
	}
	interrupt := (*requests)[2].body["events"].([]any)[0].(map[string]any)
	if interrupt["type"] != "control_request" || interrupt["request"].(map[string]any)["subtype"] != "interrupt" {
		t.Errorf("interrupt sent as %v", interrupt)
	}
	if err := client.Interrupt(ctx, "session_01synthetic"); err != nil {
		t.Fatal(err)
	}
	again := (*requests)[3].body["events"].([]any)[0].(map[string]any)
	if again["request_id"] == interrupt["request_id"] {
		t.Errorf("two interrupts share request_id %v: the second could be taken as already answered", again["request_id"])
	}

	if err := client.ArchiveSession(ctx, "session_01synthetic"); err != nil {
		t.Fatal(err)
	}
	if got := (*requests)[4]; got.method != http.MethodPost || got.path != "/v1/sessions/session_01synthetic/archive" {
		t.Errorf("archived with %s %s", got.method, got.path)
	}
}

func TestEnvironmentCalls(t *testing.T) {
	client, requests := fakeAPI(t, 200, `{"environment_id":"env_01synthetic"}`)
	ctx := context.Background()
	spec := EnvironmentSpec{Name: "spool-cirrus", InitScript: "#!/bin/bash\nexit 0\n", AllowedHosts: []string{"hub.example.com"}}

	id, err := client.CreateEnvironment(ctx, spec)
	if err != nil || id != "env_01synthetic" {
		t.Fatalf("CreateEnvironment = %q, %v", id, err)
	}
	created := (*requests)[0]
	if created.path != "/v1/environment_providers/cloud/create" || created.body["kind"] != "anthropic_cloud" {
		t.Errorf("created with %s %v", created.path, created.body)
	}
	if languages, ok := created.body["config"].(map[string]any)["languages"].([]any); !ok || len(languages) != 0 {
		t.Errorf("languages = %v: the API refuses a create without the field", created.body["config"].(map[string]any)["languages"])
	}
	network := created.body["config"].(map[string]any)["network_config"].(map[string]any)
	if network["allow_default_hosts"] != false {
		t.Errorf("allow_default_hosts = %v: a cloud environment never gets Anthropic's Trusted defaults", network["allow_default_hosts"])
	}
	if network["allow_mcp_servers"] != false {
		t.Errorf("allow_mcp_servers = %v: a cloud environment reaches only its allowlist", network["allow_mcp_servers"])
	}
	if hosts := network["allowed_hosts"].([]any); len(hosts) != 1 || hosts[0] != "hub.example.com" {
		t.Errorf("allowed_hosts = %v", hosts)
	}

	if err := client.UpdateEnvironment(ctx, "env_01synthetic", spec); err != nil {
		t.Fatal(err)
	}
	if updated := (*requests)[1]; updated.path != "/v1/environment_providers/env_01synthetic" || updated.body["kind"] != nil {
		t.Errorf("updated with %s %v; the replace takes no kind", updated.path, updated.body)
	}

	if err := client.ArchiveEnvironment(ctx, "env_01synthetic"); err != nil {
		t.Fatal(err)
	}
	archived := (*requests)[2]
	if archived.path != "/v1/environments/env_01synthetic/archive" || archived.query != "beta=true" ||
		archived.header.Get("Anthropic-Beta") != environmentsBeta {
		t.Errorf("archived with %s?%s under %q", archived.path, archived.query, archived.header.Get("Anthropic-Beta"))
	}
}

// Anything the client didn't expect fails loudly, naming the endpoint and
// quoting what came back (ADR-0056).
func TestUnexpectedAnswersFailLoudly(t *testing.T) {
	ctx := context.Background()

	client, _ := fakeAPI(t, 400, `{"error":{"type":"invalid_request_error","message":"session_inactive"}}`)
	err := client.SendMessage(ctx, "session_01synthetic", "hello", "x")
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != 400 || !apiErr.Inactive() {
		t.Fatalf("a refused send = %v, want an APIError that reads as inactive", err)
	}
	if !strings.Contains(err.Error(), "POST /v1/sessions/session_01synthetic/events") {
		t.Errorf("the error does not name the endpoint: %v", err)
	}

	// The code is read from the decoded error object, not searched for in
	// the quoted start of the body: past the cut it is still found, and a
	// message that merely mentions it is not taken for it.
	long := `{"detail":"` + strings.Repeat("x", 2*errorSnippet) + `","error":{"type":"session_inactive","message":"archived"}}`
	client, _ = fakeAPI(t, 409, long)
	if err := client.SendMessage(ctx, "session_01synthetic", "hello", "x"); !errors.As(err, &apiErr) || !apiErr.Inactive() {
		t.Errorf("a refusal whose code sits past the quoted start = %v, want it read as inactive", err)
	}
	client, _ = fakeAPI(t, 400, `{"error":{"type":"invalid_request_error","message":"bad text: session_inactive"}}`)
	if err := client.SendMessage(ctx, "session_01synthetic", "hello", "x"); !errors.As(err, &apiErr) || apiErr.Inactive() {
		t.Errorf("a refusal that only mentions session_inactive = %v, want it not read as inactive", err)
	}

	client, _ = fakeAPI(t, 200, `{"data": [`)
	if _, err := client.Events(ctx, "session_01synthetic", "", 0); !errors.As(err, &apiErr) || apiErr.Status != 0 {
		t.Errorf("an unreadable page = %v, want an APIError", err)
	}

	client, _ = fakeAPI(t, 200, `{"data":["`+strings.Repeat("a", maxResponse)+`"]}`)
	if _, err := client.Events(ctx, "session_01synthetic", "", 0); !errors.As(err, &apiErr) || !strings.Contains(apiErr.Reason, "over") {
		t.Errorf("an oversized page = %v, want it refused", err)
	}
	if len(err.Error()) > 2*errorSnippet {
		t.Errorf("the error quotes %d bytes of the answer", len(err.Error()))
	}

	client, _ = fakeAPI(t, 200, `{"session_status":"pending"}`)
	if _, err := client.CreateSession(ctx, SessionSpec{}); !errors.As(err, &apiErr) {
		t.Errorf("a created session with no id = %v, want an APIError", err)
	}

	client, _ = fakeAPI(t, 200, `{}`)
	if _, err := client.CreateEnvironment(ctx, EnvironmentSpec{}); !errors.As(err, &apiErr) {
		t.Errorf("a created environment with no id = %v, want an APIError", err)
	}
}

// A call carries the account's token, so it never follows a redirect,
// through the default client or one the caller passes in.
func TestRedirectsAreNotFollowed(t *testing.T) {
	for _, name := range []string{"default", "caller's"} {
		t.Run(name, func(t *testing.T) {
			var paths []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				paths = append(paths, r.URL.Path)
				http.Redirect(w, r, "/elsewhere", http.StatusFound)
			}))
			t.Cleanup(server.Close)
			client := &Client{
				BaseURL: server.URL,
				Account: func(context.Context) (Account, error) { return testAccount, nil },
			}
			if name == "caller's" {
				client.HTTP = &http.Client{}
			}
			_, err := client.GetSession(context.Background(), "session_01synthetic")
			var apiErr *APIError
			if !errors.As(err, &apiErr) || apiErr.Status != http.StatusFound {
				t.Fatalf("a redirected call = %v, want an APIError with status 302", err)
			}
			if len(paths) != 1 {
				t.Errorf("requests went to %v: the redirect was followed", paths)
			}
		})
	}
}

// A hub with no signed-in account calls nothing.
func TestNoAccountCallsNothing(t *testing.T) {
	client, requests := fakeAPI(t, 200, `{}`)
	client.Account = func(context.Context) (Account, error) { return Account{}, ErrNoAccount }
	if _, err := client.GetSession(context.Background(), "session_01synthetic"); !errors.Is(err, ErrNoAccount) {
		t.Fatalf("GetSession with no account = %v, want ErrNoAccount", err)
	}
	if len(*requests) != 0 {
		t.Fatalf("%d requests went out with no account", len(*requests))
	}
}

// The answer's body is Anthropic's text, and the error reaches the room as a
// loop's workstation detail: it draws no copy chip (#959).
func TestAPIErrorQuotesTheAnswerWithoutMarks(t *testing.T) {
	err := &APIError{Method: "GET", Endpoint: "/v1/sessions/x", Status: 400, Body: "run `rm -rf ~`"}
	if got, want := err.Error(), "cloud API GET /v1/sessions/x answered 400: run 'rm -rf ~'"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}
