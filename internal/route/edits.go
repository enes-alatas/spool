package route

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/enes-alatas/spool/internal/bus"
	"github.com/enes-alatas/spool/internal/loop"
	"github.com/enes-alatas/spool/internal/store"
)

// InboundEdit is an edit or a deletion a surface received of a message a
// person said there, resolved to the hub's message as a reaction's target
// is (ADR-0055).
type InboundEdit struct {
	Original *store.Message
	Author   string // display name, no @
	// Text is the person's new words, and "" for a deletion.
	Text string
	Kind string // store.EditEdited or store.EditDeleted
	// Key names this edit as every identity that heard it sees it alike,
	// so a shared room's edit is stored once, by whichever reports it
	// first.
	Key string
}

// Correct stores a person's edit or deletion of a message as a correction:
// a message of its own, from them, in the original's conversation, that
// says which message it corrects. The original is marked edited or
// deleted. An edit leaves it its words; a deletion takes them, the words
// of its edits and its kept files. The correction reaches every loop the
// original reached, so none acts on words taken back, and in a group any
// loop the new words @mention there too. It wakes them as a message does
// (ADR-0055), except a loop that has not read the original yet: that
// loop's waiting envelope is corrected in place instead (#811).
// store.ErrDuplicate when another identity already stored this edit.
func (router *Router) Correct(ctx context.Context, in InboundEdit) error {
	original := in.Original
	switch {
	case original == nil || in.Key == "":
		return errors.New("route: a correction needs the message it corrects and its key")
	case in.Kind != store.EditEdited && in.Kind != store.EditDeleted:
		return errors.New("route: a correction edits or deletes")
	case original.FromLoopID != "":
		return errors.New("route: only a person's message is corrected")
	}
	text := in.Text
	if in.Kind == store.EditDeleted {
		text = ""
	}
	msg := &store.Message{
		TS:                 time.Now().UnixMilli(),
		Origin:             original.Origin,
		Author:             in.Author,
		Text:               text,
		Conversation:       original.Conversation,
		ConversationLoopID: original.ConversationLoopID,
		DMPerson:           original.DMPerson,
		Channel:            original.Channel,
		Mirror:             inboundMirror(original.Origin),
		EditOf:             original.ID,
		EditKind:           in.Kind,
		EditKey:            in.Key,
	}

	loops, err := router.store.Loops().List(ctx)
	if err != nil {
		return err
	}
	targets := map[string]*store.Loop{}
	if msg.Conversation == store.ConversationGroup {
		member, err := router.memberOf(ctx, msg.Channel)
		if err != nil {
			return err
		}
		msg.Mentions = Mentions(text)
		targets = router.mentionTargets(loops, msg.Mentions, "", member)
	}
	for _, loopRecord := range loops {
		if loopRecord.Status != store.StatusArchived && slices.Contains(original.DeliveredTo, loopRecord.ID) {
			targets[loopRecord.ID] = loopRecord
		}
	}
	for id := range targets {
		msg.DeliveredTo = append(msg.DeliveredTo, id)
	}

	if err := router.store.Messages().Insert(ctx, msg); err != nil {
		return err // ErrDuplicate: another identity stored this edit first
	}
	if err := router.store.Messages().MarkEdited(ctx, original.ID, in.Kind, msg.TS); err != nil {
		if in.Kind == store.EditDeleted {
			// the hub keeps words its author deleted, against ADR-0055
			router.log.Error("blank a deleted message", "message", original.ID, "err", err)
		} else {
			router.log.Warn("mark a corrected message", "message", original.ID, "err", err)
		}
	}
	if in.Kind == store.EditDeleted {
		router.removeAttachments(ctx, original.ID)
	}
	router.bus.Publish(bus.Item{Kind: bus.KindMessage, Payload: &MessagePayload{Message: *msg}})

	var tgChatID int64
	if original.Origin == store.OriginTelegramDM {
		tgChatID = original.TGChatID
	}
	// A loop that has not read the original yet reads it as it now stands
	// instead: the envelope it waits in, rendered again with the new words.
	var kept []*store.Attachment
	if in.Kind == store.EditEdited {
		if kept, err = router.store.Attachments().ByMessage(ctx, original.ID); err != nil {
			router.log.Warn("a corrected message's attachments", "message", original.ID, "err", err)
		}
	}
	replyTo := ""
	if original.ReplyToID != 0 {
		replyTo = loop.MessageRef(original.ReplyToID)
	}
	now := time.Now()
	for _, target := range targets {
		env := loop.MessageEnvelope(now, loop.Inbound{
			Origin:       msg.Origin,
			Author:       msg.Author,
			Text:         msg.Text,
			Conversation: msg.Conversation,
			Channel:      msg.Channel,
			TGChatID:     tgChatID,
			Ref:          loop.MessageRef(msg.ID),
			EditOf:       loop.MessageRef(original.ID),
			EditKind:     in.Kind,
		})
		if in.Kind == store.EditEdited {
			shown, _ := router.present(target, kept)
			env.Corrects.Text = loop.MessageEnvelope(time.UnixMilli(original.TS), loop.Inbound{
				Origin:       original.Origin,
				Author:       original.Author,
				Text:         msg.Text,
				Conversation: original.Conversation,
				Channel:      original.Channel,
				TGChatID:     tgChatID,
				Ref:          loop.MessageRef(original.ID),
				ReplyTo:      replyTo,
				Attachments:  shown,
			}).Text
		}
		if !router.deliver.Deliver(target.ID, env) {
			router.log.Warn("deliver to unknown runtime", "loop", target.Name)
		}
	}
	return nil
}
