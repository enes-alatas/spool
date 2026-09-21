package redact

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/enes-alatas/spool/internal/store"
)

// The doubles below implement only the methods these tests drive; the
// embedded interface supplies the rest and panics if anything calls one,
// which is the right answer for a decorator test that reached too far.
type recordingTurns struct {
	store.TurnStore
	got *store.Turn
}

func (r *recordingTurns) Create(_ context.Context, t *store.Turn) error { r.got = t; return nil }
func (r *recordingTurns) Finish(_ context.Context, t *store.Turn) error { r.got = t; return nil }

type recordingEvents struct {
	store.EventStore
	got *store.Event
}

func (r *recordingEvents) Insert(_ context.Context, e *store.Event) (int64, error) {
	r.got = e
	return 7, nil
}

type recordingMessages struct {
	store.MessageStore
	got     *store.Message
	sendErr string
}

func (r *recordingMessages) Insert(_ context.Context, m *store.Message) error {
	r.got = m
	m.ID = 42 // the sqlite store fills the new row's id in; so must the double
	return nil
}

func (r *recordingMessages) SetSendResult(_ context.Context, _, _ int64, sendErr string) error {
	r.sendErr = sendErr
	return nil
}

type recordingLoops struct {
	store.LoopStore
	note string
}

func (r *recordingLoops) SetRotation(_ context.Context, _ string, _ bool, note string) error {
	r.note = note
	return nil
}

type fakeStore struct {
	store.Store
	loops    store.LoopStore
	turns    store.TurnStore
	events   store.EventStore
	messages store.MessageStore
}

func (f fakeStore) Loops() store.LoopStore       { return f.loops }
func (f fakeStore) Turns() store.TurnStore       { return f.turns }
func (f fakeStore) Events() store.EventStore     { return f.events }
func (f fakeStore) Messages() store.MessageStore { return f.messages }

const secretValue = "ghp_storedsecretvalue"

type doubles struct {
	loops    *recordingLoops
	turns    *recordingTurns
	events   *recordingEvents
	messages *recordingMessages
}

func decorated(t *testing.T) (store.Store, doubles) {
	t.Helper()
	r, _ := loaded(t, Secret{Name: "GH_TOKEN", Value: secretValue})
	d := doubles{&recordingLoops{}, &recordingTurns{}, &recordingEvents{}, &recordingMessages{}}
	inner := fakeStore{loops: d.loops, turns: d.turns, events: d.events, messages: d.messages}
	return Store(inner, r), d
}

func TestTurnTextIsRedactedOnTheWayIn(t *testing.T) {
	s, d := decorated(t)

	for _, write := range []struct {
		name string
		call func(*store.Turn) error
	}{
		{"Create", func(turn *store.Turn) error { return s.Turns().Create(context.Background(), turn) }},
		{"Finish", func(turn *store.Turn) error { return s.Turns().Finish(context.Background(), turn) }},
	} {
		if err := write.call(&store.Turn{ResultText: "here is " + secretValue}); err != nil {
			t.Fatalf("%s: %v", write.name, err)
		}
		if got := d.turns.got.ResultText; got != "here is <redacted:GH_TOKEN>" {
			t.Errorf("%s stored %q", write.name, got)
		}
	}
}

// The raw claude event is where a tool input lands, which is how a secret
// passed to a command ends up in the transcript.
func TestEventPayloadIsRedactedOnTheWayIn(t *testing.T) {
	s, d := decorated(t)

	id, err := s.Events().Insert(context.Background(), &store.Event{
		Payload: `{"type":"tool_use","input":{"env":"` + secretValue + `"}}`,
	})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if id != 7 {
		t.Errorf("Insert returned id %d, want the inner store's 7", id)
	}
	if got := d.events.got.Payload; got != `{"type":"tool_use","input":{"env":"<redacted:GH_TOKEN>"}}` {
		t.Errorf("stored payload %q", got)
	}
}

func TestMessageTextIsRedactedAndTheNewIDStillComesBack(t *testing.T) {
	s, d := decorated(t)

	m := &store.Message{Text: "the token is " + secretValue}
	if err := s.Messages().Insert(context.Background(), m); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if got := d.messages.got.Text; got != "the token is <redacted:GH_TOKEN>" {
		t.Errorf("stored text %q", got)
	}
	// Redacting a copy would drop this, and the router routes by it.
	if m.ID != 42 {
		t.Errorf("caller's message id = %d, want the id the store assigned", m.ID)
	}
}

// #146 itself: the transport error that quoted the URL it failed on.
func TestSendErrorIsRedacted(t *testing.T) {
	s, d := decorated(t)

	err := s.Messages().SetSendResult(context.Background(), 1, 99,
		`Post "https://api.telegram.org/bot`+secretValue+`/send": timeout`)
	if err != nil {
		t.Fatalf("SetSendResult: %v", err)
	}
	if want := `Post "https://api.telegram.org/bot<redacted:GH_TOKEN>/send": timeout`; d.messages.sendErr != want {
		t.Errorf("stored send error %q", d.messages.sendErr)
	}
}

// TestEveryStoreWriteIsClassified walks every sub-interface of the Store
// seam — all eleven, not just the decorated three — and requires each method
// to be listed with whether it writes free text a secret could be in.
//
// The walk is this wide because the narrow version lied. It covered the three
// interfaces the decorator happened to wrap, so a free-text write added
// anywhere else compiled and passed in silence; LoopStore.SetRotation, which
// stores a loop's handoff note, was already such a write when this test first
// claimed the seam was covered.
//
// A true here means the decorator redacts it and a test above proves it. A
// false is a claim that the method writes nothing a secret can hide in, or
// that redacting it would break what it is for — the comments below say
// which, for the ones where it is not obvious.
func TestEveryStoreWriteIsClassified(t *testing.T) {
	classified := map[string]map[string]bool{
		"LoopStore": {
			"SetRotation": true, // the loop's own handoff note
			// Create and Edit carry the bot and hub-MCP tokens themselves.
			// Redacting those would write a placeholder where the
			// credential belongs; the mission is operator-written text, and
			// an operator pasting their own secret into it is #30's problem.
			"Create": false, "Edit": false,
			"Delete": false, "Get": false, "GetByHubMCPToken": false,
			"GetByName": false, "List": false, "SetGroupBinding": false,
			"SetOwner": false, "SetOwnerDMChat": false, "SetPromptHash": false,
			"SetRuntime": false, "SetStatus": false, "SetWorkstationOff": false,
		},
		"TurnStore": {
			"Create": true, "Finish": true,
			"ListByLoop": false, "Latest": false, "InterruptDangling": false,
			"CostSince": false, "SessionCost": false,
		},
		"EventStore": {
			"Insert":     true,
			"ListByLoop": false, "ListByLoopBefore": false, "DeleteBefore": false,
		},
		"MessageStore": {
			"Insert": true, "SetSendResult": true,
			"SetDelivered": false, "List": false, "ListConversation": false,
			"Get": false, "UntoldSendFailures": false, "SendFailuresSince": false,
			"MarkSendFailuresTold": false,
			"PutRef":               false, "Ref": false, "RecordSighting": false, "ByRef": false,
			"ByTGKey": false, "LatestGroupPostBy": false,
		},
		"InboxStore": {
			// Push queues an envelope the loop is about to be handed. Its
			// text came from a message that was redacted at Insert, and this
			// copy is delivery input as much as a record: redacting here
			// would edit what the loop is told, not just what is kept.
			"Push": false, "Drain": false,
		},
		"LoopSecretStore": {
			// The secret values themselves. Redacting a secret on its way
			// into the table it is read back out of is a circle.
			"Set": false, "Delete": false, "List": false,
		},
		"SettingsStore": {
			"Set": false, "Get": false, // holds the operator's Claude token, same reason
		},
		"FleetRuleStore": {
			// Operator-written rule text, rendered into every prompt.
			"Create": false, "Update": false,
			"Delete": false, "Get": false, "List": false,
		},
		"SessionStore": {
			"Create": false, "End": false, "EndDangling": false, "ListByLoop": false,
		},
		"ScheduleStore": {
			"Set": false, "SetLastTick": false, "Get": false, "All": false, "Delete": false,
		},
		"TGSenderStore": {
			"Create": false, "SetStatus": false, "Delete": false, "Get": false, "List": false,
		},
	}

	seam := reflect.TypeOf((*store.Store)(nil)).Elem()
	seen := map[string]bool{}
	for i := range seam.NumMethod() {
		out := seam.Method(i).Type.Out(0)
		if out.Kind() != reflect.Interface || !strings.HasSuffix(out.Name(), "Store") {
			continue // Close() error, and anything else that is not a sub-store
		}
		seen[out.Name()] = true
		listed, ok := classified[out.Name()]
		if !ok {
			t.Errorf("%s is a new sub-store: classify its methods here", out.Name())
			continue
		}
		for j := range out.NumMethod() {
			method := out.Method(j).Name
			if _, known := listed[method]; !known {
				t.Errorf("%s.%s is new: classify it here — does it write text a secret could be in? If so, redact it in store.go and test it above.",
					out.Name(), method)
			}
		}
		if out.NumMethod() != len(listed) {
			t.Errorf("%s has %d methods but %d are listed: one was removed, or one is listed twice",
				out.Name(), out.NumMethod(), len(listed))
		}
	}
	for name := range classified {
		if !seen[name] {
			t.Errorf("%s is listed here but is no longer part of the Store seam", name)
		}
	}
}
