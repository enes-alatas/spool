//go:build integration

package itest

import (
	"strconv"
	"testing"
	"time"
)

// botMembership queues one bot's my_chat_member update: from changed the
// bot's own status in chatID from before to after.
func (tg *fakeTelegram) botMembership(token string, chatID int64, title string, from user, before, after string) {
	tg.mu.Lock()
	defer tg.mu.Unlock()
	tg.updates++
	tg.queued[token] = append(tg.queued[token], map[string]any{
		"update_id": tg.updates,
		"my_chat_member": map[string]any{
			"chat":            map[string]any{"id": chatID, "type": "supergroup", "title": title},
			"from":            map[string]any{"id": from.ID, "is_bot": false, "first_name": from.First, "username": from.Username},
			"date":            time.Now().Unix(),
			"old_chat_member": map[string]any{"status": before, "user": map[string]any{"id": 1, "is_bot": true}},
			"new_chat_member": map[string]any{"status": after, "user": map[string]any{"id": 1, "is_bot": true}},
		},
	})
}

// An allowed person adding a loop's bot to a group is enough to make the
// group known to the loop, as their first message there would be: the
// first binds to the fleet channel while it has no room, a later one is
// recorded unbound. A stranger's add records nothing, and an allowed
// person's message still records that group later. A promotion and a
// removal change no room (#1320, ADR-0038, ADR-0051).
func TestAnAllowedPersonAddingTheBotRecordsTheGroup(t *testing.T) {
	t.Parallel()
	operator := user{ID: 8181, First: "Operator", Username: "operator"}
	stranger := user{ID: 8282, First: "Stranger", Username: "stranger"}
	tg := startFakeTelegram(t, "alpha")
	srv := startTelegramServer(t, t.TempDir(), tg)
	srv.createLoop("alpha", map[string]any{"tg_bot_token": "alpha"})
	srv.allowOwner(tg, "alpha", operator)

	const strangersChat, fleetChat, laterChat int64 = -1009000000020, -1009000000021, -1009000000022
	strangersID, fleetID, laterID := strconv.FormatInt(strangersChat, 10), strconv.FormatInt(fleetChat, 10), strconv.FormatInt(laterChat, 10)
	waitRoom := func(roomID string) roomJSON {
		t.Helper()
		deadline := time.Now().Add(20 * time.Second)
		for time.Now().Before(deadline) {
			if room, ok := srv.room("alpha", roomID); ok {
				return room
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatalf("room %s never recorded; alpha's rooms = %+v", roomID, srv.rooms("alpha"))
		return roomJSON{}
	}

	// One bot's updates are taken in order, so once the operator's add is
	// recorded, the stranger's before it has been seen and turned away.
	tg.botMembership("alpha", strangersChat, "Strangers", stranger, "left", "member")
	tg.botMembership("alpha", fleetChat, "Fleet", operator, "left", "member")
	if fleet := waitRoom(fleetID); fleet.Channel != "group" || fleet.Title != "Fleet" {
		t.Fatalf("the operator's group = %+v, want it bound to the fleet channel", fleet)
	}
	if _, ok := srv.room("alpha", strangersID); ok {
		t.Fatalf("a stranger's add recorded a room: %+v", srv.rooms("alpha"))
	}
	if srv.loop("alpha").TGGroupChatID != fleetChat {
		t.Errorf("alpha's fleet group = %d, want %d", srv.loop("alpha").TGGroupChatID, fleetChat)
	}

	tg.botMembership("alpha", laterChat, "Later", operator, "kicked", "administrator")
	if later := waitRoom(laterID); later.Channel != "" {
		t.Fatalf("a second group = %+v, want it recorded unbound", later)
	}

	// A promotion is no add: the operator promoting the bot in the
	// stranger's group, where it is already a member, records nothing.
	// Their add to a fourth group after it marks the promotion as seen.
	const markerChat int64 = -1009000000023
	tg.botMembership("alpha", strangersChat, "Strangers", operator, "member", "administrator")
	tg.botMembership("alpha", laterChat, "Later", operator, "administrator", "kicked")
	tg.botMembership("alpha", markerChat, "Marker", operator, "left", "member")
	waitRoom(strconv.FormatInt(markerChat, 10))
	if _, ok := srv.room("alpha", strangersID); ok {
		t.Fatalf("a promotion recorded a room: %+v", srv.rooms("alpha"))
	}
	if _, ok := srv.room("alpha", laterID); !ok {
		t.Errorf("removing the bot forgot its room: %+v", srv.rooms("alpha"))
	}

	// The message path is unchanged: the operator's message records the
	// group the stranger added the bot to.
	tg.post(strangersChat, "supergroup", "hello", operator)
	if room := waitRoom(strangersID); room.Channel != "" {
		t.Fatalf("the stranger's group, after the operator's message = %+v, want it recorded unbound", room)
	}
	if fleet, _ := srv.room("alpha", fleetID); fleet.Channel != "group" {
		t.Errorf("the fleet group = %+v, want it still bound", fleet)
	}
}
