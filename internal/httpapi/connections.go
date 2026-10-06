package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"sort"
	"time"

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
)

// connectionView is a connection as the control room reads it: everything
// but the secret, which it knows only to be there or not, and its loops by
// name, sorted. OwnerLoop names the loop a private one belongs to, and is
// absent for one the fleet shares.
type connectionView struct {
	Name      string                 `json:"name"`
	Kind      string                 `json:"kind"`
	Config    store.ConnectionConfig `json:"config"`
	HasSecret bool                   `json:"has_secret"`
	CreatedAt int64                  `json:"created_at"`
	Loops     []string               `json:"loops"`
	OwnerLoop string                 `json:"owner_loop,omitempty"`
	RotatedAt int64                  `json:"rotated_at,omitempty"`
	RevokedAt int64                  `json:"revoked_at,omitempty"`
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
	views := make([]connectionView, 0, len(connections))
	for _, connection := range connections {
		view := connectionView{
			Name:      connection.Name,
			Kind:      connection.Kind,
			Config:    connection.Config,
			HasSecret: connection.Secret != "",
			CreatedAt: connection.CreatedAt,
			Loops:     []string{},
			OwnerLoop: names[connection.OwnerLoopID],
			RotatedAt: connection.RotatedAt,
			RevokedAt: connection.RevokedAt,
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
		server.envMu.Lock()
		defer server.envMu.Unlock()
		if !server.envFreeFor(w, r, connection, owner) {
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
	server.secretsChanged(r.Context())
	for _, id := range connection.LoopIDs {
		server.loopChanged(r.Context(), id)
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
func (server *Server) rotateConnection(ctx context.Context, connection *store.Connection, value string) error {
	if err := server.Store.Connections().SetSecret(ctx, connection.Name, value, time.Now().UnixMilli()); err != nil {
		return err
	}
	server.secretsChanged(ctx)
	for _, id := range connection.LoopIDs {
		server.loopChanged(ctx, id)
		if actor, ok := server.Manager.Get(id); ok {
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
	At         int64  `json:"at"`
}

func (server *Server) writeConnectionEvents(w http.ResponseWriter, r *http.Request, filter store.ConnectionEventFilter) {
	events, err := server.Store.Connections().Events(r.Context(), filter)
	if err != nil {
		server.jsonErr(w, 500, "%v", err)
		return
	}
	views := make([]connectionEventView, 0, len(events))
	for _, event := range events {
		views = append(views, connectionEventView{Action: event.Action, Connection: event.Connection, Loop: event.LoopName, At: event.At})
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
// detaches it. Either is idempotent, and an attached env-var is in the
// loop's env from its next wake (ADR-0043).
func (server *Server) handleLoopConnection(attach bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		loopRecord := server.loopByName(w, r)
		if loopRecord == nil {
			return
		}
		name := r.PathValue("connection")
		server.envMu.Lock()
		defer server.envMu.Unlock()
		var err error
		if attach {
			if !server.envFree(w, r, name, loopRecord) {
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
		w.WriteHeader(http.StatusNoContent)
	}
}

// envFree reports whether the named connection can be attached to the loop
// without two env-vars setting one variable, which would leave the loop's
// value to whichever the env build read last. It answers the request when
// it can't.
func (server *Server) envFree(w http.ResponseWriter, r *http.Request, name string, loopRecord *store.Loop) bool {
	connection, err := server.Store.Connections().Get(r.Context(), name)
	if err == nil && connection.RevokedAt != 0 {
		err = store.ErrConnectionRevoked // no variable to free for one that can't be attached
	}
	if err != nil {
		server.connectionErr(w, r, err)
		return false
	}
	return server.envFreeFor(w, r, connection, loopRecord)
}

// envFreeFor is envFree for a connection in hand, which may not be stored
// yet. The caller holds envMu.
func (server *Server) envFreeFor(w http.ResponseWriter, r *http.Request, connection *store.Connection, loopRecord *store.Loop) bool {
	name := connection.Name
	if connection.Kind != store.ConnectionEnvVar {
		return true
	}
	byEnv, err := server.loopEnvVars(r.Context(), loopRecord.ID)
	if err != nil {
		server.jsonErr(w, 500, "%v", err)
		return false
	}
	if holder, ok := byEnv[connection.Config.Env]; ok && holder.Name != name {
		server.jsonErrCode(w, 409, codeConnectionEnvTaken, "%s already sets %s on loop %q; detach it first",
			holder.Name, connection.Config.Env, loopRecord.Name)
		return false
	}
	return true
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
	default:
		return codeConnectionKindInvalid, "a connection's kind is env-var or mcp-server"
	}
	return "", ""
}
