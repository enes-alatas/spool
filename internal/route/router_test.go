package route

import (
	"reflect"
	"testing"

	"github.com/enes-alatas/spool/internal/store"
)

func TestMentions(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"@ping 1", []string{"ping"}},
		{"hey @Fixer and @watcher-2, look", []string{"fixer", "watcher-2"}},
		{"emails like a@b.com don't count", nil},
		{"@dup @dup once", []string{"dup"}},
		{"(@paren) works", []string{"paren"}},
		{"none here", nil},
		{"@planner_spool_bot hello", []string{"planner_spool_bot"}},
	}
	for _, c := range cases {
		got := Mentions(c.in)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("Mentions(%q) = %v want %v", c.in, got, c.want)
		}
	}
}

func TestConversationFor(t *testing.T) {
	cases := []struct {
		name   string
		in     InboundMessage
		kind   string
		loopID string
	}{
		{"dm to a loop's bot", InboundMessage{Origin: store.OriginTelegramDM, ImplicitTo: "l1"}, store.ConversationOwnerDM, "l1"},
		{"group message", InboundMessage{Origin: store.OriginTelegramGroup}, store.ConversationGroup, ""},
		{"loop reply", InboundMessage{Origin: store.OriginLoop, FromLoopID: "l1"}, store.ConversationGroup, ""},
		{"per-loop web composer", InboundMessage{Origin: store.OriginWeb, ImplicitTo: "l2"}, store.ConversationControlRoom, "l2"},
		{"composer group destination", InboundMessage{Origin: store.OriginWeb, ImplicitTo: "l2",
			Conversation: store.ConversationGroup}, store.ConversationGroup, ""},
		{"composer explicit control_room", InboundMessage{Origin: store.OriginWeb, ImplicitTo: "l2",
			Conversation: store.ConversationControlRoom}, store.ConversationControlRoom, "l2"},
		{"unaddressed web message", InboundMessage{Origin: store.OriginWeb}, store.ConversationGroup, ""},
	}
	for _, c := range cases {
		kind, loopID := conversationFor(c.in)
		if kind != c.kind || loopID != c.loopID {
			t.Errorf("%s: conversationFor = %q/%q, want %q/%q", c.name, kind, loopID, c.kind, c.loopID)
		}
	}
}

func TestSendBudget(t *testing.T) {
	r := &Router{}
	for i := 0; i < SendCapPerTurn; i++ {
		if !r.sendAllow("l1") {
			t.Fatalf("send %d refused before the cap", i+1)
		}
	}
	if r.sendAllow("l1") {
		t.Fatal("send beyond the cap allowed")
	}
	if !r.sendAllow("l2") {
		t.Fatal("another loop's budget affected")
	}
	r.StartTurn("l1")
	if !r.sendAllow("l1") {
		t.Fatal("send refused after a fresh turn")
	}
}

// TestSameConversation: a reply target must belong to the conversation being
// sent to. Only the group has no loop key; on a private kind an empty key is
// a backfilled row (migration 0009), not a wildcard — reading it as one
// would quote another loop's DM into this one (ADR-0025).
func TestSameConversation(t *testing.T) {
	cases := []struct {
		name        string
		target      store.Message
		destination string
		want        bool
	}{
		{"group message answered in the group",
			store.Message{Conversation: store.ConversationGroup}, store.ConversationGroup, true},
		{"own owner_dm message",
			store.Message{Conversation: store.ConversationOwnerDM, ConversationLoopID: "l1"}, store.ConversationOwnerDM, true},
		{"another loop's owner_dm message",
			store.Message{Conversation: store.ConversationOwnerDM, ConversationLoopID: "l2"}, store.ConversationOwnerDM, false},
		{"owner_dm message keyed to no loop",
			store.Message{Conversation: store.ConversationOwnerDM}, store.ConversationOwnerDM, false},
		{"control_room message keyed to no loop",
			store.Message{Conversation: store.ConversationControlRoom}, store.ConversationControlRoom, false},
		{"group message answered in a DM",
			store.Message{Conversation: store.ConversationGroup}, store.ConversationOwnerDM, false},
	}
	for _, c := range cases {
		if got := sameConversation(&c.target, c.destination, "l1"); got != c.want {
			t.Errorf("%s: sameConversation = %v, want %v", c.name, got, c.want)
		}
	}
}
