package telegram

import (
	"context"
	"log/slog"
	"testing"

	"github.com/enes-alatas/spool/internal/route"
	"github.com/enes-alatas/spool/internal/store"
)

// reactionStore knows one message by l1's bot's id for it, two teammates'
// posts by l2's bot's, one in a supergroup and one in a basic group, and
// the reactions on a message as the table holds them after a change.
type reactionStore struct {
	store.Store
	held []*store.Reaction
}

func (fake *reactionStore) Messages() store.MessageStore   { return reactionMessages{} }
func (fake *reactionStore) Reactions() store.ReactionStore { return reactionRows{fake: fake} }

type reactionMessages struct{ store.MessageStore }

func (reactionMessages) Ref(_ context.Context, messageID int64, botLoopID string) (*store.SurfaceRef, error) {
	if messageID != 7 || botLoopID != "l1" {
		return nil, store.ErrNotFound
	}
	return &store.SurfaceRef{MessageID: 7, BotLoopID: "l1", TGChatID: -100, TGMessageID: 70}, nil
}

func (reactionMessages) Refs(_ context.Context, messageID int64) ([]*store.SurfaceRef, error) {
	switch messageID {
	case 9:
		return []*store.SurfaceRef{{MessageID: 9, BotLoopID: "l2", TGChatID: -1001234567890, TGMessageID: 90}}, nil
	case 10:
		return []*store.SurfaceRef{{MessageID: 10, BotLoopID: "l2", TGChatID: -5541, TGMessageID: 100}}, nil
	}
	return []*store.SurfaceRef{}, nil
}

type reactionRows struct {
	store.ReactionStore
	fake *reactionStore
}

func (rows reactionRows) ListByMessages(context.Context, []int64) ([]*store.Reaction, error) {
	return rows.fake.held, nil
}

// A bot sets one reaction per message, so the mirror sets the loop's newest
// one still on it, and clears its own when the loop's last is removed. A
// person's reaction sets nothing. A teammate's post the loop's bot never
// saw takes the posting bot's id in a supergroup, where ids are the chat's,
// and nothing in a basic group, where that id names another message (#607).
func TestMirrorReactionSetsTheLoopsNewest(t *testing.T) {
	bot := &poller{loopID: "l1", sendCh: make(chan sendReq, 4)}
	fake := &reactionStore{}
	br := &Bridge{store: fake, log: slog.Default(), pollers: map[string]*poller{"l1": bot}}
	loop := store.LoopReactor("l1")
	person := store.PersonReactor(store.SurfaceTelegram, "42")
	frame := func(messageID int64, key, emoji string, removed bool) *route.ReactionPayload {
		return &route.ReactionPayload{Reaction: &store.Reaction{MessageID: messageID, ReactorKey: key, Emoji: emoji}, Removed: removed}
	}

	for _, step := range []struct {
		name     string
		held     []*store.Reaction
		frame    *route.ReactionPayload
		want     string
		setsAny  bool
		chat, on int64 // where it is set, -100 and 70 when zero
	}{
		{"a first reaction", []*store.Reaction{{ReactorKey: loop, Emoji: "🎉"}}, frame(7, loop, "🎉", false), "🎉", true, 0, 0},
		{"a second one replaces it", []*store.Reaction{{ReactorKey: loop, Emoji: "🎉"}, {ReactorKey: person, Emoji: "👀"},
			{ReactorKey: loop, Emoji: "👍"}}, frame(7, loop, "👍", false), "👍", true, 0, 0},
		{"removing the newest falls back", []*store.Reaction{{ReactorKey: loop, Emoji: "🎉"}, {ReactorKey: person, Emoji: "👀"}},
			frame(7, loop, "👍", true), "🎉", true, 0, 0},
		{"removing the last clears", []*store.Reaction{{ReactorKey: person, Emoji: "👀"}}, frame(7, loop, "🎉", true), "", true, 0, 0},
		{"a person's reaction", []*store.Reaction{{ReactorKey: person, Emoji: "👀"}}, frame(7, person, "👀", false), "", false, 0, 0},
		{"a target no bot knew", nil, frame(8, loop, "👍", false), "", false, 0, 0},
		{"a teammate's post in a supergroup", []*store.Reaction{{ReactorKey: loop, Emoji: "👍"}}, frame(9, loop, "👍", false),
			"👍", true, -1001234567890, 90},
		{"a teammate's post in a basic group", []*store.Reaction{{ReactorKey: loop, Emoji: "👍"}}, frame(10, loop, "👍", false),
			"", false, 0, 0},
	} {
		if step.chat == 0 {
			step.chat, step.on = -100, 70
		}
		fake.held = step.held
		br.mirrorReaction(context.Background(), step.frame)
		select {
		case req := <-bot.sendCh:
			if !step.setsAny || req.chatID != step.chat || req.reactTo != step.on || req.emoji != step.want {
				t.Fatalf("%s: queued %+v, want %q on %d in %d (sets=%v)", step.name, req, step.want, step.on, step.chat, step.setsAny)
			}
		default:
			if step.setsAny {
				t.Fatalf("%s: nothing queued, want %q", step.name, step.want)
			}
		}
	}
}
