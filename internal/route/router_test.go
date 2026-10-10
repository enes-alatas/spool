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
	for _, testCase := range cases {
		got := Mentions(testCase.in)
		if !reflect.DeepEqual(got, testCase.want) {
			t.Errorf("Mentions(%q) = %v want %v", testCase.in, got, testCase.want)
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
	for _, testCase := range cases {
		kind, loopID := conversationFor(testCase.in)
		if kind != testCase.kind || loopID != testCase.loopID {
			t.Errorf("%s: conversationFor = %q/%q, want %q/%q", testCase.name, kind, loopID, testCase.kind, testCase.loopID)
		}
	}
}

func TestSendBudget(t *testing.T) {
	router := &Router{}
	for i := 0; i < SendCapPerTurn; i++ {
		if !router.sendAllow("l1") {
			t.Fatalf("send %d refused before the cap", i+1)
		}
	}
	if router.sendAllow("l1") {
		t.Fatal("send beyond the cap allowed")
	}
	if !router.sendAllow("l2") {
		t.Fatal("another loop's budget affected")
	}
	router.StartTurn("l1")
	if !router.sendAllow("l1") {
		t.Fatal("send refused after a fresh turn")
	}
}

// TestSameConversation: a reply target must belong to the conversation being
// sent to. Only the group has no loop key; on a per-loop kind an empty key is
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
	for _, testCase := range cases {
		if got := sameConversation(&testCase.target, testCase.destination, "l1", ""); got != testCase.want {
			t.Errorf("%s: sameConversation = %v, want %v", testCase.name, got, testCase.want)
		}
	}
}

// TestADMIsThePersonNotTheName: a reply or a resend stays in a dm:<name>
// conversation by who it is with, so a renamed person is still the same
// conversation and a name someone else has since taken is not (ADR-0049).
func TestADMIsThePersonNotTheName(t *testing.T) {
	alice := store.TGPerson(2)
	target := store.Message{Conversation: "dm:alice", ConversationLoopID: "l1", DMPerson: alice}
	cases := []struct {
		name, destination, person string
		want                      bool
	}{
		{"the same person by the same name", "dm:alice", alice, true},
		{"the same person renamed", "dm:alicia", alice, true},
		{"someone else who took the name", "dm:alice", store.TGPerson(3), false},
		{"a name nobody answers to", "dm:alice", "", false},
		{"owner_dm", store.ConversationOwnerDM, "", false},
	}
	for _, testCase := range cases {
		if got := sameConversation(&target, testCase.destination, "l1", testCase.person); got != testCase.want {
			t.Errorf("%s: sameConversation = %v, want %v", testCase.name, got, testCase.want)
		}
	}
	if sameConversation(&target, "dm:alice", "l2", alice) {
		t.Error("another loop's conversation with the same person counted as this one's")
	}
}
