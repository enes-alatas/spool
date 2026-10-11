//go:build integration

package itest

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

// privateConversation is one entry of a loop's private conversations list.
type privateConversation struct {
	Conversation string `json:"conversation"`
	Name         string `json:"name"`
	PersonID     string `json:"person_id"`
	LastAt       int64  `json:"last_at"`
}

// privateConversations is alpha's list as session reads it, the operator
// token's for "".
func (s *server) privateConversations(session string) []privateConversation {
	s.t.Helper()
	resp, body := s.asCookie("GET", "/api/loops/alpha/conversations", session, nil)
	if session == "" {
		resp, body = s.do("GET", "/api/loops/alpha/conversations", nil)
	}
	var out []privateConversation
	if resp.StatusCode != http.StatusOK || json.Unmarshal(body, &out) != nil || out == nil {
		s.t.Fatalf("conversations = %d %s; want a list", resp.StatusCode, body)
	}
	return out
}

// thread is the text of alpha's conversation as session reads it, the
// operator token's for "".
func (s *server) thread(session, conversation string) string {
	s.t.Helper()
	path := "/api/loops/alpha/conversation?conversation=" + url.QueryEscape(conversation)
	resp, body := s.asCookie("GET", path, session, nil)
	if session == "" {
		resp, body = s.do("GET", path, nil)
	}
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(string(body), "[") {
		s.t.Fatalf("%s = %d %s; want a list", conversation, resp.StatusCode, body)
	}
	return strings.TrimSpace(string(body))
}

// A loop's private conversations are served per person (#720): an admin
// lists everyone the loop holds one with and reads each thread by name; a
// member lists and reads only their own; a thread keeps the messages its
// person wrote under an older name; and a name the caller may not read
// answers an empty thread, as a name nobody answers to does.
func TestALoopsPrivateConversationsAreServedPerPerson(t *testing.T) {
	t.Parallel()
	operator := user{ID: 8190, First: "Operator", Username: "operator"}
	friend := user{ID: 8191, First: "Friend", Username: "friend"}
	other := user{ID: 8192, First: "Other", Username: "other"}
	srv, tg := startTelegramFleet(t, operator, nil)
	mia := srv.signedInAs("mia", "member")
	srv.confirmLink(tg, "beta", friend, mia, "mia")
	srv.allowSender(tg, "beta", other)
	for _, said := range []struct {
		from user
		text string
	}{{friend, "friend's secret"}, {other, "other's secret"}, {operator, "owner's secret"}} {
		tg.dm("alpha", said.from, said.text)
		srv.waitForMessage(said.text)
	}
	// friend changes their handle on Telegram; the thread is still theirs.
	tg.dm("alpha", user{ID: friend.ID, First: "Friend", Username: "pal"}, "pal's secret")
	srv.waitForMessage("pal's secret")

	id := func(u user) string { return strconv.FormatInt(u.ID, 10) }
	got := srv.privateConversations("")
	want := []privateConversation{
		{Conversation: "dm:friend", Name: "friend", PersonID: id(friend)},
		{Conversation: "owner_dm", Name: "operator", PersonID: id(operator)},
		{Conversation: "dm:other", Name: "other", PersonID: id(other)},
	}
	if len(got) != len(want) {
		t.Fatalf("an admin's list = %+v; want %+v", got, want)
	}
	for i := range want {
		if got[i].LastAt == 0 {
			t.Errorf("entry %d has no last_at: %+v", i, got[i])
		}
		got[i].LastAt = 0
		if got[i] != want[i] {
			t.Errorf("an admin's list[%d] = %+v; want %+v", i, got[i], want[i])
		}
	}
	thread := srv.thread("", "dm:friend")
	if !strings.Contains(thread, "friend's secret") || !strings.Contains(thread, "pal's secret") {
		t.Errorf("dm:friend lost a message written under either name: %s", thread)
	}
	if strings.Contains(thread, "other's secret") || strings.Contains(thread, "owner's secret") {
		t.Errorf("dm:friend holds someone else's messages: %s", thread)
	}
	if thread := srv.thread("", "dm:@Other"); !strings.Contains(thread, "other's secret") {
		t.Errorf("dm:@Other does not read as dm:other, as a send would: %s", thread)
	}
	if thread := srv.thread("", "dm:nobody"); thread != "[]" {
		t.Errorf("a name nobody answers to = %s; want an empty thread", thread)
	}

	// A member linked as friend lists and reads only their own.
	if got := srv.privateConversations(mia); len(got) != 1 || got[0].Conversation != "dm:friend" || got[0].PersonID != id(friend) {
		t.Errorf("a member linked as friend lists %+v; want only dm:friend", got)
	}
	if thread := srv.thread(mia, "dm:friend"); !strings.Contains(thread, "pal's secret") {
		t.Errorf("a member linked as friend cannot read dm:friend: %s", thread)
	}
	for _, conversation := range []string{"dm:other", "owner_dm"} {
		if thread := srv.thread(mia, conversation); thread != "[]" {
			t.Errorf("a member linked as friend reads %s: %s", conversation, thread)
		}
	}

	// An unlinked member lists nothing and reads nothing.
	noa := srv.signedInAs("noa", "member")
	if got := srv.privateConversations(noa); len(got) != 0 {
		t.Errorf("an unlinked member lists %+v; want none", got)
	}
	if thread := srv.thread(noa, "dm:friend"); thread != "[]" {
		t.Errorf("an unlinked member reads dm:friend: %s", thread)
	}

	if resp, body := srv.do("GET", "/api/loops/alpha/conversation?conversation=group", nil); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("conversation=group = %d %s; want 400", resp.StatusCode, body)
	}
}
