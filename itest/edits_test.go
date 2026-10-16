//go:build integration

package itest

import (
	"fmt"
	"net/http"
	"strings"
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

// A person who edits or deletes a message on Slack sends a correction, as
// on Telegram; every loop's app in the channel hears it, and the hub
// stores it once. A link unfurling under a message is not an edit, and
// reaches no loop. A deletion carries no words, and takes the original's,
// its edits' and its kept file from the hub (ADR-0055, #810).
func TestASlackEditAndDeletionReachTheirLoopsAsCorrections(t *testing.T) {
	t.Parallel()
	srv, slack := startSlackFleet(t)
	const ts = "1727600000.000700"
	const said, edited = "<@U0TERRA> ship it friday", "<@U0TERRA> <@U0MILO> ship it monday"
	file := slack.addFile("F0PLAN", "plan.txt", "text/plain", []byte("ship it friday\n"))
	for _, app := range []string{slackAppToken, slackMiloAppToken} {
		slack.pushEvent(t, app, map[string]any{"type": "message", "subtype": "file_share", "channel_type": "channel",
			"channel": slackChannel, "user": slackOperator, "text": said, "ts": ts, "files": []any{file}})
	}
	srv.waitInput("terra", "@terra ship it friday", "[file: plan.txt")
	original := srv.activityWith("@terra ship it friday")[0]
	if kept := keptFiles(t, srv); len(kept) != 1 {
		t.Fatalf("the hub keeps %v; want the shared file", kept)
	}

	var group []struct {
		ID          int64            `json:"id"`
		Attachments []attachmentView `json:"attachments"`
	}
	srv.mustJSON("GET", "/api/group", nil, &group)
	var sharedFile int64
	for _, message := range group {
		if message.ID == original.ID && len(message.Attachments) == 1 {
			sharedFile = message.Attachments[0].ID
		}
	}
	if sharedFile == 0 {
		t.Fatalf("the group reads %s; want the original with its file", dump(group))
	}
	member := srv.signedInAs("mia", "member")

	changed := func(eventTS string, message map[string]any) map[string]any {
		return map[string]any{"type": "message", "subtype": "message_changed", "channel_type": "channel",
			"channel": slackChannel, "ts": eventTS, "hidden": true, "message": message,
			"previous_message": map[string]any{"type": "message", "user": slackOperator, "text": said, "ts": ts}}
	}
	// a link unfurled: the same words, and no edited mark
	for _, app := range []string{slackAppToken, slackMiloAppToken} {
		slack.pushEvent(t, app, changed("1727600000.000701", map[string]any{"type": "message", "user": slackOperator,
			"text": said, "ts": ts}))
	}
	for _, app := range []string{slackAppToken, slackMiloAppToken} {
		slack.pushEvent(t, app, changed("1727600000.000702", map[string]any{"type": "message", "user": slackOperator,
			"text": edited, "ts": ts, "edited": map[string]any{"user": slackOperator, "ts": "1727600000.000702"}}))
	}
	const readable = "@terra @milo ship it monday"
	srv.waitForMessage(readable)
	time.Sleep(time.Second)
	corrections := srv.activityWith(readable)
	if len(corrections) != 1 || corrections[0].EditOf != original.ID || corrections[0].EditKind != "edited" ||
		len(corrections[0].DeliveredTo) != 2 {
		t.Fatalf("the corrections stored: %s; want one, of %d, to both loops", dump(corrections), original.ID)
	}
	header := fmt.Sprintf("message from @u0oper via slack · group · ref:%d · edited ref:%d", corrections[0].ID, original.ID)
	srv.waitInput("terra", header, readable)
	srv.waitInput("milo", header, readable)
	for _, message := range srv.activity() {
		if message.EditOf == original.ID && message.ID != corrections[0].ID {
			t.Fatalf("the unfurl was taken for an edit: %s", dump(message))
		}
	}

	for _, app := range []string{slackAppToken, slackMiloAppToken} {
		slack.pushEvent(t, app, map[string]any{"type": "message", "subtype": "message_deleted", "channel_type": "channel",
			"channel": slackChannel, "ts": "1727600000.000703", "hidden": true, "deleted_ts": ts,
			"previous_message": map[string]any{"type": "message", "user": slackOperator, "text": edited, "ts": ts}})
	}
	srv.waitInput("terra", fmt.Sprintf("deleted ref:%d", original.ID))
	time.Sleep(time.Second)
	var deletions []activityMessage
	for _, message := range srv.activity() {
		if message.EditOf == original.ID && message.EditKind == "deleted" {
			deletions = append(deletions, message)
		}
	}
	if len(deletions) != 1 || deletions[0].Text != "" {
		t.Fatalf("the deletions stored: %s; want one, with no words", dump(deletions))
	}
	if got := srv.activityByID(original.ID); got.Text != "" || got.EditedAt == 0 || got.DeletedAt == 0 {
		t.Fatalf("the original reads %s; want it edited, deleted and its words gone", dump(got))
	}
	if got := srv.activityByID(corrections[0].ID); got.Text != "" {
		t.Fatalf("the edit reads %s; want its words gone with the message", dump(got))
	}
	if kept := keptFiles(t, srv); len(kept) != 0 {
		t.Fatalf("the hub still keeps %v; want the deleted message's file gone", kept)
	}
	// A deleted message has no conversation left to say who reads its file,
	// so only an admin learns it was there.
	path := fmt.Sprintf("/api/attachments/%d", sharedFile)
	if resp, _ := srv.asCookie("GET", path, member, nil); resp.StatusCode != http.StatusNotFound {
		t.Errorf("a member's GET of the deleted message's file = %d; want 404", resp.StatusCode)
	}
	if resp, _ := srv.do("GET", path, nil); resp.StatusCode != http.StatusGone {
		t.Errorf("the token's GET of the deleted message's file = %d; want 410", resp.StatusCode)
	}
}

// An edit of a message no turn has read yet corrects it in place: the loop
// reads the message once, with the new words, and spends no turn on the
// correction. Here alpha is busy with another turn when the message
// arrives and is edited (ADR-0055, #811).
func TestAnEditOfAnUnreadMessageReplacesItInPlace(t *testing.T) {
	t.Parallel()
	operator := user{ID: 9595, First: "Operator", Username: "operator"}
	// every turn of alpha's takes three seconds
	srv, tg := startTelegramFleet(t, operator, map[string]any{"workspace_path": workspaceWithScript(t, "!hang 3\n")})

	tg.post(groupChatID, "supergroup", "@alpha first", operator)
	srv.waitInput("alpha", "@alpha first")

	const said, edited = "@alpha ship it friday", "@alpha ship it monday"
	post := tg.post(groupChatID, "supergroup", said, operator)
	srv.waitForMessage(said)
	original := srv.activityWith(said)[0]
	tg.editPost(groupChatID, "supergroup", post, edited)
	srv.waitForMessage(edited)

	srv.waitInput("alpha", fmt.Sprintf("ref:%d", original.ID), edited)
	time.Sleep(time.Second)
	for _, inputs := range srv.turnInputs("alpha") {
		for _, envelope := range inputs {
			if strings.Contains(envelope, "friday") || strings.Contains(envelope, "edited ref:") {
				t.Fatalf("alpha read the old words or a correction: %s", dump(srv.turnInputs("alpha")))
			}
		}
	}
	if got := srv.activityWith(edited); len(got) != 1 || got[0].EditOf != original.ID {
		t.Fatalf("the correction stored: %s; want it kept as the record", dump(got))
	}
}
