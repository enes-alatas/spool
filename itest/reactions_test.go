//go:build integration

package itest

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// react queues one bot's message_reaction update: from's reactions on the
// message the bot knows as messageID, before and after.
func (tg *fakeTelegram) react(token string, chatID, messageID int64, from user, before, after []string) {
	tg.mu.Lock()
	defer tg.mu.Unlock()
	reactions := func(emojis []string) []map[string]any {
		out := []map[string]any{}
		for _, emoji := range emojis {
			out = append(out, map[string]any{"type": "emoji", "emoji": emoji})
		}
		return out
	}
	chatType := "supergroup"
	if chatID > 0 {
		chatType = "private" // a private chat's id is the person's
	}
	tg.updates++
	tg.queued[token] = append(tg.queued[token], map[string]any{
		"update_id": tg.updates,
		"message_reaction": map[string]any{
			"chat":         map[string]any{"id": chatID, "type": chatType},
			"message_id":   messageID,
			"user":         map[string]any{"id": from.ID, "is_bot": false, "first_name": from.First, "username": from.Username},
			"date":         time.Now().Unix(),
			"old_reaction": reactions(before), "new_reaction": reactions(after),
		},
	})
}

// reactionsOn reads the reactions the hub holds on a message, as
// "reactor_key emoji", oldest first.
func (s *server) reactionsOn(messageID int64) []string {
	s.t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(s.dataDir, "spool.db")+"?mode=ro")
	if err != nil {
		s.t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.Query(`SELECT reactor_key, emoji FROM reactions WHERE message_id=? ORDER BY ts, id`, messageID)
	if err != nil {
		s.t.Fatal(err)
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var key, emoji string
		if err := rows.Scan(&key, &emoji); err != nil {
			s.t.Fatal(err)
		}
		out = append(out, key+" "+emoji)
	}
	return out
}

func (s *server) waitReactions(messageID int64, want ...string) {
	s.t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		got := s.reactionsOn(messageID)
		if slices.Equal(got, want) {
			return
		}
		if time.Now().After(deadline) {
			s.t.Fatalf("reactions on %d = %v, want %v", messageID, got, want)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// A person's reaction on a loop's post reaches the hub through the bot
// that posted it, as what changed: a reaction added, then swapped for
// another, then removed. A stranger's reaction is recorded nowhere
// (ADR-0040).
func TestATelegramReactionReachesTheHub(t *testing.T) {
	t.Parallel()
	operator := user{ID: 9191, First: "Operator", Username: "operator"}
	srv, tg := startTelegramFleet(t, operator)
	alpha := mcpSession(t, srv, hubMCPToken(t, srv, "alpha"))
	const post = "@beta the build is green"
	if res := callSend(t, alpha, map[string]any{"destination": "group", "text": post}); res.IsError {
		t.Fatalf("send refused: %s", resultText(res))
	}
	sent := tg.waitSentFrom(t, groupChatID, "alpha", post)
	srv.waitForMessage(post)
	messageID := srv.activityWith(post)[0].ID
	person := "telegram:" + strconv.FormatInt(operator.ID, 10)

	stranger := user{ID: 9292, First: "Stranger", Username: "stranger"}
	tg.react("alpha", groupChatID, sent.MessageID, stranger, nil, []string{"👎"})
	tg.react("alpha", groupChatID, sent.MessageID, operator, nil, []string{"👍"})
	srv.waitReactions(messageID, person+" 👍")

	tg.react("alpha", groupChatID, sent.MessageID, operator, []string{"👍"}, []string{"🎉"})
	srv.waitReactions(messageID, person+" 🎉")

	tg.react("alpha", groupChatID, sent.MessageID, operator, []string{"🎉"}, nil)
	srv.waitReactions(messageID)
}

// waitReactionSet waits for a bot to set want on a message, by its id for
// it.
func (tg *fakeTelegram) waitReactionSet(t *testing.T, want setReaction) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		tg.mu.Lock()
		set := slices.Contains(tg.reactionsSet, want)
		got := slices.Clone(tg.reactionsSet)
		tg.mu.Unlock()
		if set {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("reaction %+v never set; the bridge set %+v", want, got)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// A loop reacts with send_message: react and reply_to, and no text. The hub
// records it as the loop's, and the loop's own bot sets it on the message
// in the room, by its own id for it; a second reaction replaces the first
// there. A react value that is not one emoji, or that comes with words, is
// refused for the loop to correct (ADR-0040).
func TestALoopReactsThroughItsOwnBot(t *testing.T) {
	t.Parallel()
	operator := user{ID: 9393, First: "Operator", Username: "operator"}
	srv, tg := startTelegramFleet(t, operator)
	alpha := mcpSession(t, srv, hubMCPToken(t, srv, "alpha"))
	const ask = "@alpha ship it when green"
	post := tg.post(groupChatID, "supergroup", ask, operator)
	srv.waitForMessage(ask)
	messageID := srv.activityWith(ask)[0].ID
	ref := fmt.Sprintf("ref:%d", messageID)

	for _, refused := range []struct {
		args map[string]any
		code string
	}{
		{map[string]any{"destination": "group", "reply_to": ref, "react": "ok"}, "invalid_reaction"},
		{map[string]any{"destination": "group", "reply_to": ref, "react": "👍 ok"}, "invalid_reaction"},
		{map[string]any{"destination": "group", "reply_to": ref, "react": ":partyparrot:"}, "invalid_reaction"},
		{map[string]any{"destination": "group", "reply_to": ref, "react": "👍", "text": "on it"}, "reaction_carries_nothing_else"},
		{map[string]any{"destination": "group", "react": "👍"}, "unknown_reply_to"},
		{map[string]any{"destination": "control_room", "reply_to": ref, "react": "👍"}, "cross_conversation_reply_to"},
	} {
		wantSendError(t, callSend(t, alpha, refused.args), refused.code)
	}
	if got := srv.reactionsOn(messageID); len(got) != 0 {
		t.Fatalf("a refused reaction was recorded: %v", got)
	}

	res := callSend(t, alpha, map[string]any{"destination": "group", "reply_to": ref, "react": "👍"})
	if res.IsError || !strings.Contains(resultText(res), `"reacted_to":"`+ref+`"`) {
		t.Fatalf("react refused or unreported: %s", resultText(res))
	}
	reactor := "loop:" + srv.loop("alpha").ID
	srv.waitReactions(messageID, reactor+" 👍")
	tg.waitReactionSet(t, setReaction{Token: "alpha", ChatID: groupChatID, MessageID: post.ids["alpha"], Emoji: "👍"})

	if res := callSend(t, alpha, map[string]any{"destination": "group", "reply_to": ref, "react": "🎉"}); res.IsError {
		t.Fatalf("second react refused: %s", resultText(res))
	}
	srv.waitReactions(messageID, reactor+" 👍", reactor+" 🎉")
	tg.waitReactionSet(t, setReaction{Token: "alpha", ChatID: groupChatID, MessageID: post.ids["alpha"], Emoji: "🎉"})
	for _, sent := range tg.sentTo(groupChatID) {
		if strings.Contains(sent.Text, "👍") || strings.Contains(sent.Text, "🎉") {
			t.Fatalf("a reaction went out as a message: %+v", sent)
		}
	}
}

// A loop reacts to a teammate's post: its own bot never received the post,
// since Telegram delivers no bot's messages to another bot, so it sets the
// reaction by the posting bot's id for it. In a supergroup that id is the
// chat's, the same for every member (#607).
func TestALoopReactsToATeammatesPost(t *testing.T) {
	t.Parallel()
	operator := user{ID: 9494, First: "Operator", Username: "operator"}
	srv, tg := startTelegramFleet(t, operator)
	alpha := mcpSession(t, srv, hubMCPToken(t, srv, "alpha"))
	beta := mcpSession(t, srv, hubMCPToken(t, srv, "beta"))
	const post = "@beta the build is green"
	if res := callSend(t, alpha, map[string]any{"destination": "group", "text": post}); res.IsError {
		t.Fatalf("send refused: %s", resultText(res))
	}
	sent := tg.waitSentFrom(t, groupChatID, "alpha", post)
	srv.waitForMessage(post)
	messageID := srv.activityWith(post)[0].ID

	res := callSend(t, beta, map[string]any{"destination": "group", "reply_to": fmt.Sprintf("ref:%d", messageID), "react": "👍"})
	if res.IsError {
		t.Fatalf("react refused: %s", resultText(res))
	}
	srv.waitReactions(messageID, "loop:"+srv.loop("beta").ID+" 👍")
	tg.waitReactionSet(t, setReaction{Token: "beta", ChatID: groupChatID, MessageID: sent.MessageID, Emoji: "👍"})
}

// A reaction to a loop's message wakes nobody, its owner's in the group
// included. It rides with the loop's next turn as a line ahead of that
// turn's envelopes, and is told once (ADR-0040).
func TestAReactionRidesWithTheNextTurn(t *testing.T) {
	t.Parallel()
	operator := user{ID: 9494, First: "Operator", Username: "operator"}
	srv, tg := startTelegramFleet(t, operator)
	alpha := mcpSession(t, srv, hubMCPToken(t, srv, "alpha"))
	const post = "@beta the build is green"
	if res := callSend(t, alpha, map[string]any{"destination": "group", "text": post}); res.IsError {
		t.Fatalf("send refused: %s", resultText(res))
	}
	sent := tg.waitSentFrom(t, groupChatID, "alpha", post)
	srv.waitForMessage(post)
	messageID := srv.activityWith(post)[0].ID
	srv.waitState("alpha", "asleep", 60*time.Second)
	before := len(srv.turns("alpha"))

	tg.react("alpha", groupChatID, sent.MessageID, operator, nil, []string{"👍"})
	srv.waitReactions(messageID, "telegram:"+strconv.FormatInt(operator.ID, 10)+" 👍")
	time.Sleep(2 * time.Second)
	if got := len(srv.turns("alpha")); got != before {
		t.Fatalf("a reaction in the group woke the loop: %d turns, had %d", got, before)
	}

	at := time.Now().UnixMilli()
	srv.message("alpha", "anything new")
	next := srv.waitTurn("alpha", 30*time.Second, func(tn turn) bool {
		return tn.EndedAt >= at && strings.Contains(tn.ResultText, "anything new")
	})
	// Ahead of the turn's envelope, as news about what the loop already
	// said. Only Spool's own notes may come before it.
	lead := strings.Index(next.ResultText, "[reactions to your messages")
	if lead < 0 || lead > strings.Index(next.ResultText, "[message from") {
		t.Fatalf("the reaction does not lead the turn's envelopes:\n%s", next.ResultText)
	}
	for _, want := range []string{
		fmt.Sprintf("reacted 👍 to your ref:%d in group", messageID),
		"the build is green", // enough of the message to know which
	} {
		if !strings.Contains(next.ResultText, want) {
			t.Fatalf("the reaction line lacks %q:\n%s", want, next.ResultText)
		}
	}

	srv.waitState("alpha", "asleep", 60*time.Second)
	again := time.Now().UnixMilli()
	srv.message("alpha", "and now")
	third := srv.waitTurn("alpha", 30*time.Second, func(tn turn) bool {
		return tn.EndedAt >= again && strings.Contains(tn.ResultText, "and now")
	})
	if strings.Contains(third.ResultText, "reacted") {
		t.Fatalf("the loop was told of the same reaction twice:\n%s", third.ResultText)
	}
}

// The owner's reaction to the loop's message in their private chat wakes
// the loop, as a message from them would. The turn it starts is the
// reaction line alone (ADR-0040).
func TestTheOwnersReactionInTheirDMWakesTheLoop(t *testing.T) {
	t.Parallel()
	operator := user{ID: 9595, First: "Operator", Username: "operator"}
	srv, tg := startTelegramFleet(t, operator)
	alpha := mcpSession(t, srv, hubMCPToken(t, srv, "alpha"))
	tg.dm("alpha", operator, "hi alpha")
	waitOwnerDMReady(t, srv, "alpha")
	const ask = "shall I deploy?"
	if res := callSend(t, alpha, map[string]any{"destination": "owner_dm", "text": ask}); res.IsError {
		t.Fatalf("send refused: %s", resultText(res))
	}
	sent := tg.waitSentFrom(t, operator.ID, "alpha", ask)
	srv.waitForMessage(ask)
	messageID := srv.activityWith(ask)[0].ID
	srv.waitState("alpha", "asleep", 60*time.Second)

	at := time.Now().UnixMilli()
	tg.react("alpha", operator.ID, sent.MessageID, operator, nil, []string{"👍"})
	woken := srv.waitTurn("alpha", 30*time.Second, func(tn turn) bool {
		return tn.EndedAt >= at && strings.Contains(tn.ResultText, "reacted 👍")
	})
	if want := fmt.Sprintf("reacted 👍 to your ref:%d in owner_dm", messageID); !strings.Contains(woken.ResultText, want) {
		t.Fatalf("the turn the reaction woke lacks %q:\n%s", want, woken.ResultText)
	}
	if strings.Contains(woken.ResultText, "[message from") || strings.Contains(woken.ResultText, "[tick ·") {
		t.Fatalf("the reaction did not wake the loop alone:\n%s", woken.ResultText)
	}
	if woken.Trigger != "message" {
		t.Fatalf("the wake was recorded as %q, want a message's", woken.Trigger)
	}
}
