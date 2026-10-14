package loop

import (
	"testing"
	"time"

	"github.com/enes-alatas/spool/internal/store"
)

// A correction of a message still waiting for a turn is applied to it in
// place: an edit replaces its text and a deletion takes it out, and the
// correction itself is not queued. One whose message a turn already took,
// or that names no waiting message, is queued as a message of its own
// (ADR-0055, #811).
func TestACorrectionOfAWaitingMessageIsAppliedInPlace(t *testing.T) {
	waiting := func() []Envelope {
		return []Envelope{
			{Trigger: store.TriggerMessage, Text: "[message · ref:1]\n\nship friday", Ref: "ref:1"},
			{Trigger: store.TriggerMessage, Text: "[message · ref:2]\n\nhello", Ref: "ref:2"},
		}
	}
	correction := func(ref, kind, text string) Envelope {
		return Envelope{Trigger: store.TriggerMessage, Text: "[message · ref:9 · " + kind + " " + ref + "]", Ref: "ref:9",
			Corrects: &Correction{Ref: ref, Kind: kind, Text: text}}
	}

	actor := &Actor{inbox: waiting()}
	actor.enqueue(correction("ref:1", store.EditEdited, "[message · ref:1]\n\nship monday"))
	if len(actor.inbox) != 2 || actor.inbox[0].Text != "[message · ref:1]\n\nship monday" || actor.inbox[1].Ref != "ref:2" {
		t.Fatalf("an edit of a waiting message left %+v; want it replaced in place and nothing queued", actor.inbox)
	}

	actor = &Actor{inbox: waiting()}
	actor.enqueue(correction("ref:1", store.EditDeleted, ""))
	if len(actor.inbox) != 1 || actor.inbox[0].Ref != "ref:2" {
		t.Fatalf("a deletion of a waiting message left %+v; want it taken out and nothing queued", actor.inbox)
	}

	for _, kind := range []string{store.EditEdited, store.EditDeleted} {
		actor = &Actor{inbox: waiting()}
		actor.enqueue(correction("ref:7", kind, "[message · ref:7]\n\nlater"))
		if len(actor.inbox) != 3 || actor.inbox[2].Ref != "ref:9" || actor.inbox[0].Text != "[message · ref:1]\n\nship friday" {
			t.Fatalf("a %s correction of a message already read left %+v; want it queued as its own", kind, actor.inbox)
		}
	}
}

// A message's envelope carries its reference, and a correction's names
// what it corrects, which is how the actor matches the two.
func TestAnEnvelopeCarriesItsRefAndWhatItCorrects(t *testing.T) {
	plain := MessageEnvelope(time.Now(), Inbound{Origin: store.OriginSlackChannel, Author: "enes", Text: "ship friday",
		Conversation: store.ConversationGroup, Ref: "ref:1"})
	if plain.Ref != "ref:1" || plain.Corrects != nil {
		t.Fatalf("a message's envelope: %+v", plain)
	}
	edit := MessageEnvelope(time.Now(), Inbound{Origin: store.OriginSlackChannel, Author: "enes", Text: "ship monday",
		Conversation: store.ConversationGroup, Ref: "ref:9", EditOf: "ref:1", EditKind: store.EditEdited})
	if edit.Ref != "ref:9" || edit.Corrects == nil || *edit.Corrects != (Correction{Ref: "ref:1", Kind: store.EditEdited}) {
		t.Fatalf("a correction's envelope: %+v", edit)
	}
}
