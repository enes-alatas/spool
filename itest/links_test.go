//go:build integration

package itest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

// linkCode asks the control room for a link code on surface as the user
// session belongs to.
func (s *server) linkCode(session, surface string) string {
	s.t.Helper()
	resp, body := s.asCookie("POST", "/api/me/links/code", session, map[string]string{"surface": surface})
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
// control room to any loop's bot in a DM and confirming it there, and then
// reads the private conversations that are theirs (#703, ADR-0050,
// ADR-0051): their own dm:<name>, and what a loop they own said with them
// on owner_dm, never anyone else's. Neither DM is a message to the loop,
// a code links once, and a removed link reads nothing.
func TestALinkedMemberReadsTheirOwnPrivateConversations(t *testing.T) {
	t.Parallel()
	operator := user{ID: 8180, First: "Operator", Username: "operator"}
	friend := user{ID: 8181, First: "Friend", Username: "friend"}
	other := user{ID: 8182, First: "Other", Username: "other"}
	srv, tg := startTelegramFleet(t, operator, nil)
	noa := srv.signedInAs("noa", "member")
	srv.confirmLink(tg, "beta", other, noa, "noa")
	for person, text := range map[user]string{other: "other's secret", operator: "owner's secret"} {
		tg.dm("alpha", person, text)
		srv.waitForMessage(text)
	}
	mia := srv.signedInAs("mia", "member")
	if got := srv.memberActivity(mia); strings.Contains(got, "secret") {
		t.Fatalf("an unlinked member reads private conversations: %s", got)
	}

	code := srv.linkCode(mia, "telegram")
	tg.dm("alpha", friend, strings.ToUpper(code))
	tg.waitSent(t, friend.ID, "Telegram account to mia")
	tg.dm("alpha", friend, "yes")
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
	tg.dm("alpha", friend, "friend's secret")
	srv.waitForMessage("friend's secret")
	got := srv.memberActivity(mia)
	if !strings.Contains(got, "friend's secret") {
		t.Errorf("a member linked as friend does not read friend's DM: %s", got)
	}
	for _, other := range []string{"other's secret", "owner's secret"} {
		if strings.Contains(got, other) {
			t.Errorf("a member linked as friend reads %q: %s", other, got)
		}
	}
	if _, body := srv.do("GET", "/api/activity", nil); strings.Contains(strings.ToLower(string(body)), code) ||
		strings.Contains(string(body), `"yes"`) {
		t.Errorf("the link code or its YES was stored as a message: %s", body)
	}

	// A code links once, and nobody else.
	tg.dm("alpha", other, code)
	tg.waitSent(t, other.ID, "unknown, already used or expired")

	// The owner's account, unlinked from its own user and so a stranger
	// again, confirms a code from mia: her Telegram link moves to it. She
	// owns alpha now, as the first person a link allows does, and is no
	// longer friend.
	ownerSession, _ := srv.signIn(fmt.Sprintf("tg%d", operator.ID), fmt.Sprintf("tg%d's own password", operator.ID))
	if resp, body := srv.asCookie("DELETE", "/api/me/links/telegram", ownerSession, nil); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("the owner's user removing its link = %d %s", resp.StatusCode, body)
	}
	srv.confirmLink(tg, "alpha", operator, mia, "mia")
	got = srv.memberActivity(mia)
	if !strings.Contains(got, "owner's secret") {
		t.Errorf("a member linked as alpha's owner does not read its owner_dm: %s", got)
	}
	if strings.Contains(got, "friend's secret") {
		t.Errorf("a replaced link still reads: %s", got)
	}
	// A loop's next owner reads what it and they say privately from then
	// on, never what it said with the owner before them.
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

	if resp, _ := srv.asCookie("POST", "/api/me/links/code", "", map[string]string{"surface": "telegram"}); resp.StatusCode != http.StatusUnauthorized {
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

// The same link works on Slack: a code sent to a loop's app in a DM, and
// the YES after it, link the sender, who then reads their own DMs (#703,
// ADR-0051). Both answers come in the DM the code came in.
func TestASlackLinkReadsItsOwnDMs(t *testing.T) {
	t.Parallel()
	srv, slack := startSlackFleet(t)
	mia := srv.signedInAs("mia", "member")
	slack.pushMessage(t, slackAppToken, "im", "D0ALICE", "U0ALICE", srv.linkCode(mia, "slack"), "1727600000.000500")
	if post := slack.waitPost(t, "Slack account to mia"); post.Channel != "D0ALICE" {
		t.Fatalf("the question was asked in %q; want the DM the code came in", post.Channel)
	}
	slack.pushMessage(t, slackAppToken, "im", "D0ALICE", "U0ALICE", "YES", "1727600000.000501")
	if post := slack.waitPost(t, "now linked to mia"); post.Channel != "D0ALICE" {
		t.Fatalf("the link was answered in %q; want the DM it came in", post.Channel)
	}
	slack.pushMessage(t, slackAppToken, "im", "D0ALICE", "U0ALICE", "alice's secret", "1727600000.000502")
	srv.waitForMessage("alice's secret")
	if got := srv.memberActivity(mia); !strings.Contains(got, "alice's secret") {
		t.Errorf("a member linked as alice does not read her DM: %s", got)
	}
}
