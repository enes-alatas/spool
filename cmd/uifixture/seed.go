package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/enes-alatas/spool/internal/store"
)

// One loop's timeline: the turn a screenshot of a loop page is of.
//
// The events are the shapes the room draws — an envelope it woke on, the
// assistant text it produced, the spool note for the spawn, and the result
// line that prices the turn. Their payloads are the CLI's own JSON, because
// the room parses them (web/src/timeline.ts) and a fixture that only looked
// right would go stale the first time that parser changed.
const timelineSessionID = "sess_fixture_gardener"

func seedTimeline(ctx context.Context, db store.Store, loopID string) error {
	sessionID := timelineSessionID
	if err := db.Sessions().Create(ctx, &store.Session{
		ID: sessionID, LoopID: loopID, StartedAt: ms(-3 * time.Hour),
	}); err != nil {
		return fmt.Errorf("session: %w", err)
	}

	turn := &store.Turn{
		ID:        "turn_fixture_01",
		LoopID:    loopID,
		SessionID: sessionID,
		Trigger:   store.TriggerMessage,
		Model:     "claude-opus-5",
		StartedAt: ms(-9 * time.Minute),
	}
	if err := db.Turns().Create(ctx, turn); err != nil {
		return fmt.Errorf("turn: %w", err)
	}
	turn.EndedAt = ms(-8 * time.Minute)
	turn.DurationMS = 64_000
	turn.CostUSD = 0.1382
	turn.SessionCostUSD = 1.9041
	turn.InputTokens = 4_318
	turn.OutputTokens = 1_204
	turn.CacheReadTokens = 61_480
	turn.CacheWriteTokens = 2_907
	turn.ContextTokens = 71_902
	turn.ResultText = "Fixed the three pages whose examples no longer compile; opened PR #48."
	if err := db.Turns().Finish(ctx, turn); err != nil {
		return fmt.Errorf("finish turn: %w", err)
	}

	events := []struct {
		typ, subtype string
		at           time.Duration
		payload      any
	}{
		{typ: "spool", subtype: "proc_spawn", at: -9 * time.Minute, payload: map[string]any{
			"resume": true, "session_id": sessionID,
		}},
		{typ: "envelope", at: -9 * time.Minute, payload: map[string]any{
			"trigger": store.TriggerMessage,
			"text": "[message from @rana via telegram · group · ref:118]\n\n" +
				"@gardener the install page still tells people to run `make setup`, which we removed last week. Can you take a pass?",
		}},
		{typ: "assistant", at: -8*time.Minute - 40*time.Second, payload: map[string]any{
			"message": map[string]any{"content": []map[string]any{{
				"type": "text",
				"text": "Three pages referred to `make setup`: install, contributing, and the quickstart's second step.\n\n" +
					"The quickstart was the only one that also showed its output, so I replaced that block rather than the command alone.",
			}}},
		}},
		{typ: "assistant", at: -8*time.Minute - 20*time.Second, payload: map[string]any{
			"message": map[string]any{"content": []map[string]any{{
				"type": "tool_use", "name": "Edit", "id": "toolu_fixture_1",
				"input": map[string]any{
					"file_path":  "/srv/example/handbook/docs/install.md",
					"old_string": "run `make setup` once",
					"new_string": "run `make bootstrap` once",
				},
			}}},
		}},
		{typ: "result", at: -8 * time.Minute, payload: map[string]any{
			"total_cost_usd": turn.SessionCostUSD,
			"duration_ms":    turn.DurationMS,
			"is_error":       false,
			"usage": map[string]any{
				"input_tokens": turn.InputTokens, "output_tokens": turn.OutputTokens,
			},
		}},
	}
	for _, e := range events {
		raw, err := json.Marshal(e.payload)
		if err != nil {
			return fmt.Errorf("payload: %w", err)
		}
		if _, err := db.Events().Insert(ctx, &store.Event{
			LoopID:    loopID,
			SessionID: sessionID,
			TurnID:    turn.ID,
			TS:        ms(e.at),
			Type:      e.typ,
			Subtype:   e.subtype,
			Payload:   string(raw),
		}); err != nil {
			return fmt.Errorf("event %s: %w", e.typ, err)
		}
	}
	return nil
}

// Enough finished turns for the Fleet page's spend columns and the cost chart
// to have a shape. Figures are invented but plausible: a loop that wakes every
// half hour on a small model does not cost what an opus loop with a workspace
// does, and a screenshot where every loop costs the same teaches nothing.
func seedSpend(ctx context.Context, db store.Store, ids map[string]string) error {
	perLoop := map[string]struct {
		turns int
		cost  float64
	}{
		"gardener":  {turns: 18, cost: 0.1382},
		"watcher":   {turns: 46, cost: 0.0121},
		"courier":   {turns: 31, cost: 0.0064},
		"archivist": {turns: 4, cost: 0.0417},
	}
	for name, spend := range perLoop {
		loopID := ids[name]
		sessionID := "sess_fixture_" + name + "_hist"
		if err := db.Sessions().Create(ctx, &store.Session{
			ID: sessionID, LoopID: loopID, StartedAt: ms(-26 * time.Hour),
			EndedAt: ms(-2 * time.Hour), EndReason: store.EndReasonRotated,
		}); err != nil {
			return fmt.Errorf("history session %s: %w", name, err)
		}
		running := 0.0
		for i := range spend.turns {
			// Spread across the last day, so the day's spend is a day's worth.
			at := -24*time.Hour + time.Duration(i)*29*time.Minute
			running += spend.cost
			t := &store.Turn{
				ID:        fmt.Sprintf("turn_fixture_%s_%02d", name, i),
				LoopID:    loopID,
				SessionID: sessionID,
				Trigger:   store.TriggerTick,
				StartedAt: ms(at),
			}
			if err := db.Turns().Create(ctx, t); err != nil {
				return fmt.Errorf("history turn %s: %w", name, err)
			}
			t.EndedAt = ms(at + 40*time.Second)
			t.DurationMS = 40_000
			t.CostUSD = spend.cost
			t.SessionCostUSD = running
			t.InputTokens = 2_100
			t.OutputTokens = 480
			t.ContextTokens = 38_000
			t.ResultText = "Nothing to report."
			if err := db.Turns().Finish(ctx, t); err != nil {
				return fmt.Errorf("finish history turn %s: %w", name, err)
			}
		}
	}
	return nil
}

// The Fleet page's CONTEXT column reads the latest turn's fill only when that
// turn belongs to the loop's *current* session (internal/httpapi/api.go), so
// a finished session's last figure is never reported as a live one. A fixture
// whose loops have no current session therefore renders four dashes, which is
// exactly the shape a screenshot of that column must not teach.
//
// So every loop gets a session that is genuinely open. Gardener's is the
// timeline session the loop page already draws, because the two pages must
// agree about the same loop. The history sessions cannot serve: they are
// written ended and rotated, and naming one as current would describe a
// finished session as live.
func seedCurrentSessions(ctx context.Context, db store.Store, ids map[string]string) error {
	// Fills chosen to span the column: a megatoken model barely used, one
	// half gone, a 200k model nearly full. A shot where every bar is the
	// same length says nothing about what the bar means.
	live := []struct {
		name    string
		model   string
		tokens  int
		cost    float64
		endedAt time.Duration
	}{
		{name: "watcher", model: "claude-sonnet-5", tokens: 498_300, cost: 0.0121, endedAt: -6 * time.Minute},
		{name: "courier", model: "claude-haiku-4-5-20251001", tokens: 171_450, cost: 0.0064, endedAt: -14 * time.Minute},
		// Paused hours ago and still holding the context it stopped on.
		{name: "archivist", model: "claude-sonnet-5", tokens: 88_150, cost: 0.0417, endedAt: -3 * time.Hour},
	}

	// Gardener's current session is the one the timeline is in, and its turn
	// is already seeded — pointing the loop at it is the whole wiring.
	if err := db.Loops().SetRuntime(ctx, ids["gardener"], timelineSessionID, 0); err != nil {
		return fmt.Errorf("current session gardener: %w", err)
	}

	for _, cur := range live {
		loopID := ids[cur.name]
		sessionID := "sess_fixture_" + cur.name
		if err := db.Sessions().Create(ctx, &store.Session{
			ID: sessionID, LoopID: loopID, StartedAt: ms(cur.endedAt - 5*time.Hour),
		}); err != nil {
			return fmt.Errorf("current session %s: %w", cur.name, err)
		}
		t := &store.Turn{
			ID:        "turn_fixture_" + cur.name + "_current",
			LoopID:    loopID,
			SessionID: sessionID,
			Trigger:   store.TriggerTick,
			Model:     cur.model,
			StartedAt: ms(cur.endedAt - 40*time.Second),
		}
		if err := db.Turns().Create(ctx, t); err != nil {
			return fmt.Errorf("current turn %s: %w", cur.name, err)
		}
		t.EndedAt = ms(cur.endedAt)
		t.DurationMS = 40_000
		t.CostUSD = cur.cost
		t.SessionCostUSD = cur.cost
		t.InputTokens = 2_100
		t.OutputTokens = 480
		t.ContextTokens = cur.tokens
		t.ResultText = "Nothing to report."
		if err := db.Turns().Finish(ctx, t); err != nil {
			return fmt.Errorf("finish current turn %s: %w", cur.name, err)
		}
		if err := db.Loops().SetRuntime(ctx, loopID, sessionID, 0); err != nil {
			return fmt.Errorf("point %s at its session: %w", cur.name, err)
		}
	}
	return nil
}

// A group conversation and one control-room thread. Every handle here is
// invented; the display names are first names with no surname, and none of
// them belongs to anyone.
func seedConversations(ctx context.Context, db store.Store, ids map[string]string) error {
	group := []struct {
		author string
		from   string
		text   string
		at     time.Duration
		// failed is the surface's reason when this send never landed; empty
		// for the ones that did. One message in the fixture is undelivered,
		// because the room says so in three places — the message, the loop's
		// timeline, and the count on the Fleet row (#201) — and a store where
		// everything arrived shoots none of them.
		failed string
	}{
		{author: "rana", text: "@gardener the install page still tells people to run `make setup`, which we removed last week. Can you take a pass?", at: -9 * time.Minute},
		{author: "gardener", from: ids["gardener"], text: "Three pages, all fixed — PR #48. The quickstart also showed the old output, so that block went too.", at: -8 * time.Minute},
		{author: "watcher", from: ids["watcher"], text: "Nightly is green again: the break was a missing fixture in 4f1c2ab, fixed in 9d0e77c.", at: -34 * time.Minute},
		{author: "watcher", from: ids["watcher"], text: "Tonight's run broke again at the same fixture. Not reverting it myself — the change it belongs to is still open.", at: -21 * time.Minute, failed: "timeout reaching the surface"},
		// The archivist's second failure, and to the group where its first
		// is to the control room: the Undelivered list is a pane of one
		// loop's page (#281), so the two destinations the list's columns
		// need (#282) have to be one loop's. A day old, because a failure
		// counts at any age (#269) and the pane dates its rows: a fixture
		// whose failures are all today's shoots a date column that could be
		// missing a day and look the same.
		{author: "archivist", from: ids["archivist"], text: "Incident summary is up for review: nineteen reports, two of them one line each.", at: -26 * time.Hour, failed: "timeout reaching the surface"},
	}
	for i, m := range group {
		origin := store.OriginTelegramGroup
		if m.from != "" {
			origin = store.OriginLoop
		}
		msg := &store.Message{
			TS:         ms(m.at),
			Origin:     origin,
			Author:     m.author,
			FromLoopID: m.from,
			Text:       m.text,
			// Telegram numbers message_id per bot conversation (ADR-0020),
			// so these have to differ: the store keys a sighting on the pair.
			TGChatID:     -1001000000000,
			TGMessageID:  int64(4100 + i),
			Conversation: store.ConversationGroup,
			DeliveredTo:  []string{"gardener", "watcher"},
		}
		if m.failed != "" {
			// Nobody received it, so it reached nobody's inbox either.
			msg.DeliveredTo = nil
		}
		if err := db.Messages().Insert(ctx, msg); err != nil {
			return fmt.Errorf("group message: %w", err)
		}
		if m.failed != "" {
			// Recorded the way the sender records it, after the retries are
			// spent — the fields are never written by the insert.
			if err := db.Messages().SetSendResult(ctx, msg.ID, ms(m.at+30*time.Second), m.failed); err != nil {
				return fmt.Errorf("group message failure: %w", err)
			}
		}
	}

	room := []struct {
		author string
		from   string
		text   string
		at     time.Duration
		// The archivist's control-room failure, beside its group one above:
		// the Undelivered pane lines its rows up in columns (#282), and a
		// loop whose failures all went to the same place shoots a pane that
		// would look identical if they did not. Two destinations of
		// different widths is what makes the shot show the alignment. A
		// long reason for the same reason — the short one is on the group
		// row. And a long message: the pane clamps each to one line, and a
		// fixture whose failures all fit on one line shoots the same pane
		// whether the clamp works or not.
		failed string
	}{
		{author: "operator", text: "How far did you get on the incident summary?", at: -2 * time.Hour},
		{author: "archivist", from: ids["archivist"], text: "Eleven of nineteen reports read. Two have no timeline at all, so they will be one line each rather than a guess.", at: -2*time.Hour + 30*time.Second},
		{author: "archivist", from: ids["archivist"], text: "The two without timelines are in, one line each. That closes the nineteen. The summary is in the incident folder: seven outages traced to the same expired certificate, four to a config push that skipped review, and the rest one-offs with nothing in common worth a pattern.", at: -96 * time.Minute, failed: "Bad Request: message text is empty after entity parsing"},
	}
	for _, m := range room {
		origin := store.OriginWeb
		if m.from != "" {
			origin = store.OriginLoop
		}
		msg := &store.Message{
			TS:                 ms(m.at),
			Origin:             origin,
			Author:             m.author,
			FromLoopID:         m.from,
			Text:               m.text,
			Conversation:       store.ConversationControlRoom,
			ConversationLoopID: ids["archivist"],
			DeliveredTo:        []string{"archivist"},
		}
		if m.failed != "" {
			msg.DeliveredTo = nil
		}
		if err := db.Messages().Insert(ctx, msg); err != nil {
			return fmt.Errorf("control-room message: %w", err)
		}
		if m.failed != "" {
			if err := db.Messages().SetSendResult(ctx, msg.ID, ms(m.at+30*time.Second), m.failed); err != nil {
				return fmt.Errorf("control-room message failure: %w", err)
			}
		}
	}
	return nil
}

// The sender catalogue the Access page lists: one allowed, one pending, one
// blocked, so the page shows all three states. Invented people.
func seedSenders(ctx context.Context, db store.Store) error {
	senders := []struct {
		id      int64
		user    string
		display string
		status  string
		via     string
	}{
		{id: 700000001, user: "rana_example", display: "Rana", status: store.SenderAllowed, via: "group:gardener"},
		{id: 700000002, user: "devrim_example", display: "Devrim", status: store.SenderPending, via: "dm:watcher"},
		{id: 700000003, user: "passerby_example", display: "Passer-by", status: store.SenderBlocked, via: "group:watcher"},
	}
	for _, s := range senders {
		if err := db.TGSenders().Create(ctx, &store.TGSender{
			TGUserID:     s.id,
			Username:     s.user,
			Display:      s.display,
			Status:       store.SenderPending,
			PairCode:     fmt.Sprintf("%06d", s.id%1000000),
			FirstSeenVia: s.via,
			CreatedAt:    ms(-13 * 24 * time.Hour),
			UpdatedAt:    ms(-13 * 24 * time.Hour),
		}); err != nil {
			return fmt.Errorf("sender %s: %w", s.user, err)
		}
		if s.status != store.SenderPending {
			if err := db.TGSenders().SetStatus(ctx, s.id, s.status, ms(-12*24*time.Hour)); err != nil {
				return fmt.Errorf("sender status %s: %w", s.user, err)
			}
		}
	}
	return nil
}

// Per-loop secrets, for the panel that shows names and never values. The
// values here are obviously fake and are never rendered anywhere — the API
// answers presence only — but they are written as fixtures all the same
// (CONVENTIONS.md "Fixtures are synthetic").
func seedSecrets(ctx context.Context, db store.Store, loopID string) error {
	secrets := []struct{ name, value string }{
		{"GITHUB_TOKEN", "ghp_000000000000_not_a_real_token"},
		{"HANDBOOK_DEPLOY_KEY", "not-a-real-deploy-key-0000"},
	}
	for _, s := range secrets {
		if err := db.LoopSecrets().Set(ctx, loopID, s.name, s.value, ms(-6*24*time.Hour)); err != nil {
			return fmt.Errorf("secret %s: %w", s.name, err)
		}
	}
	return nil
}

// Two fleet rules, because one rule does not show that they are a list.
func seedRules(ctx context.Context, db store.Store) error {
	rules := []struct {
		id, title, body string
		enabled         bool
	}{
		{
			id: "rule_fixture_1", title: "Say what you did not check", enabled: true,
			body: "When you report a result, name the part you did not verify. A confident summary that hides its gaps costs more to undo than it saved.",
		},
		{
			id: "rule_fixture_2", title: "Quiet hours", enabled: false,
			body: "Between 22:00 and 07:00 local, hold anything that is not an outage until morning.",
		},
	}
	for _, r := range rules {
		if err := db.FleetRules().Create(ctx, &store.FleetRule{
			ID: r.id, Title: r.title, Body: r.body, Enabled: r.enabled,
			CreatedAt: ms(-30 * 24 * time.Hour), UpdatedAt: ms(-30 * 24 * time.Hour),
		}); err != nil {
			return fmt.Errorf("rule %s: %w", r.title, err)
		}
	}
	return nil
}
