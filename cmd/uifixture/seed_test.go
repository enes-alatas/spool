package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"testing"

	"github.com/enes-alatas/spool/internal/attach"
	"github.com/enes-alatas/spool/internal/loop"
	"github.com/enes-alatas/spool/internal/operator"
	"github.com/enes-alatas/spool/internal/store"
	"github.com/enes-alatas/spool/internal/store/sqlite"
)

// The fixture is written through the store interfaces, so a schema change
// that the fixture violates fails here rather than at the moment someone
// needs a screenshot. Both times this file was wrong during development —
// a workspace_mode the CHECK constraint refused, two group messages sharing
// one telegram id — the failure was a constraint at seed time, which is
// exactly what this test runs.
func TestSeedWritesAFleetTheRoomCanRender(t *testing.T) {
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "spool.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	files, err := attach.Open(filepath.Join(t.TempDir(), "files"))
	if err != nil {
		t.Fatalf("files: %v", err)
	}
	ctx := context.Background()
	if err := seed(ctx, db, files); err != nil {
		t.Fatalf("seed: %v", err)
	}

	got, err := db.Loops().List(ctx)
	if err != nil {
		t.Fatalf("list loops: %v", err)
	}
	if len(got) != len(loops) {
		t.Fatalf("seeded %d loops, want %d", len(got), len(loops))
	}

	// The shapes the room draws differently — a screenshot set where every
	// loop looks the same teaches nothing about the page.
	var bare, withWorkspace, paused, withSurface, withoutSurface int
	for _, loopRecord := range got {
		// The loop page says a surface is attached from the token's
		// presence, so a bot name without one draws as a private loop.
		if (loopRecord.TGBotUsername != "") != (loopRecord.TGBotToken != "") {
			t.Errorf("%s: bot username %q with token set %v; a store no operator could make",
				loopRecord.Name, loopRecord.TGBotUsername, loopRecord.TGBotToken != "")
		}
		if loopRecord.TGBotToken != "" {
			withSurface++
		} else {
			withoutSurface++
		}
		if loopRecord.Runtime == store.RuntimeBare {
			bare++
		}
		if loopRecord.WorkspaceMode == store.WorkspaceWorktree {
			withWorkspace++
		}
		if loopRecord.Status == store.StatusPaused {
			paused++
		}
	}
	for _, check := range []struct {
		what  string
		count int
	}{{"uncontained", bare}, {"with a workspace", withWorkspace}, {"paused", paused},
		{"attached to Telegram", withSurface}, {"without a surface", withoutSurface}} {
		if check.count == 0 {
			t.Errorf("no fixture loop is %s, so no shot can show one", check.what)
		}
	}

	// The exact gate the Fleet list applies before it fills the CONTEXT
	// column (internal/httpapi/api.go): the loop's latest turn must belong
	// to the session the loop is currently in. A fixture that seeds context
	// tokens but never points a loop at their session renders a dash in
	// every row while looking, in the seed code, entirely correct.
	for _, loopRecord := range got {
		latest, err := db.Turns().Latest(ctx, loopRecord.ID)
		if err != nil {
			t.Errorf("%s has no finished turn: its row shows no context and no spend", loopRecord.Name)
			continue
		}
		if latest.SessionID != loopRecord.CurrentSessionID {
			t.Errorf("%s: latest turn is in session %q but the loop's current session is %q, so CONTEXT renders as a dash",
				loopRecord.Name, latest.SessionID, loopRecord.CurrentSessionID)
		}
		// The percentage needs a model the room knows; an unknown one shows
		// bare tokens, which is a different cell than the one being shot.
		if loop.ContextLimit(latest.Model) == 0 {
			t.Errorf("%s: latest turn reports model %q, whose context window the room does not know", loopRecord.Name, latest.Model)
		}
		// And the session has to be open: naming an ended one as current
		// would report a finished session's last figure as a live one.
		sessions, err := db.Sessions().ListByLoop(ctx, loopRecord.ID, 20)
		if err != nil {
			t.Fatalf("sessions %s: %v", loopRecord.Name, err)
		}
		for _, sess := range sessions {
			if sess.ID == loopRecord.CurrentSessionID && sess.EndedAt != 0 {
				t.Errorf("%s: current session %s is already ended", loopRecord.Name, sess.ID)
			}
		}
	}

	// By name, not by List's order: the loop with a timeline is a fact about
	// the fixture, and an index would quietly test a different loop the day
	// the order changed.
	gardener, err := db.Loops().GetByName(ctx, "gardener")
	if err != nil {
		t.Fatalf("gardener: %v", err)
	}

	// The timeline shot is of these: an envelope, assistant text, a tool use
	// and a result, all parsed by web/src/timeline.ts.
	events, err := db.Events().ListByLoop(ctx, gardener.ID, 0, 100)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	seen := map[string]bool{}
	for _, event := range events {
		seen[event.Type] = true
	}
	for _, typ := range []string{"envelope", "assistant", "result", "spool"} {
		if !seen[typ] {
			t.Errorf("no %q event seeded: the timeline shot would be missing that row", typ)
		}
	}

	// The Fleet row's undelivered count reads an unresolved send failure
	// (internal/httpapi/api.go), so the fixture needs one — a store where
	// every message arrived shoots an empty version of that badge.
	var withFailure int
	for _, loopRecord := range got {
		failures, err := db.Messages().UnresolvedSendFailures(ctx, loopRecord.ID)
		if err != nil {
			t.Fatalf("send failures %s: %v", loopRecord.Name, err)
		}
		if failures > 0 {
			withFailure++
		}
	}
	if withFailure == 0 {
		t.Error("no loop has an undelivered message: the Fleet row's count would be a zero in every row")
	}

	// And more than one, going to different places, from the loop the shot
	// opens. The Undelivered pane puts its rows in columns (#282), so a
	// single-row fixture shoots a pane that would look the same whether or
	// not the columns line up — which is how the misalignment survived being
	// screenshotted. Two destinations is the smallest fixture that can show
	// it, and they have to be one loop's, since the pane lists one loop's
	// (#281; `web/scripts/ui-shots.mjs` names the loop).
	archivist, err := db.Loops().GetByName(ctx, "archivist")
	if err != nil {
		t.Fatalf("archivist: %v", err)
	}
	undelivered, err := db.Messages().Undelivered(ctx, archivist.ID)
	if err != nil {
		t.Fatalf("undelivered: %v", err)
	}
	dests := map[string]bool{}
	for _, message := range undelivered {
		dests[message.Conversation] = true
	}
	if len(undelivered) < 2 || len(dests) < 2 {
		t.Errorf("archivist has %d undelivered messages across %d destinations; want at least 2 of each, or the Undelivered shot cannot show its columns",
			len(undelivered), len(dests))
	}

	// One of them too long for a line: the tab clamps each message, and a
	// shot where every message fits looks the same whether the clamp works
	// or not — which is how a clamp that never engaged got past review.
	var clamps bool
	for _, message := range undelivered {
		if len(message.Text) > 200 {
			clamps = true
		}
	}
	if !clamps {
		t.Error("no undelivered message is over 200 characters: the Undelivered shot cannot show its one-line clamp")
	}

	// One of each loop-side outcome (#561): a row on the pane the loop has
	// left, and a failure in its control room the loop dismissed itself.
	var left bool
	for _, message := range undelivered {
		left = left || message.SendLeftByLoop
	}
	room, err := db.Messages().ListConversation(ctx, store.ConversationControlRoom, archivist.ID, 100)
	if err != nil {
		t.Fatalf("control room: %v", err)
	}
	var dismissedByLoop bool
	for _, message := range room {
		dismissedByLoop = dismissedByLoop || message.SendResolution == store.SendResolutionDismissedByLoop
	}
	if !left || !dismissedByLoop {
		t.Errorf("archivist has a left failure %v and a failure it dismissed %v; want both, or the shots cannot show either mark",
			left, dismissedByLoop)
	}

	// The fleet channel shot (#286) draws three things a timeline of plain
	// posts would not show: a reply's quote, the operator's own post, and a
	// human's that came in from a surface. Each needs a row, or the shot
	// looks the same with it broken.
	channel, err := db.Messages().ListConversation(ctx, store.ConversationGroup, "", 100)
	if err != nil {
		t.Fatalf("fleet channel: %v", err)
	}
	inChannel := map[int64]bool{}
	for _, message := range channel {
		inChannel[message.ID] = true
	}
	var reply, operator, human bool
	for _, message := range channel {
		reply = reply || (message.ReplyToID != 0 && inChannel[message.ReplyToID])
		operator = operator || message.Origin == store.OriginWeb
		human = human || message.Origin == store.OriginTelegramGroup
	}
	if !reply || !operator || !human {
		t.Errorf("fleet channel: reply %v, operator post %v, human post %v; want all three, or the channel shot cannot show them",
			reply, operator, human)
	}

	// The channel marks a post that stays on the hub (#285), so the mirror
	// states have to be the hub's own: the operator's post on the hub only,
	// everything that came in or got out on the surface too.
	for _, message := range channel {
		want := store.MirrorMirrored
		switch {
		case message.Origin == store.OriginWeb:
			want = store.MirrorNotMirrored
		case message.SendFailedAt != 0:
			want = store.MirrorPending
		}
		if message.Mirror != want {
			t.Errorf("fleet channel message %d (%s, %s): mirror %q, want %q", message.ID, message.Origin, message.Author, message.Mirror, want)
		}
	}

	// Presence only, like every other reader of this store: the panel shows
	// names, so the fixture needs names.
	secrets, err := db.LoopSecrets().List(ctx, gardener.ID)
	if err != nil {
		t.Fatalf("list secrets: %v", err)
	}
	if len(secrets) == 0 {
		t.Error("no secrets seeded: the secrets panel would shoot its empty state")
	}

	// The page draws each shape differently, so each must be there: an env
	// credential, an http server with a secret, a stdio one without (#506).
	connections, err := db.Connections().List(ctx)
	if err != nil {
		t.Fatalf("list connections: %v", err)
	}
	shapes := map[string]bool{}
	attached, unattached := 0, 0
	for _, connection := range connections {
		if len(connection.LoopIDs) > 0 {
			attached++
		} else {
			unattached++
		}
		shapes[connection.Kind+"/"+connection.Config.Transport+"/"+strconv.FormatBool(connection.Secret != "")] = true
	}
	for _, want := range []string{"env-var//true", "mcp-server/http/true", "mcp-server/stdio/false"} {
		if !shapes[want] {
			t.Errorf("no %s connection seeded: the Connections page would not shoot that shape", want)
		}
	}
	if attached == 0 || unattached == 0 {
		t.Errorf("%d attached and %d unattached connections: the page shows both, and the loop panel needs one left to attach", attached, unattached)
	}
}

// The token only helps if the hub takes it instead of minting its own.
func TestTheHubReadsTheFixtureOperatorToken(t *testing.T) {
	dir := t.TempDir()
	if err := writeOperatorToken(dir); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, minted, err := operator.Load(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if minted || got != fixtureOperatorToken {
		t.Fatalf("hub token = %q (minted %v), want the fixture's %q", got, minted, fixtureOperatorToken)
	}
}

// A real token already in the directory survives: the fixture refuses rather
// than replacing it with a value this repo publishes.
func TestTheFixtureNeverReplacesAnOperatorToken(t *testing.T) {
	dir := t.TempDir()
	const existing = "an-existing-operator-token"
	if err := os.WriteFile(operator.Path(dir), []byte(existing), 0o600); err != nil {
		t.Fatalf("seed a token: %v", err)
	}
	if err := writeOperatorToken(dir); err == nil {
		t.Fatal("writeOperatorToken over an existing token: no error, want a refusal")
	}
	got, err := os.ReadFile(operator.Path(dir))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != existing {
		t.Fatalf("token after the refusal = %q, want the original %q", got, existing)
	}
}

// The room draws an attachment four ways (#460): an image it thumbnails, a
// file it links, and the greyed lines for bytes retention removed and for a
// file never kept. A fixture missing one shoots a channel where that line
// could be broken and look the same. The kept ones must be on disk, or the
// hub answers the thumbnail with a 404.
func TestSeedCarriesEveryAttachmentState(t *testing.T) {
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "spool.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	files, err := attach.Open(filepath.Join(t.TempDir(), "files"))
	if err != nil {
		t.Fatalf("files: %v", err)
	}
	ctx := context.Background()
	if err := seed(ctx, db, files); err != nil {
		t.Fatalf("seed: %v", err)
	}

	msgs, err := db.Messages().ListConversation(ctx, store.ConversationGroup, "", 100)
	if err != nil {
		t.Fatalf("group: %v", err)
	}
	var image, file, removed, notKept int
	for _, msg := range msgs {
		rows, err := db.Attachments().ByMessage(ctx, msg.ID)
		if err != nil {
			t.Fatalf("attachments of %d: %v", msg.ID, err)
		}
		for _, row := range rows {
			_, statErr := os.Stat(files.Path(row.Path))
			switch {
			case row.NotKept != "":
				notKept++
			case row.RemovedAt != 0:
				removed++
				if statErr == nil {
					t.Errorf("%s: removed at %d but still on disk", row.Name, row.RemovedAt)
				}
			default:
				if statErr != nil {
					t.Errorf("%s: kept but not on disk: %v", row.Name, statErr)
				}
				if row.Kind == store.AttachmentImage {
					image++
					if row.Width == 0 || row.Height == 0 {
						t.Errorf("%s: an image without dimensions", row.Name)
					}
				} else {
					file++
				}
			}
		}
	}
	for _, check := range []struct {
		what  string
		count int
	}{{"a kept image", image}, {"a kept file", file}, {"removed by retention", removed}, {"never kept", notKept}} {
		if check.count == 0 {
			t.Errorf("no fixture attachment is %s, so no shot can show one", check.what)
		}
	}
}

// The Channels page draws a card per channel and a chip per loop in it
// (#484), and the loop page lists the channels a loop is in. A store with
// only the fleet channel shoots one card that looks the same whether the
// list works or not.
func TestSeedFillsTheChannelsPage(t *testing.T) {
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "spool.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	files, err := attach.Open(filepath.Join(t.TempDir(), "files"))
	if err != nil {
		t.Fatalf("files: %v", err)
	}
	ctx := context.Background()
	if err := seed(ctx, db, files); err != nil {
		t.Fatalf("seed: %v", err)
	}

	channelList, err := db.Channels().List(ctx)
	if err != nil {
		t.Fatalf("channels: %v", err)
	}
	var several, single int
	inHowMany := map[string]int{}
	for _, channel := range channelList {
		if channel.Name == store.FleetChannel {
			continue
		}
		switch len(channel.LoopIDs) {
		case 0:
		case 1:
			single++
		default:
			several++
		}
		for _, id := range channel.LoopIDs {
			inHowMany[id]++
		}
	}
	var inTwo int
	for _, count := range inHowMany {
		if count > 1 {
			inTwo++
		}
	}
	for _, check := range []struct {
		what  string
		count int
	}{{"several loops", several}, {"a single loop", single}, {"a loop that is in another channel too", inTwo}} {
		if check.count == 0 {
			t.Errorf("no fixture channel shows %s, so no shot can show one", check.what)
		}
	}
}

// The loop page lists a loop's rooms and asks about the unbound ones (#515).
// A fixture where every room is bound shoots a list that looks the same
// whether the pick-list works or not, and one with no rooms shoots only the
// empty state.
func TestSeedFillsTheLoopsRooms(t *testing.T) {
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "spool.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	files, err := attach.Open(filepath.Join(t.TempDir(), "files"))
	if err != nil {
		t.Fatalf("files: %v", err)
	}
	ctx := context.Background()
	if err := seed(ctx, db, files); err != nil {
		t.Fatalf("seed: %v", err)
	}

	gardener, err := db.Loops().GetByName(ctx, "gardener")
	if err != nil {
		t.Fatalf("gardener: %v", err)
	}
	rooms, err := db.Rooms().List(ctx, gardener.ID)
	if err != nil {
		t.Fatalf("rooms: %v", err)
	}
	var bound, unbound, fleet int
	for _, room := range rooms {
		switch room.Channel {
		case "":
			unbound++
		case store.FleetChannel:
			fleet++
			bound++
		default:
			bound++
		}
	}
	for _, check := range []struct {
		what  string
		count int
	}{{"a bound room", bound}, {"an unbound room", unbound}, {"the fleet channel's room", fleet}} {
		if check.count == 0 {
			t.Errorf("gardener has no %s, so no shot can show one", check.what)
		}
	}
	// The fleet channel's room and the loop record name the same group:
	// binding writes both, and a fixture where they differ is a store the
	// hub could not have made.
	for _, room := range rooms {
		if room.Channel == store.FleetChannel && room.RoomID != strconv.FormatInt(gardener.TGGroupChatID, 10) {
			t.Errorf("the fleet channel's room is %s, but gardener's group is %d", room.RoomID, gardener.TGGroupChatID)
		}
	}
}

// The channel shot has a message with reactions on it, one emoji put there
// by two people, so the room's count and its list of who are both in a shot
// (#535).
func TestSeedPutsReactionsOnTheChannel(t *testing.T) {
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "spool.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	files, err := attach.Open(filepath.Join(t.TempDir(), "files"))
	if err != nil {
		t.Fatalf("files: %v", err)
	}
	ctx := context.Background()
	if err := seed(ctx, db, files); err != nil {
		t.Fatalf("seed: %v", err)
	}

	msgs, err := db.Messages().ListChannel(ctx, store.FleetChannel, 100)
	if err != nil {
		t.Fatalf("group: %v", err)
	}
	ids := make([]int64, len(msgs))
	for i, msg := range msgs {
		ids[i] = msg.ID
	}
	reactions, err := db.Reactions().ListByMessages(ctx, ids)
	if err != nil {
		t.Fatalf("reactions: %v", err)
	}
	reactors := map[string]int{}
	for _, reaction := range reactions {
		reactors[fmt.Sprintf("%d %s", reaction.MessageID, reaction.Emoji)]++
	}
	shared := false
	for _, count := range reactors {
		shared = shared || count > 1
	}
	if len(reactions) == 0 || !shared {
		t.Errorf("the fleet channel's reactions are %v, want one emoji from two reactors", reactors)
	}
}

// Activity's channel filter has a channel besides the fleet channel to narrow
// to, and the loop page a channel with no group to bind one to (#549).
func TestSeedFillsAChannelBesidesTheFleetChannel(t *testing.T) {
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "spool.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	files, err := attach.Open(filepath.Join(t.TempDir(), "files"))
	if err != nil {
		t.Fatalf("files: %v", err)
	}
	ctx := context.Background()
	if err := seed(ctx, db, files); err != nil {
		t.Fatalf("seed: %v", err)
	}

	msgs, err := db.Messages().ListChannel(ctx, "docs", 100)
	if err != nil {
		t.Fatalf("docs: %v", err)
	}
	if len(msgs) == 0 {
		t.Error("docs has no messages, so no shot can show Activity narrowed to a channel")
	}

	gardener, err := db.Loops().GetByName(ctx, "gardener")
	if err != nil {
		t.Fatalf("gardener: %v", err)
	}
	rooms, err := db.Rooms().List(ctx, gardener.ID)
	if err != nil {
		t.Fatalf("rooms: %v", err)
	}
	carried := map[string]bool{}
	for _, room := range rooms {
		carried[room.Channel] = true
	}
	channels, err := db.Channels().List(ctx)
	if err != nil {
		t.Fatalf("channels: %v", err)
	}
	var waiting int
	for _, channel := range channels {
		if channel.Name != store.FleetChannel && slices.Contains(channel.LoopIDs, gardener.ID) && !carried[channel.Name] {
			waiting++
		}
	}
	if waiting == 0 {
		t.Error("every channel gardener is in has a group, so no shot can show one waiting for a group")
	}
}

// The poll shots have an open poll in the fleet channel and a closed one in
// docs, the closed one with a voter who picked two options (#554).
func TestSeedPutsAnOpenAndAClosedPoll(t *testing.T) {
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "spool.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	files, err := attach.Open(filepath.Join(t.TempDir(), "files"))
	if err != nil {
		t.Fatalf("files: %v", err)
	}
	ctx := context.Background()
	if err := seed(ctx, db, files); err != nil {
		t.Fatalf("seed: %v", err)
	}

	ballots := map[string][]*store.Poll{}
	votes := map[string][]*store.Vote{}
	for _, channel := range []string{store.FleetChannel, "docs"} {
		msgs, err := db.Messages().ListChannel(ctx, channel, 100)
		if err != nil {
			t.Fatalf("%s: %v", channel, err)
		}
		ids := make([]int64, len(msgs))
		for i, msg := range msgs {
			ids[i] = msg.ID
		}
		if ballots[channel], err = db.Polls().ListByMessages(ctx, ids); err != nil {
			t.Fatalf("%s polls: %v", channel, err)
		}
		for _, poll := range ballots[channel] {
			pollVotes, err := db.Polls().Votes(ctx, []int64{poll.MessageID})
			if err != nil {
				t.Fatalf("votes: %v", err)
			}
			votes[channel] = append(votes[channel], pollVotes...)
		}
	}
	if open := ballots[store.FleetChannel]; len(open) != 1 || open[0].ClosedAt != 0 || open[0].ClosesAt == 0 ||
		len(votes[store.FleetChannel]) < 2 {
		t.Errorf("the fleet channel's polls are %v with %d votes, want one open with a close time and votes", open, len(votes[store.FleetChannel]))
	}
	closed := ballots["docs"]
	picksTwo := false
	for _, vote := range votes["docs"] {
		picksTwo = picksTwo || len(vote.Choice) > 1
	}
	if len(closed) != 1 || closed[0].ClosedAt == 0 || !closed[0].Multiple || !picksTwo {
		t.Errorf("docs' polls are %v, want one closed multiple-choice poll with a two-option vote", closed)
	}
}
