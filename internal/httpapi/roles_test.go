package httpapi

import (
	"net/http/httptest"
	"strings"
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

// TestAMemberStreamCarriesNoOwnerDM: on a stream a member gets every
// message but an owner DM, resent or not, and no turn result or agent
// event; an admin gets them all.
func TestAMemberStreamCarriesNoOwnerDM(t *testing.T) {
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
		if got := streamable(member, test.item); got != test.want {
			t.Errorf("a member's stream with %s %+v = %v; want %v", test.item.Kind, test.item.Payload, got, test.want)
		}
		if !streamable(admin, test.item) {
			t.Errorf("an admin's stream refused %s", test.item.Kind)
		}
	}
	if streamable(nil, message(bus.KindMessage, store.ConversationOwnerDM)) {
		t.Error("a request the guard never admitted streamed an owner DM")
	}
}
