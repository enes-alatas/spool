package httpapi

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/enes-alatas/spool/internal/bus"
	"github.com/enes-alatas/spool/internal/route"
	"github.com/enes-alatas/spool/internal/store"
)

// TestEveryRouteHasARole: each /api route the hub registers is in the role
// table exactly once, and the table names no route that is gone, so adding
// a route means deciding who may call it (ADR-0048).
func TestEveryRouteHasARole(t *testing.T) {
	registered := map[string]bool{}
	for _, pattern := range (&Server{}).routes().patterns {
		if !strings.Contains(pattern, "/api/") {
			continue
		}
		registered[pattern] = true
		if _, ok := routeRoles[pattern]; !ok {
			t.Errorf("route %q is in none of memberRoutes, adminRoutes and ownerRoutes", pattern)
		}
	}
	listed := map[string]bool{}
	for _, pattern := range append(append(append([]string{}, memberRoutes...), adminRoutes...), ownerRoutes...) {
		if listed[pattern] {
			t.Errorf("route %q is listed twice", pattern)
		}
		listed[pattern] = true
		if !registered[pattern] {
			t.Errorf("the role table lists %q, which no route registers", pattern)
		}
	}
}

// TestRolesRankOverTheTable: a member reaches member routes only, an admin
// member and admin routes, an owner and the operator token all three, and
// a route the table does not name,
// or a role the hub does not know, opens nothing.
func TestRolesRankOverTheTable(t *testing.T) {
	mux := (&Server{}).routes().ServeMux
	member := &caller{user: &store.User{Role: store.RoleMember}}
	admin := &caller{user: &store.User{Role: store.RoleAdmin}}
	owner := &caller{user: &store.User{Role: store.RoleOwner}}
	token := &caller{}
	stranger := &caller{user: &store.User{Role: "superuser"}}
	for _, test := range []struct {
		method, path string
		who          *caller
		want         bool
	}{
		{"GET", "/api/loops", member, true},
		{"POST", "/api/loops/scout/message", member, true},
		{"POST", "/api/loops", member, false},
		{"POST", "/api/loops/scout/pause", member, false},
		{"PUT", "/api/settings", member, false},
		{"POST", "/api/loops", admin, true},
		{"PUT", "/api/settings", owner, true},
		{"DELETE", "/api/loops/scout", token, true},
		{"GET", "/api/users", admin, false},
		{"PATCH", "/api/users/dana", admin, false},
		{"POST", "/api/users", owner, true},
		{"DELETE", "/api/users/dana", token, true},
		{"GET", "/api/loops", stranger, false},
	} {
		r := httptest.NewRequest(test.method, test.path, nil)
		if got := allows(mux, r, test.who); got != test.want {
			t.Errorf("%s %s as %q = %v; want %v", test.method, test.path, roleOf(test.who), got, test.want)
		}
	}
	unlisted := httptest.NewRequest("GET", "/somewhere/else", nil)
	if allows(mux, unlisted, member) {
		t.Error("a route the table does not name admitted a member")
	}
}

// TestAMemberStreamCarriesNoOwnerDM: on a stream a member with no link
// gets every message but an owner DM, resent or not, a reaction or a vote
// only on a message they read, and no turn result or agent event; an admin
// gets them all (#922).
func TestAMemberStreamCarriesNoOwnerDM(t *testing.T) {
	unlinked := (&privateReader{persons: map[string]bool{}}).reads
	member := &caller{user: &store.User{Role: store.RoleMember}}
	admin := &caller{user: &store.User{Role: store.RoleAdmin}}
	message := func(kind, conversation string) bus.Item {
		return bus.Item{Kind: kind, Payload: &route.MessagePayload{Message: store.Message{Conversation: conversation}}}
	}
	reaction := func(conversation string) bus.Item {
		return bus.Item{Kind: bus.KindReaction, Payload: &route.ReactionPayload{Reaction: &store.Reaction{Emoji: "👍"},
			Target: &store.Message{Conversation: conversation}}}
	}
	poll := func(conversation string) bus.Item {
		return bus.Item{Kind: bus.KindPoll, Payload: &route.PollPayload{Vote: &store.Vote{Choice: []int{0}},
			Target: &store.Message{Conversation: conversation}}}
	}
	for _, test := range []struct {
		item bus.Item
		want bool
	}{
		{message(bus.KindMessage, store.ConversationGroup), true},
		{message(bus.KindMessage, store.ConversationControlRoom), true},
		{message(bus.KindMessage, store.ConversationOwnerDM), false},
		{message(bus.KindSendRetry, store.ConversationOwnerDM), false},
		{bus.Item{Kind: bus.KindTurnResult, Payload: &store.Turn{ResultText: "said in a DM"}}, false},
		{bus.Item{Kind: bus.KindAgentEvent}, false},
		{bus.Item{Kind: bus.KindLoopStatus}, true},
		{reaction(store.ConversationGroup), true},
		{reaction(store.ConversationOwnerDM), false},
		{reaction("dm:someone"), false},
		{poll(store.ConversationGroup), true},
		{poll(store.ConversationOwnerDM), false},
		// a frame published with no target reaches no member
		{bus.Item{Kind: bus.KindReaction, Payload: &route.ReactionPayload{Reaction: &store.Reaction{}}}, false},
		{bus.Item{Kind: bus.KindPoll, Payload: &route.PollPayload{Closed: true}}, false},
	} {
		if got := streamable(member, test.item, unlinked); got != test.want {
			t.Errorf("a member's stream with %s %+v = %v; want %v", test.item.Kind, test.item.Payload, got, test.want)
		}
		if !streamable(admin, test.item, unlinked) {
			t.Errorf("an admin's stream refused %s", test.item.Kind)
		}
	}
	if streamable(nil, message(bus.KindMessage, store.ConversationOwnerDM), unlinked) {
		t.Error("a request the guard never admitted streamed an owner DM")
	}
}

// TestAPrivateConversationIsReadByItsPerson: a member linked as a person
// reads that person's dm:<name> conversations with any loop, and the
// owner_dm messages with them, and nothing else private; with no link
// they read nothing private (ADR-0050).
func TestAPrivateConversationIsReadByItsPerson(t *testing.T) {
	alice, bob := store.TGPerson(1), store.SlackPerson("U0BOB")
	linked := &privateReader{persons: map[string]bool{alice: true}}
	unlinked := &privateReader{persons: map[string]bool{}}
	for _, test := range []struct {
		name string
		msg  store.Message
		want bool
	}{
		{"the group", store.Message{Conversation: store.ConversationGroup}, true},
		{"control_room", store.Message{Conversation: store.ConversationControlRoom, ConversationLoopID: "l2"}, true},
		{"their dm with a loop", store.Message{Conversation: "dm:alice", ConversationLoopID: "l2", DMPerson: alice}, true},
		{"their dm under another name", store.Message{Conversation: "dm:alicia", ConversationLoopID: "l1", DMPerson: alice}, true},
		{"someone else's dm", store.Message{Conversation: "dm:bob", ConversationLoopID: "l1", DMPerson: bob}, false},
		{"a dm with no person", store.Message{Conversation: "dm:alice", ConversationLoopID: "l1"}, false},
		{"an owner_dm with them", store.Message{Conversation: store.ConversationOwnerDM, ConversationLoopID: "l1", DMPerson: alice}, true},
		{"an owner_dm with an earlier owner", store.Message{Conversation: store.ConversationOwnerDM, ConversationLoopID: "l1", DMPerson: bob}, false},
		{"an owner_dm that records nobody", store.Message{Conversation: store.ConversationOwnerDM, ConversationLoopID: "l1"}, false},
	} {
		if got := linked.reads(&test.msg); got != test.want {
			t.Errorf("linked, %s: reads = %v; want %v", test.name, got, test.want)
		}
		if got, want := unlinked.reads(&test.msg), !privateMessage(&test.msg); got != want {
			t.Errorf("unlinked, %s: reads = %v; want %v", test.name, got, want)
		}
	}
	if got := linked.readable([]*store.Message{
		{ID: 1, Conversation: "dm:bob", DMPerson: bob}, {ID: 2, Conversation: "dm:alice", DMPerson: alice},
	}); len(got) != 1 || got[0].ID != 2 {
		t.Errorf("readable kept %+v; want only alice's", got)
	}
}

// accessStore is a store of users and their links that counts its reads.
type accessStore struct {
	store.Store
	*accessTables
}

type accessTables struct {
	users map[string]*store.User
	links map[string][]*store.UserLink
	reads atomic.Int32
}

type accessUsers struct {
	store.UserStore
	*accessTables
}

type accessLinks struct {
	store.UserLinkStore
	*accessTables
}

func (fake accessStore) Users() store.UserStore { return accessUsers{accessTables: fake.accessTables} }
func (fake accessStore) UserLinks() store.UserLinkStore {
	return accessLinks{accessTables: fake.accessTables}
}

func (users accessUsers) Get(_ context.Context, id string) (*store.User, error) {
	tables := users.accessTables
	tables.reads.Add(1)
	user, ok := tables.users[id]
	if !ok {
		return nil, store.ErrNotFound
	}
	copied := *user
	return &copied, nil
}

func (links accessLinks) List(_ context.Context, userID string) ([]*store.UserLink, error) {
	tables := links.accessTables
	tables.reads.Add(1)
	return tables.links[userID], nil
}

// openAccess opens a stream's access as the user id names, over tables.
func openAccess(t *testing.T, tables *accessTables, id string) *streamAccess {
	t.Helper()
	server := &Server{Store: accessStore{accessTables: tables}, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	access, err := server.openStreamAccess(context.Background(), &caller{user: tables.users[id]})
	if err != nil {
		t.Fatal(err)
	}
	return access
}

// published runs item through a real bus with access noticing it, as an
// open stream's filter does.
func published(access *streamAccess, item bus.Item) {
	events := bus.New()
	items, cancel := events.Subscribe(func(item bus.Item) bool {
		access.notice(item)
		return true
	})
	defer cancel()
	events.Publish(item)
	<-items
}

func admitted(t *testing.T, access *streamAccess, item bus.Item) bool {
	t.Helper()
	send, err := access.admits(context.Background(), item)
	if err != nil {
		t.Fatalf("admits: %v", err)
	}
	return send
}

// TestAStreamReadsAccessOutsideThePublish: a member's stream reads their
// role and links when it opens and again only after a link of theirs
// changes, and never while the bus publishes, so no store read holds bus.mu
// (#721). A link removed counts from the stream's next item.
func TestAStreamReadsAccessOutsideThePublish(t *testing.T) {
	alice := store.TGPerson(1)
	tables := &accessTables{users: map[string]*store.User{"u-mia": {ID: "u-mia", Role: store.RoleMember}},
		links: map[string][]*store.UserLink{"u-mia": {{UserID: "u-mia", Person: alice}}}}
	access := openAccess(t, tables, "u-mia")
	opened := tables.reads.Load()
	hers := bus.Item{Kind: bus.KindMessage, Payload: &route.MessagePayload{
		Message: store.Message{Conversation: "dm:alice", DMPerson: alice}}}
	for range 3 {
		if !admitted(t, access, hers) {
			t.Fatal("mia's stream withheld her own DM")
		}
	}
	if got := tables.reads.Load(); got != opened {
		t.Fatalf("%d store reads for a stream with no change; want only the %d at open", got, opened)
	}

	published(access, bus.Item{Kind: bus.KindLink, Payload: &route.LinkPayload{UserID: "u-bea", State: route.LinkUnlinked}})
	if !admitted(t, access, hers) || tables.reads.Load() != opened {
		t.Fatalf("another user's link change reloaded mia's access (%d reads)", tables.reads.Load())
	}
	tables.links["u-mia"] = nil
	published(access, bus.Item{Kind: bus.KindLink, Payload: &route.LinkPayload{UserID: "u-mia", State: route.LinkUnlinked}})
	if got := tables.reads.Load(); got != opened {
		t.Fatalf("the publish read the store (%d reads); want the stream to read it", got)
	}
	if admitted(t, access, hers) {
		t.Fatal("mia's stream still carries a DM after her link was removed")
	}
	if got := tables.reads.Load(); got <= opened {
		t.Fatal("mia's stream did not reread her access after her link changed")
	}
}

// TestAStreamFollowsItsUsersRole: an admin's open stream stops carrying
// private messages and transcripts from the next item once they are made a
// member, and the stream of a removed user ends (#722). A change the bus
// never hears, as `spool user` makes, counts once streamAccessTTL passes.
func TestAStreamFollowsItsUsersRole(t *testing.T) {
	tables := &accessTables{users: map[string]*store.User{"u-ada": {ID: "u-ada", Role: store.RoleAdmin}},
		links: map[string][]*store.UserLink{}}
	access := openAccess(t, tables, "u-ada")
	private := bus.Item{Kind: bus.KindMessage, Payload: &route.MessagePayload{
		Message: store.Message{Conversation: store.ConversationOwnerDM}}}
	transcript := bus.Item{Kind: bus.KindAgentEvent}
	if !admitted(t, access, private) || !admitted(t, access, transcript) {
		t.Fatal("an admin's stream withheld a private item")
	}

	tables.users["u-ada"].Role = store.RoleMember
	published(access, bus.Item{Kind: bus.KindUser, Payload: &userChange{UserID: "u-ada"}})
	if admitted(t, access, private) || admitted(t, access, transcript) {
		t.Fatal("a demoted admin's open stream still carries private items")
	}

	tables.users["u-ada"].Role = store.RoleAdmin
	access.loadedAt = time.Now().Add(-streamAccessTTL - time.Second)
	if !admitted(t, access, private) {
		t.Fatal("a role changed without the bus never reached an open stream")
	}

	delete(tables.users, "u-ada")
	published(access, bus.Item{Kind: bus.KindUser, Payload: &userChange{UserID: "u-ada"}})
	if send, err := access.admits(context.Background(), private); send || !errors.Is(err, errAccessEnded) {
		t.Fatalf("a removed user's stream = %v, %v; want it ended", send, err)
	}
}

// TestAStreamEndsWithItsSession: a stream opened under a session ends at
// its next item once that session is signed out of or ended by a password
// change, while one under the session the change keeps carries on; a reset
// with no bus to tell, as `spool user` makes, ends it once streamAccessTTL
// passes, and so does signing out of a session traded for the operator
// token, which has no user to publish for (#787).
func TestAStreamEndsWithItsSession(t *testing.T) {
	ctx := context.Background()
	hubUsers := testUsers(t)
	oneTime, err := hubUsers.Add(ctx, "ada", store.RoleAdmin)
	if err != nil {
		t.Fatal(err)
	}
	ada, _ := hubUsers.Store.Users().GetByName(ctx, "ada")
	server := &Server{Store: hubUsers.Store, Users: hubUsers, Bus: bus.New(),
		Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	private := bus.Item{Kind: bus.KindMessage, Payload: &route.MessagePayload{
		Message: store.Message{Conversation: store.ConversationOwnerDM}}}
	open := func() *streamAccess {
		t.Helper()
		session, err := hubUsers.StartSession(ctx, ada.ID)
		if err != nil {
			t.Fatal(err)
		}
		access, err := server.openStreamAccess(ctx, &caller{user: ada, session: session})
		if err != nil {
			t.Fatal(err)
		}
		_, cancel := server.Bus.Subscribe(func(item bus.Item) bool {
			access.notice(item)
			return true
		})
		t.Cleanup(cancel)
		return access
	}
	ends := func(access *streamAccess, after string) {
		t.Helper()
		if send, err := access.admits(ctx, private); send || !errors.Is(err, errAccessEnded) {
			t.Fatalf("a stream %s = %v, %v; want it ended", after, send, err)
		}
	}

	signedOut, kept := open(), open()
	logout := httptest.NewRequest("POST", logoutPath, nil)
	logout.AddCookie(&http.Cookie{Name: SessionCookie, Value: signedOut.who.session})
	server.handleLogout(httptest.NewRecorder(), logout)
	ends(signedOut, "after a sign-out")
	if !admitted(t, kept, private) {
		t.Fatal("signing out of one session ended a stream under another")
	}

	other := open()
	change := httptest.NewRequest("POST", mePasswordPath, strings.NewReader(
		`{"current_password":"`+oneTime+`","new_password":"a synthetic new password"}`))
	change = change.WithContext(context.WithValue(change.Context(), callerKey{}, &caller{user: ada, session: kept.who.session}))
	recorder := httptest.NewRecorder()
	server.handleMePassword(recorder, change)
	if recorder.Code != http.StatusOK {
		t.Fatalf("password change = %d %s", recorder.Code, recorder.Body)
	}
	ends(other, "after a password change")
	if !admitted(t, kept, private) {
		t.Fatal("a password change ended the stream under the session it keeps")
	}

	if _, err := hubUsers.Reset(ctx, "ada"); err != nil {
		t.Fatal(err)
	}
	if !admitted(t, kept, private) {
		t.Fatal("a reset with no bus to tell counted before streamAccessTTL passed")
	}
	kept.loadedAt = time.Now().Add(-streamAccessTTL - time.Second)
	ends(kept, "streamAccessTTL after a reset")

	tokenSession, err := hubUsers.StartTokenSession(ctx, testToken)
	if err != nil {
		t.Fatal(err)
	}
	traded, err := server.openStreamAccess(ctx, &caller{session: tokenSession})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := hubUsers.EndSession(ctx, tokenSession); err != nil {
		t.Fatal(err)
	}
	traded.loadedAt = time.Now().Add(-streamAccessTTL - time.Second)
	ends(traded, "streamAccessTTL after signing out of a token-traded session")
}
