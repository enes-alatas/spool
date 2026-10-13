//go:build integration

package itest

import (
	"bufio"
	"context"
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
