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

func (recorder *recordingTurns) Create(_ context.Context, turn *store.Turn) error {
	recorder.got = turn
	return nil
}

func (recorder *recordingTurns) Finish(_ context.Context, turn *store.Turn) error {
	recorder.got = turn
	return nil
}

type recordingEvents struct {
	store.EventStore
	got *store.Event
}

func (recorder *recordingEvents) Insert(_ context.Context, event *store.Event) (int64, error) {
	recorder.got = event
	return 7, nil
}

type recordingMessages struct {
	store.MessageStore
	got     *store.Message
	sendErr string
}

func (recorder *recordingMessages) Insert(_ context.Context, message *store.Message) error {
	recorder.got = message
	message.ID = 42 // the sqlite store fills the new row's id in; so must the double
	return nil
}

func (recorder *recordingMessages) SetSendResult(_ context.Context, _, _ int64, sendErr string) error {
	recorder.sendErr = sendErr
	return nil
}

func (recorder *recordingMessages) FailInterruptedSends(_ context.Context, _ int64, sendErr string) ([]*store.Message, error) {
	recorder.sendErr = sendErr
	return nil, nil
}

type recordingLoops struct {
	store.LoopStore
	note    string
	refusal string
}

func (recorder *recordingLoops) SetModelRefusal(_ context.Context, _, _, refusal string, _ int64) error {
	recorder.refusal = refusal
	return nil
}

func (recorder *recordingLoops) SetRotation(_ context.Context, _ string, _ bool, _, note string) error {
	recorder.note = note
	return nil
}

type fakeStore struct {
	store.Store
	loops    store.LoopStore
	turns    store.TurnStore
	events   store.EventStore
	messages store.MessageStore
}

func (fake fakeStore) Loops() store.LoopStore       { return fake.loops }
func (fake fakeStore) Turns() store.TurnStore       { return fake.turns }
func (fake fakeStore) Events() store.EventStore     { return fake.events }
func (fake fakeStore) Messages() store.MessageStore { return fake.messages }

const secretValue = "ghp_storedsecretvalue"

type doubles struct {
	loops    *recordingLoops
	turns    *recordingTurns
	events   *recordingEvents
	messages *recordingMessages
}

func decorated(t *testing.T) (store.Store, doubles) {
	t.Helper()
	redactor, _ := loaded(t, Secret{Name: "GH_TOKEN", Value: secretValue})
	recorders := doubles{&recordingLoops{}, &recordingTurns{}, &recordingEvents{}, &recordingMessages{}}
	inner := fakeStore{loops: recorders.loops, turns: recorders.turns, events: recorders.events, messages: recorders.messages}
	return Store(inner, redactor), recorders
}

func TestTurnTextIsRedactedOnTheWayIn(t *testing.T) {
	redacting, recorders := decorated(t)

	for _, write := range []struct {
		name string
		call func(*store.Turn) error
	}{
		{"Create", func(turn *store.Turn) error { return redacting.Turns().Create(context.Background(), turn) }},
		{"Finish", func(turn *store.Turn) error { return redacting.Turns().Finish(context.Background(), turn) }},
	} {
		if err := write.call(&store.Turn{ResultText: "here is " + secretValue}); err != nil {
			t.Fatalf("%s: %v", write.name, err)
		}
		if got := recorders.turns.got.ResultText; got != "here is <redacted:GH_TOKEN>" {
			t.Errorf("%s stored %q", write.name, got)
		}
	}
}

// A model refusal is the refused turn's result text, stored on the loop.
func TestModelRefusalIsRedacted(t *testing.T) {
	redacting, recorders := decorated(t)

	if err := redacting.Loops().SetModelRefusal(context.Background(), "loop", "m", "no model named "+secretValue, 1); err != nil {
		t.Fatalf("SetModelRefusal: %v", err)
	}
	if want := "no model named <redacted:GH_TOKEN>"; recorders.loops.refusal != want {
		t.Errorf("stored refusal %q", recorders.loops.refusal)
	}
}

// The raw claude event is where a tool input lands, which is how a secret
// passed to a command ends up in the transcript.
func TestEventPayloadIsRedactedOnTheWayIn(t *testing.T) {
	redacting, recorders := decorated(t)

	id, err := redacting.Events().Insert(context.Background(), &store.Event{
		Payload: `{"type":"tool_use","input":{"env":"` + secretValue + `"}}`,
	})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if id != 7 {
		t.Errorf("Insert returned id %d, want the inner store's 7", id)
	}
	if got := recorders.events.got.Payload; got != `{"type":"tool_use","input":{"env":"<redacted:GH_TOKEN>"}}` {
		t.Errorf("stored payload %q", got)
	}
}

type recordingAttachments struct {
	store.AttachmentStore
	got *store.Attachment
}

func (recorder *recordingAttachments) Insert(_ context.Context, attachment *store.Attachment) error {
	recorder.got = attachment
	attachment.ID = 9
	return nil
}

type attachmentStore struct {
	store.Store
	attachments store.AttachmentStore
}

func (fake attachmentStore) Attachments() store.AttachmentStore { return fake.attachments }

// An attachment's name is the sender's to choose, so it is free text.
func TestAttachmentNameIsRedacted(t *testing.T) {
	redactor, _ := loaded(t, Secret{Name: "GH_TOKEN", Value: secretValue})
	recorder := &recordingAttachments{}
	redacting := Store(attachmentStore{attachments: recorder}, redactor)
	attachment := &store.Attachment{Name: secretValue + ".txt"}
	if err := redacting.Attachments().Insert(context.Background(), attachment); err != nil {
		t.Fatal(err)
	}
	if recorder.got.Name != "<redacted:GH_TOKEN>.txt" || attachment.ID != 9 {
		t.Errorf("stored %+v", recorder.got)
	}
}

type recordingPolls struct {
	store.PollStore
	got *store.Poll
}

func (recorder *recordingPolls) Create(_ context.Context, poll *store.Poll) error {
	recorder.got = poll
	return nil
}

type pollStore struct {
	store.Store
	polls store.PollStore
}

func (fake pollStore) Polls() store.PollStore { return fake.polls }

// A poll's options are the polling loop's words, so they are free text.
func TestPollOptionsAreRedacted(t *testing.T) {
	redactor, _ := loaded(t, Secret{Name: "GH_TOKEN", Value: secretValue})
	recorder := &recordingPolls{}
	redacting := Store(pollStore{polls: recorder}, redactor)
	poll := &store.Poll{Options: []string{"keep", "use " + secretValue}}
	if err := redacting.Polls().Create(context.Background(), poll); err != nil {
		t.Fatal(err)
	}
	if got := recorder.got.Options; got[0] != "keep" || got[1] != "use <redacted:GH_TOKEN>" {
		t.Errorf("stored options %q", got)
	}
}

func TestMessageTextIsRedactedAndTheNewIDStillComesBack(t *testing.T) {
	redacting, recorders := decorated(t)

	message := &store.Message{Text: "the token is " + secretValue}
	if err := redacting.Messages().Insert(context.Background(), message); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if got := recorders.messages.got.Text; got != "the token is <redacted:GH_TOKEN>" {
		t.Errorf("stored text %q", got)
	}
	// Redacting a copy would drop this, and the router routes by it.
	if message.ID != 42 {
		t.Errorf("caller's message id = %d, want the id the store assigned", message.ID)
	}
}

// #146 itself: the transport error that quoted the URL it failed on.
func TestSendErrorIsRedacted(t *testing.T) {
	redacting, recorders := decorated(t)

	err := redacting.Messages().SetSendResult(context.Background(), 1, 99,
		`Post "https://api.telegram.org/bot`+secretValue+`/send": timeout`)
	if err != nil {
		t.Fatalf("SetSendResult: %v", err)
	}
	if want := `Post "https://api.telegram.org/bot<redacted:GH_TOKEN>/send": timeout`; recorders.messages.sendErr != want {
		t.Errorf("stored send error %q", recorders.messages.sendErr)
	}
}

// The restart sweep stores its reason in the same column as a transport
// error, so it goes through the same redaction.
func TestInterruptedSendErrorIsRedacted(t *testing.T) {
	redacting, recorders := decorated(t)

	if _, err := redacting.Messages().FailInterruptedSends(context.Background(), 99,
		"restarted mid-send to bot"+secretValue); err != nil {
		t.Fatalf("FailInterruptedSends: %v", err)
	}
	if want := "restarted mid-send to bot<redacted:GH_TOKEN>"; recorders.messages.sendErr != want {
		t.Errorf("stored send error %q", recorders.messages.sendErr)
	}
}

// TestEveryStoreWriteIsClassified walks every sub-interface of the Store
// seam — all twelve, not just the decorated three — and requires each method
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
			"SetRotation":     true, // the loop's own handoff note
			"SetModelRefusal": true, // the refused turn's result text
			// Create and Edit carry the surface and hub-MCP tokens themselves.
			// Redacting those would write a placeholder where the
			// credential belongs; the mission is operator-written text, and
			// an operator pasting their own secret into it is #30's problem.
			"Create": false, "Edit": false,
			"Delete": false, "Get": false, "GetByHubMCPToken": false,
			"GetByName": false, "List": false,
			"SetOwner": false, "SetOwnerDMChat": false, "SetPromptHash": false,
			"SetRuntime": false, "SetStatus": false, "SetWorkstationOff": false,
			// Slack ids and a timestamp, the counterparts of the two
			// Telegram setters above and the fleet channel's room.
			"SetSlackBinding": false, "SetSlackOwner": false, "SetSlackOwnerDM": false,
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
			"Insert": true, "SetSendResult": true, "FailInterruptedSends": true,
			// SetMirror writes one of three constants, never text.
			"SetMirror":    false,
			"SetDelivered": false, "List": false, "ListConversation": false, "ListChannel": false,
			"Get": false, "UntoldSendFailures": false, "UnresolvedSendFailures": false,
			"Undelivered": false,
			// ResolveSend and ResolveResends write timestamps and ids,
			// never text.
			"ResolveSend": false, "ResolveResends": false,
			"MarkSendFailuresTold": false,
			"PutRef":               false, "Ref": false, "RecordSighting": false, "ByRef": false,
			"ByTGKey": false, "LatestGroupPostBy": false, "BySlackTS": false,
			// a channel id and a ts, both Slack's
			"SetSlackRef": false,
			// A reply target is a message id, read or written; never text.
			"SightedReplyTarget": false, "AdoptReplyTarget": false,
		},
		"AttachmentStore": {
			"Insert": true,
			// Expire writes a timestamp; the reads return rows as stored.
			"Expire": false, "Get": false, "ByMessage": false,
			// Claim writes a message id and ExpireUnsent a timestamp; the
			// name was redacted when the upload was inserted.
			"ByMessages": false, "Claim": false, "ExpireUnsent": false,
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
		"ChannelStore": {
			// A channel's description is operator-written text for the
			// loops in it to read, like a rule; the rest are names, ids and
			// timestamps.
			"Create": false, "SetDescription": false, "Delete": false,
			"AddLoop": false, "RemoveLoop": false, "Get": false, "List": false,
		},
		"ReactionStore": {
			// Ids, timestamps, a reactor's display name as the surface
			// reports it, and an emoji. A loop's emoji is one the hub
			// accepted as an emoji and nothing else (route.SendReaction),
			// so it cannot carry text.
			"Add": false, "Remove": false, "ListByMessages": false, "Untold": false, "MarkTold": false, "Seen": false,
		},
		"PollStore": {
			// A ballot's options are the polling loop's words.
			"Create": true,
			// A vote is option indexes, a voter's display name as the
			// surface reports it, and ids; a close is a timestamp.
			"Vote": false, "Close": false, "MarkVotesTold": false, "MarkClosesTold": false,
			"Get": false, "ListByMessages": false, "Votes": false, "Due": false,
			"UntoldVotes": false, "UntoldCloses": false,
		},
		"RoomStore": {
			// Ids, channel names and timestamps, and a chat's title as the
			// surface reports it: a name people chose for a room, written
			// by no loop.
			"Sight": false, "Bind": false, "Forget": false, "Move": false, "List": false, "ListByRoom": false,
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
		"SlackSenderStore": {
			"Create": false, "SetStatus": false, "Delete": false, "Get": false, "List": false,
		},
		"ModelStore": {
			// A resolution is a model id the CLI reported at init. A custom
			// entry is an operator-typed model id and label, like a rule.
			"SetResolution": false, "AddCustom": false, "SetCustomLabel": false,
			"DeleteCustom": false, "Resolutions": false, "ListCustom": false,
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
