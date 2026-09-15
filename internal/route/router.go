// Package route implements Spool's group-chat semantics: mention parsing,
// message persistence, and delivery (with wake) to loop runtimes.
package route

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/enes-alatas/spool/internal/bus"
	"github.com/enes-alatas/spool/internal/loop"
	"github.com/enes-alatas/spool/internal/store"
)

var mentionRe = regexp.MustCompile(`(?:^|[^\w@])@([A-Za-z0-9_-]+)`)

// stormLimit caps deliveries per ordered loop pair per hour so two loops
// can't ping-pong forever.
const (
	stormLimit  = 12
	stormWindow = time.Hour
)

// InboundMessage is anything entering the router: human input (web/telegram)
// or a loop's reply being fanned out.
type InboundMessage struct {
	Origin      string // store.Origin*
	Author      string // display name, no @
	FromLoopID  string // set when Origin == store.OriginLoop
	Text        string
	TGChatID    int64 // source chat (telegram origins)
	TGMessageID int64
	// TGBotLoopID is the loop whose bot saw the message; part of a telegram
	// message's identity, since message_id is numbered per bot.
	TGBotLoopID string
	// ImplicitTo optionally targets a loop with no mention needed
	// (DM to a loop's bot, or POST /api/loops/{name}/message).
	ImplicitTo string // loop ID
	// ReplyDMChats: for loop replies, DM chats of the triggering turn.
	ReplyDMChats []int64
}

// MessagePayload is what KindMessage bus items carry (UI + telegram mirror).
type MessagePayload struct {
	store.Message
	FromLoopName string  `json:"from_loop_name,omitempty"`
	ReplyDMChats []int64 `json:"reply_dm_chats,omitempty"`
}

type Deliverer interface {
	Deliver(loopID string, env loop.Envelope) bool
}

type Router struct {
	store   store.Store
	bus     *bus.Bus
	deliver Deliverer
	log     *slog.Logger

	mu    sync.Mutex
	storm map[string][]time.Time // "fromID→toID" → delivery timestamps
}

func New(st store.Store, b *bus.Bus, d Deliverer, log *slog.Logger) *Router {
	if log == nil {
		log = slog.Default()
	}
	return &Router{store: st, bus: b, deliver: d, log: log, storm: map[string][]time.Time{}}
}

// Mentions extracts unique lowercase mention tokens from a text.
func Mentions(text string) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range mentionRe.FindAllStringSubmatch(text, -1) {
		name := strings.ToLower(m[1])
		if !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	return out
}

// conversationFor derives the conversation a message belongs to (ADR-0026)
// from its origin: a DM to a loop's bot is that loop's owner_dm, the per-loop
// web composer is its control_room, and everything else — group traffic and
// loop replies — is the shared group. Explicit destinations replace this
// derivation once loops send through the MCP tool.
func conversationFor(in InboundMessage) (kind, loopID string) {
	switch {
	case in.Origin == store.OriginTelegramDM:
		return store.ConversationOwnerDM, in.ImplicitTo
	case in.Origin == store.OriginWeb && in.ImplicitTo != "":
		return store.ConversationControlRoom, in.ImplicitTo
	default:
		return store.ConversationGroup, ""
	}
}

// Ingest persists and routes one message. Returns store.ErrDuplicate when
// the same bot's poller re-reads a telegram message it already ingested.
func (r *Router) Ingest(ctx context.Context, in InboundMessage) error {
	mentions := Mentions(in.Text)
	conv, convLoopID := conversationFor(in)
	msg := &store.Message{
		TS:          time.Now().UnixMilli(),
		Origin:      in.Origin,
		Author:      in.Author,
		FromLoopID:  in.FromLoopID,
		Text:        in.Text,
		Mentions:    mentions,
		TGChatID:    in.TGChatID,
		TGMessageID: in.TGMessageID,
		TGBotLoopID: in.TGBotLoopID,

		Conversation:       conv,
		ConversationLoopID: convLoopID,
	}

	// resolve recipients before persisting so delivered_to lands in one write
	loops, err := r.store.Loops().List(ctx)
	if err != nil {
		return err
	}
	byKey := map[string]*store.Loop{} // name and bot-username → loop
	var fromLoop *store.Loop
	for _, l := range loops {
		byKey[strings.ToLower(l.Name)] = l
		if l.TGBotUsername != "" {
			byKey[strings.ToLower(l.TGBotUsername)] = l
		}
		if l.ID == in.FromLoopID {
			fromLoop = l
		}
	}

	targets := map[string]*store.Loop{}
	for _, m := range mentions {
		if l, ok := byKey[m]; ok && l.ID != in.FromLoopID && l.Status != store.StatusArchived {
			targets[l.ID] = l
		}
	}
	if in.ImplicitTo != "" {
		for _, l := range loops {
			if l.ID == in.ImplicitTo && l.Status != store.StatusArchived {
				targets[l.ID] = l
			}
		}
	}

	var delivered []string
	for id := range targets {
		delivered = append(delivered, id)
	}
	msg.DeliveredTo = delivered

	if err := r.store.Messages().Insert(ctx, msg); err != nil {
		return err // includes ErrDuplicate for telegram double-polls
	}

	fromLoopName := ""
	if fromLoop != nil {
		fromLoopName = fromLoop.Name
	}
	r.bus.Publish(bus.Item{Kind: bus.KindMessage, LoopID: in.FromLoopID, Payload: &MessagePayload{
		Message:      *msg,
		FromLoopName: fromLoopName,
		ReplyDMChats: in.ReplyDMChats,
	}})

	nowT := time.Now()
	for _, target := range targets {
		if in.FromLoopID != "" && !r.stormAllow(in.FromLoopID, target.ID, nowT) {
			r.recordStormDrop(ctx, in.FromLoopID, fromLoopName, target)
			continue
		}
		env := loop.MessageEnvelope(nowT, in.Origin, in.Author, in.Text, in.FromLoopID != "", dmChatFor(in))
		if !r.deliver.Deliver(target.ID, env) {
			r.log.Warn("deliver to unknown runtime", "loop", target.Name)
		}
	}
	return nil
}

// LoopReply handles a loop's finished turn: persist as a loop-origin message
// and route its mentions. resultText arrives with the trailer already present;
// the stored/mirrored text has it stripped.
func (r *Router) LoopReply(l *store.Loop, resultText string, replyDMChats []int64) {
	text := loop.StripTrailer(resultText)
	if text == "" {
		return
	}
	err := r.Ingest(context.Background(), InboundMessage{
		Origin:       store.OriginLoop,
		Author:       l.Name,
		FromLoopID:   l.ID,
		Text:         text,
		ReplyDMChats: replyDMChats,
	})
	if err != nil {
		r.log.Error("loop reply ingest", "loop", l.Name, "err", err)
	}
}

func dmChatFor(in InboundMessage) int64 {
	if in.Origin == store.OriginTelegramDM {
		return in.TGChatID
	}
	return 0
}

func (r *Router) stormAllow(fromID, toID string, now time.Time) bool {
	key := fromID + "→" + toID
	r.mu.Lock()
	defer r.mu.Unlock()
	times := r.storm[key]
	cutoff := now.Add(-stormWindow)
	kept := times[:0]
	for _, t := range times {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= stormLimit {
		r.storm[key] = kept
		return false
	}
	r.storm[key] = append(kept, now)
	return true
}

func (r *Router) recordStormDrop(ctx context.Context, fromID, fromName string, target *store.Loop) {
	r.log.Warn("storm guard dropped delivery", "from", fromName, "to", target.Name)
	e := &store.Event{
		LoopID:  fromID,
		TS:      time.Now().UnixMilli(),
		Type:    "spool",
		Subtype: "storm_drop",
		Payload: fmt.Sprintf(`{"from":%q,"to":%q,"limit_per_hour":%d}`, fromName, target.Name, stormLimit),
	}
	if _, err := r.store.Events().Insert(ctx, e); err == nil {
		r.bus.Publish(bus.Item{Kind: bus.KindAgentEvent, LoopID: fromID, Payload: e})
	}
}
