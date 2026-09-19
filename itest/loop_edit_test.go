//go:build integration

package itest

import (
	"testing"
	"time"
)

// A settings save used to read the loop row, apply the request, and write all
// twenty-three columns back. When the save carried a bot token it held that
// copy across a live getMe against api.telegram.org — and the poller
// re-learns the group binding and the owner's DM chat in exactly that window.
// Both were reverted from the stale copy, silently: the loop's group traffic
// stopped and its owner's DMs stopped arriving, with nothing in the control
// room to say a write had been undone (#164, the same mechanism as #161).

// TestReplacingATokenKeepsWhatThePollerLearnedMeanwhile drives that window
// open rather than racing for it: getMe hangs until the test lets go, and the
// owner's DM lands while the save is inside it.
func TestReplacingATokenKeepsWhatThePollerLearnedMeanwhile(t *testing.T) {
	operator := user{ID: 6464, First: "Operator", Username: "operator"}
	srv, tg := startTelegramFleet(t, operator)

	bound := srv.loop("alpha")
	if bound.TGGroupChatID != groupChatID {
		t.Fatalf("the loop was expected to be bound to the group first, got %d", bound.TGGroupChatID)
	}

	// The operator opens the settings form, then presses save. The save is
	// inside the round-trip from here until release.
	tg.addBot("alpha-rotated")
	entered, release := tg.blockGetMe()
	saved := make(chan map[string]any, 1)
	go func() {
		var out map[string]any
		srv.mustJSON("PATCH", "/api/loops/alpha",
			map[string]any{"tg_bot_token": "alpha-rotated"}, &out)
		saved <- out
	}()
	select {
	case <-entered:
	case <-time.After(20 * time.Second):
		release()
		t.Fatal("the save never reached the token round-trip")
	}

	// Inside the window: the owner writes to the bot for the first time, so
	// the poller captures a chat the saved copy does not have.
	tg.dm("alpha", operator, "hi alpha")
	waitOwnerDMReady(t, srv, "alpha")

	release()
	select {
	case <-saved:
	case <-time.After(30 * time.Second):
		t.Fatal("the save never returned")
	}

	after := srv.loop("alpha")
	if !after.OwnerDMReady {
		t.Fatal("the save reverted the owner's DM chat, captured while it was in flight")
	}
	if after.TGGroupChatID != groupChatID {
		t.Fatalf("group binding = %d, want the save to have left it at %d", after.TGGroupChatID, groupChatID)
	}
	if after.TGBotUsername != botUsername("alpha-rotated") {
		t.Fatalf("bot username = %q, want the replaced token's own", after.TGBotUsername)
	}
}

// TestSavingOneFieldTouchesNoOther is the same promise without the race: a
// save that names the mission writes the mission. Cheap to state and the
// thing a future whole-row writer would break first.
func TestSavingOneFieldTouchesNoOther(t *testing.T) {
	operator := user{ID: 6465, First: "Operator", Username: "operator"}
	srv, _ := startTelegramFleet(t, operator)

	before := srv.loop("alpha")
	var out map[string]any
	srv.mustJSON("PATCH", "/api/loops/alpha", map[string]any{"mission": "hold the line"}, &out)

	after := srv.loop("alpha")
	if after.Mission != "hold the line" {
		t.Fatalf("mission = %q, want the edit to have landed", after.Mission)
	}
	if after.TGGroupChatID != before.TGGroupChatID {
		t.Fatalf("group binding changed on a mission edit: %d -> %d",
			before.TGGroupChatID, after.TGGroupChatID)
	}
	if after.OwnerTGUserID != before.OwnerTGUserID {
		t.Fatalf("owner changed on a mission edit: %d -> %d",
			before.OwnerTGUserID, after.OwnerTGUserID)
	}
	if after.TGBotUsername != before.TGBotUsername {
		t.Fatalf("bot username changed on a mission edit: %q -> %q",
			before.TGBotUsername, after.TGBotUsername)
	}
}
