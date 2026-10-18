package httpapi

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"time"

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
	"GET " + meLinksPath, "POST " + meLinkCodePath, "GET " + meLinksWaitingPath, "DELETE " + meLinksPath + "/{surface}",

	"GET /api/loops", "GET /api/loops/{name}", "GET /api/loops/{name}/telegram/status",
	"GET /api/loops/{name}/slack/status", "GET /api/loops/{name}/rooms",
	"GET /api/loops/{name}/conversation", "GET /api/loops/{name}/conversations",
	"GET /api/loops/{name}/connection-events",
	"GET /api/loops/{name}/stream", "GET /api/stream", "GET /api/activity", "GET /api/changes",
	"GET /api/attachments/{id}", "GET /api/undelivered", "GET /api/group",
	"GET /api/connections", "GET /api/connections/{name}", "GET /api/connections/{name}/events",
	"GET /api/channels", "GET /api/channels/{name}", "GET /api/channels/{name}/messages",
	"GET /api/settings", "GET /api/settings/egress", "GET /api/settings/cloud", "GET /api/onboarding", "GET /api/plan-usage",
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
	"POST /api/loops/{name}/problems/{symptom}/acknowledge",
	"PUT /api/loops/{name}/owner", "PUT /api/loops/{name}/rooms",
	"DELETE /api/loops/{name}/rooms/{surface}/{room}",
	"PUT /api/loops/{name}/connections/{connection}", "DELETE /api/loops/{name}/connections/{connection}",
	"POST /api/messages/{id}/retry", "POST /api/messages/{id}/dismiss",
	"POST /api/connections", "DELETE /api/connections/{name}", "POST /api/connections/{name}/share",
	"PUT /api/connections/{name}/secret", "POST /api/connections/{name}/revoke",
	"POST /api/channels", "PATCH /api/channels/{name}", "DELETE /api/channels/{name}",
	"PUT /api/channels/{name}/loops/{loop}", "DELETE /api/channels/{name}/loops/{loop}",
	"PUT /api/settings", "POST /api/settings/egress/hosts", "DELETE /api/settings/egress/hosts/{entry}",
	"PUT /api/settings/cloud/address", "DELETE /api/settings/cloud/address", "POST /api/settings/cloud/check",
	"PUT /api/settings/cloud/listener", "POST /api/settings/cloud/account/sign-in", "POST /api/settings/cloud/account/code",
	"DELETE /api/settings/cloud/account", "POST /api/settings/cloud/account/check",
	"POST /api/plan-cap/resume", "POST /api/onboarding/harness-check", "POST /api/onboarding/skip-surface",
	"POST /api/workspace/inspect",
	"POST /api/rules", "PATCH /api/rules/{id}", "DELETE /api/rules/{id}",
	"POST /api/models/custom", "PATCH /api/models/custom/{id}", "DELETE /api/models/custom/{id}",
	"POST /api/telegram/senders/{id}/allow", "POST /api/telegram/senders/{id}/block",
	"DELETE /api/telegram/senders/{id}",
	"POST /api/slack/senders/{id}/allow", "POST /api/slack/senders/{id}/block",
	"DELETE /api/slack/senders/{id}",
	"GET " + linkChangesPath,
	"GET /api/strangers", "DELETE /api/strangers/{surface}/pause",
}

// ownerRoutes manage the hub's users, which is where an owner differs
// from an admin (ADR-0048): an admin who could raise anyone, themselves
// included, would be an owner in all but name.
var ownerRoutes = []string{
	"GET /api/users", "POST /api/users", "POST /api/users/{name}/reset",
	"PATCH /api/users/{name}", "DELETE /api/users/{name}",
	"DELETE /api/users/{name}/links/{surface}", "POST /api/users/{name}/block", "POST /api/users/{name}/unblock",
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

// seesSyncErrors reports whether who reads why a cloud environment's
// allowlist write failed: an admin or an owner. The reason quotes the
// cloud's answer, which reaches the view without passing the redactor.
func seesSyncErrors(who *caller) bool {
	return who != nil && roleRank[roleOf(who)] >= roleRank[store.RoleAdmin]
}

// seesCloudCheckDetail reports whether who reads how a check of the public
// address failed, beyond the stage: an admin or an owner, who chose the
// address. The detail says what answered there, or how nothing did, and
// for a loopback address that is news about the hub's own host.
func seesCloudCheckDetail(who *caller) bool {
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

// streamAccess is what one open stream's caller reads: their role and their
// links, read when the stream opens and again after anything that may
// change them, so a demotion (#722), a removal, a link made or removed, or
// the end of the session the stream opened under (#787) counts from the
// stream's next item. The bus only marks it stale, which costs no store
// read; the stream reloads it in its own goroutine before it writes the next
// item, so no store read happens in Bus.Publish with bus.mu held (#721).
// `spool user` changes users, and a session runs out, with no bus to tell,
// so the stream also reloads once streamAccessTTL has passed.
type streamAccess struct {
	server *Server
	// userID is whose stream this is, "" for the operator token. It never
	// changes, so the bus may read it while the stream reloads.
	userID   string
	who      *caller
	reader   *privateReader
	loadedAt time.Time
	stale    atomic.Bool
}

// streamAccessTTL bounds how long a stream trusts a role and links it read.
const streamAccessTTL = time.Minute

// errAccessEnded ends the stream of a user who was removed, or whose
// session ended: signed out, expired, or ended by a password change or a
// reset (#787).
var errAccessEnded = errors.New("the stream's user was removed or their session ended")

// openStreamAccess reads who's access for a stream they open.
func (server *Server) openStreamAccess(ctx context.Context, who *caller) (*streamAccess, error) {
	access := &streamAccess{server: server, who: who}
	if who != nil && who.user != nil {
		access.userID = who.user.ID
	}
	return access, access.load(ctx)
}

func (access *streamAccess) load(ctx context.Context) error {
	who := access.who
	// A session traded for the operator token has no user to reread, but
	// it ends as a user's does: its stream within streamAccessTTL.
	if who != nil && who.session != "" {
		live, err := access.server.Users.SessionLive(ctx, who.session)
		if err != nil {
			return err
		} else if !live {
			return errAccessEnded
		}
	}
	if access.userID != "" {
		user, err := access.server.Store.Users().Get(ctx, access.userID)
		if errors.Is(err, store.ErrNotFound) {
			return errAccessEnded
		} else if err != nil {
			return err
		}
		who = &caller{user: user, session: access.who.session}
	}
	reader, err := access.server.privateReaderOf(ctx, who)
	if err != nil {
		return err
	}
	access.who, access.reader, access.loadedAt = who, reader, time.Now()
	return nil
}

// notice marks the access stale when item changes it: a link of the
// stream's user, or a change to the user. It runs in Bus.Publish, so it
// reads nothing but the item.
func (access *streamAccess) notice(item bus.Item) {
	if access.userID == "" {
		return
	}
	switch payload := item.Payload.(type) {
	case *route.LinkPayload:
		if item.Kind == bus.KindLink && payload.UserID == access.userID {
			access.stale.Store(true)
		}
	case *userChange:
		if item.Kind == bus.KindUser && payload.UserID == access.userID {
			access.stale.Store(true)
		}
	}
}

// admits reports whether item may reach the caller, reloading their access
// first if anything changed it or it is older than streamAccessTTL. A
// store error withholds the item and tries again on the next one, and a
// removed user's stream, or one whose session ended, ends with
// errAccessEnded.
func (access *streamAccess) admits(ctx context.Context, item bus.Item) (bool, error) {
	if access.stale.Swap(false) || time.Since(access.loadedAt) > streamAccessTTL {
		if err := access.load(ctx); errors.Is(err, errAccessEnded) {
			return false, err
		} else if err != nil {
			access.stale.Store(true)
			access.server.Log.Warn("stream: read a caller's access", "err", err)
			return false, nil
		}
	}
	return streamable(access.who, item, access.reader.reads), nil
}

// userChanged tells every open stream of userID to read their access
// again: their role changed, they were removed, or a session of theirs
// ended, which ends the stream it opened.
func (server *Server) userChanged(userID string) {
	server.Bus.Publish(bus.Item{Kind: bus.KindUser, Payload: &userChange{UserID: userID}})
}

// userChange is a KindUser item's payload: the user whose role changed or
// who was removed.
type userChange struct {
	UserID string `json:"user_id"`
}

// streamable reports whether item may reach who on a stream. A turn's
// result carries its text, and an agent event the loop's raw output, so a
// member gets neither; a message reaches them if reads says they read it.
// A reaction, a vote or a poll's close reaches them if they read its
// message, which the router reads once as it publishes, so a member learns
// nothing of a private message they don't read (#922). A link's change
// reaches the user it names, so their Account page follows it.
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
	case bus.KindReaction:
		payload, ok := item.Payload.(*route.ReactionPayload)
		return ok && payload.Target != nil && reads(payload.Target)
	case bus.KindPoll:
		payload, ok := item.Payload.(*route.PollPayload)
		return ok && payload.Target != nil && reads(payload.Target)
	case bus.KindLink:
		payload, ok := item.Payload.(*route.LinkPayload)
		return ok && who.user != nil && payload.UserID == who.user.ID
	}
	return true
}

// seesEveryUpload reports whether who may read and send a file another
// user uploaded and has not sent yet: whoever reads every private
// conversation, since an upload may be meant for one. Anyone else reads
// only their own.
func seesEveryUpload(who *caller) bool {
	return seesPrivate(who)
}

// uploadSenderOf is who sends a message's upload when who posts it.
func uploadSenderOf(who *caller) route.UploadSender {
	if seesEveryUpload(who) {
		return route.UploadSender{Any: true}
	}
	if who == nil || who.user == nil {
		return route.UploadSender{}
	}
	return route.UploadSender{UserID: who.user.ID}
}

// uploaderOf is the user id an upload by who records, "" for the operator
// token.
func uploaderOf(who *caller) string {
	if who == nil || who.user == nil {
		return ""
	}
	return who.user.ID
}
