//go:build integration

package itest

import (
	"fmt"
	"testing"
	"time"
)

// editPost queues Telegram's edited_message for post to every bot that
// holds it, by each bot's own id for it, as it now reads. Each edit is a
// second later than the last, as Telegram's edit_date counts in seconds.
func (tg *fakeTelegram) editPost(chatID int64, chatType string, post *fakePost, text string) {
	tg.mu.Lock()
	defer tg.mu.Unlock()
	post.edits++
	for token, id := range post.ids {
		tg.updates++
		tg.queued[token] = append(tg.queued[token], map[string]any{
			"update_id": tg.updates,
			"edited_message": map[string]any{
				"message_id": id,
				"date":       post.date,
				"edit_date":  post.date + post.edits,
				"text":       text,
				"from": map[string]any{
					"id": post.from.ID, "is_bot": false,
					"first_name": post.from.First, "username": post.from.Username,
				},
				"chat": map[string]any{"id": chatID, "type": chatType, "title": tg.titles[chatID]},
			},
		})
	}
}

// activityByID is the message the hub holds as id.
func (s *server) activityByID(id int64) activityMessage {
	s.t.Helper()
	for _, message := range s.activity() {
		if message.ID == id {
			return message
		}
	}
	s.t.Fatalf("no message %d in the activity", id)
	return activityMessage{}
}

// A person who edits a message on Telegram sends a correction: a message
// of its own, from them, in the same conversation, which says the ref it
// edits and carries the new words. It reaches every loop the original
// reached, and in a group any loop the new words @mention as well. Every
// bot in the group hears the edit, and the hub stores it once. The
// original keeps its words and is marked edited (ADR-0055, #810).
func TestATelegramEditReachesItsLoopsAsACorrection(t *testing.T) {
	t.Parallel()
	operator := user{ID: 9595, First: "Operator", Username: "operator"}
	srv, tg := startTelegramFleet(t, operator)

	const said, edited = "@alpha ship it friday", "@alpha @beta ship it monday"
	post := tg.post(groupChatID, "supergroup", said, operator)
	srv.waitInput("alpha", said)
	original := srv.activityWith(said)[0]
	tg.editPost(groupChatID, "supergroup", post, edited)
	srv.waitForMessage(edited)
	// give the second bot's report time to be refused
	time.Sleep(time.Second)
	corrections := srv.activityWith(edited)
	if len(corrections) != 1 || corrections[0].EditOf != original.ID || corrections[0].EditKind != "edited" ||
		corrections[0].Conversation != "group" || len(corrections[0].DeliveredTo) != 2 {
		t.Fatalf("the corrections stored: %s; want one, of %d, to both loops", dump(corrections), original.ID)
	}
	header := fmt.Sprintf("message from @operator via telegram · group · ref:%d · edited ref:%d", corrections[0].ID, original.ID)
	srv.waitInput("alpha", header, edited)
	srv.waitInput("beta", header, edited)
	if got := srv.activityByID(original.ID); got.Text != said || got.EditedAt == 0 {
		t.Fatalf("the original reads %s; want its words kept and an edited mark", dump(got))
	}

	// a DM's edit corrects it in the same private conversation
	const asked, reasked = "remind me at five", "remind me at six"
	dm := tg.dm("alpha", operator, asked)
	srv.waitInput("alpha", asked)
	private := srv.activityWith(asked)[0]
	tg.editPost(operator.ID, "private", dm, reasked)
	srv.waitInput("alpha", "via telegram dm · owner_dm", fmt.Sprintf("edited ref:%d", private.ID), reasked)
	if got := srv.activityWith(reasked); len(got) != 1 || got[0].Conversation != "owner_dm" || got[0].EditOf != private.ID {
		t.Fatalf("the DM's correction: %s", dump(got))
	}
}
