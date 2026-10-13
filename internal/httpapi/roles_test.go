package httpapi

import (
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

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
// gets every message but an owner DM, resent or not, and no turn result or
// agent event; an admin gets them all.
func TestAMemberStreamCarriesNoOwnerDM(t *testing.T) {
	unlinked := (&privateReader{persons: map[string]bool{}}).reads
	member := &caller{user: &store.User{Role: store.RoleMember}}
	admin := &caller{user: &store.User{Role: store.RoleAdmin}}
	message := func(kind, conversation string) bus.Item {
		return bus.Item{Kind: kind, Payload: &route.MessagePayload{Message: store.Message{Conversation: conversation}}}
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

// countedLinks is a link store that counts its reads.
type countedLinks struct {
	store.UserLinkStore
	links map[string][]*store.UserLink
	reads atomic.Int32
}

func (links *countedLinks) List(_ context.Context, userID string) ([]*store.UserLink, error) {
	links.reads.Add(1)
	return links.links[userID], nil
}

type linksStore struct {
	store.Store
	links *countedLinks
}

func (fake linksStore) UserLinks() store.UserLinkStore { return fake.links }

// TestAStreamReadsLinksOutsideThePublish: a member's stream reads their
// links when it opens and again only after a link of theirs changes, and
// never while the bus publishes, so no store read holds bus.mu (#721).
// A link removed counts from the stream's next item.
func TestAStreamReadsLinksOutsideThePublish(t *testing.T) {
	alice := store.TGPerson(1)
	links := &countedLinks{links: map[string][]*store.UserLink{"u-mia": {{UserID: "u-mia", Person: alice}}}}
	server := &Server{Store: linksStore{links: links}, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	ctx := context.Background()
	access, err := server.openStreamAccess(ctx, &caller{user: &store.User{ID: "u-mia", Role: store.RoleMember}})
	if err != nil {
		t.Fatal(err)
	}
	hers := bus.Item{Kind: bus.KindMessage, Payload: &route.MessagePayload{
		Message: store.Message{Conversation: "dm:alice", DMPerson: alice}}}
	for range 3 {
		if !access.admits(ctx, hers) {
			t.Fatal("mia's stream withheld her own DM")
		}
	}
	if got := links.reads.Load(); got != 1 {
		t.Fatalf("links read %d times for one stream with no change; want once, at open", got)
	}

	events := bus.New()
	items, cancel := events.Subscribe(func(item bus.Item) bool {
		access.notice(item)
		return true
	})
	defer cancel()
	events.Publish(bus.Item{Kind: bus.KindLink, Payload: &route.LinkPayload{UserID: "u-bea", State: route.LinkUnlinked}})
	<-items
	if !access.admits(ctx, hers) || links.reads.Load() != 1 {
		t.Fatalf("another user's link change reloaded mia's access (%d reads)", links.reads.Load())
	}
	links.links["u-mia"] = nil
	events.Publish(bus.Item{Kind: bus.KindLink, Payload: &route.LinkPayload{UserID: "u-mia", State: route.LinkUnlinked}})
	<-items
	if got := links.reads.Load(); got != 1 {
		t.Fatalf("the publish read the store (%d reads); want the stream to read it", got)
	}
	if access.admits(ctx, hers) {
		t.Fatal("mia's stream still carries a DM after her link was removed")
	}
	if got := links.reads.Load(); got != 2 {
		t.Fatalf("links read %d times after mia's link changed; want twice", got)
	}
}
