//go:build integration

package itest

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/enes-alatas/spool/internal/store"
	"github.com/enes-alatas/spool/internal/store/sqlite"
)

// allowLegacySender makes person an allowed sender no user is linked as,
// as a hub from before ADR-0051 holds them: a pending row, which nothing
// creates any more, allowed on the Access page.
func (s *server) allowLegacySender(person user) {
	s.t.Helper()
	db, err := sqlite.Open(filepath.Join(s.dataDir, "spool.db"))
	if err != nil {
		s.t.Fatal(err)
	}
	defer db.Close()
	if err := db.TGSenders().Create(context.Background(), &store.TGSender{TGUserID: person.ID,
		Username: person.Username, Display: person.First, Status: store.SenderPending,
		FirstSeenVia: "dm:alpha"}); err != nil {
		s.t.Fatalf("legacy sender: %v", err)
	}
	s.mustJSON("POST", "/api/telegram/senders/"+strconv.FormatInt(person.ID, 10)+"/allow", nil, nil)
}

// senderUsers is the user each Telegram sender is linked to, "" for one
// not linked, by sender id.
func (s *server) senderUsers() map[int64]string {
	s.t.Helper()
	var senders []struct {
		TGUserID int64  `json:"tg_user_id"`
		User     string `json:"user"`
	}
	s.mustJSON("GET", "/api/telegram/senders", nil, &senders)
	out := map[int64]string{}
	for _, sender := range senders {
		out[sender.TGUserID] = sender.User
	}
	return out
}

// A loop's owner is named as a hub user, and is the person their link on
// the loop's surface is (#731). A user with no link there cannot own it.
// An allowed sender from before ADR-0051, whom no user is linked as, keeps
// owning and reaching their loop, shown as not linked, and removing the
// owner's link leaves the loop with no owner.
func TestALoopsOwnerIsAUserByTheirLink(t *testing.T) {
	t.Parallel()
	tg := startFakeTelegram(t, "alpha", "beta")
	srv := startTelegramServer(t, t.TempDir(), tg)
	srv.createLoop("alpha", map[string]any{"tg_bot_token": "alpha"})
	srv.createLoop("beta", map[string]any{"tg_bot_token": "beta"})

	legacy := user{ID: 6767, First: "Legacy", Username: "legacy"}
	srv.allowLegacySender(legacy)
	if v := srv.loop("alpha"); v.OwnerTGUserID != legacy.ID || v.OwnerUser != "" {
		t.Fatalf("alpha's owner = %d, user %q; want %d, not linked", v.OwnerTGUserID, v.OwnerUser, legacy.ID)
	}
	if got, ok := srv.senderUsers()[legacy.ID]; !ok || got != "" {
		t.Fatalf("legacy sender listed %v as user %q; want listed, not linked", ok, got)
	}
	tg.dm("alpha", legacy, "an unlinked owner still reaches alpha")
	srv.waitForMessage("an unlinked owner still reaches alpha")

	noa := srv.signedInAs("noa", "member")
	for _, refused := range []struct{ user, code string }{{"noa", "no_link"}, {"nobody", "unknown_user"}} {
		resp, body := srv.do("PUT", "/api/loops/alpha/owner", map[string]any{"user": refused.user})
		var answer struct {
			Code string `json:"code"`
		}
		if resp.StatusCode != http.StatusBadRequest || json.Unmarshal(body, &answer) != nil || answer.Code != refused.code {
			t.Fatalf("owner %s = %d %s; want 400 %s", refused.user, resp.StatusCode, body, refused.code)
		}
	}

	friend := user{ID: 6768, First: "Friend", Username: "friend"}
	srv.confirmLink(tg, "beta", friend, noa, "noa")
	var v loopView
	srv.mustJSON("PUT", "/api/loops/alpha/owner", map[string]any{"user": "noa"}, &v)
	if v.OwnerTGUserID != friend.ID || v.OwnerUser != "noa" || v.OwnerDMReady {
		t.Fatalf("alpha owned by noa: owner %d, user %q, ready %v; want %d, noa, not ready",
			v.OwnerTGUserID, v.OwnerUser, v.OwnerDMReady, friend.ID)
	}
	if got := srv.senderUsers()[friend.ID]; got != "noa" {
		t.Fatalf("friend listed as user %q, want noa", got)
	}
	tg.dm("alpha", friend, "hi from your owner")
	waitOwnerDMReady(t, srv, "alpha")

	if resp, body := srv.asCookie("DELETE", "/api/me/links/telegram", noa, nil); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("remove noa's link = %d %s", resp.StatusCode, body)
	}
	if v := srv.loop("alpha"); v.OwnerTGUserID != 0 || v.OwnerUser != "" || v.OwnerDMReady {
		t.Fatalf("after noa's link went: owner %d, user %q, ready %v; want none", v.OwnerTGUserID, v.OwnerUser, v.OwnerDMReady)
	}
}

// A Slack loop's owner is named as a user too, by their Slack link (#731).
func TestASlackLoopsOwnerIsAUserByTheirLink(t *testing.T) {
	t.Parallel()
	srv, _ := startSlackFleet(t)
	name := "slack-" + strings.ToLower(slackOperator)
	var owned struct {
		OwnerSlackUserID string `json:"owner_slack_user_id"`
		OwnerUser        string `json:"owner_user"`
	}
	srv.mustJSON("PUT", "/api/loops/terra/owner", map[string]any{"user": name}, &owned)
	if owned.OwnerSlackUserID != slackOperator || owned.OwnerUser != name {
		t.Fatalf("terra owned by %s reads %+v; want %s", name, owned, slackOperator)
	}
	var senders []struct {
		SlackUserID string `json:"slack_user_id"`
		User        string `json:"user"`
	}
	srv.mustJSON("GET", "/api/slack/senders", nil, &senders)
	if len(senders) != 1 || senders[0].SlackUserID != slackOperator || senders[0].User != name {
		t.Fatalf("slack senders = %+v; want %s linked to %s", senders, slackOperator, name)
	}
}
