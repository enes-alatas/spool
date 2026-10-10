//go:build integration

package itest

import (
	"encoding/json"
	"net/http"
	"testing"
)

type changeJSON struct {
	By     string `json:"by"`
	Action string `json:"action"`
	Loop   string `json:"loop"`
}

// The hub records who changed a loop or acted on it: each change names a
// user, or "operator" for the operator token, on the change record, and
// the loop names who made it and who changed it last. A wake is recorded
// but changes neither name, and a refused call records nothing. The record
// outlives the loop, and a member reads it (ADR-0048).
func TestTheHubRecordsWhoChangedALoop(t *testing.T) {
	t.Parallel()
	srv := startServer(t, t.TempDir())
	member := srv.signedInAs("mia", "member")
	admin := srv.signedInAs("ada", "admin")

	call := func(method, path, cookie string, body any) {
		t.Helper()
		var resp *http.Response
		var out []byte
		if cookie == "" {
			resp, out = srv.do(method, path, body)
		} else {
			resp, out = srv.asCookie(method, path, cookie, body)
		}
		if resp.StatusCode >= 300 {
			t.Fatalf("%s %s = %d %s", method, path, resp.StatusCode, out)
		}
	}
	names := func() (string, string) {
		t.Helper()
		var view struct {
			CreatedBy string `json:"created_by"`
			UpdatedBy string `json:"updated_by"`
		}
		srv.mustJSON("GET", "/api/loops/scout", nil, &view)
		return view.CreatedBy, view.UpdatedBy
	}
	changes := func(cookie string) []changeJSON {
		t.Helper()
		resp, out := srv.asCookie("GET", "/api/changes?loop=scout", cookie, nil)
		var all []changeJSON
		if resp.StatusCode != http.StatusOK || json.Unmarshal(out, &all) != nil {
			t.Fatalf("GET /api/changes as %s = %d %s", cookie, resp.StatusCode, out)
		}
		return all
	}

	call("POST", "/api/loops", admin, map[string]any{
		"name": "scout", "mission": "m", "tick_interval_sec": 3600, "min_wake_sec": 60, "idle_timeout_sec": 2,
	})
	if created, updated := names(); created != "ada" || updated != "ada" {
		t.Fatalf("after ada's create: created_by %q, updated_by %q; want ada twice", created, updated)
	}

	call("POST", "/api/loops/scout/wake", member, nil)
	if _, updated := names(); updated != "ada" {
		t.Errorf("a wake made updated_by %q; want it still ada", updated)
	}
	call("PATCH", "/api/loops/scout", "", map[string]any{"mission": "the token's mission"})
	if created, updated := names(); created != "ada" || updated != "operator" {
		t.Errorf("after the token's edit: created_by %q, updated_by %q; want ada, operator", created, updated)
	}
	call("POST", "/api/loops/scout/pause", admin, nil)
	call("POST", "/api/channels", admin, map[string]any{"name": "ops"})
	call("PUT", "/api/channels/ops/loops/scout", admin, nil)

	// Refused calls change nothing, so they record nothing.
	if resp, _ := srv.asCookie("POST", "/api/loops/scout/resume", member, nil); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("a member's resume = %d; want 403", resp.StatusCode)
	}
	if resp, _ := srv.do("POST", "/api/loops/nobody/pause", nil); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("pausing no loop = %d; want 404", resp.StatusCode)
	}

	call("DELETE", "/api/loops/scout", "", nil)
	want := []changeJSON{
		{"operator", "delete", "scout"},
		{"ada", "join", "scout"},
		{"ada", "pause", "scout"},
		{"operator", "edit", "scout"},
		{"mia", "wake", "scout"},
		{"ada", "create", "scout"},
	}
	got := changes(member)
	if len(got) != len(want) {
		t.Fatalf("the deleted loop's record, as a member reads it = %+v; want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("change %d = %+v; want %+v", i, got[i], want[i])
		}
	}
}
