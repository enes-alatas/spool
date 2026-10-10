//go:build integration

package itest

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

// linkCode asks the control room for a link code as the user session
// belongs to.
func (s *server) linkCode(session string) string {
	s.t.Helper()
	resp, body := s.asCookie("POST", "/api/me/links/code", session, nil)
	if resp.StatusCode != http.StatusCreated {
		s.t.Fatalf("link code = %d %s", resp.StatusCode, body)
	}
	var out struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(body, &out); err != nil || out.Code == "" {
		s.t.Fatalf("link code answer %s: %v", body, err)
	}
	return out.Code
}

// memberActivity is the text of every message in Activity as session
// reads it.
func (s *server) memberActivity(session string) string {
	s.t.Helper()
	resp, body := s.asCookie("GET", "/api/activity", session, nil)
	if resp.StatusCode != http.StatusOK {
		s.t.Fatalf("activity = %d %s", resp.StatusCode, body)
	}
	return string(body)
}

// A user links their own Telegram identity by sending a code from the
// control room to any loop's bot in a DM, and then reads the private
// conversations that are theirs (#703, ADR-0050): their own dm:<name>,
// and what a loop they own said with them on owner_dm, never anyone
// else's. The code is not a message to the loop, links once, and a
// removed link reads nothing.
func TestALinkedMemberReadsTheirOwnPrivateConversations(t *testing.T) {
	t.Parallel()
	operator := user{ID: 8180, First: "Operator", Username: "operator"}
	friend := user{ID: 8181, First: "Friend", Username: "friend"}
	other := user{ID: 8182, First: "Other", Username: "other"}
	srv, tg := startTelegramFleet(t, operator, nil)
	for _, person := range []user{friend, other} {
		tg.dm("beta", person, "knock") // registers them as pending
		srv.allowSender(person.ID)
	}
	for person, text := range map[user]string{friend: "friend's secret", other: "other's secret", operator: "owner's secret"} {
		tg.dm("alpha", person, text)
		srv.waitForMessage(text)
	}
	mia := srv.signedInAs("mia", "member")
	if got := srv.memberActivity(mia); strings.Contains(got, "secret") {
		t.Fatalf("an unlinked member reads private conversations: %s", got)
	}

	code := srv.linkCode(mia)
	tg.dm("alpha", friend, strings.ToUpper(code))
	tg.waitSent(t, friend.ID, "now linked to mia")
	var links []struct {
		Surface  string `json:"surface"`
		ID       string `json:"id"`
		Username string `json:"username"`
	}
	if resp, body := srv.asCookie("GET", "/api/me/links", mia, nil); resp.StatusCode != http.StatusOK ||
		json.Unmarshal(body, &links) != nil || len(links) != 1 || links[0].Surface != "telegram" ||
		links[0].ID != strconv.FormatInt(friend.ID, 10) || links[0].Username != "friend" {
		t.Fatalf("mia's links = %d %s; want friend on telegram", resp.StatusCode, body)
	}
	got := srv.memberActivity(mia)
	if !strings.Contains(got, "friend's secret") {
		t.Errorf("a member linked as friend does not read friend's DM: %s", got)
	}
	for _, other := range []string{"other's secret", "owner's secret"} {
		if strings.Contains(got, other) {
			t.Errorf("a member linked as friend reads %q: %s", other, got)
		}
	}
	if _, body := srv.do("GET", "/api/activity", nil); strings.Contains(strings.ToLower(string(body)), code) {
		t.Errorf("the link code was stored as a message: %s", body)
	}

	// A code links once, and nobody else.
	tg.dm("alpha", other, code)
	tg.waitSent(t, other.ID, "unknown, already used or expired")

	// A new code sent from the owner's account replaces mia's Telegram
	// link: she owns alpha now, and is no longer friend.
	tg.dm("alpha", operator, srv.linkCode(mia))
	tg.waitSent(t, operator.ID, "now linked to mia")
	got = srv.memberActivity(mia)
	if !strings.Contains(got, "owner's secret") {
		t.Errorf("a member linked as alpha's owner does not read its owner_dm: %s", got)
	}
	if strings.Contains(got, "friend's secret") {
		t.Errorf("a replaced link still reads: %s", got)
	}
	// A loop's next owner reads what it and they say privately from then
	// on, never what it said with the owner before them.
	noa := srv.signedInAs("noa", "member")
	tg.dm("alpha", other, srv.linkCode(noa))
	tg.waitSent(t, other.ID, "now linked to noa")
	srv.mustJSON("PUT", "/api/loops/alpha/owner", map[string]any{"tg_user_id": other.ID}, nil)
	tg.dm("alpha", other, "the new owner's secret")
	srv.waitForMessage("the new owner's secret")
	got = srv.memberActivity(noa)
	if !strings.Contains(got, "the new owner's secret") || !strings.Contains(got, "other's secret") {
		t.Errorf("the new owner does not read their own private messages: %s", got)
	}
	if strings.Contains(got, "\"owner's secret") {
		t.Errorf("the new owner reads the previous owner's owner_dm: %s", got)
	}
	if got := srv.memberActivity(mia); !strings.Contains(got, "\"owner's secret") || strings.Contains(got, "the new owner's secret") {
		t.Errorf("the previous owner lost their own owner_dm, or reads the new one's: %s", got)
	}

	if resp, _ := srv.asCookie("POST", "/api/me/links/code", "", nil); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("a link code with no session = %d; want 401", resp.StatusCode)
	}

	if resp, body := srv.asCookie("DELETE", "/api/me/links/telegram", mia, nil); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("removing the link = %d %s", resp.StatusCode, body)
	}
	if got := srv.memberActivity(mia); strings.Contains(got, "secret") {
		t.Errorf("a removed link still reads: %s", got)
	}
	if resp, _ := srv.asCookie("DELETE", "/api/me/links/telegram", mia, nil); resp.StatusCode != http.StatusNotFound {
		t.Errorf("removing a removed link = %d; want 404", resp.StatusCode)
	}
}

// The same link works on Slack: a code sent to a loop's app in a DM links
// the sender, who then reads their own DMs (#703).
func TestASlackLinkReadsItsOwnDMs(t *testing.T) {
	t.Parallel()
	srv, slack := startSlackFleet(t)
	slack.pushMessage(t, slackAppToken, "channel", slackChannel, "U0ALICE", "hi all", "1727600000.000500")
	srv.allowSlackSender("U0ALICE")
	slack.pushMessage(t, slackAppToken, "im", "D0ALICE", "U0ALICE", "alice's secret", "1727600000.000501")
	srv.waitForMessage("alice's secret")

	mia := srv.signedInAs("mia", "member")
	slack.pushMessage(t, slackAppToken, "im", "D0ALICE", "U0ALICE", srv.linkCode(mia), "1727600000.000502")
	if post := slack.waitPost(t, "now linked to mia"); post.Channel != "D0ALICE" {
		t.Fatalf("the link was answered in %q; want the DM it came in", post.Channel)
	}
	if got := srv.memberActivity(mia); !strings.Contains(got, "alice's secret") {
		t.Errorf("a member linked as alice does not read her DM: %s", got)
	}
}
