//go:build integration

package itest

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// sseStream is an open /api/stream as one session reads it: every data
// line it has carried so far.
type sseStream struct {
	mu   sync.Mutex
	seen strings.Builder
	done chan struct{}
}

// openStream opens the hub's stream as session, until the test ends.
func (s *server) openStream(session string) *sseStream {
	s.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, "GET", s.baseURL+"/api/stream", nil)
	if err != nil {
		s.t.Fatal(err)
	}
	req.Header.Set("Cookie", "spool_operator="+session)
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		s.t.Fatalf("open stream: %v %v", resp, err)
	}
	stream := &sseStream{done: make(chan struct{})}
	go func() {
		defer close(stream.done)
		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 1<<20), 1<<20)
		for scanner.Scan() {
			if data, ok := strings.CutPrefix(scanner.Text(), "data: "); ok {
				stream.mu.Lock()
				stream.seen.WriteString(data + "\n")
				stream.mu.Unlock()
			}
		}
	}()
	s.t.Cleanup(func() {
		cancel()
		resp.Body.Close()
		<-stream.done
	})
	return stream
}

func (stream *sseStream) has(text string) bool {
	stream.mu.Lock()
	defer stream.mu.Unlock()
	return strings.Contains(stream.seen.String(), text)
}

// waitFor blocks until the stream has carried text.
func (stream *sseStream) waitFor(t *testing.T, text string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if stream.has(text) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("the stream never carried %q", text)
}

// Several members' streams each carry the private messages their user
// reads and no other (ADR-0050), with the check made outside the bus's
// publish (#721): a link made while a stream is open counts from its next
// message, and two streams of one user both follow it.
func TestMembersStreamsCarryOnlyTheirOwnPrivateMessages(t *testing.T) {
	t.Parallel()
	operator := user{ID: 8280, First: "Operator", Username: "operator"}
	alex := user{ID: 8281, First: "Alex", Username: "alex"}
	blair := user{ID: 8282, First: "Blair", Username: "blair"}
	srv, tg := startTelegramFleet(t, operator)
	mia := srv.signedInAs("mia", "member")
	bea := srv.signedInAs("bea", "member")
	srv.confirmLink(tg, "beta", alex, mia, "mia")

	streams := map[string]*sseStream{"mia's first": srv.openStream(mia), "mia's second": srv.openStream(mia),
		"bea's": srv.openStream(bea)}
	srv.message("alpha", "every stream is open")
	for _, stream := range streams {
		stream.waitFor(t, "every stream is open")
	}

	srv.confirmLink(tg, "beta", blair, bea, "bea")
	said := map[user]string{alex: "alex's private word", blair: "blair's private word", operator: "the owner's private word"}
	for person, text := range said {
		tg.dm("alpha", person, text)
		srv.waitForMessage(text)
	}
	srv.message("alpha", "every private word is in")
	for name, stream := range streams {
		stream.waitFor(t, "every private word is in")
		reads := map[string]bool{said[alex]: strings.HasPrefix(name, "mia"), said[blair]: name == "bea's"}
		for _, text := range said {
			if got := stream.has(text); got != reads[text] {
				t.Errorf("%s stream carries %q: %v; want %v", name, text, got, reads[text])
			}
		}
	}
}

// An admin's open stream follows their role (#722): made a member, it
// stops carrying the private messages they no longer read from the next
// one, and once they are removed it ends.
func TestAnOpenStreamFollowsItsUsersRole(t *testing.T) {
	t.Parallel()
	operator := user{ID: 8290, First: "Operator", Username: "operator"}
	srv, tg := startTelegramFleet(t, operator)
	ada := srv.signedInAs("ada", "admin")
	stream := srv.openStream(ada)
	srv.message("alpha", "ada's stream is open")
	stream.waitFor(t, "ada's stream is open")

	tg.dm("alpha", operator, "said before the demotion")
	stream.waitFor(t, "said before the demotion")

	srv.mustJSON("PATCH", "/api/users/ada", map[string]any{"role": "member"}, nil)
	tg.dm("alpha", operator, "said after the demotion")
	srv.waitForMessage("said after the demotion")
	srv.message("alpha", "the demotion has settled")
	stream.waitFor(t, "the demotion has settled")
	if stream.has("said after the demotion") {
		t.Fatal("a demoted admin's open stream carried the owner's DM")
	}

	srv.mustJSON("DELETE", "/api/users/ada", nil, nil)
	srv.message("alpha", "ada is gone")
	select {
	case <-stream.done:
	case <-time.After(20 * time.Second):
		t.Fatal("a removed user's stream stayed open")
	}
	if stream.has("ada is gone") {
		t.Fatal("a removed user's stream carried a message after the removal")
	}
}

// A stream ends with the session it opened under (#787): a password change
// ends the stream of every other session from its next item, the stream of
// the session the change keeps carries on, and signing out of that one ends
// its stream too.
func TestAnOpenStreamEndsWithItsSession(t *testing.T) {
	t.Parallel()
	operator := user{ID: 8300, First: "Operator", Username: "operator"}
	srv, _ := startTelegramFleet(t, operator)
	kept := srv.signedInAs("ada", "admin")
	other, _ := srv.signIn("ada", "ada's own password")
	keptStream, otherStream := srv.openStream(kept), srv.openStream(other)
	srv.message("alpha", "both of ada's streams are open")
	keptStream.waitFor(t, "both of ada's streams are open")
	otherStream.waitFor(t, "both of ada's streams are open")

	if resp, body := srv.asCookie("POST", "/api/me/password", kept, map[string]string{
		"current_password": "ada's own password", "new_password": "ada's newer password"}); resp.StatusCode != http.StatusOK {
		t.Fatalf("ada's change = %d %s", resp.StatusCode, body)
	}
	srv.message("alpha", "ada changed the password")
	keptStream.waitFor(t, "ada changed the password")
	ended(t, otherStream, "a session the password change ended")

	if resp, body := srv.asCookie("POST", "/api/logout", kept, nil); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("ada's sign-out = %d %s", resp.StatusCode, body)
	}
	srv.message("alpha", "ada signed out")
	ended(t, keptStream, "a session signed out of")
}

// ended waits for stream to end, and fails if it ends late.
func ended(t *testing.T, stream *sseStream, whose string) {
	t.Helper()
	select {
	case <-stream.done:
	case <-time.After(20 * time.Second):
		t.Fatalf("the stream of %s stayed open", whose)
	}
}

// A member's stream carries a reaction, a vote or a poll's close only on a
// message they read, as it carries the message (#922): none on the owner's
// private chat with a loop, whether a person or the loop reacted or voted,
// and each on a group message. An admin's stream carries them all.
func TestAMembersStreamCarriesNoReactionOrVoteOnAPrivateMessage(t *testing.T) {
	t.Parallel()
	operator := user{ID: 8290, First: "Operator", Username: "operator"}
	srv, tg := startTelegramFleet(t, operator)
	member := srv.openStream(srv.signedInAs("mia", "member"))
	admin := srv.openStream(srv.signedInAs("ada", "admin"))
	alpha := mcpSession(t, srv, hubMCPToken(t, srv, "alpha"))
	tg.dm("alpha", operator, "hi alpha")
	waitOwnerDMReady(t, srv, "alpha")
	send := func(args map[string]any) {
		t.Helper()
		if res := callSend(t, alpha, args); res.IsError {
			t.Fatalf("send %v refused: %s", args, resultText(res))
		}
	}
	idOf := func(text string) int64 {
		t.Helper()
		srv.waitForMessage(text)
		return srv.activityWith(text)[0].ID
	}
	telegramPoll := func(question string) string {
		t.Helper()
		var id string
		eventually(t, "the poll sent on Telegram", func() bool {
			polls, _ := tg.polls()
			for _, poll := range polls {
				if poll.Question == question {
					id = poll.PollID
					return true
				}
			}
			return false
		})
		return id
	}
	reaction := func(messageID int64) string { return fmt.Sprintf(`"message_id":%d,"reactor_key"`, messageID) }
	ballot := func(pollID int64) string { return fmt.Sprintf(`"poll_id":%d,`, pollID) }

	// on the owner's private chat: the owner's reaction and vote, the loop's
	// own, and the poll's close
	const ask = "a private ask"
	send(map[string]any{"destination": "owner_dm", "text": ask})
	sent := tg.waitSentFrom(t, operator.ID, "alpha", ask)
	private := idOf(ask)
	tg.react("alpha", operator.ID, sent.MessageID, operator, nil, []string{"👍"})
	srv.waitReactions(private, "telegram:8290 👍")
	send(map[string]any{"destination": "owner_dm", "reply_to": fmt.Sprintf("ref:%d", private), "react": "🎉"})
	const privateQuestion = "a private poll?"
	send(map[string]any{"destination": "owner_dm", "text": privateQuestion,
		"poll": map[string]any{"options": []string{"yes", "no"}, "closes_in": "2h"}})
	privatePoll := idOf(privateQuestion)
	tg.answerPoll("alpha", telegramPoll(privateQuestion), operator, 0)
	admin.waitFor(t, ballot(privatePoll))
	send(map[string]any{"destination": "owner_dm", "reply_to": fmt.Sprintf("ref:%d", privatePoll), "vote": []int{2}})
	send(map[string]any{"destination": "owner_dm", "close_poll": fmt.Sprintf("ref:%d", privatePoll)})
	admin.waitFor(t, fmt.Sprintf(`"poll_id":%d,"closed":true`, privatePoll))

	// in the group: a reaction and a vote
	const groupAsk = "@alpha a group ask"
	tg.post(groupChatID, "supergroup", groupAsk, operator)
	group := idOf(groupAsk)
	send(map[string]any{"destination": "group", "reply_to": fmt.Sprintf("ref:%d", group), "react": "👀"})
	const groupQuestion = "@beta a group poll?"
	send(map[string]any{"destination": "group", "text": groupQuestion, "poll": map[string]any{"options": []string{"yes", "no"}}})
	groupPoll := idOf(groupQuestion)
	send(map[string]any{"destination": "group", "reply_to": fmt.Sprintf("ref:%d", groupPoll), "vote": []int{1}})

	for _, want := range []string{reaction(private), `"emoji":"🎉"`, reaction(group), ballot(groupPoll)} {
		admin.waitFor(t, want)
	}
	member.waitFor(t, reaction(group))
	member.waitFor(t, ballot(groupPoll))
	srv.message("alpha", "every reaction and vote is in")
	member.waitFor(t, "every reaction and vote is in")
	for _, private := range []string{reaction(private), ballot(privatePoll), `"emoji":"👍"`, `"emoji":"🎉"`} {
		if member.has(private) {
			t.Errorf("a member's stream carried %s from the owner's private chat", private)
		}
	}
}
