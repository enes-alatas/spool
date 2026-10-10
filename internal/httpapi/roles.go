package httpapi

import (
	"context"
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
// fleet, less the private conversations that are not theirs and loops'
// raw transcripts (see privateReader and seesPrivate), linking their own
// surface identities, and talking to it. Every other route the table
// lists is an admin's or an owner's, and any route it does not is an
// admin's, so a route added later is closed to members until someone
// decides otherwise.
var memberRoutes = []string{
	// Answered before the role is asked, or not at all: listed so the
	// table names every route.
	"GET /api/health", "GET /api/version", "POST /api/login", "POST /api/logout", "/api/",

	"GET " + mePath, "POST " + mePasswordPath,
	"GET " + meLinksPath, "POST " + meLinkCodePath, "DELETE " + meLinksPath + "/{surface}",

	"GET /api/loops", "GET /api/loops/{name}", "GET /api/loops/{name}/telegram/status",
	"GET /api/loops/{name}/slack/status", "GET /api/loops/{name}/rooms",
	"GET /api/loops/{name}/conversation", "GET /api/loops/{name}/connection-events",
	"GET /api/loops/{name}/stream", "GET /api/stream", "GET /api/activity", "GET /api/changes",
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
// which can hold its private messages anywhere in it (see seesPrivate). Listed
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

// ownerRoutes manage the hub's users, which is where an owner differs
// from an admin (ADR-0048): an admin who could raise anyone, themselves
// included, would be an owner in all but name.
var ownerRoutes = []string{
	"GET /api/users", "POST /api/users", "POST /api/users/{name}/reset",
	"PATCH /api/users/{name}", "DELETE /api/users/{name}",
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
	for _, pattern := range ownerRoutes {
		roles[pattern] = store.RoleOwner
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
// so a test can hold every route to the table above. wrap, when set, sees
// every handler as it is registered, with its pattern.
type routeMux struct {
	*http.ServeMux
	patterns []string
	wrap     func(pattern string, handler http.HandlerFunc) http.HandlerFunc
}

func (mux *routeMux) HandleFunc(pattern string, handler func(http.ResponseWriter, *http.Request)) {
	mux.patterns = append(mux.patterns, pattern)
	if mux.wrap != nil {
		handler = mux.wrap(pattern, handler)
	}
	mux.ServeMux.HandleFunc(pattern, handler)
}

// seesPrivate reports whether who reads every private conversation and
// loops' raw transcripts: an admin or an owner. A loop's events, turns and
// live agent stream can carry a private message's text anywhere in them,
// so a member is kept from those whole (ADR-0048). Of the private
// messages themselves, a member reads the ones privateReader allows.
func seesPrivate(who *caller) bool {
	return who != nil && roleRank[roleOf(who)] >= roleRank[store.RoleAdmin]
}

// seesPairCodes reports whether who may read pending senders' pairing
// codes: an admin or an owner, who can allow a sender. The code is what
// tells them the person vouching for a sender got the bot's DM, so a
// member who read every code could vouch for any of them (#689).
func seesPairCodes(who *caller) bool {
	return who != nil && roleRank[roleOf(who)] >= roleRank[store.RoleAdmin]
}

// privateMessage reports whether msg is part of a loop's private
// conversation: with its owner, or with another allowed sender (ADR-0049).
func privateMessage(msg *store.Message) bool {
	return msg.Conversation == store.ConversationOwnerDM || store.IsDM(msg.Conversation)
}

// privateReader is which private messages one caller reads (ADR-0050). A
// private conversation is read by its person, through the user's link to
// who they are on the surface, and by admins and owners. A private message
// records who it is with: the person of a dm:<name>, or the owner an
// owner_dm was with at the time. So a member reads their own dm:<name>
// conversations with any loop, and what a loop and they said privately
// while they owned it, and no other; an owner_dm older than #703 records
// nobody, and stays an admin's.
type privateReader struct {
	all bool
	// persons are the people the caller is linked as.
	persons map[string]bool
}

// privateReaderOf reads who's links as they are now, so a link removed
// reads nothing from the next request on.
func (server *Server) privateReaderOf(ctx context.Context, who *caller) (*privateReader, error) {
	if seesPrivate(who) {
		return &privateReader{all: true}, nil
	}
	reader := &privateReader{persons: map[string]bool{}}
	if who == nil || who.user == nil {
		return reader, nil
	}
	links, err := server.Store.UserLinks().List(ctx, who.user.ID)
	if err != nil {
		return nil, err
	}
	for _, link := range links {
		reader.persons[link.Person] = true
	}
	return reader, nil
}

// reads reports whether the caller reads msg.
func (reader *privateReader) reads(msg *store.Message) bool {
	if reader.all || !privateMessage(msg) {
		return true
	}
	return msg.DMPerson != "" && reader.persons[msg.DMPerson]
}

// readable is msgs without the ones reader does not read.
func (reader *privateReader) readable(msgs []*store.Message) []*store.Message {
	if reader.all {
		return msgs
	}
	kept := make([]*store.Message, 0, len(msgs))
	for _, msg := range msgs {
		if reader.reads(msg) {
			kept = append(kept, msg)
		}
	}
	return kept
}

// readsNow is a stream's test of whether who reads a message, asked anew
// for each private one, so a link made or removed while the stream is open
// counts from its next message. A store error reads as no.
func (server *Server) readsNow(ctx context.Context, who *caller) func(*store.Message) bool {
	return func(msg *store.Message) bool {
		if seesPrivate(who) || !privateMessage(msg) {
			return true
		}
		reader, err := server.privateReaderOf(ctx, who)
		if err != nil {
			server.Log.Warn("stream: read a caller's links", "err", err)
			return false
		}
		return reader.reads(msg)
	}
}

// streamable reports whether item may reach who on a stream. A turn's
// result carries its text, and an agent event the loop's raw output, so a
// member gets neither; a message reaches them if reads says they read it.
// A reaction or a vote still does whatever its target: it carries only
// ids, an emoji or a choice, no text, and telling which conversation its
// message is in would take a store read per item. So a member can learn
// that a private message with some id exists and what it got.
func streamable(who *caller, item bus.Item, reads func(*store.Message) bool) bool {
	if seesPrivate(who) {
		return true
	}
	switch item.Kind {
	case bus.KindAgentEvent, bus.KindTurnResult:
		return false
	case bus.KindMessage, bus.KindSendRetry:
		payload, ok := item.Payload.(*route.MessagePayload)
		return ok && reads(&payload.Message)
	}
	return true
}
