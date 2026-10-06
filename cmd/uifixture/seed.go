package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/enes-alatas/spool/internal/attach"
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
	for _, event := range events {
		raw, err := json.Marshal(event.payload)
		if err != nil {
			return fmt.Errorf("payload: %w", err)
		}
		if _, err := db.Events().Insert(ctx, &store.Event{
			LoopID:    loopID,
			SessionID: sessionID,
			TurnID:    turn.ID,
			TS:        ms(event.at),
			Type:      event.typ,
			Subtype:   event.subtype,
			Payload:   string(raw),
		}); err != nil {
			return fmt.Errorf("event %s: %w", event.typ, err)
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
			turn := &store.Turn{
				ID:        fmt.Sprintf("turn_fixture_%s_%02d", name, i),
				LoopID:    loopID,
				SessionID: sessionID,
				Trigger:   store.TriggerTick,
				StartedAt: ms(at),
			}
			if err := db.Turns().Create(ctx, turn); err != nil {
				return fmt.Errorf("history turn %s: %w", name, err)
			}
			turn.EndedAt = ms(at + 40*time.Second)
			turn.DurationMS = 40_000
			turn.CostUSD = spend.cost
			turn.SessionCostUSD = running
			turn.InputTokens = 2_100
			turn.OutputTokens = 480
			turn.ContextTokens = 38_000
			turn.ResultText = "Nothing to report."
			if err := db.Turns().Finish(ctx, turn); err != nil {
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
		turn := &store.Turn{
			ID:        "turn_fixture_" + cur.name + "_current",
			LoopID:    loopID,
			SessionID: sessionID,
			Trigger:   store.TriggerTick,
			Model:     cur.model,
			StartedAt: ms(cur.endedAt - 40*time.Second),
		}
		if err := db.Turns().Create(ctx, turn); err != nil {
			return fmt.Errorf("current turn %s: %w", cur.name, err)
		}
		turn.EndedAt = ms(cur.endedAt)
		turn.DurationMS = 40_000
		turn.CostUSD = cur.cost
		turn.SessionCostUSD = cur.cost
		turn.InputTokens = 2_100
		turn.OutputTokens = 480
		turn.ContextTokens = cur.tokens
		turn.ResultText = "Nothing to report."
		if err := db.Turns().Finish(ctx, turn); err != nil {
			return fmt.Errorf("finish current turn %s: %w", cur.name, err)
		}
		if err := db.Loops().SetRuntime(ctx, loopID, sessionID, 0); err != nil {
			return fmt.Errorf("point %s at its session: %w", cur.name, err)
		}
	}
	return nil
}

// position names an entry of a fixture list by its index, for the fields
// that are optional: a plain int would make the zero entry the default.
func position(index int) *int { return &index }

// A group conversation and one control-room thread. Every handle here is
// invented; the display names are first names with no surname, and none of
// them belongs to anyone.
func seedConversations(ctx context.Context, db store.Store, files *attach.Files, ids map[string]string) error {
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
		// left marks a failure its loop was told of and reminded of as
		// often as it will be, and neither resent nor dismissed (#561), so
		// the Undelivered pane has one row of each kind to draw.
		left bool
		// to is the loops the hub delivered it to, by id as the store keeps
		// them, which the fleet channel names under a message (#286). Only
		// the loops a post addresses: a fixture that listed the same two
		// everywhere would shoot a page claiming every loop reads
		// everything, and one that stored names would shoot a page that
		// hides the id-to-name step.
		to []string
		// web marks the operator's own post from the channel page: it has
		// no surface, so no Telegram ids either (ADR-0032).
		web bool
		// replyTo is the position in this list of the message it answers.
		replyTo *int
		// files came with it, one per state the room draws (#460).
		files []fixtureFile
	}{
		// Oldest first, as the store's ids would be: the channel shows them
		// in id order, so an entry out of time order here shoots a thread that
		// jumps back and forth in time.
		//
		// The archivist's second failure, and to the group where its first
		// is to the control room: the Undelivered list is a pane of one
		// loop's page (#281), so the two destinations the list's columns
		// need (#282) have to be one loop's. A day old, because a failure
		// counts at any age (#269) and the pane dates its rows: a fixture
		// whose failures are all today's shoots a date column that could be
		// missing a day and look the same.
		// The oldest, and a month old: retention has removed its photo and
		// the recording was never kept, so the channel shows both greyed
		// lines. Addressed to the archivist, which is where it went.
		{author: "rana", text: "@archivist notes from the planning call: the whiteboard, and the recording for anyone who missed it.", at: -33 * 24 * time.Hour, to: []string{ids["archivist"]}, files: []fixtureFile{
			{name: "whiteboard.png", body: fixtureScreenshot(), removed: true},
			{name: "planning-call.mp4", notKept: store.NotKeptTooLarge, size: 48_234_496},
		}},
		{author: "archivist", from: ids["archivist"], text: "Incident summary is up for review: nineteen reports, two of them one line each.", at: -26 * time.Hour, failed: "timeout reaching the surface", left: true},
		{author: "watcher", from: ids["watcher"], text: "Nightly is green again: the break was a missing fixture in 4f1c2ab, fixed in 9d0e77c.", at: -34 * time.Minute},
		{author: "watcher", from: ids["watcher"], text: "Tonight's run broke again at the same fixture. Not reverting it myself — the change it belongs to is still open.", at: -21 * time.Minute, failed: "timeout reaching the surface", files: []fixtureFile{
			{name: "nightly.log", body: fixtureLog()},
		}},
		// The operator answering in the channel, as a reply: the page draws
		// the quote from `reply_to_id`, and a fixture without a reply shoots
		// a page that could have lost it.
		{author: "operator", text: "Which change is it? I'll chase the author rather than have you revert it.", at: -19 * time.Minute, to: []string{ids["watcher"]}, web: true, replyTo: position(3)},
		{author: "rana", text: "@gardener the install page still tells people to run `make setup`, which we removed last week. Can you take a pass?", at: -9 * time.Minute, to: []string{ids["gardener"]}, files: []fixtureFile{
			{name: "install-page.png", body: fixtureScreenshot()},
		}},
		{author: "gardener", from: ids["gardener"], text: "Three pages, all fixed — PR #48. The quickstart also showed the old output, so that block went too.", at: -8 * time.Minute},
	}
	groupIDs := make([]int64, len(group))
	for i, message := range group {
		origin := store.OriginTelegramGroup
		if message.from != "" {
			origin = store.OriginLoop
		}
		msg := &store.Message{
			TS:         ms(message.at),
			Origin:     origin,
			Author:     message.author,
			FromLoopID: message.from,
			Text:       message.text,
			// Telegram numbers message_id per bot conversation (ADR-0020),
			// so these have to differ: the store keys a sighting on the pair.
			TGChatID:     -1001000000000,
			TGMessageID:  int64(4100 + i),
			Conversation: store.ConversationGroup,
			DeliveredTo:  message.to,
		}
		// Mirrored as the hub records it (#285): a post that came in from
		// Telegram is on Telegram, and so is a loop's send that got through.
		// A failed send is pending from SetSendResult below. Without this
		// every row takes the insert's default, not_mirrored, and the channel
		// shot calls every post "hub only".
		if message.failed == "" {
			msg.Mirror = store.MirrorMirrored
		}
		if message.web {
			msg.Origin, msg.TGChatID, msg.TGMessageID = store.OriginWeb, 0, 0
			// The operator's post never leaves the hub (ADR-0032).
			msg.Mirror = store.MirrorNotMirrored
		}
		if message.replyTo != nil {
			msg.ReplyToID = groupIDs[*message.replyTo]
		}
		if err := db.Messages().Insert(ctx, msg); err != nil {
			return fmt.Errorf("group message: %w", err)
		}
		groupIDs[i] = msg.ID
		if err := keepFixtureFiles(ctx, db, files, msg.ID, message.at, message.files); err != nil {
			return err
		}
		if message.failed != "" {
			// Recorded the way the sender records it, after the retries are
			// spent — the fields are never written by the insert.
			if err := db.Messages().SetSendResult(ctx, msg.ID, ms(message.at+30*time.Second), message.failed); err != nil {
				return fmt.Errorf("group message failure: %w", err)
			}
		}
		for telling := 0; message.left && telling < store.SendFailureTellings; telling++ {
			if err := db.Messages().MarkSendFailuresTold(ctx, []int64{msg.ID}, ms(message.at+time.Duration(telling+1)*time.Hour)); err != nil {
				return fmt.Errorf("group message tellings: %w", err)
			}
		}
	}

	// Reactions on two of the group's posts (#535): an emoji two people put
	// on the same message, so a chip counts more than one, a loop's own
	// beside them, and one emoji alone on another post. Rana is the allowed
	// sender above, by her Telegram id; the operator reacted there too.
	rana := store.PersonReactor(store.SurfaceTelegram, "700000001")
	operatorKey := store.PersonReactor(store.SurfaceTelegram, "700000000")
	reactions := []struct {
		on      int
		key     string
		reactor string
		emoji   string
		at      time.Duration
	}{
		{on: 6, key: rana, reactor: "Rana", emoji: "👍", at: -7 * time.Minute},
		{on: 6, key: operatorKey, reactor: "operator", emoji: "👍", at: -6 * time.Minute},
		{on: 6, key: store.LoopReactor(ids["watcher"]), reactor: "watcher", emoji: "🎉", at: -6 * time.Minute},
		{on: 2, key: rana, reactor: "Rana", emoji: "✅", at: -30 * time.Minute},
	}
	for _, reaction := range reactions {
		if _, err := db.Reactions().Add(ctx, &store.Reaction{MessageID: groupIDs[reaction.on], ReactorKey: reaction.key,
			Reactor: reaction.reactor, Emoji: reaction.emoji, TS: ms(reaction.at)}); err != nil {
			return fmt.Errorf("reaction: %w", err)
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
		// dismissedByLoop resolves the failure the way a loop does when its
		// next message makes the lost one stale (#561), so the thread shows
		// that mark beside the unresolved one.
		dismissedByLoop bool
	}{
		{author: "operator", text: "How far did you get on the incident summary?", at: -2 * time.Hour},
		{author: "archivist", from: ids["archivist"], text: "On it: reading the reports now.", at: -2*time.Hour + 10*time.Second, failed: "timeout reaching the surface", dismissedByLoop: true},
		{author: "archivist", from: ids["archivist"], text: "Eleven of nineteen reports read. Two have no timeline at all, so they will be one line each rather than a guess.", at: -2*time.Hour + 30*time.Second},
		{author: "archivist", from: ids["archivist"], text: "The two without timelines are in, one line each. That closes the nineteen. The summary is in the incident folder: seven outages traced to the same expired certificate, four to a config push that skipped review, and the rest one-offs with nothing in common worth a pattern.", at: -96 * time.Minute, failed: "Bad Request: message text is empty after entity parsing"},
	}
	for _, message := range room {
		origin := store.OriginWeb
		if message.from != "" {
			origin = store.OriginLoop
		}
		msg := &store.Message{
			TS:                 ms(message.at),
			Origin:             origin,
			Author:             message.author,
			FromLoopID:         message.from,
			Text:               message.text,
			Conversation:       store.ConversationControlRoom,
			ConversationLoopID: ids["archivist"],
			DeliveredTo:        []string{ids["archivist"]},
		}
		if message.failed != "" {
			msg.DeliveredTo = nil
		}
		if err := db.Messages().Insert(ctx, msg); err != nil {
			return fmt.Errorf("control-room message: %w", err)
		}
		if message.failed != "" {
			if err := db.Messages().SetSendResult(ctx, msg.ID, ms(message.at+30*time.Second), message.failed); err != nil {
				return fmt.Errorf("control-room message failure: %w", err)
			}
			if message.dismissedByLoop {
				if _, err := db.Messages().ResolveSend(ctx, msg.ID, ms(message.at+time.Minute), store.SendResolutionDismissedByLoop, 0); err != nil {
					return fmt.Errorf("control-room message dismissal: %w", err)
				}
			}
		}
	}
	return nil
}

// The sender catalogue the Access page lists: one allowed, one pending, one
// blocked, so the page shows all three states. Invented people, with codes
// in the shape surface.PairCode mints, since the operator types the pending
// one to allow them.
func seedSenders(ctx context.Context, db store.Store) error {
	senders := []struct {
		id      int64
		user    string
		display string
		status  string
		via     string
		code    string
	}{
		{id: 700000001, user: "rana_example", display: "Rana", status: store.SenderAllowed, via: "group:gardener", code: "K7QM3X"},
		{id: 700000002, user: "devrim_example", display: "Devrim", status: store.SenderPending, via: "dm:watcher", code: "R4TZ8P"},
		{id: 700000003, user: "passerby_example", display: "Passer-by", status: store.SenderBlocked, via: "group:watcher", code: "H2WN6C"},
	}
	for _, sender := range senders {
		if err := db.TGSenders().Create(ctx, &store.TGSender{
			TGUserID:     sender.id,
			Username:     sender.user,
			Display:      sender.display,
			Status:       store.SenderPending,
			PairCode:     sender.code,
			FirstSeenVia: sender.via,
			CreatedAt:    ms(-13 * 24 * time.Hour),
			UpdatedAt:    ms(-13 * 24 * time.Hour),
		}); err != nil {
			return fmt.Errorf("sender %s: %w", sender.user, err)
		}
		if sender.status != store.SenderPending {
			if err := db.TGSenders().SetStatus(ctx, sender.id, sender.status, ms(-12*24*time.Hour)); err != nil {
				return fmt.Errorf("sender status %s: %w", sender.user, err)
			}
		}
	}
	return nil
}

// A loop's own variables, for the Connections panel that shows names and
// never values: each an env-var connection private to the one loop, as
// the loop page's Connections panel adds them (ADR-0043). The values here are obviously fake and are never
// rendered anywhere — the API answers presence only — but they are written
// as fixtures all the same (CONVENTIONS.md "Fixtures are synthetic").
func seedSecrets(ctx context.Context, db store.Store, loopID string) error {
	secrets := []struct{ connection, env, value string }{
		{"gardener-github-token-0f1a7e", "GITHUB_TOKEN", "ghp_000000000000_not_a_real_token"},
		{"gardener-handbook-deploy-0f1a7e", "HANDBOOK_DEPLOY_KEY", "not-a-real-deploy-key-0000"},
	}
	at := ms(-6 * 24 * time.Hour)
	for _, secret := range secrets {
		connection := &store.Connection{Name: secret.connection, Kind: store.ConnectionEnvVar,
			Config: store.ConnectionConfig{Env: secret.env}, Secret: secret.value, CreatedAt: at, OwnerLoopID: loopID}
		if err := db.Connections().Create(ctx, connection); err != nil {
			return fmt.Errorf("secret %s: %w", secret.env, err)
		}
	}
	return nil
}

// Three channels besides the fleet channel, for the Channels page and the
// loop page's Channels panel (#484, #549). They differ in the ways the pages
// draw differently: several loops, one loop, and a loop that is in more than
// one. Gardener is in releases for the release notes, and no group carries
// releases for it, so its panel shows a channel waiting for a group.
// Each pairs loops whose missions would plausibly share the room.
func seedChannels(ctx context.Context, db store.Store, ids map[string]string) error {
	channels := []struct {
		name, description string
		loops             []string
	}{
		{name: "docs", description: "The handbook and the incident write-ups: what changed, and what went stale.", loops: []string{"gardener", "archivist"}},
		{name: "on-call", description: "A broken build, said once, with the commit that broke it.", loops: []string{"watcher"}},
		{name: "releases", description: "Release dates, and anything that moves one.", loops: []string{"courier", "gardener", "watcher"}},
	}
	for _, channel := range channels {
		if err := db.Channels().Create(ctx, &store.Channel{
			Name: channel.name, Description: channel.description, CreatedAt: ms(-20 * 24 * time.Hour),
		}); err != nil {
			return fmt.Errorf("channel %s: %w", channel.name, err)
		}
		for _, name := range channel.loops {
			if err := db.Channels().AddLoop(ctx, channel.name, ids[name], ms(-20*24*time.Hour)); err != nil {
				return fmt.Errorf("channel %s, loop %s: %w", channel.name, name, err)
			}
		}
	}
	return nil
}

// Traffic in docs, a channel besides the fleet channel (#549): Activity's
// channel filter narrows to it, and every row names its channel. A fixture
// whose channel traffic was all the fleet channel's shoots a filter that
// could be ignoring its pick and a label that could be hard-coded.
func seedChannelTraffic(ctx context.Context, db store.Store, ids map[string]string) error {
	docs := []struct {
		author, from, text string
		at                 time.Duration
		to                 []string
		web                bool
	}{
		{author: "archivist", from: ids["archivist"], text: "@gardener the incident write-up template links the old runbook. Yours or mine?", at: -48 * time.Minute, to: []string{ids["gardener"]}},
		{author: "gardener", from: ids["gardener"], text: "Mine, it moved with the handbook. Fixed in the same PR as the install page.", at: -12 * time.Minute, to: []string{ids["archivist"]}},
		{author: "operator", text: "@archivist once that lands, close the stale-runbook issue too.", at: -5 * time.Minute, to: []string{ids["archivist"]}, web: true},
	}
	for _, message := range docs {
		msg := &store.Message{
			TS: ms(message.at), Origin: store.OriginLoop, Author: message.author, FromLoopID: message.from,
			Text: message.text, Conversation: store.ConversationGroup, Channel: "docs",
			DeliveredTo: message.to, Mirror: store.MirrorMirrored,
		}
		if message.web {
			// The operator's post never leaves the hub (ADR-0032).
			msg.Origin, msg.Mirror = store.OriginWeb, store.MirrorNotMirrored
		}
		if err := db.Messages().Insert(ctx, msg); err != nil {
			return fmt.Errorf("docs message: %w", err)
		}
	}
	return nil
}

// Two polls (#554), each its channel's newest message, since a thread runs
// in insert order: an open single-choice one in the fleet channel, with
// a close time still ahead and votes from a person, the operator and a
// loop, and a closed multiple-choice one in docs. A fixture with one poll
// shoots a ballot that could be ignoring its close, its choice kind, or a
// voter who picked two options.
func seedPolls(ctx context.Context, db store.Store, ids map[string]string) error {
	rana := store.PersonReactor(store.SurfaceTelegram, "700000001")
	operatorKey := store.PersonReactor(store.SurfaceTelegram, "700000000")
	polls := []struct {
		author, channel, text string
		at                    time.Duration
		to                    []string
		options               []string
		multiple              bool
		closesIn, closedAgo   time.Duration
		votes                 []store.Vote
	}{
		{
			author: "gardener", channel: store.FleetChannel, at: -4 * time.Minute,
			text:     "@watcher @archivist the handbook freeze for the release: which day?",
			to:       []string{ids["watcher"], ids["archivist"]},
			options:  []string{"Thursday", "Friday", "after the release"},
			closesIn: 2 * time.Hour,
			votes: []store.Vote{
				{VoterKey: rana, Voter: "Rana", Choice: []int{1}, TS: ms(-3 * time.Minute)},
				{VoterKey: store.LoopReactor(ids["watcher"]), Voter: "watcher", Choice: []int{1}, TS: ms(-3 * time.Minute)},
				{VoterKey: operatorKey, Voter: "operator", Choice: []int{0}, TS: ms(-2 * time.Minute)},
			},
		},
		{
			author: "archivist", channel: "docs", at: -3 * time.Minute,
			text:     "@gardener which write-ups should move into the handbook?",
			to:       []string{ids["gardener"]},
			options:  []string{"the certificate outages", "the config push", "the one-offs"},
			multiple: true, closedAgo: time.Minute,
			votes: []store.Vote{
				{VoterKey: store.LoopReactor(ids["gardener"]), Voter: "gardener", Choice: []int{0, 1}, TS: ms(-150 * time.Second)},
				{VoterKey: rana, Voter: "Rana", Choice: []int{0}, TS: ms(-2 * time.Minute)},
			},
		},
	}
	for _, poll := range polls {
		msg := &store.Message{
			TS: ms(poll.at), Origin: store.OriginLoop, Author: poll.author, FromLoopID: ids[poll.author],
			Text: poll.text, Conversation: store.ConversationGroup, Channel: poll.channel,
			DeliveredTo: poll.to, Mirror: store.MirrorMirrored,
		}
		if err := db.Messages().Insert(ctx, msg); err != nil {
			return fmt.Errorf("poll message: %w", err)
		}
		ballot := &store.Poll{MessageID: msg.ID, Options: poll.options, Multiple: poll.multiple}
		if poll.closesIn > 0 {
			ballot.ClosesAt = ms(poll.closesIn)
		}
		if err := db.Polls().Create(ctx, ballot); err != nil {
			return fmt.Errorf("poll: %w", err)
		}
		for _, vote := range poll.votes {
			vote.PollID = msg.ID
			if _, err := db.Polls().Vote(ctx, &vote); err != nil {
				return fmt.Errorf("vote: %w", err)
			}
		}
		if poll.closedAgo > 0 {
			if _, err := db.Polls().Close(ctx, msg.ID, ms(-poll.closedAgo)); err != nil {
				return fmt.Errorf("poll close: %w", err)
			}
		}
	}
	return nil
}

// The rooms the loop page lists under gardener's Telegram rows (#515): the
// fleet channel's group, a group carrying docs, and one the bot has heard
// from that nobody has bound yet, the row the page asks the operator about.
// Watcher's bot is in that same chat and carries releases there, so a
// channel's pane shows two loops in one channel whose bots differ (#549).
// Each loop's fleet channel room is the group its loop record already
// names, so the two agree.
type fixtureRoom struct {
	id, title, channel string
}

func seedRooms(ctx context.Context, db store.Store, ids map[string]string) error {
	for _, name := range []string{"gardener", "watcher"} {
		loopRecord, err := db.Loops().Get(ctx, ids[name])
		if err != nil {
			return fmt.Errorf("rooms: %w", err)
		}
		fleetRoom := fixtureRoom{id: strconv.FormatInt(loopRecord.TGGroupChatID, 10), channel: store.FleetChannel}
		var rooms []fixtureRoom
		switch name {
		case "gardener":
			fleetRoom.title = "Handbook crew"
			rooms = []fixtureRoom{fleetRoom,
				{id: "-1002000000001", title: "Docs reviewers", channel: "docs"},
				{id: "-1002000000002", title: "Release chat"}}
		case "watcher":
			fleetRoom.title = "Build alerts"
			rooms = []fixtureRoom{fleetRoom,
				{id: "-1002000000002", title: "Release chat", channel: "releases"}}
		}
		if err := seedLoopRooms(ctx, db, loopRecord.ID, rooms); err != nil {
			return err
		}
	}
	return nil
}

func seedLoopRooms(ctx context.Context, db store.Store, loopID string, rooms []fixtureRoom) error {
	for n, room := range rooms {
		seen := ms(-time.Duration(19-n) * 24 * time.Hour)
		if _, _, err := db.Rooms().Sight(ctx, &store.Room{
			LoopID: loopID, Surface: store.SurfaceTelegram, RoomID: room.id, Title: room.title, FirstSeenAt: seen,
		}); err != nil {
			return fmt.Errorf("room %s: %w", room.title, err)
		}
		if room.channel == "" {
			continue
		}
		if _, err := db.Rooms().Bind(ctx, loopID, store.SurfaceTelegram, room.id, room.channel, seen); err != nil {
			return fmt.Errorf("bind %s: %w", room.title, err)
		}
	}
	return nil
}

// Three connections, one for each shape the Connections page draws
// differently (#506): an env variable, an http MCP server with a secret,
// and a stdio MCP server without one. The secrets are obviously fake and the
// API never answers them, only that they are there (ADR-0043).
//
// GitHub is on two loops and the tracker on one, so the page shows a list
// of loops and the gardener's panel shows a list of connections; the
// handbook search is on none, so the page shows that too, and the panel has
// something left to attach.
//
// GitHub's value was rotated this morning and an old deploy key revoked
// (#507), so the page draws both states and the record has more than
// attaches on it.
func seedConnections(ctx context.Context, db store.Store, ids map[string]string) error {
	connections := []*store.Connection{
		{
			Name: "github", Kind: store.ConnectionEnvVar,
			Config: store.ConnectionConfig{Env: "GH_TOKEN"},
			Secret: "ghp_000000000000_not_a_real_token", CreatedAt: ms(-9 * 24 * time.Hour),
		},
		{
			Name: "tracker", Kind: store.ConnectionMCPServer,
			Config: store.ConnectionConfig{Transport: store.MCPTransportHTTP, URL: "https://mcp.tracker.example/mcp"},
			Secret: "not-a-real-tracker-key-0000", CreatedAt: ms(-4 * 24 * time.Hour),
		},
		{
			Name: "handbook-search", Kind: store.ConnectionMCPServer,
			Config: store.ConnectionConfig{
				Transport: store.MCPTransportStdio, Command: "handbook-mcp",
				Args: []string{"--index", "/srv/handbook", "--read-only"},
			},
			CreatedAt: ms(-2 * 24 * time.Hour),
		},
		{
			Name: "old-deploy-key", Kind: store.ConnectionEnvVar,
			Config: store.ConnectionConfig{Env: "DEPLOY_KEY"},
			Secret: "not-a-real-deploy-key-0000", CreatedAt: ms(-20 * 24 * time.Hour),
		},
	}
	for _, connection := range connections {
		if err := db.Connections().Create(ctx, connection); err != nil {
			return fmt.Errorf("connection %s: %w", connection.Name, err)
		}
	}
	attachments := []struct{ connection, loop string }{
		{"github", "gardener"}, {"github", "archivist"}, {"tracker", "gardener"}, {"old-deploy-key", "gardener"},
	}
	for _, attachment := range attachments {
		if err := db.Connections().Attach(ctx, attachment.connection, ids[attachment.loop], ms(-24*time.Hour)); err != nil {
			return fmt.Errorf("attach %s to %s: %w", attachment.connection, attachment.loop, err)
		}
	}
	if err := db.Connections().SetSecret(ctx, "github", "ghp_000000000001_not_a_real_token", ms(-3*time.Hour)); err != nil {
		return fmt.Errorf("rotate github: %w", err)
	}
	if _, err := db.Connections().Revoke(ctx, "old-deploy-key", ms(-6*time.Hour)); err != nil {
		return fmt.Errorf("revoke old-deploy-key: %w", err)
	}
	return nil
}

// fixtureEgressHosts are the operator's extra egress hosts: more than one,
// so Settings → Egress draws a list with a Remove on each, none built in,
// and one a leading-dot domain. The fixture hub starts without
// --egress-allow, so the stored list is what it serves.
var fixtureEgressHosts = []string{"index.crates.io", "static.crates.io", ".huggingface.co"}

func seedEgressHosts(ctx context.Context, db store.Store) error {
	raw, err := json.Marshal(store.EgressHosts{Hosts: fixtureEgressHosts, ChangedAt: ms(-2 * time.Hour)})
	if err != nil {
		return fmt.Errorf("egress hosts: %w", err)
	}
	if err := db.Settings().Set(ctx, store.SettingEgressHosts, string(raw)); err != nil {
		return fmt.Errorf("egress hosts: %w", err)
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
	for _, rule := range rules {
		if err := db.FleetRules().Create(ctx, &store.FleetRule{
			ID: rule.id, Title: rule.title, Body: rule.body, Enabled: rule.enabled,
			CreatedAt: ms(-30 * 24 * time.Hour), UpdatedAt: ms(-30 * 24 * time.Hour),
		}); err != nil {
			return fmt.Errorf("rule %s: %w", rule.title, err)
		}
	}
	return nil
}
