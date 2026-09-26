package redact

import (
	"context"

	"github.com/enes-alatas/spool/internal/store"
)

// Store wraps inner so that the free text Spool persists — a turn's result,
// a raw claude event (which carries the assistant's words and every tool
// input), a chat message, a failed send's error — cannot carry a known secret
// value into the database.
//
// It decorates the store rather than the callers because the callers are the
// problem: inserts happen in internal/loop, internal/route and
// internal/surface/telegram, and the next one will happen somewhere else
// again. A decorator wired once in cmd/ covers the sites that do not know it
// exists.
//
// Writes are redacted, reads are not: a row written before this existed stays
// as it is on disk, and the API boundary redacts it on the way out.
//
// Redaction is in place — the caller's struct is edited, not copied. Two
// reasons, and the first is a bug this cost: these writes hand values *back*
// (Messages.Insert fills in the new row's ID), so a decorator that persisted
// a copy would drop that, and would keep dropping whatever the next
// write-back field turns out to be. The second is that the caller usually
// goes on to publish the same struct on the bus, and the copy it published
// would still hold the secret.
func Store(inner store.Store, r *Redactor) store.Store {
	return redactedStore{Store: inner, r: r}
}

type redactedStore struct {
	store.Store
	r *Redactor
}

func (s redactedStore) Loops() store.LoopStore       { return loops{s.Store.Loops(), s.r} }
func (s redactedStore) Turns() store.TurnStore       { return turns{s.Store.Turns(), s.r} }
func (s redactedStore) Events() store.EventStore     { return events{s.Store.Events(), s.r} }
func (s redactedStore) Messages() store.MessageStore { return messages{s.Store.Messages(), s.r} }

// loops redacts the one free-text field a loop row carries. Its token fields
// are deliberately untouched: those values *are* the secrets, and a decorator
// that redacted them on the way in would write a placeholder where the bot
// token belongs and take the loop's bot down with it.
type loops struct {
	store.LoopStore
	r *Redactor
}

func (l loops) SetRotation(ctx context.Context, id string, pending bool, reason, note string) error {
	// The handoff note is written by the loop, in its own words, and read
	// back into the next session's prompt — free text with a turn's worth of
	// whatever it was holding.
	return l.LoopStore.SetRotation(ctx, id, pending, reason, l.r.Text(note))
}

func (l loops) SetModelRefusal(ctx context.Context, id, model, refusal string, updatedAt int64) error {
	// The CLI's sentence around the configured model id. It is the refused
	// turn's result text, which the turn row stores redacted, so the loop
	// row stores the same.
	return l.LoopStore.SetModelRefusal(ctx, id, model, l.r.Text(refusal), updatedAt)
}

// Each decorator embeds the interface it wraps, so a method added to the
// store seam keeps compiling here and passes straight through. That is the
// right default for reads; a new *write* of free text has to be added below,
// and the tier-1 test that walks the interfaces is what says so.
type turns struct {
	store.TurnStore
	r *Redactor
}

func (t turns) Create(ctx context.Context, turn *store.Turn) error {
	t.clean(turn)
	return t.TurnStore.Create(ctx, turn)
}

func (t turns) Finish(ctx context.Context, turn *store.Turn) error {
	t.clean(turn)
	return t.TurnStore.Finish(ctx, turn)
}

func (t turns) clean(turn *store.Turn) {
	if turn != nil {
		turn.ResultText = t.r.Text(turn.ResultText)
	}
}

type events struct {
	store.EventStore
	r *Redactor
}

func (e events) Insert(ctx context.Context, ev *store.Event) (int64, error) {
	if ev != nil {
		ev.Payload = e.r.Text(ev.Payload)
	}
	return e.EventStore.Insert(ctx, ev)
}

type messages struct {
	store.MessageStore
	r *Redactor
}

func (m messages) Insert(ctx context.Context, msg *store.Message) error {
	if msg != nil {
		msg.Text = m.r.Text(msg.Text)
		msg.SendError = m.r.Text(msg.SendError)
	}
	return m.MessageStore.Insert(ctx, msg)
}

func (m messages) SetSendResult(ctx context.Context, id, failedAt int64, sendErr string) error {
	// The one that bit us: #146's leak was a transport error, stored here.
	return m.MessageStore.SetSendResult(ctx, id, failedAt, m.r.Text(sendErr))
}

func (m messages) FailInterruptedSends(ctx context.Context, failedAt int64, sendErr string) ([]*store.Message, error) {
	// The hub's own fixed sentence today, but it is stored as a send error,
	// and that column is where #146's leak lived.
	return m.MessageStore.FailInterruptedSends(ctx, failedAt, m.r.Text(sendErr))
}
