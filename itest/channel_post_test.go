//go:build integration

package itest

import (
	"slices"
	"strings"
	"testing"
	"time"
)

// The operator posts into a channel from the control room (#549), which is
// in every channel. The post wakes the loops its text addresses among the
// channel's own, stays in that channel's timeline, and never reaches the
// fleet channel's. A reply must answer a message of the same channel, and a
// channel that does not exist is refused, as a send to it is.
func TestOperatorPostsIntoAChannel(t *testing.T) {
	t.Parallel()
	s := startServer(t, t.TempDir())
	for _, name := range []string{"aster", "briar", "cedar"} {
		s.createLoop(name, nil)
	}
	s.mustJSON("POST", "/api/channels", map[string]any{"name": "backend"}, nil)
	for _, name := range []string{"aster", "briar"} {
		if resp, body := s.do("PUT", "/api/channels/backend/loops/"+name, nil); resp.StatusCode != 204 {
			t.Fatalf("adding %s to backend = %d %s", name, resp.StatusCode, body)
		}
	}

	// cedar is named but outside backend, so the post does not reach it.
	const post = "@aster @cedar the schema moved"
	if resp, body := s.do("POST", "/api/channels/backend/messages", map[string]any{"text": post}); resp.StatusCode != 202 {
		t.Fatalf("posting into backend = %d %s, want 202", resp.StatusCode, body)
	}
	s.waitTurn("aster", 30*time.Second, func(tn turn) bool {
		return strings.Contains(tn.ResultText, "the schema moved")
	})

	var timeline []activityMessage
	s.mustJSON("GET", "/api/channels/backend/messages", nil, &timeline)
	if len(timeline) != 1 {
		t.Fatalf("backend's timeline = %s, want the one post", dump(timeline))
	}
	if m := timeline[0]; m.Text != post || m.Origin != "web" || m.Author != "operator" ||
		m.Conversation != "group" || m.Channel != "backend" ||
		!slices.Equal(m.DeliveredTo, []string{s.loop("aster").ID}) {
		t.Fatalf("the post = %s, want the operator's web post in backend, delivered to aster alone", dump(m))
	}
	var group []activityMessage
	s.mustJSON("GET", "/api/group", nil, &group)
	for _, m := range group {
		if m.Text == post {
			t.Fatalf("a post into backend is in the fleet channel's timeline: %s", dump(m))
		}
	}
	for _, tn := range s.completed("cedar") {
		if strings.Contains(tn.ResultText, "the schema moved") {
			t.Fatalf("cedar, outside backend, was woken by it: %s", dump(tn))
		}
	}

	// A reply addresses its target's author, which has to be a message of
	// this channel: one from the fleet channel is refused.
	if resp, body := s.do("POST", "/api/group", map[string]any{"text": "a fleet-wide note"}); resp.StatusCode != 202 {
		t.Fatalf("posting to the fleet channel = %d %s", resp.StatusCode, body)
	}
	fleet := s.activityWith("a fleet-wide note")
	if len(fleet) != 1 {
		t.Fatalf("the fleet channel post stored %d times, want 1", len(fleet))
	}
	resp, body := s.do("POST", "/api/channels/backend/messages", map[string]any{"text": "crossing over", "reply_to_id": fleet[0].ID})
	if resp.StatusCode != 400 || errorCode(body) != "cross_conversation_reply_to" {
		t.Fatalf("replying in backend to a fleet channel post = %d %s, want 400 cross_conversation_reply_to", resp.StatusCode, body)
	}
	const reply = "and the migration with it"
	if resp, body := s.do("POST", "/api/channels/backend/messages", map[string]any{"text": reply, "reply_to_id": timeline[0].ID}); resp.StatusCode != 202 {
		t.Fatalf("replying in backend to a backend post = %d %s, want 202", resp.StatusCode, body)
	}

	s.wantRefusal("POST", "/api/channels/nowhere/messages", map[string]any{"text": "anyone?"}, 404, "channel_not_found")
	s.wantRefusal("POST", "/api/channels/backend/messages", map[string]any{"text": "   "}, 400, "")
	for _, text := range []string{"crossing over", "anyone?"} {
		if got := s.activityWith(text); len(got) != 0 {
			t.Fatalf("a refused post %q was stored: %s", text, dump(got))
		}
	}

	// The fleet channel answers the same route, as it does the others.
	if resp, body := s.do("POST", "/api/channels/group/messages", map[string]any{"text": "the same post, by name"}); resp.StatusCode != 202 {
		t.Fatalf("posting into group by name = %d %s, want 202", resp.StatusCode, body)
	}
	s.mustJSON("GET", "/api/group", nil, &group)
	if !slices.ContainsFunc(group, func(m activityMessage) bool { return m.Text == "the same post, by name" }) {
		t.Fatalf("a post into group by name is missing from the fleet channel's timeline: %s", dump(group))
	}
}
