package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
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
)

// connectionView is a connection as the control room reads it: everything
// but the secret, which it knows only to be there or not.
type connectionView struct {
	Name      string                 `json:"name"`
	Kind      string                 `json:"kind"`
	Config    store.ConnectionConfig `json:"config"`
	HasSecret bool                   `json:"has_secret"`
	CreatedAt int64                  `json:"created_at"`
}

func newConnectionView(connection *store.Connection) connectionView {
	return connectionView{
		Name:      connection.Name,
		Kind:      connection.Kind,
		Config:    connection.Config,
		HasSecret: connection.Secret != "",
		CreatedAt: connection.CreatedAt,
	}
}

func (server *Server) handleListConnections(w http.ResponseWriter, r *http.Request) {
	connections, err := server.Store.Connections().List(r.Context())
	if err != nil {
		server.jsonErr(w, 500, "%v", err)
		return
	}
	views := make([]connectionView, 0, len(connections))
	for _, connection := range connections {
		views = append(views, newConnectionView(connection))
	}
	writeJSON(w, 200, views)
}

func (server *Server) handleGetConnection(w http.ResponseWriter, r *http.Request) {
	connection, err := server.Store.Connections().Get(r.Context(), r.PathValue("name"))
	if err != nil {
		server.connectionErr(w, r, err)
		return
	}
	writeJSON(w, 200, newConnectionView(connection))
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
	writeJSON(w, 201, newConnectionView(connection))
}

func (server *Server) handleDeleteConnection(w http.ResponseWriter, r *http.Request) {
	if err := server.Store.Connections().Delete(r.Context(), r.PathValue("name")); err != nil {
		server.connectionErr(w, r, err)
		return
	}
	server.secretsChanged(r.Context())
	writeJSON(w, 200, map[string]bool{"deleted": true})
}

// connectionErr answers a store error about the connection named in the path.
func (server *Server) connectionErr(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, store.ErrNotFound) {
		server.jsonErrCode(w, 404, codeConnectionNotFound, "connection %q not found", r.PathValue("name"))
		return
	}
	server.jsonErr(w, 500, "%v", err)
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
	case store.ConnectionEnvCredential:
		switch {
		case validateSecretName(config.Env) != nil:
			return codeConnectionConfigInvalid, "an env-credential's config.env must be an env var name ([A-Za-z_][A-Za-z0-9_]*)"
		case config.Transport != "" || config.URL != "" || config.Command != "" || len(config.Args) > 0:
			return codeConnectionConfigInvalid, "an env-credential's config has env and nothing else"
		case connection.Secret == "":
			return codeConnectionSecretInvalid, "an env-credential needs a secret: the value its env var carries"
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
		return codeConnectionKindInvalid, "a connection's kind is env-credential or mcp-server"
	}
	return "", ""
}
