package httpapi

import (
	"net/http"

	"github.com/enes-alatas/spool/internal/bus"
	"github.com/enes-alatas/spool/internal/route"
	"github.com/enes-alatas/spool/internal/store"
)

// codeForbiddenRole refuses a caller whose role is below the route's.
const codeForbiddenRole = "forbidden_role"

// roleRank orders the roles: each may do whatever the ones below it may.
var roleRank = map[string]int{store.RoleMember: 1, store.RoleAdmin: 2, store.RoleOwner: 3}

// memberRoutes are the routes a member may call (ADR-0048): reading the
// fleet, less its owner DMs and loops' raw transcripts (see seesPrivate),
// and talking to it. Every other route the table lists is an
// admin's, and so is any route it does not, so a route added later is
// closed to members until someone decides otherwise.
var memberRoutes = []string{
	// Answered before the role is asked, or not at all: listed so the
	// table names every route.
	"GET /api/health", "GET /api/version", "POST /api/login", "POST /api/logout", "/api/",

	"GET " + mePath, "POST " + mePasswordPath,

	"GET /api/loops", "GET /api/loops/{name}", "GET /api/loops/{name}/telegram/status",
	"GET /api/loops/{name}/slack/status", "GET /api/loops/{name}/rooms",
	"GET /api/loops/{name}/conversation", "GET /api/loops/{name}/connection-events",
	"GET /api/loops/{name}/stream", "GET /api/stream", "GET /api/activity",
	"GET /api/attachments/{id}", "GET /api/undelivered", "GET /api/group",
	"GET /api/connections", "GET /api/connections/{name}", "GET /api/connections/{name}/events",
	"GET /api/channels", "GET /api/channels/{name}", "GET /api/channels/{name}/messages",
	"GET /api/settings", "GET /api/settings/egress", "GET /api/onboarding", "GET /api/plan-usage",
	"GET /api/rules", "GET /api/models", "GET /api/telegram/senders", "GET /api/slack/senders",

	"POST /api/loops/{name}/message", "POST /api/loops/{name}/wake",
	"POST /api/group", "POST /api/channels/{name}/messages", "POST " + uploadPath,
}

// adminRoutes are the routes that create, change, remove or operate
// something in the fleet, and the two that read a loop's raw transcript,
// which can hold its owner DMs anywhere in it (see seesPrivate). Listed
// although admin is the default, so a test can hold every route to having
// been decided.
var adminRoutes = []string{
	"GET /api/loops/{name}/events", "GET /api/loops/{name}/turns",

	"POST /api/loops", "PATCH /api/loops/{name}", "DELETE /api/loops/{name}",
	"POST /api/loops/{name}/pause", "POST /api/loops/{name}/resume",
	"POST /api/loops/{name}/kill", "POST /api/loops/{name}/rotate", "POST /api/loops/{name}/rehome",
	"POST /api/loops/{name}/workstation/restart", "POST /api/loops/{name}/workstation/poweroff",
	"POST /api/loops/{name}/workstation/poweron", "POST /api/loops/{name}/workstation/recreate",
	"PUT /api/loops/{name}/owner", "PUT /api/loops/{name}/rooms",
	"DELETE /api/loops/{name}/rooms/{surface}/{room}",
	"PUT /api/loops/{name}/connections/{connection}", "DELETE /api/loops/{name}/connections/{connection}",
	"POST /api/messages/{id}/retry", "POST /api/messages/{id}/dismiss",
	"POST /api/connections", "DELETE /api/connections/{name}", "POST /api/connections/{name}/share",
	"PUT /api/connections/{name}/secret", "POST /api/connections/{name}/revoke",
	"POST /api/channels", "PATCH /api/channels/{name}", "DELETE /api/channels/{name}",
	"PUT /api/channels/{name}/loops/{loop}", "DELETE /api/channels/{name}/loops/{loop}",
	"PUT /api/settings", "POST /api/settings/egress/hosts", "DELETE /api/settings/egress/hosts/{entry}",
	"POST /api/plan-cap/resume", "POST /api/onboarding/harness-check", "POST /api/workspace/inspect",
	"POST /api/rules", "PATCH /api/rules/{id}", "DELETE /api/rules/{id}",
	"POST /api/models/custom", "PATCH /api/models/custom/{id}", "DELETE /api/models/custom/{id}",
	"POST /api/telegram/senders/{id}/allow", "POST /api/telegram/senders/{id}/block",
	"DELETE /api/telegram/senders/{id}",
	"POST /api/slack/senders/{id}/allow", "POST /api/slack/senders/{id}/block",
	"DELETE /api/slack/senders/{id}",
}

// routeRoles is the lowest role each route pattern admits.
var routeRoles = func() map[string]string {
	roles := map[string]string{}
	for _, pattern := range memberRoutes {
		roles[pattern] = store.RoleMember
	}
	for _, pattern := range adminRoutes {
		roles[pattern] = store.RoleAdmin
	}
	return roles
}()

// roleOf is the role a caller acts with: the operator token has no user
// and acts as the owner (ADR-0048).
func roleOf(who *caller) string {
	if who.user == nil {
		return store.RoleOwner
	}
	return who.user.Role
}

// allows reports whether who may call the route r matches on mux. A route
// the table does not name needs an admin, and an unknown role ranks below
// every role, so neither opens anything by accident.
func allows(mux *http.ServeMux, r *http.Request, who *caller) bool {
	_, pattern := mux.Handler(r)
	need, ok := routeRoles[pattern]
	if !ok {
		need = store.RoleAdmin
	}
	return roleRank[roleOf(who)] >= roleRank[need]
}

// routeMux is the API's mux, which remembers each pattern registered on it
// so a test can hold every route to the table above.
type routeMux struct {
	*http.ServeMux
	patterns []string
}

func (mux *routeMux) HandleFunc(pattern string, handler func(http.ResponseWriter, *http.Request)) {
	mux.patterns = append(mux.patterns, pattern)
	mux.ServeMux.HandleFunc(pattern, handler)
}

// seesPrivate reports whether who may read loops' owner DMs and raw
// transcripts: an admin or an owner. A member may not until hub users are
// mapped to the loops they own (#100). A loop's events, turns and live
// agent stream can carry an owner DM's text anywhere in them, so a member
// is kept from those whole, and from owner-DM messages wherever messages
// are listed or streamed (ADR-0048).
func seesPrivate(who *caller) bool {
	return who != nil && roleRank[roleOf(who)] >= roleRank[store.RoleAdmin]
}

// privateMessage reports whether msg is part of a loop's private
// conversation with its owner.
func privateMessage(msg *store.Message) bool {
	return msg.Conversation == store.ConversationOwnerDM
}

// withoutPrivate is msgs without the ones who may not read.
func withoutPrivate(who *caller, msgs []*store.Message) []*store.Message {
	if seesPrivate(who) {
		return msgs
	}
	kept := make([]*store.Message, 0, len(msgs))
	for _, msg := range msgs {
		if !privateMessage(msg) {
			kept = append(kept, msg)
		}
	}
	return kept
}

// streamable reports whether item may reach who on a stream. A turn's
// result carries its text, and an agent event the loop's raw output, so a
// member gets neither; a message reaches them unless it is an owner DM.
// A reaction or a vote still does whatever its target: it carries only
// ids, an emoji or a choice, no text, and telling which conversation its
// message is in would take a store read per item. So a member can learn
// that an owner-DM message with some id exists and what it got; #100 is
// where that closes.
func streamable(who *caller, item bus.Item) bool {
	if seesPrivate(who) {
		return true
	}
	switch item.Kind {
	case bus.KindAgentEvent, bus.KindTurnResult:
		return false
	case bus.KindMessage, bus.KindSendRetry:
		payload, ok := item.Payload.(*route.MessagePayload)
		return ok && !privateMessage(&payload.Message)
	}
	return true
}
