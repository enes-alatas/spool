//go:build integration

package itest

import (
	"net/http"
	"strings"
	"testing"
)

// accessUserJSON is a user as GET /api/users lists them for Settings →
// Access (#744).
type accessUserJSON struct {
	Name  string `json:"name"`
	Links []struct {
		Surface string `json:"surface"`
		ID      string `json:"id"`
	} `json:"links"`
	Blocked bool `json:"blocked"`
}

// accessUser is the user name names, as the owner reads them.
func (s *server) accessUser(name string) accessUserJSON {
	s.t.Helper()
	var all []accessUserJSON
	s.mustJSON("GET", "/api/users", nil, &all)
	for _, user := range all {
		if user.Name == name {
			return user
		}
	}
	s.t.Fatalf("no user %q in %s", name, dump(all))
	return accessUserJSON{}
}

// An owner sees each user's links, and removes another user's link, which
// makes its person a stranger at once; a member can do neither (#744).
func TestAnOwnerRemovesAnotherUsersLink(t *testing.T) {
	t.Parallel()
	operator := user{ID: 8310, First: "Operator", Username: "operator"}
	alex := user{ID: 8311, First: "Alex", Username: "alex"}
	srv, tg := startTelegramFleet(t, operator)
	mia := srv.signedInAs("mia", "member")
	srv.confirmLink(tg, "alpha", alex, mia, "mia")

	if got := srv.accessUser("mia"); len(got.Links) != 1 || got.Links[0].Surface != "telegram" ||
		got.Links[0].ID != "8311" || got.Blocked {
		t.Fatalf("mia as the owner reads them = %s; want one Telegram link, not blocked", dump(got))
	}
	for _, call := range [][2]string{{"GET", "/api/users"}, {"DELETE", "/api/users/mia/links/telegram"}} {
		if resp, _ := srv.asCookie(call[0], call[1], mia, nil); resp.StatusCode != http.StatusForbidden {
			t.Errorf("a member's %s %s = %d; want 403", call[0], call[1], resp.StatusCode)
		}
	}

	srv.mustJSON("DELETE", "/api/users/mia/links/telegram", nil, nil)
	if status := telegramSenderStatus(srv, alex.ID); status != "" {
		t.Fatalf("a link the owner removed left its person %q; want a stranger", status)
	}
	tg.dm("alpha", alex, "still there?")
	settled(t, srv, tg, "alpha", operator, "after the owner's unlink")
	if got := srv.activityWith("still there?"); len(got) != 0 {
		t.Fatalf("a person whose link the owner removed still reaches the loop: %s", dump(got))
	}
	if got := srv.accessUser("mia"); len(got.Links) != 0 {
		t.Fatalf("mia after the unlink = %s; want no links", dump(got))
	}
	for path, want := range map[string]int{"/api/users/mia/links/telegram": http.StatusNotFound,
		"/api/users/nobody/links/telegram": http.StatusNotFound} {
		if resp, body := srv.do("DELETE", path, nil); resp.StatusCode != want {
			t.Errorf("DELETE %s = %d %s; want %d", path, resp.StatusCode, body, want)
		}
	}
}

// An owner blocks a user on every surface they are linked on: their
// person's DMs reach nothing, a link code from them is not taken up, and
// the block outlives the link. Unblocked, they reach the loops again. A
// member cannot block, and a user with no link has nothing to block
// (#744).
func TestAnOwnerBlocksAUser(t *testing.T) {
	t.Parallel()
	operator := user{ID: 8320, First: "Operator", Username: "operator"}
	alex := user{ID: 8321, First: "Alex", Username: "alex"}
	srv, tg := startTelegramFleet(t, operator)
	mia := srv.signedInAs("mia", "member")
	srv.confirmLink(tg, "alpha", alex, mia, "mia")

	if resp, _ := srv.asCookie("POST", "/api/users/mia/block", mia, nil); resp.StatusCode != http.StatusForbidden {
		t.Errorf("a member's block = %d; want 403", resp.StatusCode)
	}
	var blocked accessUserJSON
	srv.mustJSON("POST", "/api/users/mia/block", nil, &blocked)
	if !blocked.Blocked || len(blocked.Links) != 1 {
		t.Fatalf("mia blocked = %s; want blocked, still linked", dump(blocked))
	}
	if status := telegramSenderStatus(srv, alex.ID); status != "blocked" {
		t.Fatalf("a blocked user's person is %q; want blocked", status)
	}
	answered := len(tg.sentTo(alex.ID))
	tg.dm("alpha", alex, "can you hear me?")
	tg.dm("alpha", alex, srv.linkCode(mia, "telegram"))
	settled(t, srv, tg, "alpha", operator, "after the block")
	if got := srv.activityWith("can you hear me?"); len(got) != 0 {
		t.Fatalf("a blocked user's person reached the loop: %s", dump(got))
	}
	if sent := tg.sentTo(alex.ID); len(sent) != answered {
		t.Fatalf("a blocked person was answered, a link code included: %s", dump(sent))
	}

	var unblocked accessUserJSON
	srv.mustJSON("POST", "/api/users/mia/unblock", nil, &unblocked)
	if unblocked.Blocked || telegramSenderStatus(srv, alex.ID) != "allowed" {
		t.Fatalf("mia unblocked = %s, person %q; want allowed", dump(unblocked), telegramSenderStatus(srv, alex.ID))
	}
	tg.dm("alpha", alex, "back again")
	srv.waitForMessage("back again")

	srv.mustJSON("POST", "/api/users/mia/block", nil, nil)
	srv.mustJSON("DELETE", "/api/users/mia/links/telegram", nil, nil)
	if status := telegramSenderStatus(srv, alex.ID); status != "blocked" {
		t.Fatalf("a removed link left a blocked person %q; want the block kept", status)
	}
	if resp, body := srv.do("POST", "/api/users/mia/unblock", nil); resp.StatusCode != http.StatusBadRequest ||
		!strings.Contains(string(body), `"no_link"`) {
		t.Fatalf("unblocking a user with no link = %d %s; want 400 no_link", resp.StatusCode, body)
	}
}
