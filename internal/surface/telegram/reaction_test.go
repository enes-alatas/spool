package telegram

import (
	"context"
	"log/slog"
	"testing"

	"github.com/enes-alatas/spool/internal/route"
	"github.com/enes-alatas/spool/internal/store"
)

// reactionStore knows one message by l1's bot's id for it, and the
// reactions on it as the table holds them after a change.
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

type reactionRows struct {
	store.ReactionStore
	fake *reactionStore
}

func (rows reactionRows) ListByMessages(context.Context, []int64) ([]*store.Reaction, error) {
	return rows.fake.held, nil
}

// A bot sets one reaction per message, so the mirror sets the loop's newest
// one still on it, and clears its own when the loop's last is removed. A
// person's reaction, and a target the loop's bot never knew, set nothing.
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
		name    string
		held    []*store.Reaction
		frame   *route.ReactionPayload
		want    string
		setsAny bool
	}{
		{"a first reaction", []*store.Reaction{{ReactorKey: loop, Emoji: "🎉"}}, frame(7, loop, "🎉", false), "🎉", true},
		{"a second one replaces it", []*store.Reaction{{ReactorKey: loop, Emoji: "🎉"}, {ReactorKey: person, Emoji: "👀"},
			{ReactorKey: loop, Emoji: "👍"}}, frame(7, loop, "👍", false), "👍", true},
		{"removing the newest falls back", []*store.Reaction{{ReactorKey: loop, Emoji: "🎉"}, {ReactorKey: person, Emoji: "👀"}},
			frame(7, loop, "👍", true), "🎉", true},
		{"removing the last clears", []*store.Reaction{{ReactorKey: person, Emoji: "👀"}}, frame(7, loop, "🎉", true), "", true},
		{"a person's reaction", []*store.Reaction{{ReactorKey: person, Emoji: "👀"}}, frame(7, person, "👀", false), "", false},
		{"a target the bot never knew", nil, frame(8, loop, "👍", false), "", false},
	} {
		fake.held = step.held
		br.mirrorReaction(context.Background(), step.frame)
		select {
		case req := <-bot.sendCh:
			if !step.setsAny || req.chatID != -100 || req.reactTo != 70 || req.emoji != step.want {
				t.Fatalf("%s: queued %+v, want %q on 70 in -100 (sets=%v)", step.name, req, step.want, step.setsAny)
			}
		default:
			if step.setsAny {
				t.Fatalf("%s: nothing queued, want %q", step.name, step.want)
			}
		}
	}
}
