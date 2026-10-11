//go:build integration

package itest

import (
	"strings"
	"testing"
	"time"
)

// A loop's owner is configured, not inferred (#73): only that person's DMs
// reach the loop, and owner_dm always addresses them — which is what lets a
// loop open a private conversation on a tick rather than only in reply.

// The first allowlisted sender owns the fleet by default, and a proactive
// owner_dm on a turn with no inbound DM reaches their chat.
func TestProactiveOwnerDMWithoutAnInboundDM(t *testing.T) {
	t.Parallel()
	operator := user{ID: 6161, First: "Operator", Username: "operator"}
	// line 1 answers the creation tick; line 2 answers a control_room
	// message — a turn that is not a DM at all
	ws := workspaceWithScript(t, "!ctx 0\n"+
		`!send {"destination":"owner_dm","text":"the deploy needs you"}`+"\n")
	srv, tg := startTelegramFleet(t, operator, map[string]any{"workspace_path": ws})

	if got := srv.loop("alpha").OwnerTGUserID; got != operator.ID {
		t.Fatalf("owner = %d, want the first allowlisted sender %d", got, operator.ID)
	}
	// The owner has only ever written in the group, so there is no private
	// chat yet: the send must say so rather than find some other audience.
	srv.message("alpha", "anything private?")
	srv.waitTurn("alpha", 20*time.Second, func(tn turn) bool {
		return strings.Contains(tn.ResultText, "owner_dm_unavailable")
	})
	for _, m := range tg.sentTo(groupChatID) {
		if strings.Contains(m.Text, "the deploy needs you") {
			t.Fatalf("an unroutable owner DM fell back to the group: %q", m.Text)
		}
	}

	// The owner writes to this loop's bot once; that captures the chat.
	tg.dm("alpha", operator, "hi alpha")
	waitOwnerDMReady(t, srv, "alpha")

	srv.message("alpha", "now try again")
	sent := tg.waitSent(t, operator.ID, "the deploy needs you")
	if sent.Token != "alpha" {
		t.Fatalf("owner DM sent by bot %q, want alpha's own", sent.Token)
	}
}

// Messaging a bot does not make you its owner: a second allowlisted sender's
// DM leaves the owner as it was, and reaches the loop as the sender's own
// conversation, never as owner_dm (ADR-0049).
func TestAnotherSenderCannotBecomeTheOwner(t *testing.T) {
	t.Parallel()
	operator := user{ID: 6262, First: "Operator", Username: "operator"}
	srv, tg := startTelegramFleet(t, operator)
	stranger := user{ID: 6263, First: "Stranger", Username: "stranger"}

	srv.allowSender(tg, "alpha", stranger)
	tg.dm("alpha", stranger, "can you do this for me?")

	srv.waitForMessage("can you do this for me?")
	if got := srv.loop("alpha").OwnerTGUserID; got != operator.ID {
		t.Fatalf("owner = %d after a stranger's DM, want %d", got, operator.ID)
	}
	if got := srv.activityWith("can you do this for me?"); len(got) != 1 || got[0].Conversation != "dm:stranger" {
		t.Fatalf("a non-owner's DM was stored as %s", dump(got))
	}
}

// Losing access loses ownership: blocking or deleting the owner leaves the
// loops they owned unable to message them, rather than quietly going on
// doing it. An owner is an allowed sender — the check at set-time only
// establishes that; the reverse transitions have to keep it true.
func TestRevokingAccessDisownsTheLoop(t *testing.T) {
	t.Parallel()
	operator := user{ID: 6565, First: "Operator", Username: "operator"}
	// line 1 answers the creation tick, line 2 the owner's DM, and only the
	// third — a turn taken after the owner is blocked — tries to send
	ws := workspaceWithScript(t, "!ctx 0\nnoted\n"+
		`!send {"destination":"owner_dm","text":"something private"}`+"\n")
	srv, tg := startTelegramFleet(t, operator, map[string]any{"workspace_path": ws})

	tg.dm("alpha", operator, "hi")
	waitOwnerDMReady(t, srv, "alpha")
	srv.waitTurn("alpha", 20*time.Second, func(tn turn) bool {
		return strings.Contains(tn.ResultText, "noted")
	})

	srv.mustJSON("POST", "/api/telegram/senders/6565/block", nil, nil)
	v := srv.loop("alpha")
	if v.OwnerTGUserID != 0 || v.OwnerDMReady {
		t.Fatalf("after blocking the owner: owner %d, ready %v; want neither", v.OwnerTGUserID, v.OwnerDMReady)
	}

	srv.message("alpha", "anything private?")
	srv.waitTurn("alpha", 20*time.Second, func(tn turn) bool {
		return strings.Contains(tn.ResultText, "owner_not_configured")
	})
	for _, m := range tg.sentTo(operator.ID) {
		if strings.Contains(m.Text, "something private") {
			t.Fatalf("a disowned loop still messaged the blocked owner: %q", m.Text)
		}
	}

	// deleting the sender outright is the other exit from the allowlist
	srv.mustJSON("POST", "/api/telegram/senders/6565/allow", nil, nil)
	tg.dm("alpha", operator, "back again")
	waitOwnerDMReady(t, srv, "alpha")
	resp, _ := srv.do("DELETE", "/api/telegram/senders/6565", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("deleting the sender = %d", resp.StatusCode)
	}
	if v := srv.loop("alpha"); v.OwnerTGUserID != 0 || v.OwnerDMReady {
		t.Fatalf("after deleting the owner: owner %d, ready %v; want neither", v.OwnerTGUserID, v.OwnerDMReady)
	}
}

// A person other than the owner holds a conversation of their own with
// each loop they write to (ADR-0049). A private chat's id is the human's
// user id in every bot's numbering, so the conversation is keyed to the
// loop as well: what they tell alpha is alpha's, and beta's is beta's.
func TestEachLoopHoldsItsOwnConversationWithAPerson(t *testing.T) {
	t.Parallel()
	operator := user{ID: 6666, First: "Operator", Username: "operator"}
	srv, tg := startTelegramFleet(t, operator)
	stranger := user{ID: 6667, First: "Stranger", Username: "stranger"}

	srv.allowSender(tg, "alpha", stranger)

	tg.dm("alpha", stranger, "hello alpha")
	tg.dm("beta", stranger, "hello beta")
	srv.waitForMessage("hello alpha")
	srv.waitForMessage("hello beta")
	for loop, text := range map[string]string{"alpha": "hello alpha", "beta": "hello beta"} {
		messages := srv.activityWith(text)
		if len(messages) != 1 || messages[0].Conversation != "dm:stranger" || messages[0].ConversationLoopID != srv.loop(loop).ID {
			t.Fatalf("%q was not %s's conversation with the stranger: %+v", text, loop, messages)
		}
		srv.waitTurn(loop, 30*time.Second, func(tn turn) bool { return strings.Contains(tn.ResultText, text) })
	}
	for _, m := range tg.sentTo(stranger.ID) {
		if strings.Contains(m.Text, "direct messages only from") {
			t.Fatalf("a loop still turned the stranger away: %q", m.Text)
		}
	}
}

// The operator can reassign a loop's owner, and doing so drops the captured
// chat: it belonged to the previous owner's bot conversation.
func TestOwnerIsReassignableAndCaptureFollows(t *testing.T) {
	t.Parallel()
	operator := user{ID: 6363, First: "Operator", Username: "operator"}
	srv, tg := startTelegramFleet(t, operator)
	colleague := user{ID: 6364, First: "Colleague", Username: "colleague"}

	tg.dm("alpha", operator, "hi")
	waitOwnerDMReady(t, srv, "alpha")

	srv.allowSender(tg, "beta", colleague)

	var v loopView
	srv.mustJSON("PUT", "/api/loops/alpha/owner", map[string]any{"tg_user_id": colleague.ID}, &v)
	if v.OwnerTGUserID != colleague.ID || v.OwnerDMReady {
		t.Fatalf("after reassignment: owner %d, ready %v; want %d and not ready",
			v.OwnerTGUserID, v.OwnerDMReady, colleague.ID)
	}
	tg.dm("alpha", colleague, "hi from your new owner")
	waitOwnerDMReady(t, srv, "alpha")

	// An unknown or merely pending sender is not an owner.
	resp, _ := srv.do("PUT", "/api/loops/alpha/owner", map[string]any{"tg_user_id": 999999})
	if resp.StatusCode != 400 {
		t.Fatalf("setting an unknown owner = %d, want 400", resp.StatusCode)
	}
}

// The owner's address is stored, not rediscovered: it survives a restart of
// the orchestrator.
func TestOwnerDMSurvivesRestart(t *testing.T) {
	t.Parallel()
	operator := user{ID: 6464, First: "Operator", Username: "operator"}
	dir := t.TempDir()
	tg := startFakeTelegram(t, "alpha", "beta")
	srv := startTelegramServer(t, dir, tg)
	ws := workspaceWithScript(t, "!ctx 0\n"+
		`!send {"destination":"owner_dm","text":"still know where you are"}`+"\n")
	for _, name := range []string{"alpha", "beta"} {
		srv.createLoop(name, map[string]any{"tg_bot_token": name, "workspace_path": ws})
	}
	srv.allowOwner(tg, "alpha", operator)
	tg.dm("alpha", operator, "you own this one")
	waitOwnerDMReady(t, srv, "alpha")
	srv.stop()

	srv2 := startTelegramServer(t, dir, tg)
	if v := srv2.loop("alpha"); !v.OwnerDMReady || v.OwnerTGUserID != operator.ID {
		t.Fatalf("after restart: owner %d, ready %v", v.OwnerTGUserID, v.OwnerDMReady)
	}
	srv2.message("alpha", "say something private")
	if sent := tg.waitSent(t, operator.ID, "still know where you are"); sent.Token != "alpha" {
		t.Fatalf("owner DM sent by bot %q, want alpha's own", sent.Token)
	}
}

// waitOwnerDMReady blocks until the loop can write to its owner privately.
func waitOwnerDMReady(t *testing.T, s *server, name string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if s.loop(name).OwnerDMReady {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("loop %s never became ready to DM its owner", name)
}

// A link is self-service, so only an owner's makes them the fleet's owner:
// a member who links first owns no loop, old or new, and the owner who
// links after them takes every loop with none (ADR-0051).
func TestAMembersLinkOwnsNoLoop(t *testing.T) {
	t.Parallel()
	tg := startFakeTelegram(t, "alpha", "beta")
	srv := startTelegramServer(t, t.TempDir(), tg)
	srv.createLoop("alpha", map[string]any{"tg_bot_token": "alpha"})

	member := user{ID: 6565, First: "Member", Username: "member"}
	srv.allowSender(tg, "alpha", member)
	srv.createLoop("beta", map[string]any{"tg_bot_token": "beta"})
	for _, name := range []string{"alpha", "beta"} {
		if got := srv.loop(name).OwnerTGUserID; got != 0 {
			t.Fatalf("%s's owner = %d after a member linked, want none", name, got)
		}
	}

	operator := user{ID: 6566, First: "Operator", Username: "operator"}
	srv.allowOwner(tg, "alpha", operator)
	for _, name := range []string{"alpha", "beta"} {
		if got := srv.loop(name).OwnerTGUserID; got != operator.ID {
			t.Fatalf("%s's owner = %d after the owner linked, want %d", name, got, operator.ID)
		}
	}
}
