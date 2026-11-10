//go:build integration

package itest

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// managedLoop is one loop as list_loops gives it.
type managedLoop struct {
	Name       string `json:"name"`
	Runtime    string `json:"runtime"`
	State      string `json:"state"`
	Paused     bool   `json:"paused"`
	Resumable  bool   `json:"resumable"`
	Health     string `json:"health"`
	LastTurnAt int64  `json:"last_turn_at"`
}

// manageSession connects to the management MCP at baseURL with token as
// its bearer.
func manageSession(t *testing.T, baseURL, token string) *mcp.ClientSession {
	t.Helper()
	client := mcp.NewClient(&mcp.Implementation{Name: "itest-agent", Version: "0"}, nil)
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint:             baseURL + "/manage/mcp",
		HTTPClient:           &http.Client{Transport: bearerTransport{token}},
		DisableStandaloneSSE: true,
		MaxRetries:           -1,
	}, nil)
	if err != nil {
		t.Fatalf("connect to the management mcp at %s: %v", baseURL, err)
	}
	t.Cleanup(func() { session.Close() })
	return session
}

// manageCall calls a management tool and returns its result, failing the
// test if the call itself didn't get through.
func manageCall(t *testing.T, session *mcp.ClientSession, tool string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		t.Fatalf("%s %v: %v", tool, args, err)
	}
	return result
}

// manageOK calls a management tool that must succeed, and decodes what it
// answered into out.
func manageOK(t *testing.T, session *mcp.ClientSession, tool string, args map[string]any, out any) {
	t.Helper()
	result := manageCall(t, session, tool, args)
	if result.IsError {
		t.Fatalf("%s %v refused: %s", tool, args, resultText(result))
	}
	raw, _ := json.Marshal(result.StructuredContent)
	if err := json.Unmarshal(raw, out); err != nil {
		t.Fatalf("%s %v answered %s: %v", tool, args, raw, err)
	}
}

func listManaged(t *testing.T, session *mcp.ClientSession) map[string]managedLoop {
	t.Helper()
	var out struct {
		Loops []managedLoop `json:"loops"`
	}
	manageOK(t, session, "list_loops", map[string]any{}, &out)
	loops := map[string]managedLoop{}
	for _, managed := range out.Loops {
		loops[managed.Name] = managed
	}
	return loops
}

// changesBy is the loop's change record as "by action" lines, newest first.
func changesBy(srv *server, loopName string) []string {
	srv.t.Helper()
	var changes []struct {
		By     string `json:"by"`
		Action string `json:"action"`
	}
	srv.mustJSON("GET", "/api/changes?loop="+loopName, nil, &changes)
	lines := make([]string, 0, len(changes))
	for _, change := range changes {
		lines = append(lines, change.By+" "+change.Action)
	}
	return lines
}

// An operator's agent holding a management token lists the loops, pauses
// one and resumes one, and has no other tool (ADR-0067, #1404). The list
// carries no mission. A pause and a resume are on the loop's change record
// under the token's name and id, as the control room's are under a
// person's. The agent resumes only what an agent paused: a person's pause
// stays theirs, and the agent pausing it again doesn't make it its own.
func TestAnAgentListsPausesAndResumesLoops(t *testing.T) {
	t.Parallel()
	hub := startServer(t, t.TempDir())
	hub.createLoop("alpha", map[string]any{"mission": "the alpha mission, never listed", "fleet": false})
	hub.createLoop("beta", map[string]any{"fleet": false})
	token, id := hub.mintManagementToken("", map[string]any{"name": "watcher"})
	actor := "manage:watcher#" + id
	agent := manageSession(t, hub.baseURL, token)

	tools, err := agent.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range tools.Tools {
		names = append(names, tool.Name)
	}
	slices.Sort(names)
	if strings.Join(names, ",") != "list_loops,pause_loop,resume_loop" {
		t.Fatalf("the management mcp offers %v, want list_loops, pause_loop and resume_loop alone", names)
	}

	listed := manageCall(t, agent, "list_loops", map[string]any{})
	raw, _ := json.Marshal(listed.StructuredContent)
	if strings.Contains(string(raw), "alpha mission") || strings.Contains(resultText(listed), "alpha mission") {
		t.Fatalf("list_loops carries a mission: %s", raw)
	}
	loops := listManaged(t, agent)
	if alpha := loops["alpha"]; len(loops) != 2 || alpha.Runtime != "bare" || alpha.Paused || alpha.Health != "ok" || alpha.State == "" {
		t.Fatalf("listed %+v, want alpha and beta, bare, running and ok", loops)
	}

	var paused struct {
		Loop    managedLoop `json:"loop"`
		Changed bool        `json:"changed"`
	}
	manageOK(t, agent, "pause_loop", map[string]any{"name": "alpha"}, &paused)
	if !paused.Changed || !paused.Loop.Paused || !paused.Loop.Resumable {
		t.Fatalf("pause_loop alpha answered %+v, want it paused, by the agent", paused)
	}
	if view := hub.loop("alpha"); view.Status != "paused" {
		t.Fatalf("alpha after the agent's pause: %s, want paused", view.Status)
	}
	manageOK(t, agent, "pause_loop", map[string]any{"name": "alpha"}, &paused)
	if paused.Changed {
		t.Fatal("pausing a paused loop changed it")
	}
	// a person's pause of the agent's keeps the agent's
	if resp, out := hub.do("POST", "/api/loops/alpha/pause", nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("pause alpha = %d %s", resp.StatusCode, out)
	}
	if alpha := listManaged(t, agent)["alpha"]; !alpha.Resumable {
		t.Fatalf("after a person paused it again alpha is %+v, want the agent's pause kept", alpha)
	}
	manageOK(t, agent, "resume_loop", map[string]any{"name": "alpha"}, &paused)
	if !paused.Changed || paused.Loop.Paused {
		t.Fatalf("resume_loop alpha answered %+v, want it resumed", paused)
	}
	if view := hub.loop("alpha"); view.Status != "active" {
		t.Fatalf("alpha after the agent's resume: %s, want active", view.Status)
	}
	if got := changesBy(hub, "alpha"); strings.Join(got, ",") != actor+" resume,operator pause,"+actor+" pause,operator create" {
		t.Fatalf("alpha's change record = %v, want the agent's pause, the operator's and the agent's resume", got)
	}

	// a person's pause
	if resp, out := hub.do("POST", "/api/loops/beta/pause", nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("pause beta = %d %s", resp.StatusCode, out)
	}
	manageOK(t, agent, "pause_loop", map[string]any{"name": "beta"}, &paused)
	if paused.Changed || paused.Loop.Resumable {
		t.Fatalf("the agent's pause of a person's pause answered %+v, want no change, not its to resume", paused)
	}
	refused := manageCall(t, agent, "resume_loop", map[string]any{"name": "beta"})
	if !refused.IsError || !strings.Contains(resultText(refused), "paused by a person") {
		t.Fatalf("the agent's resume of a person's pause: %s, want it refused", resultText(refused))
	}
	if view := hub.loop("beta"); view.Status != "paused" {
		t.Fatalf("beta after the refused resume: %s, want paused", view.Status)
	}
	if got := changesBy(hub, "beta"); strings.Join(got, ",") != "operator pause,operator create" {
		t.Fatalf("beta's change record = %v, want the operator's pause alone", got)
	}
	for _, call := range []struct{ tool, name, want string }{
		{"resume_loop", "alpha", "not paused"},
		{"pause_loop", "nosuch", "no loop is named"},
	} {
		result := manageCall(t, agent, call.tool, map[string]any{"name": call.name})
		if !result.IsError || !strings.Contains(resultText(result), call.want) {
			t.Errorf("%s %s: %s, want a refusal saying %q", call.tool, call.name, resultText(result), call.want)
		}
	}
	// the person's resume takes it, and the agent's pause after it is the agent's
	if resp, out := hub.do("POST", "/api/loops/beta/resume", nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("resume beta = %d %s", resp.StatusCode, out)
	}
	manageOK(t, agent, "pause_loop", map[string]any{"name": "beta"}, &paused)
	if !paused.Changed || !paused.Loop.Resumable {
		t.Fatalf("the agent's pause after a person's resume answered %+v, want it the agent's", paused)
	}
}

// The management MCP takes a management token and nothing else: not the
// operator token, a signed-in session or a loop's token (ADR-0067). It is
// served on the operator listener and the cloud listener, never the loop
// listener. A revoked token is refused at its next call, a session open
// on it included, and a restarted hub keeps both the tokens it holds and
// the revocation.
func TestTheManagementMCPTakesOnlyALiveManagementToken(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	hub := startServerArgs(t, dataDir, "--runtime", "bare", "--cloud-listen", "0")
	hub.createLoop("gamma", nil)
	admin := hub.signedInAs("ada", "admin")
	kept, _ := hub.mintManagementToken(admin, map[string]any{"name": "kept"})
	revoked, revokedID := hub.mintManagementToken("", map[string]any{"name": "revoked"})

	post := func(baseURL string, headers map[string]string) int {
		t.Helper()
		req, _ := http.NewRequest(http.MethodPost, baseURL+"/manage/mcp", strings.NewReader(mcpInitialize))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		for key, value := range headers {
			req.Header.Set(key, value)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	bearer := func(token string) map[string]string { return map[string]string{"Authorization": "Bearer " + token} }

	for _, listener := range []string{hub.baseURL, hub.cloudURL} {
		if got := post(listener, bearer(kept)); got != http.StatusOK {
			t.Errorf("a management token on %s: %d, want 200", listener, got)
		}
	}
	if got := post(hub.mcpURL, bearer(kept)); got != http.StatusNotFound {
		t.Errorf("a management token on the loop listener: %d, want 404", got)
	}
	for what, headers := range map[string]map[string]string{
		"no token":             {},
		"the operator token":   bearer(hub.operatorToken),
		"a loop's token":       bearer(hubMCPToken(t, hub, "gamma")),
		"a signed-in session":  {"Cookie": "spool_operator=" + admin},
		"a token's shape only": bearer("spool_manage_" + strings.Repeat("A", 43)),
	} {
		for _, listener := range []string{hub.baseURL, hub.cloudURL} {
			if got := post(listener, headers); got != http.StatusUnauthorized {
				t.Errorf("%s on %s/manage/mcp: %d, want 401", what, listener, got)
			}
		}
	}
	if got := post(hub.baseURL, map[string]string{"Authorization": "Bearer " + kept, "Sec-Fetch-Site": "cross-site"}); got != http.StatusForbidden {
		t.Errorf("a cross-site request with a management token on the operator listener: %d, want 403", got)
	}

	open := manageSession(t, hub.baseURL, revoked)
	manageOK(t, open, "pause_loop", map[string]any{"name": "gamma"}, &struct{}{})
	if resp, out := hub.do("DELETE", "/api/management-tokens/"+revokedID, nil); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("revoke = %d %s", resp.StatusCode, out)
	}
	if _, err := open.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "resume_loop", Arguments: map[string]any{"name": "gamma"}}); err == nil {
		t.Fatal("a session open on a revoked token made its next call")
	}
	if view := hub.loop("gamma"); view.Status != "paused" {
		t.Fatalf("gamma after the revoked token's resume: %s, want paused", view.Status)
	}

	hub.stop()
	hub = startServerArgs(t, dataDir, "--runtime", "bare", "--cloud-listen", "0")
	if got := post(hub.baseURL, bearer(revoked)); got != http.StatusUnauthorized {
		t.Errorf("the revoked token after a restart: %d, want 401", got)
	}
	agent := manageSession(t, hub.cloudURL, kept)
	if loops := listManaged(t, agent); !loops["gamma"].Paused || !loops["gamma"].Resumable {
		t.Fatalf("after a restart gamma is %+v, want paused by the revoked token, an agent's to resume", loops["gamma"])
	}
	manageOK(t, agent, "resume_loop", map[string]any{"name": "gamma"}, &struct{}{})
	if view := hub.loop("gamma"); view.Status != "active" {
		t.Fatalf("gamma after the kept token's resume: %s, want active", view.Status)
	}
}
