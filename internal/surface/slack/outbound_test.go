package slack

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/enes-alatas/spool/internal/route"
	"github.com/enes-alatas/spool/internal/store"
)

// A loop's text goes out as mrkdwn: Slack's three markup characters are
// escaped, so a loop cannot post markup by accident, and an @ naming a
// loop's app or an allowed sender becomes the mention Slack notifies on.
func TestSlackText(t *testing.T) {
	adapter, _, _ := inboundFixture(t)
	cases := []struct{ in, want string }{
		{"@milo status?", "<@U0milo> status?"},
		{"thanks @Alice.", "thanks <@U0ALICE>."},
		{"cc @nobody and @all", "cc @nobody and @all"},
		{"a <b> && c", "a &lt;b&gt; &amp;&amp; c"},
		{"<@U0milo> is not a mention a loop writes", "&lt;@U0milo&gt; is not a mention a loop writes"},
		{"mail me@example.com", "mail me@example.com"},
	}
	for _, testCase := range cases {
		if got := adapter.slackText(context.Background(), testCase.in); got != testCase.want {
			t.Errorf("slackText(%q) = %q, want %q", testCase.in, got, testCase.want)
		}
	}
}

// A long message is split where Slack takes it whole: at a line break when
// one is near, and never inside a character.
func TestSplit(t *testing.T) {
	if got := split("short", 10); len(got) != 1 || got[0] != "short" {
		t.Fatalf("split(short) = %q", got)
	}
	lines := split("aaaaaaa\nbbbbbbb\nccc", 10)
	if strings.Join(lines, "|") != "aaaaaaa|bbbbbbb|ccc" {
		t.Errorf("split at line breaks = %q", lines)
	}
	text := strings.Repeat("ç", 20) // two bytes each, no line breaks
	var joined string
	for _, piece := range split(text, 7) {
		if len(piece) > 7 || !utf8.ValidString(piece) {
			t.Fatalf("piece %q is over the limit or cut inside a character", piece)
		}
		joined += piece
	}
	if joined != text {
		t.Errorf("pieces rejoin to %q, want the text back", joined)
	}
}

// A loop's reply goes in the thread of what it answers when that is on
// Slack in the same channel: under the thread's first message, since a
// Slack thread is one level deep. Anything else gets the quoted line.
func TestRenderThreadsAReply(t *testing.T) {
	adapter, db, _ := inboundFixture(t)
	ctx := context.Background()
	insert := func(msg *store.Message) *store.Message {
		t.Helper()
		msg.Conversation = store.ConversationGroup
		if err := db.Messages().Insert(ctx, msg); err != nil {
			t.Fatal(err)
		}
		return msg
	}
	root := insert(&store.Message{Origin: store.OriginSlackChannel, Author: "alice", Text: "who has the deploy?",
		SlackChannelID: "C0FLEET", SlackTS: "1.1"})
	inThread := insert(&store.Message{Origin: store.OriginSlackChannel, Author: "bob", Text: "not me",
		SlackChannelID: "C0FLEET", SlackTS: "1.2", ReplyToID: root.ID})
	fromWeb := insert(&store.Message{Origin: store.OriginWeb, Author: "enes", Text: "and the release notes?"})

	cases := []struct {
		name       string
		replyTo    int64
		channel    string
		wantThread string
		wantQuote  bool
	}{
		{"a channel message", root.ID, "C0FLEET", "1.1", false},
		{"a reply in its thread", inThread.ID, "C0FLEET", "1.1", false},
		{"a message in another channel", root.ID, "D0OWNER", "", true},
		{"a message not on Slack", fromWeb.ID, "C0FLEET", "", true},
	}
	for _, testCase := range cases {
		mp := &route.MessagePayload{Message: store.Message{Origin: store.OriginLoop, FromLoopID: "loop_terra",
			Text: "on it", ReplyToID: testCase.replyTo}}
		thread, text := adapter.render(ctx, mp, testCase.channel)
		if thread != testCase.wantThread {
			t.Errorf("%s: thread %q, want %q", testCase.name, thread, testCase.wantThread)
		}
		if quoted := strings.HasPrefix(text, "↳ re "); quoted != testCase.wantQuote || !strings.HasSuffix(text, "on it") {
			t.Errorf("%s: text %q, quoted %v, want quoted %v", testCase.name, text, quoted, testCase.wantQuote)
		}
	}
}

// landThenStop answers every post as Slack does when the message landed,
// and stops the app as it does: a detach or swap arriving with the
// response.
type landThenStop struct{ stop context.CancelFunc }

func (answer landThenStop) RoundTrip(*http.Request) (*http.Response, error) {
	answer.stop()
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"ok":true,"ts":"9.9"}`)),
	}, nil
}

// A message that landed as its app stopped is recorded as landed. Blaming
// the stop would be false, and the operator's retry would post it twice.
func TestASendThatLandsAsItsAppStopsIsSent(t *testing.T) {
	adapter, db, _ := inboundFixture(t)
	ctx := context.Background()
	adapter.ctx = ctx // the hub is running
	linkCtx, stop := context.WithCancel(ctx)
	defer stop()
	adapter.client.http = &http.Client{Transport: landThenStop{stop: stop}}
	msg := &store.Message{Origin: store.OriginLoop, FromLoopID: "loop_terra", Conversation: store.ConversationGroup,
		Text: "landed", Mirror: store.MirrorPending}
	if err := db.Messages().Insert(ctx, msg); err != nil {
		t.Fatal(err)
	}

	adapter.send(linkCtx, &route.MessagePayload{Message: *msg})

	got, err := db.Messages().Get(ctx, msg.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Mirror != store.MirrorMirrored || got.SlackTS != "9.9" || got.SendError != "" {
		t.Fatalf("settled as mirror=%q ts=%q err=%q, want mirrored at 9.9 with no failure",
			got.Mirror, got.SlackTS, got.SendError)
	}
}
