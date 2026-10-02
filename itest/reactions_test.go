//go:build integration

package itest

import (
	"database/sql"
	"path/filepath"
	"slices"
	"strconv"
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
	tg.updates++
	tg.queued[token] = append(tg.queued[token], map[string]any{
		"update_id": tg.updates,
		"message_reaction": map[string]any{
			"chat":         map[string]any{"id": chatID, "type": "supergroup"},
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
