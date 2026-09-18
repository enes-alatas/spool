//go:build integration

package itest

import (
	"fmt"
	"testing"
	"time"
)

type ownedLoop struct {
	Name        string `json:"name"`
	OwnerTGUser int64  `json:"owner_tg_user_id"`
	TGGroupChat int64  `json:"tg_group_chat_id"`
}

// Allowlisting the operator adopts every ownerless loop, and it happens while
// the bots are handling the same group message that binds them. Both wrote the
// whole loops row, so whichever landed second put the other's column back —
// and a loop whose owner went to 0 refuses the operator's DMs from then on,
// silently, while its group traffic keeps working (#161).
//
// Several loops, because each is an independent throw of the same race: one
// alone reproduced about twice in twelve runs.
//
// The group binding is deliberately not asserted here. The same setup also
// races the allowlist commit against the group message, and a message from a
// sender who is not yet allowed is refused by design — a different outcome
// with the same shape, which would make a failure here ambiguous.
func TestAllowlistingTheOperatorKeepsEveryLoopOwned(t *testing.T) {
	operator := user{ID: 5454, First: "Operator", Username: "operator"}
	names := []string{"alpha", "beta", "gamma", "delta", "epsilon", "zeta"}

	tg := startFakeTelegram(t, names...)
	srv := startTelegramServer(t, t.TempDir(), tg)
	for _, name := range names {
		srv.createLoop(name, map[string]any{"tg_bot_token": name})
	}

	// Registers the operator as a pending sender, without allowing them: the
	// loops are still ownerless when the allow lands.
	tg.post(groupChatID, "supergroup", "hello", operator)
	srv.waitForPendingSender(operator.ID)

	// The two writers, together: the allow adopts every ownerless loop while
	// the bots bind themselves to the group. The POST returns once adoption
	// has run, so anything that reverts an owner after this point is the
	// poller's write landing second.
	go tg.post(groupChatID, "supergroup", "binding", operator)
	srv.mustJSON("POST", fmt.Sprintf("/api/telegram/senders/%d/allow", operator.ID), nil, nil)

	// One more message, now certainly from an allowed sender, so every bot
	// binds whether or not the racing one arrived before the allowlist
	// committed. A bind reads the row it is about to write, so if an earlier
	// one already reverted the owner this writes the 0 back rather than
	// repairing it — waiting for the binding is safe, and it is what tells us
	// both writers have finished rather than guessing with a sleep.
	tg.post(groupChatID, "supergroup", "settle", operator)

	deadline := time.Now().Add(20 * time.Second)
	for {
		bound, owners := 0, map[string]int64{}
		for _, name := range names {
			var l ownedLoop
			srv.mustJSON("GET", "/api/loops/"+name, nil, &l)
			owners[name] = l.OwnerTGUser
			if l.TGGroupChat == groupChatID {
				bound++
			}
		}
		if bound == len(names) {
			for _, name := range names {
				if owners[name] != operator.ID {
					t.Errorf("%s owner = %d, want %d — the group bind reverted it",
						name, owners[name], operator.ID)
				}
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("only %d of %d loops bound to the group; the race never ran", bound, len(names))
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// waitForPendingSender blocks until a bot has registered the sender, which is
// what makes them allowable — without allowing them.
func (s *server) waitForPendingSender(id int64) {
	s.t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		var senders []tgSender
		s.mustJSON("GET", "/api/telegram/senders", nil, &senders)
		for _, sender := range senders {
			if sender.TGUserID == id {
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	s.t.Fatalf("sender %d never registered", id)
}
