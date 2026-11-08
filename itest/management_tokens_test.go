//go:build integration

package itest

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"
)

type managementTokenJSON struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Owner      string `json:"owner"`
	CreatedAt  int64  `json:"created_at"`
	ExpiresAt  int64  `json:"expires_at"`
	LastUsedAt int64  `json:"last_used_at"`
}

var managementTokenShape = regexp.MustCompile(`^spool_manage_[A-Za-z0-9_-]{43}$`)

// mintManagementToken mints a token as the session cookie's user, or as the
// operator token when cookie is "", and returns it with the id its
// Location names.
func (srv *server) mintManagementToken(cookie string, body map[string]any) (string, string) {
	srv.t.Helper()
	var resp *http.Response
	var out []byte
	if cookie == "" {
		resp, out = srv.do("POST", "/api/management-tokens", body)
	} else {
		resp, out = srv.asCookie("POST", "/api/management-tokens", cookie, body)
	}
	if resp.StatusCode != http.StatusCreated {
		srv.t.Fatalf("mint %v = %d %s", body, resp.StatusCode, out)
	}
	token := string(out)
	if !managementTokenShape.MatchString(token) {
		srv.t.Fatalf("the mint answered %.20s…, not a management token", token)
	}
	if got := resp.Header.Get("Cache-Control"); got != "no-store" {
		srv.t.Fatalf("the mint's Cache-Control = %q, want no-store", got)
	}
	if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, "text/plain") {
		srv.t.Fatalf("the mint's Content-Type = %q, want text/plain", got)
	}
	id, ok := strings.CutPrefix(resp.Header.Get("Location"), "/api/management-tokens/")
	if !ok || id == "" {
		srv.t.Fatalf("the mint's Location = %q", resp.Header.Get("Location"))
	}
	return token, id
}

func (srv *server) managementTokens() []managementTokenJSON {
	srv.t.Helper()
	resp, out := srv.do("GET", "/api/management-tokens", nil)
	if resp.StatusCode != http.StatusOK {
		srv.t.Fatalf("list = %d %s", resp.StatusCode, out)
	}
	var tokens []managementTokenJSON
	if err := json.Unmarshal(out, &tokens); err != nil {
		srv.t.Fatal(err)
	}
	return tokens
}

// An admin mints a token an operator's agent holds, and the hub shows it
// once: no list, log or later answer holds it, and it ends 90 days on by
// default (ADR-0067, #1402). It is a credential of its own,
// so the operator API refuses it as a bearer on every route, its own
// included. A member may not mint one.
func TestAManagementTokenIsShownOnceAndOpensNoOperatorRoute(t *testing.T) {
	t.Parallel()
	srv := startServer(t, t.TempDir())
	srv.createLoop("scout", nil)
	admin := srv.signedInAs("ada", "admin")
	member := srv.signedInAs("mia", "member")

	before := time.Now().UnixMilli()
	token, id := srv.mintManagementToken(admin, map[string]any{"name": "watcher"})
	other, _ := srv.mintManagementToken("", map[string]any{"name": "forever", "expires_at": 0})
	if other == token {
		t.Fatal("two mints gave the same token")
	}
	if resp, out := srv.do("POST", "/api/management-tokens", map[string]any{"name": "watcher"}); resp.StatusCode != http.StatusConflict {
		t.Fatalf("a second watcher = %d %s, want 409", resp.StatusCode, out)
	}
	for _, body := range []map[string]any{{"name": "Bad Name"}, {"name": "past", "expires_at": before - 1}} {
		if resp, out := srv.do("POST", "/api/management-tokens", body); resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("mint %v = %d %s, want 400", body, resp.StatusCode, out)
		}
	}
	if resp, out := srv.asCookie("POST", "/api/management-tokens", member, map[string]any{"name": "mias"}); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("a member's mint = %d %s, want 403", resp.StatusCode, out)
	}

	tokens := srv.managementTokens()
	if len(tokens) != 2 || tokens[0].Name != "forever" || tokens[1].Name != "watcher" {
		t.Fatalf("listed %+v, want forever and watcher", tokens)
	}
	watcher := tokens[1]
	day := int64(24 * time.Hour / time.Millisecond)
	if watcher.ID != id || watcher.Owner != "ada" || watcher.CreatedAt < before ||
		watcher.ExpiresAt-watcher.CreatedAt != 90*day || watcher.LastUsedAt != 0 {
		t.Fatalf("watcher listed as %+v, want ada's, made now, ending in 90 days, never used", watcher)
	}
	if tokens[0].ExpiresAt != 0 || tokens[0].Owner != "" {
		t.Fatalf("forever listed as %+v, want no end and the operator token's", tokens[0])
	}
	_, listed := srv.do("GET", "/api/management-tokens", nil)
	for _, place := range map[string]string{"the list": string(listed), "the log": srv.log()} {
		if strings.Contains(place, token[len("spool_manage_"):]) || strings.Contains(place, other[len("spool_manage_"):]) {
			t.Fatal("a minted token's value is in " + place)
		}
	}

	for _, call := range []struct{ method, path string }{
		{"GET", "/api/loops"},
		{"GET", "/api/management-tokens"},
		{"POST", "/api/management-tokens"},
		{"POST", "/api/loops/scout/pause"},
		{"POST", "/api/loops/scout/resume"},
		{"DELETE", "/api/management-tokens/" + id},
	} {
		resp, out := srv.raw(call.method, call.path, strings.NewReader(`{"name":"sneaky"}`), map[string]string{
			"Authorization": "Bearer " + token, "Content-Type": "application/json"})
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s %s with a management token = %d %s, want 401", call.method, call.path, resp.StatusCode, out)
		}
	}
	if len(srv.managementTokens()) != 2 || srv.loop("scout").State == "paused" {
		t.Fatal("a management token's refused calls changed something")
	}
}

// Revoking a token removes it at once, removing a user revokes every token
// they own, and a restarted hub keeps both: the tokens still held are
// listed as before, and the revoked ones stay gone (ADR-0067,
// #1402). That a revoked token's next call is refused is held where a call
// can be made with one, /manage/mcp (#1404).
func TestARevokedManagementTokenStaysRevokedAcrossARestart(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	srv := startServer(t, dataDir)
	admin := srv.signedInAs("ada", "admin")
	boss := srv.signedInAs("bea", "admin")
	srv.mintManagementToken(admin, map[string]any{"name": "adas"})
	_, beas := srv.mintManagementToken(boss, map[string]any{"name": "beas"})
	_, revoked := srv.mintManagementToken("", map[string]any{"name": "revoked"})
	srv.mintManagementToken("", map[string]any{"name": "kept"})

	if resp, out := srv.asCookie("DELETE", "/api/management-tokens/"+revoked, admin, nil); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("revoke = %d %s, want 204", resp.StatusCode, out)
	}
	if resp, out := srv.do("DELETE", "/api/management-tokens/"+revoked, nil); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("revoking it again = %d %s, want 404", resp.StatusCode, out)
	}
	if resp, out := srv.do("DELETE", "/api/users/ada", nil); resp.StatusCode >= 300 {
		t.Fatalf("remove ada = %d %s", resp.StatusCode, out)
	}
	names := func() string {
		var out []string
		for _, managementToken := range srv.managementTokens() {
			out = append(out, managementToken.Name+"#"+managementToken.ID)
		}
		return strings.Join(out, ",")
	}
	want := names()
	if strings.Contains(want, "adas#") || strings.Contains(want, "revoked#") || !strings.Contains(want, "beas#"+beas) {
		t.Fatalf("listed %s, want beas and kept alone", want)
	}

	srv.stop()
	srv = startServer(t, dataDir)
	if got := names(); got != want {
		t.Fatalf("after a restart the list is %s, want %s as before", got, want)
	}
}

// A token's owner or an admin revokes it (ADR-0067). A minter demoted to
// member still ends their own token, and a member can't end another's.
func TestAManagementTokensOwnerRevokesItOnceDemoted(t *testing.T) {
	t.Parallel()
	srv := startServer(t, t.TempDir())
	cara := srv.signedInAs("cara", "admin")
	mia := srv.signedInAs("mia", "member")
	_, caras := srv.mintManagementToken(cara, map[string]any{"name": "caras"})
	_, operators := srv.mintManagementToken("", map[string]any{"name": "operators"})
	if resp, out := srv.do("PATCH", "/api/users/cara", map[string]string{"role": "member"}); resp.StatusCode != http.StatusOK {
		t.Fatalf("demote cara = %d %s", resp.StatusCode, out)
	}
	for who, session := range map[string]string{"mia": mia, "cara": cara} {
		for _, id := range []string{operators, caras} {
			if who == "cara" && id == caras {
				continue
			}
			if resp, out := srv.asCookie("DELETE", "/api/management-tokens/"+id, session, nil); resp.StatusCode != http.StatusForbidden {
				t.Fatalf("%s revoking %s = %d %s, want 403", who, id, resp.StatusCode, out)
			}
		}
	}
	if resp, out := srv.asCookie("DELETE", "/api/management-tokens/"+caras, cara, nil); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("cara revoking cara's own = %d %s, want 204", resp.StatusCode, out)
	}
	if tokens := srv.managementTokens(); len(tokens) != 1 || tokens[0].ID != operators {
		t.Fatalf("listed %+v, want the operator's alone", tokens)
	}
}
