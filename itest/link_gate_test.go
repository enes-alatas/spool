//go:build integration

package itest

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

// settled waits until everything sent to bot before it has been handled:
// a bot takes its updates in order, so once the operator's DM after them is
// in, the ones before it are done.
func settled(t *testing.T, srv *server, tg *fakeTelegram, bot string, operator user, text string) {
	t.Helper()
	tg.dm(bot, operator, text)
	srv.waitForMessage(text)
}

// telegramSenderStatus is the status the hub holds for a Telegram sender,
// or "" when it holds none.
func telegramSenderStatus(srv *server, id int64) string {
	srv.t.Helper()
	var senders []struct {
		TGUserID int64  `json:"tg_user_id"`
		Status   string `json:"status"`
	}
	srv.mustJSON("GET", "/api/telegram/senders", nil, &senders)
	for _, sender := range senders {
		if sender.TGUserID == id {
			return sender.Status
		}
	}
	return ""
}

type linkJSON struct {
	Surface   string `json:"surface"`
	ID        string `json:"id"`
	Loop      string `json:"loop"`
	ExpiresAt int64  `json:"expires_at"`
}

// myLinks is what session reads at path: its links, or the ones waiting
// for a person to confirm.
func (s *server) myLinks(session, path string) []linkJSON {
	s.t.Helper()
	resp, body := s.asCookie("GET", path, session, nil)
	var links []linkJSON
	if resp.StatusCode != http.StatusOK || json.Unmarshal(body, &links) != nil {
		s.t.Fatalf("my links = %d %s", resp.StatusCode, body)
	}
	return links
}

// A link code is the one way a person becomes an allowed sender (#730,
// ADR-0051). A stranger is answered with nothing and registered nowhere;
// a code minted for another surface, or said in a group, links nobody; a
// good code in a DM asks the sender to confirm, naming the user who will
// read their private conversations, and the room shows it waiting; only
// the YES links and allows them. Admins read the record of links made and
// removed, and a removed link makes its person a stranger again.
func TestTheLinkCodeIsTheGate(t *testing.T) {
	t.Parallel()
	operator := user{ID: 9190, First: "Operator", Username: "operator"}
	stranger := user{ID: 9191, First: "Stranger", Username: "stranger"}
	srv, tg := startTelegramFleet(t, operator, nil)
	bea := srv.signedInAs("bea", "member")

	tg.dm("alpha", stranger, "let me in")
	tg.post(groupChatID, "supergroup", "anyone?", stranger)
	tg.dm("alpha", stranger, srv.linkCode(bea, "slack"))
	settled(t, srv, tg, "alpha", operator, "after the stranger")
	if sent := tg.sentTo(stranger.ID); len(sent) != 0 {
		t.Fatalf("a stranger was answered: %+v", sent)
	}
	if status := telegramSenderStatus(srv, stranger.ID); status != "" {
		t.Fatalf("a stranger was registered as %q", status)
	}
	for _, text := range []string{"let me in", "anyone?"} {
		if got := srv.activityWith(text); len(got) != 0 {
			t.Fatalf("a stranger's %q was stored: %s", text, dump(got))
		}
	}

	// a code said in a group is just words: nothing claims it, and it
	// still works in a DM (#790)
	code := srv.linkCode(bea, "telegram")
	tg.post(groupChatID, "supergroup", code, stranger)
	settled(t, srv, tg, "alpha", operator, "after the code in the group")
	if sent := tg.sentTo(stranger.ID); len(sent) != 0 {
		t.Fatalf("a code in a group was answered: %+v", sent)
	}
	if waiting := srv.myLinks(bea, "/api/me/links/waiting"); len(waiting) != 0 {
		t.Fatalf("a code in a group left bea waiting on %+v", waiting)
	}
	if got := srv.activityWith(code); len(got) != 0 {
		t.Fatalf("a stranger's code in a group was stored: %s", dump(got))
	}

	tg.dm("alpha", stranger, code)
	asked := tg.waitSent(t, stranger.ID, "Telegram account to bea")
	if !strings.Contains(asked.Text, "bea will read your private conversations") || asked.Token != "alpha" {
		t.Fatalf("the question = %+v; want alpha's bot naming who reads what", asked)
	}
	if links := srv.myLinks(bea, "/api/me/links/waiting"); len(links) != 1 || links[0].Loop != "alpha" ||
		links[0].ID != strconv.FormatInt(stranger.ID, 10) || links[0].ExpiresAt == 0 {
		t.Fatalf("bea's links while she waits = %+v; want the stranger waiting on alpha", links)
	}
	tg.dm("alpha", stranger, "can I talk now?")
	settled(t, srv, tg, "alpha", operator, "while the stranger is asked")
	if status := telegramSenderStatus(srv, stranger.ID); status != "" {
		t.Fatalf("a code alone registered the stranger as %q", status)
	}
	if got := srv.activityWith("can I talk now?"); len(got) != 0 {
		t.Fatalf("a stranger's message was stored before their YES: %s", dump(got))
	}

	tg.dm("alpha", stranger, "YES")
	tg.waitSent(t, stranger.ID, "now linked to bea")
	if links := srv.myLinks(bea, "/api/me/links"); len(links) != 1 || links[0].Surface != "telegram" {
		t.Fatalf("bea's links after the YES = %+v; want the stranger linked", links)
	}
	if waiting := srv.myLinks(bea, "/api/me/links/waiting"); len(waiting) != 0 {
		t.Fatalf("bea still waits after the YES: %+v", waiting)
	}
	if status := telegramSenderStatus(srv, stranger.ID); status != "allowed" {
		t.Fatalf("a confirmed link left its person %q; want allowed", status)
	}
	tg.dm("beta", stranger, "hello beta")
	srv.waitForMessage("hello beta")
	if got := srv.activityWith("hello beta"); len(got) != 1 || got[0].Conversation != "dm:stranger" {
		t.Fatalf("a linked person's DM was stored as %s", dump(got))
	}

	if resp, _ := srv.asCookie("GET", "/api/links/changes", bea, nil); resp.StatusCode != http.StatusForbidden {
		t.Errorf("a member read the record of links: %d", resp.StatusCode)
	}
	if resp, body := srv.asCookie("DELETE", "/api/me/links/telegram", bea, nil); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("removing the link = %d %s", resp.StatusCode, body)
	}
	if status := telegramSenderStatus(srv, stranger.ID); status != "" {
		t.Fatalf("a removed link left its person %q; want a stranger", status)
	}
	tg.dm("alpha", stranger, "still there?")
	settled(t, srv, tg, "alpha", operator, "after the unlink")
	if got := srv.activityWith("still there?"); len(got) != 0 {
		t.Fatalf("a person whose link was removed still reaches the loop: %s", dump(got))
	}

	var changes []struct {
		Action string `json:"action"`
		User   string `json:"user"`
		Person string `json:"person"`
		Loop   string `json:"loop"`
	}
	srv.mustJSON("GET", "/api/links/changes?limit=2", nil, &changes)
	id := strconv.FormatInt(stranger.ID, 10)
	if len(changes) != 2 || changes[0].Action != "unlink" || changes[1].Action != "link" ||
		changes[1].User != "bea" || changes[1].Person != id || changes[1].Loop != "alpha" {
		t.Fatalf("the record of links = %+v; want bea's link through alpha, then its removal", changes)
	}
}

// Five wrong codes and a sender is not listened to: their next code, good
// as it is, asks nothing and links nobody, and none of it is answered
// (ADR-0051).
func TestWrongLinkCodesSilenceASender(t *testing.T) {
	t.Parallel()
	operator := user{ID: 9290, First: "Operator", Username: "operator"}
	guesser := user{ID: 9291, First: "Guesser", Username: "guesser"}
	srv, tg := startTelegramFleet(t, operator, nil)
	bea := srv.signedInAs("bea", "member")
	for _, guess := range []string{"link-aaaaaaaaaa", "link-bbbbbbbbbb", "link-cccccccccc", "yes", "link-dddddddddd"} {
		tg.dm("alpha", guesser, guess)
	}
	tg.dm("alpha", guesser, srv.linkCode(bea, "telegram"))
	settled(t, srv, tg, "alpha", operator, "after the guesses")
	if sent := tg.sentTo(guesser.ID); len(sent) != 0 {
		t.Fatalf("a sender guessing codes was answered: %+v", sent)
	}
	if links := srv.myLinks(bea, "/api/me/links/waiting"); len(links) != 0 {
		t.Fatalf("a silenced sender's code asked them to confirm: %+v", links)
	}
}

// A block outlives the link (ADR-0051): a person blocked while linked is
// still blocked once the link is removed, and a fresh code of theirs is
// refused unanswered, so removing a link and linking again lifts nothing.
func TestABlockOutlivesTheLink(t *testing.T) {
	t.Parallel()
	operator := user{ID: 9292, First: "Operator", Username: "operator"}
	friend := user{ID: 9293, First: "Friend", Username: "friend"}
	srv, tg := startTelegramFleet(t, operator, nil)
	session := srv.allowSender(tg, "alpha", friend)
	srv.mustJSON("POST", "/api/telegram/senders/"+strconv.FormatInt(friend.ID, 10)+"/block", nil, nil)
	if resp, body := srv.asCookie("DELETE", "/api/me/links/telegram", session, nil); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("removing the link = %d %s", resp.StatusCode, body)
	}
	if status := telegramSenderStatus(srv, friend.ID); status != "blocked" {
		t.Fatalf("the sender after their link went = %q, want still blocked", status)
	}

	answered := len(tg.sentTo(friend.ID))
	tg.dm("alpha", friend, srv.linkCode(session, "telegram"))
	settled(t, srv, tg, "alpha", operator, "after the blocked code")
	if sent := tg.sentTo(friend.ID); len(sent) != answered {
		t.Fatalf("a blocked sender's code was answered: %+v", sent[answered:])
	}
	if links := srv.myLinks(session, "/api/me/links/waiting"); len(links) != 0 {
		t.Fatalf("a blocked sender's code asked them to confirm: %+v", links)
	}
}
