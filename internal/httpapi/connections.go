package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"sort"
	"time"

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
)

// connectionView is a connection as the control room reads it: everything
// but the secret, which it knows only to be there or not, and its loops by
// name, sorted.
type connectionView struct {
	Name      string                 `json:"name"`
	Kind      string                 `json:"kind"`
	Config    store.ConnectionConfig `json:"config"`
	HasSecret bool                   `json:"has_secret"`
	CreatedAt int64                  `json:"created_at"`
	Loops     []string               `json:"loops"`
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

func (server *Server) handleCreateConnection(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name   string                 `json:"name"`
		Kind   string                 `json:"kind"`
		Config store.ConnectionConfig `json:"config"`
		Secret string                 `json:"secret"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		server.jsonErr(w, 400, "bad json: %v", err)
		return
	}
	connection := &store.Connection{Name: req.Name, Kind: req.Kind, Config: req.Config, Secret: req.Secret, CreatedAt: time.Now().UnixMilli()}
	if code, problem := connectionProblem(connection); problem != "" {
		server.jsonErrCode(w, 400, code, "%s", problem)
		return
	}
	if err := server.Store.Connections().Create(r.Context(), connection); err != nil {
		if errors.Is(err, store.ErrDuplicate) {
			server.jsonErrCode(w, 409, codeConnectionExists, "a connection named %q already exists", connection.Name)
			return
		}
		server.jsonErr(w, 500, "%v", err)
		return
	}
	server.secretsChanged(r.Context())
	server.writeConnection(w, r, 201, connection)
}

func (server *Server) handleDeleteConnection(w http.ResponseWriter, r *http.Request) {
	if err := server.Store.Connections().Delete(r.Context(), r.PathValue("name")); err != nil {
		server.connectionErr(w, r, err)
		return
	}
	server.secretsChanged(r.Context())
	writeJSON(w, 200, map[string]bool{"deleted": true})
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
		var err error
		if attach {
			err = server.Store.Connections().Attach(r.Context(), name, loopRecord.ID, time.Now().UnixMilli())
		} else {
			err = server.Store.Connections().Detach(r.Context(), name, loopRecord.ID)
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

// connectionErr answers a store error about the connection the path names:
// {name} on the connection's own routes, {connection} under a loop.
func (server *Server) connectionErr(w http.ResponseWriter, r *http.Request, err error) {
	name := defaultStr(r.PathValue("connection"), r.PathValue("name"))
	switch {
	case errors.Is(err, store.ErrNotFound):
		server.jsonErrCode(w, 404, codeConnectionNotFound, "connection %q not found", name)
	case errors.Is(err, store.ErrConnectionAttached):
		server.jsonErrCode(w, 409, codeConnectionAttached, "connection %q is attached to a loop; detach it first", name)
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
		if config.Env != "" {
			return codeConnectionConfigInvalid, "an mcp-server's config has no env"
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
			if config.Command != "" || len(config.Args) > 0 {
				return codeConnectionConfigInvalid, "an http mcp-server's config has a url, not a command or args"
			}
		case store.MCPTransportStdio:
			if config.Command == "" {
				return codeConnectionConfigInvalid, "a stdio mcp-server's config.command names the program to run"
			}
			if config.URL != "" {
				return codeConnectionConfigInvalid, "a stdio mcp-server's config has a command, not a url"
			}
		default:
			return codeConnectionConfigInvalid, "an mcp-server's config.transport is http or stdio"
		}
	default:
		return codeConnectionKindInvalid, "a connection's kind is env-var or mcp-server"
	}
	return "", ""
}
