package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"time"

	"github.com/enes-alatas/spool/internal/githubapp"
	"github.com/enes-alatas/spool/internal/loop"
	"github.com/enes-alatas/spool/internal/store"
)

// Connections (ADR-0043): the org's tool credentials and configs, each
// defined once under a name. A secret goes in and never comes back out; the
// redactor learns it the moment it is stored.

const (
	codeConnectionNameInvalid   = "connection_name_invalid"
	codeConnectionExists        = "connection_exists"
	codeConnectionKindInvalid   = "connection_kind_invalid"
	codeConnectionConfigInvalid = "connection_config_invalid"
	codeConnectionSecretInvalid = "connection_secret_invalid"
	codeConnectionNotFound      = "connection_not_found"
	codeConnectionAttached      = "connection_attached"
	codeConnectionEnvTaken      = "connection_env_taken"
	codeConnectionPrivate       = "connection_private"
	codeConnectionRevoked       = "connection_revoked"
	codeConnectionRuntime       = "connection_runtime"
	codeConnectionGitHubTaken   = "connection_github_taken"
	codeConnectionGitHubEnv     = "connection_github_env"
)

// connectionView is a connection as the control room reads it: everything
// but the secret, which it knows only to be there or not, and its loops by
// name, sorted. Config is absent for a reader who doesn't see it
// (seesConnectionConfig). OwnerLoop names the loop a private one belongs
// to, and is absent for one the fleet shares.
type connectionView struct {
	Name      string                  `json:"name"`
	Kind      string                  `json:"kind"`
	Config    *store.ConnectionConfig `json:"config,omitempty"`
	HasSecret bool                    `json:"has_secret"`
	CreatedAt int64                   `json:"created_at"`
	Loops     []string                `json:"loops"`
	OwnerLoop string                  `json:"owner_loop,omitempty"`
	// Teams are the teams it is attached to as a whole, sorted; Loops stays
	// the loops it is attached to directly (#1610).
	Teams     []string `json:"teams"`
	RotatedAt int64    `json:"rotated_at,omitempty"`
	RevokedAt int64    `json:"revoked_at,omitempty"`
	// LastCheck is the last check of the value it holds, absent for none
	// and for a reader who doesn't see Config: a check is about the address
	// an admin set, and its reason is news about what the hub can reach
	// (#1360).
	LastCheck *store.ConnectionCheck `json:"last_check,omitempty"`
}

func (server *Server) connectionViews(r *http.Request, connections ...*store.Connection) ([]connectionView, error) {
	loops, err := server.Store.Loops().List(r.Context())
	if err != nil {
		return nil, err
	}
	names := make(map[string]string, len(loops))
	for _, loopRecord := range loops {
		names[loopRecord.ID] = loopRecord.Name
	}
	seesConfig := seesConnectionConfig(callerOf(r))
	views := make([]connectionView, 0, len(connections))
	for _, connection := range connections {
		view := connectionView{
			Name:      connection.Name,
			Kind:      connection.Kind,
			HasSecret: connection.Secret != "",
			CreatedAt: connection.CreatedAt,
			Loops:     []string{},
			OwnerLoop: names[connection.OwnerLoopID],
			Teams:     connection.Teams,
			RotatedAt: connection.RotatedAt,
			RevokedAt: connection.RevokedAt,
		}
		if seesConfig {
			config := connection.Config
			view.Config = &config
			view.LastCheck = connection.LastCheck
		}
		for _, id := range connection.LoopIDs {
			if name, ok := names[id]; ok {
				view.Loops = append(view.Loops, name)
			}
		}
		sort.Strings(view.Loops)
		views = append(views, view)
	}
	return views, nil
}

func (server *Server) writeConnection(w http.ResponseWriter, r *http.Request, status int, connection *store.Connection) {
	views, err := server.connectionViews(r, connection)
	if err != nil {
		server.jsonErr(w, 500, "%v", err)
		return
	}
	writeJSON(w, status, views[0])
}

func (server *Server) handleListConnections(w http.ResponseWriter, r *http.Request) {
	connections, err := server.Store.Connections().List(r.Context())
	if err != nil {
		server.jsonErr(w, 500, "%v", err)
		return
	}
	views, err := server.connectionViews(r, connections...)
	if err != nil {
		server.jsonErr(w, 500, "%v", err)
		return
	}
	writeJSON(w, 200, views)
}

func (server *Server) handleGetConnection(w http.ResponseWriter, r *http.Request) {
	connection, err := server.Store.Connections().Get(r.Context(), r.PathValue("name"))
	if err != nil {
		server.connectionErr(w, r, err)
		return
	}
	server.writeConnection(w, r, 200, connection)
}

// handleCreateConnection creates a connection, shared, or private to the
// loop owner_loop names and attached to it. A private env-var may leave its
// name to the hub, which names it as a loop secret's is named.
func (server *Server) handleCreateConnection(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name      string                 `json:"name"`
		Kind      string                 `json:"kind"`
		Config    store.ConnectionConfig `json:"config"`
		Secret    string                 `json:"secret"`
		OwnerLoop string                 `json:"owner_loop"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		server.jsonErr(w, 400, "bad json: %v", err)
		return
	}
	connection := &store.Connection{Name: req.Name, Kind: req.Kind, Config: req.Config, Secret: req.Secret, CreatedAt: time.Now().UnixMilli()}
	var owner *store.Loop
	if req.OwnerLoop != "" {
		var err error
		if owner, err = server.Store.Loops().GetByName(r.Context(), req.OwnerLoop); err != nil {
			server.storeErr(w, err, "loop")
			return
		}
		connection.OwnerLoopID = owner.ID
	}
	named := connection.Name != "" || owner == nil || connection.Kind != store.ConnectionEnvVar
	if !named {
		// a valid stand-in to check the rest by; createLoopEnvVar names it
		connection.Name = "unnamed"
	}
	if code, problem := connectionProblem(connection); problem != "" {
		server.jsonErrCode(w, 400, code, "%s", problem)
		return
	}
	if owner != nil {
		server.attachMu.Lock()
		defer server.attachMu.Unlock()
		if !server.attachableTo(w, r, connection, owner) {
			return
		}
	}
	var err error
	if named {
		err = server.Store.Connections().Create(r.Context(), connection)
	} else {
		err = server.createLoopEnvVar(r.Context(), connection, owner)
	}
	switch {
	case errors.Is(err, store.ErrDuplicate):
		server.jsonErrCode(w, 409, codeConnectionExists, "a connection named %q already exists", connection.Name)
		return
	case err != nil:
		// the owner was found a moment ago, so a missing row is the loop deleted since
		server.storeErr(w, err, "loop")
		return
	}
	server.secretsChanged(r.Context())
	if owner != nil {
		server.loopChanged(r.Context(), owner.ID)
		server.envChanged(connection, owner.ID)
	}
	server.writeConnection(w, r, 201, connection)
}

// handleDeleteConnection deletes a connection no loop holds, or a private
// one its owner alone holds, detaching it in the same step.
func (server *Server) handleDeleteConnection(w http.ResponseWriter, r *http.Request) {
	connection, err := server.Store.Connections().Get(r.Context(), r.PathValue("name"))
	if err != nil {
		server.connectionErr(w, r, err)
		return
	}
	if err := server.Store.Connections().Delete(r.Context(), connection.Name, time.Now().UnixMilli()); err != nil {
		server.connectionErr(w, r, err)
		return
	}
	server.revokeGitHubToken(r.Context(), connection.Name)
	server.secretsChanged(r.Context())
	for _, id := range connection.LoopIDs {
		server.loopChanged(r.Context(), id)
		server.envChanged(connection, id)
	}
	writeJSON(w, 200, map[string]bool{"deleted": true})
}

// handleShareConnection makes a private connection the fleet's, one way:
// its value may be in another loop's env from then on. Sharing a shared one
// answers as if it had just been shared.
func (server *Server) handleShareConnection(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := server.Store.Connections().Share(r.Context(), name, time.Now().UnixMilli()); err != nil {
		server.connectionErr(w, r, err)
		return
	}
	connection, err := server.Store.Connections().Get(r.Context(), name)
	if err != nil {
		server.connectionErr(w, r, err)
		return
	}
	server.writeConnection(w, r, 200, connection)
}

// handleRotateConnection replaces a connection's value. The old one is
// retired, still redacted and never read back, and every loop that held
// the connection ends the session that ran with it. Setting the value it
// holds changes nothing (ADR-0043).
func (server *Server) handleRotateConnection(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Value string `json:"value"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		server.jsonErr(w, 400, "bad json: %v", err)
		return
	}
	connection, err := server.Store.Connections().Get(r.Context(), r.PathValue("name"))
	if err == nil && connection.RevokedAt != 0 {
		err = store.ErrConnectionRevoked // ahead of the checks a new value would need
	}
	if err != nil {
		server.connectionErr(w, r, err)
		return
	}
	if req.Value == "" {
		server.jsonErrCode(w, 400, codeConnectionSecretInvalid, "a new value can't be empty")
		return
	}
	rotated := *connection
	rotated.Secret = req.Value
	if code, problem := connectionProblem(&rotated); problem != "" {
		server.jsonErrCode(w, 400, code, "%s", problem)
		return
	}
	if req.Value != connection.Secret {
		if err := server.rotateConnection(r.Context(), connection, req.Value); err != nil {
			server.connectionErr(w, r, err)
			return
		}
	}
	if connection, err = server.Store.Connections().Get(r.Context(), connection.Name); err != nil {
		server.connectionErr(w, r, err)
		return
	}
	server.writeConnection(w, r, 200, connection)
}

// rotateConnection replaces a connection's value, and asks each loop that
// holds it for a context rotation: the session that ran with the old value
// ends at the loop's next quiet boundary, and its successor's wake reads
// the new one. A loop with no session needs none (Enes, 2026-10-05, #507).
// A github-app ends no session: no loop held its key.
func (server *Server) rotateConnection(ctx context.Context, connection *store.Connection, value string) error {
	if err := server.Store.Connections().SetSecret(ctx, connection.Name, value, time.Now().UnixMilli()); err != nil {
		return err
	}
	server.secretsChanged(ctx)
	// A github-app's key never reached a loop, so no session ran with it:
	// the token minted under the old key ends, and the next fetch mints
	// under the new one (#887).
	appKey := connection.Kind == store.ConnectionGitHubApp
	if appKey {
		server.revokeGitHubToken(ctx, connection.Name)
	}
	for _, id := range connection.HolderIDs {
		server.loopChanged(ctx, id)
		if actor, ok := server.Manager.Get(id); ok && !appKey {
			_ = actor.Rotate(store.RotationReasonConnection) // an error is no session to end
		}
	}
	return nil
}

// handleRevokeConnection takes a connection from every loop that holds it
// and refuses it from then on: attaching, sharing, a new value, and
// revoking it again. Its value is retired, still redacted, and each loop
// that held it ends the session that ran with it (ADR-0043). The operator
// may delete it after; nothing revives it.
func (server *Server) handleRevokeConnection(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	loopIDs, err := server.Store.Connections().Revoke(r.Context(), name, time.Now().UnixMilli())
	if err != nil {
		server.connectionErr(w, r, err)
		return
	}
	server.revokeGitHubToken(r.Context(), name)
	server.secretsChanged(r.Context())
	for _, id := range loopIDs {
		server.loopChanged(r.Context(), id)
		if actor, ok := server.Manager.Get(id); ok {
			_ = actor.Rotate(store.RotationReasonRevoke) // an error is no session to end
		}
	}
	connection, err := server.Store.Connections().Get(r.Context(), name)
	if err != nil {
		server.connectionErr(w, r, err)
		return
	}
	server.writeConnection(w, r, 200, connection)
}

// connectionEventView is one change on a connection's record. Loop is the
// loop's name as it was, absent for a change no loop is part of.
type connectionEventView struct {
	Action     string `json:"action"`
	Connection string `json:"connection"`
	Loop       string `json:"loop,omitempty"`
	// Team is the team of a team attach or detach (#1610).
	Team string `json:"team,omitempty"`
	At   int64  `json:"at"`
}

func (server *Server) writeConnectionEvents(w http.ResponseWriter, r *http.Request, filter store.ConnectionEventFilter) {
	events, err := server.Store.Connections().Events(r.Context(), filter)
	if err != nil {
		server.jsonErr(w, 500, "%v", err)
		return
	}
	views := make([]connectionEventView, 0, len(events))
	for _, event := range events {
		views = append(views, connectionEventView{Action: event.Action, Connection: event.Connection, Loop: event.LoopName, Team: event.Team, At: event.At})
	}
	writeJSON(w, 200, views)
}

// handleConnectionEvents lists a connection's record, newest first. It
// outlives the connection, so a name with no connection behind it is
// still answered, with whatever its record holds.
func (server *Server) handleConnectionEvents(w http.ResponseWriter, r *http.Request) {
	server.writeConnectionEvents(w, r, store.ConnectionEventFilter{Connection: r.PathValue("name")})
}

// handleLoopConnectionEvents lists the changes a loop was part of, newest
// first: by the loop's id while it exists, so a new loop doesn't inherit
// the record of a deleted one it shares a name with, and by the name once
// the loop is gone.
func (server *Server) handleLoopConnectionEvents(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	loopRecord, err := server.Store.Loops().GetByName(r.Context(), name)
	switch {
	case err == nil:
		server.writeConnectionEvents(w, r, store.ConnectionEventFilter{LoopID: loopRecord.ID})
	case errors.Is(err, store.ErrNotFound):
		server.writeConnectionEvents(w, r, store.ConnectionEventFilter{LoopName: name})
	default:
		server.jsonErr(w, 500, "%v", err)
	}
}

// handleLoopConnection attaches the connection to the loop (attach) or
// detaches it. Either is idempotent, and an env-var's change reaches the
// loop's env from its next turn (ADR-0043, #640).
func (server *Server) handleLoopConnection(attach bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		loopRecord := server.loopByName(w, r)
		if loopRecord == nil {
			return
		}
		name := r.PathValue("connection")
		server.attachMu.Lock()
		defer server.attachMu.Unlock()
		var err error
		if attach {
			if !server.attachable(w, r, name, loopRecord) {
				return
			}
			err = server.Store.Connections().Attach(r.Context(), name, loopRecord.ID, time.Now().UnixMilli())
		} else {
			err = server.Store.Connections().Detach(r.Context(), name, loopRecord.ID, time.Now().UnixMilli())
		}
		if err != nil {
			// The loop was found a moment ago, so a missing row is the
			// connection, or the loop deleted since.
			server.connectionErr(w, r, err)
			return
		}
		server.loopChanged(r.Context(), loopRecord.ID)
		if connection, err := server.Store.Connections().Get(r.Context(), name); err == nil {
			server.envChanged(connection, loopRecord.ID)
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// envChanged tells a loop that an env-var or a github-app connection was
// attached to it or taken from it, so its live process closes at its next
// quiet boundary and its next turn runs with the new env (#640): a
// github-app gives the loop its token route and git's helper (#1054). A
// rotation needs no call: it ends the session, which spawns afresh. An
// mcp-server's config is read at spawn too, and is left to its next wake.
func (server *Server) envChanged(connection *store.Connection, loopID string) {
	if connection.Kind != store.ConnectionEnvVar && connection.Kind != store.ConnectionGitHubApp {
		return
	}
	if actor, ok := server.Manager.Get(loopID); ok {
		actor.RefreshEnv()
	}
}

// attachable reports whether the named connection can be attached to the
// loop, and answers the request when it can't (attachableTo).
func (server *Server) attachable(w http.ResponseWriter, r *http.Request, name string, loopRecord *store.Loop) bool {
	connection, err := server.Store.Connections().Get(r.Context(), name)
	if err == nil && connection.RevokedAt != 0 {
		err = store.ErrConnectionRevoked // ahead of what attaching it would clash with
	}
	if err != nil {
		server.connectionErr(w, r, err)
		return false
	}
	return server.attachableTo(w, r, connection, loopRecord)
}

// attachableTo is attachable for a connection in hand, which may not be
// stored yet. It refuses what would leave the loop two answers to one
// question: two env-vars setting one variable, which would leave the value
// to whichever the env build read last, or two GitHub identities, a second
// github-app or a github-app beside an env-var on GH_TOKEN (#887). Nor
// may an env-var beside a github-app set a variable the hub sets for its
// git and gh (#1060). A cloud loop can't hold a github-app: its listener
// doesn't serve the token route.
// The caller holds attachMu.
func (server *Server) attachableTo(w http.ResponseWriter, r *http.Request, connection *store.Connection, loopRecord *store.Loop) bool {
	held, err := server.Store.Connections().ListByLoop(r.Context(), loopRecord.ID)
	if err != nil {
		server.jsonErr(w, 500, "%v", err)
		return false
	}
	if code, problem := attachConflict(connection, loopRecord, held); problem != "" {
		server.jsonErrCode(w, 409, code, "%s", problem)
		return false
	}
	return true
}

// attachConflict is attachableTo's rule over what the loop holds, as a
// refusal code and its sentence, or "" when the loop may hold connection:
// the one rule a loop's attach, a team's, and a loop joining a team use
// (#1610).
func attachConflict(connection *store.Connection, loopRecord *store.Loop, held []*store.Connection) (code, problem string) {
	if connection.Kind == store.ConnectionGitHubApp && loopRecord.Runtime == store.RuntimeCloud {
		return codeConnectionRuntime, fmt.Sprintf("loop %q is a cloud loop, and a cloud loop can't hold a github-app: its listener doesn't serve the token route", loopRecord.Name)
	}
	return heldConflict(connection, held, fmt.Sprintf("loop %q", loopRecord.Name))
}

// heldConflict is the part of attachConflict that reads only the
// connections held, not who holds them, so it checks a team with no loops
// yet too (#1610). holder names the holder in the sentence.
func heldConflict(connection *store.Connection, held []*store.Connection, holder string) (code, problem string) {
	if connection.Kind == store.ConnectionMCPServer {
		return "", ""
	}
	for _, other := range held {
		if other.Name == connection.Name {
			continue
		}
		switch {
		case connection.Kind == store.ConnectionEnvVar && other.Kind == store.ConnectionEnvVar && other.Config.Env == connection.Config.Env:
			return codeConnectionEnvTaken, fmt.Sprintf("%s already sets %s on %s; detach it first",
				other.Name, connection.Config.Env, holder)
		case githubIdentity(connection) && githubIdentity(other):
			return codeConnectionGitHubTaken, fmt.Sprintf("%s already gives %s its GitHub identity; detach it first",
				other.Name, holder)
		case shadowsGitHubEnv(connection, other) || shadowsGitHubEnv(other, connection):
			envVar := connection
			if envVar.Kind != store.ConnectionEnvVar {
				envVar = other
			}
			return codeConnectionGitHubEnv, fmt.Sprintf("%s sets %s, which %s's github-app sets for its git and gh; detach it first",
				envVar.Name, envVar.Config.Env, holder)
		}
	}
	return "", ""
}

// teamCanHold refuses attaching connection to a team that already holds
// one it clashes with, whether or not the team has loops yet: an empty
// team would otherwise take both, then refuse every loop that joined it
// (#1610). The caller holds attachMu.
func (server *Server) teamCanHold(w http.ResponseWriter, r *http.Request, connection *store.Connection, team string) bool {
	connections, err := server.Store.Connections().List(r.Context())
	if err != nil {
		server.jsonErr(w, 500, "%v", err)
		return false
	}
	var given []*store.Connection
	for _, other := range connections {
		if slices.Contains(other.Teams, team) {
			given = append(given, other)
		}
	}
	if code, problem := heldConflict(connection, given, fmt.Sprintf("team %q's loops", team)); problem != "" {
		server.jsonErrCode(w, 409, code, "%s", problem)
		return false
	}
	return true
}

// shadowsGitHubEnv reports whether envVar is an env-var that would replace
// what app, a github-app, has the hub set for the loop's git and gh.
func shadowsGitHubEnv(envVar, app *store.Connection) bool {
	return envVar.Kind == store.ConnectionEnvVar && app.Kind == store.ConnectionGitHubApp &&
		loop.ShadowsGitHubEnv(envVar.Config.Env)
}

// githubIdentity reports whether a connection is a loop's answer to which
// GitHub identity it acts as: a github-app, or an env-var on GH_TOKEN. Two
// on one loop would leave git and gh to disagree.
func githubIdentity(connection *store.Connection) bool {
	return connection.Kind == store.ConnectionGitHubApp ||
		connection.Kind == store.ConnectionEnvVar && connection.Config.Env == "GH_TOKEN"
}

// connectionErr answers a store error about the connection the path names:
// {name} on the connection's own routes, {connection} under a loop.
func (server *Server) connectionErr(w http.ResponseWriter, r *http.Request, err error) {
	name := defaultStr(r.PathValue("connection"), r.PathValue("name"))
	switch {
	case errors.Is(err, store.ErrNotFound):
		server.jsonErrCode(w, 404, codeConnectionNotFound, "connection %q not found", name)
	case errors.Is(err, store.ErrConnectionAttached):
		server.jsonErrCode(w, 409, codeConnectionAttached, "connection %q is attached to a loop; detach it first", name)
	case errors.Is(err, store.ErrConnectionPrivate):
		server.jsonErrCode(w, 409, codeConnectionPrivate, "connection %q is private to another loop; share it first", name)
	case errors.Is(err, store.ErrConnectionRevoked):
		server.jsonErrCode(w, 409, codeConnectionRevoked, "connection %q is revoked; create a new one", name)
	default:
		server.jsonErr(w, 500, "%v", err)
	}
}

// connectionProblem says what is wrong with a connection about to be
// created, as a refusal code and the sentence that goes with it, or "" when
// nothing is. A config field another kind uses is refused rather than
// dropped, so a connection reads back as it was asked for.
func connectionProblem(connection *store.Connection) (code, problem string) {
	config := connection.Config
	if !store.ValidConnectionName(connection.Name) {
		return codeConnectionNameInvalid, "a connection name is 1 to 32 of a-z, 0-9 and '-', not starting with '-'"
	}
	if len(connection.Secret) > maxSecretValueLen {
		return codeConnectionSecretInvalid, "the secret is too large (max 16 KiB)"
	}
	if connection.Kind != store.ConnectionGitHubApp && (config.AppID != "" || config.InstallationID != 0) {
		return codeConnectionConfigInvalid, "only a github-app's config has app_id and installation_id"
	}
	switch connection.Kind {
	case store.ConnectionEnvVar:
		switch {
		case validateSecretName(config.Env) != nil:
			return codeConnectionConfigInvalid, "an env-var's config.env must be an env var name ([A-Za-z_][A-Za-z0-9_]*)"
		case config.Transport != "" || config.URL != "" || config.Command != "" || len(config.Args) > 0:
			return codeConnectionConfigInvalid, "an env-var's config has env and nothing else"
		case connection.Secret == "":
			return codeConnectionSecretInvalid, "an env-var needs a secret: the value it carries"
		}
	case store.ConnectionMCPServer:
		if connection.Name == loop.SpoolMCPServer {
			// the hub's own server's name in a loop's --mcp-config
			return codeConnectionNameInvalid, "an mcp-server can't be named spool: that is the hub's own server"
		}
		switch config.Transport {
		case store.MCPTransportHTTP:
			parsed, err := url.Parse(config.URL)
			if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
				return codeConnectionConfigInvalid, "an http mcp-server's config.url must be an http or https URL"
			}
			if parsed.User != nil {
				// Userinfo is always a credential, and the config is read back
				// in full and redacted by nobody.
				return codeConnectionConfigInvalid, "an http mcp-server's config.url carries no user:password; put the credential in secret"
			}
			if config.Command != "" || len(config.Args) > 0 || config.Env != "" {
				return codeConnectionConfigInvalid, "an http mcp-server's config has a url, not a command, args or env"
			}
			if connection.Secret != "" && config.Cleartext() {
				return codeConnectionConfigInvalid, "an http mcp-server with a secret needs an https url, or http to a loopback host: the secret is sent as a bearer token"
			}
		case store.MCPTransportStdio:
			if config.Command == "" {
				return codeConnectionConfigInvalid, "a stdio mcp-server's config.command names the program to run"
			}
			if config.URL != "" {
				return codeConnectionConfigInvalid, "a stdio mcp-server's config has a command, not a url"
			}
			// a stdio server is handed its secret in the env var config.env
			// names, so one is never given without the other
			if config.Env != "" && validateSecretName(config.Env) != nil {
				return codeConnectionConfigInvalid, "a stdio mcp-server's config.env must be an env var name ([A-Za-z_][A-Za-z0-9_]*)"
			}
			if (config.Env == "") != (connection.Secret == "") {
				return codeConnectionConfigInvalid, "a stdio mcp-server's secret is handed to it in the env var config.env names: give both or neither"
			}
		default:
			return codeConnectionConfigInvalid, "an mcp-server's config.transport is http or stdio"
		}
	case store.ConnectionGitHubApp:
		switch {
		case !appIDRe.MatchString(config.AppID):
			return codeConnectionConfigInvalid, "a github-app's config.app_id is the app's App ID or Client ID"
		case config.InstallationID <= 0:
			return codeConnectionConfigInvalid, "a github-app's config.installation_id is the installation's id, a positive number"
		case config.Env != "" || config.Transport != "" || config.URL != "" || config.Command != "" || len(config.Args) > 0:
			return codeConnectionConfigInvalid, "a github-app's config has app_id and installation_id and nothing else"
		}
		if _, err := githubapp.ParseKey(connection.Secret); err != nil {
			return codeConnectionSecretInvalid, "a github-app's secret is the app's private key, an RSA key in PEM: " + err.Error()
		}
	default:
		return codeConnectionKindInvalid, "a connection's kind is env-var, mcp-server or github-app"
	}
	return "", ""
}

// appIDRe matches a GitHub App's App ID, a number, or its Client ID, which
// starts Iv.
var appIDRe = regexp.MustCompile(`^([1-9][0-9]{0,19}|Iv[0-9A-Za-z.]{1,40})$`)

// revokeGitHubToken ends a github-app connection's cached token at GitHub
// when it is revoked, given a new key or deleted, so the last token a loop
// was handed dies now rather than within the hour. Best effort: it expires
// anyway. Any other connection has no token cached.
func (server *Server) revokeGitHubToken(ctx context.Context, name string) {
	if server.GitHubApps == nil {
		return
	}
	if err := server.GitHubApps.Revoke(ctx, name); err != nil {
		server.Log.Warn("github-app: the cached token lives out its hour", "connection", name, "err", err)
	}
}

// handleTeamConnection attaches the connection to a team as a whole, or
// detaches it (#1610): every loop in the team holds it, those that join
// later included. Either is idempotent. A private or revoked connection is
// refused, and so is one that clashes with a connection the team already
// holds, members or none (teamCanHold), or one a member couldn't hold,
// naming that member: the team's loops are checked one by one, as a
// loop's own attach checks it.
// Each member's env changes from its next turn, as on a loop's attach.
func (server *Server) handleTeamConnection(attach bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		teamName, name := r.PathValue("team"), r.PathValue("connection")
		// the members are read under the lock a loop joining the team holds
		server.attachMu.Lock()
		defer server.attachMu.Unlock()
		team, err := server.Store.Teams().Get(r.Context(), teamName)
		if err != nil {
			server.teamErr(w, err, teamName)
			return
		}
		if attach {
			connection, err := server.Store.Connections().Get(r.Context(), name)
			switch {
			case err != nil:
				server.connectionErr(w, r, err)
				return
			case connection.RevokedAt != 0:
				server.connectionErr(w, r, store.ErrConnectionRevoked)
				return
			case connection.OwnerLoopID != "":
				server.connectionErr(w, r, store.ErrConnectionPrivate)
				return
			}
			if !server.teamCanHold(w, r, connection, teamName) {
				return
			}
			for _, loopID := range team.LoopIDs {
				member, err := server.Store.Loops().Get(r.Context(), loopID)
				if err != nil {
					server.jsonErr(w, 500, "%v", err)
					return
				}
				if !server.attachableTo(w, r, connection, member) {
					return
				}
			}
			err = server.Store.Connections().AttachTeam(r.Context(), name, teamName, time.Now().UnixMilli())
			if err != nil {
				server.connectionErr(w, r, err)
				return
			}
		} else if err := server.Store.Connections().DetachTeam(r.Context(), name, teamName, time.Now().UnixMilli()); err != nil {
			server.connectionErr(w, r, err)
			return
		}
		connection, err := server.Store.Connections().Get(r.Context(), name)
		if err != nil {
			server.connectionErr(w, r, err)
			return
		}
		for _, loopID := range team.LoopIDs {
			server.loopChanged(r.Context(), loopID)
			server.envChanged(connection, loopID)
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
