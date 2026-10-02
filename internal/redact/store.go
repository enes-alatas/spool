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
func Store(inner store.Store, redactor *Redactor) store.Store {
	return redactedStore{Store: inner, redactor: redactor}
}

type redactedStore struct {
	store.Store
	redactor *Redactor
}

func (redacted redactedStore) Loops() store.LoopStore {
	return loops{redacted.Store.Loops(), redacted.redactor}
}

func (redacted redactedStore) Turns() store.TurnStore {
	return turns{redacted.Store.Turns(), redacted.redactor}
}

func (redacted redactedStore) Events() store.EventStore {
	return events{redacted.Store.Events(), redacted.redactor}
}

func (redacted redactedStore) Messages() store.MessageStore {
	return messages{redacted.Store.Messages(), redacted.redactor}
}

func (redacted redactedStore) Attachments() store.AttachmentStore {
	return attachments{redacted.Store.Attachments(), redacted.redactor}
}

func (redacted redactedStore) Polls() store.PollStore {
	return polls{redacted.Store.Polls(), redacted.redactor}
}

// polls redacts a ballot's options, which the loop that polled wrote. Its
// question is the poll message's text, redacted where the message is.
type polls struct {
	store.PollStore
	redactor *Redactor
}

func (pollStore polls) Create(ctx context.Context, poll *store.Poll) error {
	if poll != nil {
		for i, option := range poll.Options {
			poll.Options[i] = pollStore.redactor.Text(option)
		}
	}
	return pollStore.PollStore.Create(ctx, poll)
}

// attachments redacts the one free-text field an attachment row carries:
// its name, which the sender chose. The file is not text the redactor can
// read, and is never logged.
type attachments struct {
	store.AttachmentStore
	redactor *Redactor
}

func (attachmentStore attachments) Insert(ctx context.Context, attachment *store.Attachment) error {
	if attachment != nil {
		attachment.Name = attachmentStore.redactor.Text(attachment.Name)
	}
	return attachmentStore.AttachmentStore.Insert(ctx, attachment)
}

// loops redacts the one free-text field a loop row carries. Its token fields
// are deliberately untouched: those values *are* the secrets, and a decorator
// that redacted them on the way in would write a placeholder where the bot
// token belongs and take the loop's bot down with it.
type loops struct {
	store.LoopStore
	redactor *Redactor
}

func (loopStore loops) SetRotation(ctx context.Context, id string, pending bool, reason, note string) error {
	// The handoff note is written by the loop, in its own words, and read
	// back into the next session's prompt — free text with a turn's worth of
	// whatever it was holding.
	return loopStore.LoopStore.SetRotation(ctx, id, pending, reason, loopStore.redactor.Text(note))
}

func (loopStore loops) SetModelRefusal(ctx context.Context, id, model, refusal string, updatedAt int64) error {
	// The CLI's sentence around the configured model id. It is the refused
	// turn's result text, which the turn row stores redacted, so the loop
	// row stores the same.
	return loopStore.LoopStore.SetModelRefusal(ctx, id, model, loopStore.redactor.Text(refusal), updatedAt)
}

// Each decorator embeds the interface it wraps, so a method added to the
// store seam keeps compiling here and passes straight through. That is the
// right default for reads; a new *write* of free text has to be added below,
// and the tier-1 test that walks the interfaces is what says so.
type turns struct {
	store.TurnStore
	redactor *Redactor
}

func (turnStore turns) Create(ctx context.Context, turn *store.Turn) error {
	turnStore.clean(turn)
	return turnStore.TurnStore.Create(ctx, turn)
}

func (turnStore turns) Finish(ctx context.Context, turn *store.Turn) error {
	turnStore.clean(turn)
	return turnStore.TurnStore.Finish(ctx, turn)
}

func (turnStore turns) clean(turn *store.Turn) {
	if turn != nil {
		turn.ResultText = turnStore.redactor.Text(turn.ResultText)
	}
}

type events struct {
	store.EventStore
	redactor *Redactor
}

func (eventStore events) Insert(ctx context.Context, ev *store.Event) (int64, error) {
	if ev != nil {
		ev.Payload = eventStore.redactor.Text(ev.Payload)
	}
	return eventStore.EventStore.Insert(ctx, ev)
}

type messages struct {
	store.MessageStore
	redactor *Redactor
}

func (messageStore messages) Insert(ctx context.Context, msg *store.Message) error {
	if msg != nil {
		msg.Text = messageStore.redactor.Text(msg.Text)
		msg.SendError = messageStore.redactor.Text(msg.SendError)
	}
	return messageStore.MessageStore.Insert(ctx, msg)
}

func (messageStore messages) SetSendResult(ctx context.Context, id, failedAt int64, sendErr string) error {
	// The one that bit us: #146's leak was a transport error, stored here.
	return messageStore.MessageStore.SetSendResult(ctx, id, failedAt, messageStore.redactor.Text(sendErr))
}

func (messageStore messages) FailInterruptedSends(ctx context.Context, failedAt int64, sendErr string) ([]*store.Message, error) {
	// The hub's own fixed sentence today, but it is stored as a send error,
	// and that column is where #146's leak lived.
	return messageStore.MessageStore.FailInterruptedSends(ctx, failedAt, messageStore.redactor.Text(sendErr))
}
