//go:build integration

package itest

import (
	"strings"
	"testing"
	"time"

	"github.com/enes-alatas/spool/internal/loop"
)

// messageRef renders a stored message's reply reference the way an envelope
// header gives it to a loop.
func messageRef(id int64) string { return loop.MessageRef(id) }

// messageTurns counts the turns a loop has run off an inbound message.
// Comparing it across an event is how a test says "this did not wake it",
// without mistaking the turn that drove the setup for the one under test.
func messageTurns(srv *server, loopName string) int {
	n := 0
	for _, tn := range srv.completed(loopName) {
		if tn.Trigger == "message" {
			n++
		}
	}
	return n
}

// Explicit message references and native replies (#79, ADR-0025). A
// reference identifies one message durably; a native reply renders only
// where the sending bot owns Telegram's id for the target, and the quote
// line stands in for it everywhere else. Every scenario asserts who does
// *not* receive the message as well as who does.

// A human's native reply to a loop, with no mention at all, is addressed to
// that loop and to nobody else.
func TestNativeReplyToLoopReachesThatLoopOnly(t *testing.T) {
	operator := user{ID: 5151, First: "Operator", Username: "operator"}
	// alpha answers the creation tick, then posts to the group
	wsAlpha := workspaceWithScript(t, "!ctx 0\n"+
		`!send {"destination":"group","text":"@operator deploy is green"}`+"\n")
	srv, tg := startTelegramFleet(t, operator, map[string]any{"workspace_path": wsAlpha})

	srv.message("alpha", "report")
	target := tg.sentPost(t, groupChatID, "deploy is green")

	tg.postReply(groupChatID, "supergroup", "nice, ship it", operator, target)
	srv.waitForMessage("nice, ship it")

	stored := srv.activityWith("nice, ship it")
	if len(stored) != 1 {
		t.Fatalf("reply stored %d times, want 1", len(stored))
	}
	if got := stored[0].DeliveredTo; len(got) != 1 {
		t.Fatalf("delivered_to = %v, want alpha alone", got)
	}
	time.Sleep(2 * time.Second)
	for _, tn := range srv.completed("beta") {
		if tn.Trigger == "message" {
			t.Fatalf("an unaddressed loop was woken by the reply: %s", dump(tn))
		}
	}
}

// A loop replying to a peer's group post addresses that peer without a
// mention. No bot receives another bot's message, so the reply cannot thread
// natively: it is posted plainly, quoting what it answers (ADR-0025
// amendment).
func TestLoopReplyToPeerAddressesItAndQuotes(t *testing.T) {
	operator := user{ID: 5252, First: "Operator", Username: "operator"}
	wsAlpha := workspaceWithScript(t, "!ctx 0\n"+
		`!send {"destination":"group","text":"@beta who owns the migration?"}`+"\n")
	// beta answers its own creation tick, then replies to alpha's question
	wsBeta := workspaceWithScript(t, "!ctx 0\n"+
		`!send {"destination":"group","text":"I do","reply_to":"$ref"}`+"\n")
	srv, tg := startTelegramFleet(t, operator,
		map[string]any{"workspace_path": wsAlpha}, map[string]any{"workspace_path": wsBeta})

	srv.message("alpha", "ask")

	sent := tg.waitSent(t, groupChatID, "I do")
	if sent.Token != "beta" {
		t.Fatalf("reply posted by bot %q, want beta's own", sent.Token)
	}
	if sent.ReplyTo != 0 {
		t.Fatalf("reply threaded under id %d; a bot holds no id for another bot's post", sent.ReplyTo)
	}
	if !strings.HasPrefix(sent.Text, "↳ re alpha: @beta who owns the migration?") {
		t.Fatalf("reply does not quote what it answers: %q", sent.Text)
	}

	stored := srv.activityWith(sent.Text[strings.Index(sent.Text, "I do"):])
	if len(stored) == 0 {
		t.Fatal("beta's reply not stored")
	}
	if got := stored[0].DeliveredTo; len(got) != 1 {
		t.Fatalf("delivered_to = %v, want alpha alone — the author it replied to", got)
	}
}

// The shape #243 was filed for: a human natively replies to a loop's post
// that itself quoted a peer, and the bot that ingests the reply is not the
// one that posted it. Nothing about it resolves by id — the ingesting bot
// holds none for a peer's post — and the text Telegram embeds is the text
// as sent, quote line and all, which is not what the store holds. The
// author must still be woken.
func TestNativeReplyToAQuotedLoopReplyWakesItsAuthor(t *testing.T) {
	operator := user{ID: 5656, First: "Operator", Username: "operator"}
	// Each script speaks once and then only echoes: the last line repeats
	// for every later turn, and two loops that keep addressing each other
	// would answer forever, drowning the one wake this test is counting.
	wsAlpha := workspaceWithScript(t, "!ctx 0\n"+
		`!send {"destination":"group","text":"@beta who owns the migration?"}`+"\n!echo\n")
	wsBeta := workspaceWithScript(t, "!ctx 0\n"+
		`!send {"destination":"group","text":"I do","reply_to":"$ref"}`+"\n!echo\n")
	srv, tg := startTelegramFleet(t, operator,
		map[string]any{"workspace_path": wsAlpha}, map[string]any{"workspace_path": wsBeta})

	srv.message("alpha", "ask")

	// beta's reply carries the quote line, because no bot can thread under
	// another bot's post. alpha is the group's ingest bot — elected by loop
	// id, not by who posted — so the reply below arrives at a bot holding
	// no id for what it answers.
	betas := tg.sentPostFrom(t, groupChatID, "beta", "I do")
	if !strings.HasPrefix(betas.text, "↳ re alpha:") {
		t.Fatalf("the case needs a quoted post to reply to, got %q", betas.text)
	}

	betaTurns, alphaTurns := messageTurns(srv, "beta"), messageTurns(srv, "alpha")

	const reply = "good, start with the index"
	tg.postReply(groupChatID, "supergroup", reply, operator, betas)
	srv.waitForMessage(reply)

	stored := srv.activityWith(reply)
	if len(stored) != 1 {
		t.Fatalf("reply stored %d times, want 1", len(stored))
	}
	if stored[0].ReplyToID == 0 {
		t.Error("the reply resolved to no target: the quote line is still being matched verbatim")
	}
	if got := stored[0].DeliveredTo; len(got) != 1 {
		t.Fatalf("delivered_to = %v, want beta alone — the author of what was replied to", got)
	}
	time.Sleep(2 * time.Second)
	if messageTurns(srv, "beta") == betaTurns {
		t.Error("beta ran no turn for a reply addressed to it")
	}
	if got := messageTurns(srv, "alpha"); got != alphaTurns {
		t.Errorf("the ingesting bot's loop woke on a reply to a peer: %d message turns, want %d", got, alphaTurns)
	}
}

// A loop replying to a human's group message threads natively when its own
// poller saw that message, and never borrows another bot's id.
func TestLoopReplyToHumanThreadsNatively(t *testing.T) {
	operator := user{ID: 5353, First: "Operator", Username: "operator"}
	wsAlpha := workspaceWithScript(t, "!ctx 0\n"+
		`!send {"destination":"group","text":"@operator on it","reply_to":"$ref"}`+"\n")
	srv, tg := startTelegramFleet(t, operator, map[string]any{"workspace_path": wsAlpha})

	const asks = "@alpha can you take the migration?"
	asked := tg.post(groupChatID, "supergroup", asks, operator)
	srv.waitForMessage(asks)

	sent := tg.waitSent(t, groupChatID, "on it")
	want := asked.ids[sent.Token]
	if sent.ReplyTo != want {
		t.Fatalf("threaded under id %d, want %d — this bot's own id for the message", sent.ReplyTo, want)
	}
	if strings.Contains(sent.Text, "↳") {
		t.Fatalf("a natively threaded reply must not also quote: %q", sent.Text)
	}
}

// References are durable: the mapping a reply needs survives a restart of
// the orchestrator, and still resolves to the same message.
func TestReplyReferencesSurviveRestart(t *testing.T) {
	operator := user{ID: 5454, First: "Operator", Username: "operator"}
	dir := t.TempDir()
	tg := startFakeTelegram(t, "alpha", "beta")
	srv := startTelegramServer(t, dir, tg)
	for _, name := range []string{"alpha", "beta"} {
		srv.createLoop(name, map[string]any{"tg_bot_token": name})
	}
	tg.post(groupChatID, "supergroup", "hello", operator)
	srv.allowSender(operator.ID)
	tg.post(groupChatID, "supergroup", "binding", operator)
	settleBindings()

	const asks = "@alpha status of the index?"
	asked := tg.post(groupChatID, "supergroup", asks, operator)
	srv.waitForMessage(asks)
	srv.stop()

	// a fresh orchestrator on the same data directory
	srv2 := startTelegramServer(t, dir, tg)
	settleBindings()
	ref := ""
	for _, m := range srv2.activity() {
		if m.Text == asks {
			ref = messageRef(m.ID)
		}
	}
	if ref == "" {
		t.Fatal("the message did not survive the restart")
	}
	sess := mcpSession(t, srv2, hubMCPToken(t, srv2, "alpha"))
	res := callSend(t, sess, map[string]any{
		"destination": "group", "text": "@operator rebuilt overnight", "reply_to": ref})
	if res.IsError {
		t.Fatalf("a reference minted before the restart was refused: %s", resultText(res))
	}
	sent := tg.waitSent(t, groupChatID, "rebuilt overnight")
	if want := asked.ids[sent.Token]; sent.ReplyTo != want {
		t.Fatalf("threaded under id %d, want %d: the surface mapping did not survive", sent.ReplyTo, want)
	}
}

// Two loops can post the same words, and no bot holds an id for another
// bot's post. Telegram names the bot that posted the message being replied
// to, though, and a bot belongs to exactly one loop — so the words no
// longer have to carry the identification on their own. #79 protected
// "never wake a loop about a message it did not write" by declining to
// answer; the author is known now, so the same property holds with the
// reply delivered (#243).
func TestNativeReplyToIdenticalWordsWakesTheOneRepliedTo(t *testing.T) {
	operator := user{ID: 5555, First: "Operator", Username: "operator"}
	const ack = "@operator on it"
	ws := workspaceWithScript(t, "!ctx 0\n"+`!send {"destination":"group","text":"`+ack+`"}`+"\n")
	srv, tg := startTelegramFleet(t, operator,
		map[string]any{"workspace_path": ws}, map[string]any{"workspace_path": ws})

	srv.message("alpha", "ack")
	tg.waitSentFrom(t, groupChatID, "alpha", "on it")
	srv.message("beta", "ack")
	// beta is not the group's ingest bot, so the bot that receives the
	// reply holds no id for beta's post — and alpha posted the same words.
	betas := tg.sentPostFrom(t, groupChatID, "beta", "on it")

	betaTurns, alphaTurns := messageTurns(srv, "beta"), messageTurns(srv, "alpha")

	const reply = "thanks, both of you"
	tg.postReply(groupChatID, "supergroup", reply, operator, betas)
	srv.waitForMessage(reply)

	stored := srv.activityWith(reply)
	if len(stored) != 1 {
		t.Fatalf("reply stored %d times, want 1", len(stored))
	}
	if stored[0].ReplyToID == 0 {
		t.Error("identical words left the target unresolved, though the poster was named")
	}
	if got := stored[0].DeliveredTo; len(got) != 1 {
		t.Fatalf("delivered_to = %v, want beta alone — the loop whose post was replied to", got)
	}
	time.Sleep(2 * time.Second)
	if messageTurns(srv, "beta") == betaTurns {
		t.Error("beta ran no turn for a reply to its own post")
	}
	if got := messageTurns(srv, "alpha"); got != alphaTurns {
		t.Errorf("the loop that merely said the same words woke: %d message turns, want %d", got, alphaTurns)
	}
}

// A reply plus a mention delivers once to each addressed loop, and to no
// other: the original's own recipients are never inherited.
func TestReplyPlusMentionDeliversOnceToEach(t *testing.T) {
	s := startServer(t, t.TempDir())
	for _, name := range []string{"aster", "briar", "cedar", "dahlia"} {
		s.createLoop(name, nil)
	}
	briar := mcpSession(t, s, hubMCPToken(t, s, "briar"))
	res := callSend(t, briar, map[string]any{
		"destination": "group", "text": "@aster @dahlia planning tomorrow"})
	if res.IsError {
		t.Fatalf("group send refused: %s", resultText(res))
	}
	ref := messageRef(waitStored(t, s, "planning tomorrow"))

	aster := mcpSession(t, s, hubMCPToken(t, s, "aster"))
	res = callSend(t, aster, map[string]any{
		"destination": "group", "text": "works for me, @cedar can you join?", "reply_to": ref})
	if res.IsError {
		t.Fatalf("reply refused: %s", resultText(res))
	}

	id := waitStored(t, s, "works for me")
	var delivered []string
	for _, m := range s.activity() {
		if m.ID == id {
			delivered = m.DeliveredTo
		}
	}
	if len(delivered) != 2 {
		t.Fatalf("delivered_to = %v, want the author replied to plus the mention", delivered)
	}
	got := map[string]bool{}
	for _, id := range delivered {
		got[id] = true
	}
	if len(got) != len(delivered) {
		t.Fatalf("delivered_to has duplicates: %v", delivered)
	}
	time.Sleep(2 * time.Second)
	for _, tn := range s.completed("dahlia") {
		if tn.Trigger == "message" && strings.Contains(tn.ResultText, "works for me") {
			t.Fatalf("a recipient of the original received the reply: %s", dump(tn))
		}
	}
}

// A reference the loop was never given, or one from another conversation, is
// refused in-turn. Neither silently becomes a plain post or a broadcast.
func TestReplyToUnusableReferenceIsRefused(t *testing.T) {
	s := startServer(t, t.TempDir())
	s.createLoop("aster", nil)
	s.createLoop("briar", nil)
	aster := mcpSession(t, s, hubMCPToken(t, s, "aster"))

	res := callSend(t, aster, map[string]any{
		"destination": "control_room", "text": "a private note"})
	if res.IsError {
		t.Fatalf("control_room send refused: %s", resultText(res))
	}
	private := messageRef(waitStored(t, s, "a private note"))

	for _, c := range []struct {
		name string
		args map[string]any
		code string
	}{
		{"not a reference", map[string]any{
			"destination": "group", "text": "@briar hi", "reply_to": "the last one"}, "unknown_reply_to"},
		{"a reference to nothing", map[string]any{
			"destination": "group", "text": "@briar hi", "reply_to": "ref:999999"}, "unknown_reply_to"},
		{"a reference from another conversation", map[string]any{
			"destination": "group", "text": "@briar hi", "reply_to": private}, "cross_conversation_reply_to"},
	} {
		t.Run(c.name, func(t *testing.T) {
			wantSendError(t, callSend(t, aster, c.args), c.code)
		})
	}
	for _, m := range s.activity() {
		if strings.Contains(m.Text, "@briar hi") {
			t.Fatalf("a refused reply was stored: %s", dump(m))
		}
	}
}

// Two identical messages stay distinct: a reply to the older one targets it,
// not the newest lookalike.
func TestReplyTargetsTheNamedMessageNotTheLatest(t *testing.T) {
	s := startServer(t, t.TempDir())
	s.createLoop("aster", nil)
	s.createLoop("briar", nil)
	briar := mcpSession(t, s, hubMCPToken(t, s, "briar"))

	for i := 0; i < 2; i++ {
		if res := callSend(t, briar, map[string]any{
			"destination": "group", "text": "@aster ping"}); res.IsError {
			t.Fatalf("send %d refused: %s", i, resultText(res))
		}
		time.Sleep(50 * time.Millisecond)
	}
	var ids []int64
	for _, m := range s.activity() {
		if m.Author == "briar" && m.Text == "@aster ping" {
			ids = append(ids, m.ID)
		}
	}
	if len(ids) != 2 {
		t.Fatalf("stored %d lookalike messages, want 2 distinct rows", len(ids))
	}
	older := ids[len(ids)-1] // activity is newest first

	aster := mcpSession(t, s, hubMCPToken(t, s, "aster"))
	if res := callSend(t, aster, map[string]any{
		"destination": "group", "text": "pong", "reply_to": messageRef(older)}); res.IsError {
		t.Fatalf("reply to the older message refused: %s", resultText(res))
	}
	for _, m := range s.activity() {
		if m.Text == "pong" {
			if m.ReplyToID != older {
				t.Fatalf("reply recorded against %d, want the named message %d", m.ReplyToID, older)
			}
			return
		}
	}
	t.Fatal("reply not stored")
}

// waitStored returns the id of the newest message whose text contains want.
func waitStored(t *testing.T, s *server, want string) int64 {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		for _, m := range s.activity() {
			if strings.Contains(m.Text, want) {
				return m.ID
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("message %q never stored", want)
	return 0
}
