package loop

import (
	"reflect"
	"testing"

	"github.com/enes-alatas/spool/internal/claude"
	"github.com/enes-alatas/spool/internal/store"
)

// TestBuildExecEnv pins how a wake's env is assembled: nothing to inject
// stays nil, system vars and attached env-vars merge, an env-var wins a name
// collision with a system var — the deliberate operator escape hatch — and a
// connection of another kind is not env.
func TestBuildExecEnv(t *testing.T) {
	envVar := func(name, value string) *store.Connection {
		return &store.Connection{Kind: store.ConnectionEnvVar, Config: store.ConnectionConfig{Env: name}, Secret: value}
	}
	mcpServer := &store.Connection{Kind: store.ConnectionMCPServer, Secret: "mcp-fixture",
		Config: store.ConnectionConfig{Transport: store.MCPTransportStdio, Command: "docs-mcp"}}

	t.Run("empty is nil", func(t *testing.T) {
		if env := buildExecEnv(nil, nil); env != nil {
			t.Fatalf("got %v, want nil so spec.Env stays unset", env)
		}
		if env := buildExecEnv(nil, []*store.Connection{mcpServer}); env != nil {
			t.Fatalf("an mcp-server alone gave %v, want nil", env)
		}
	})

	t.Run("system and env-vars merge", func(t *testing.T) {
		env := buildExecEnv(
			map[string]string{"CLAUDE_CODE_OAUTH_TOKEN": "sk-ant-tok"},
			[]*store.Connection{envVar("GH_TOKEN", "ghp_x"), mcpServer},
		)
		if len(env) != 2 || env["CLAUDE_CODE_OAUTH_TOKEN"] != "sk-ant-tok" || env["GH_TOKEN"] != "ghp_x" {
			t.Fatalf("got %v, want the system var and GH_TOKEN alone", env)
		}
	})

	t.Run("an env-var overrides a colliding system var", func(t *testing.T) {
		env := buildExecEnv(
			map[string]string{"CLAUDE_CODE_OAUTH_TOKEN": "operator-token"},
			[]*store.Connection{envVar("CLAUDE_CODE_OAUTH_TOKEN", "loop-token")},
		)
		if env["CLAUDE_CODE_OAUTH_TOKEN"] != "loop-token" {
			t.Fatalf("got %q, want the env-var to win the collision", env["CLAUDE_CODE_OAUTH_TOKEN"])
		}
	})
}

// An attached mcp-server's secret goes to its server alone: an http
// server's as a bearer token, a stdio server's in the env var its config
// names. An env-var is no server. A stdio server with no env var named, and
// a server on plain http off the host, are given no secret (ADR-0043).
func TestMCPServersCarryTheirSecrets(t *testing.T) {
	got := mcpServers([]*store.Connection{
		{Name: "github", Kind: store.ConnectionEnvVar, Config: store.ConnectionConfig{Env: "GH_TOKEN"}, Secret: "fixture-gh"},
		{Name: "tracker", Kind: store.ConnectionMCPServer, Secret: "fixture-tracker",
			Config: store.ConnectionConfig{Transport: store.MCPTransportHTTP, URL: "https://mcp.example.test/"}},
		{Name: "handbook", Kind: store.ConnectionMCPServer, Secret: "fixture-handbook",
			Config: store.ConnectionConfig{Transport: store.MCPTransportStdio, Command: "handbook-mcp", Env: "HANDBOOK_KEY"}},
		{Name: "open-docs", Kind: store.ConnectionMCPServer,
			Config: store.ConnectionConfig{Transport: store.MCPTransportHTTP, URL: "https://docs.example.test/"}},
		{Name: "legacy", Kind: store.ConnectionMCPServer, Secret: "fixture-legacy",
			Config: store.ConnectionConfig{Transport: store.MCPTransportStdio, Command: "legacy-mcp"}},
		{Name: "cleartext", Kind: store.ConnectionMCPServer, Secret: "fixture-cleartext",
			Config: store.ConnectionConfig{Transport: store.MCPTransportHTTP, URL: "http://mcp.example.test/"}},
		{Name: "local", Kind: store.ConnectionMCPServer, Secret: "fixture-local",
			Config: store.ConnectionConfig{Transport: store.MCPTransportHTTP, URL: "http://127.0.0.1:8931/mcp"}},
	})
	want := []claude.MCPServerConfig{
		{Name: "tracker", Transport: "http", URL: "https://mcp.example.test/",
			Headers: map[string]string{"Authorization": "Bearer fixture-tracker"}},
		{Name: "handbook", Transport: "stdio", Command: "handbook-mcp", Env: map[string]string{"HANDBOOK_KEY": "fixture-handbook"}},
		{Name: "open-docs", Transport: "http", URL: "https://docs.example.test/"},
		{Name: "legacy", Transport: "stdio", Command: "legacy-mcp"},
		{Name: "cleartext", Transport: "http", URL: "http://mcp.example.test/"},
		{Name: "local", Transport: "http", URL: "http://127.0.0.1:8931/mcp",
			Headers: map[string]string{"Authorization": "Bearer fixture-local"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("mcpServers =\n%+v\nwant\n%+v", got, want)
	}
}

// A loop's own egress entries are the hosts its attached http MCP servers
// live on, with the port when it isn't a web one. A loopback server and a
// stdio one open nothing (#599).
func TestEgressAllowFollowsHTTPServers(t *testing.T) {
	server := func(transport, url string) *store.Connection {
		return &store.Connection{Kind: store.ConnectionMCPServer, Config: store.ConnectionConfig{Transport: transport, URL: url, Command: "c"}}
	}
	got := egressAllow([]*store.Connection{
		{Kind: store.ConnectionEnvVar, Config: store.ConnectionConfig{Env: "GH_TOKEN"}, Secret: "g"},
		server(store.MCPTransportHTTP, "https://mcp.tracker.example/mcp"),
		server(store.MCPTransportHTTP, "https://mcp.handbook.example:8443/mcp"),
		server(store.MCPTransportHTTP, "http://plain.example:80/mcp"),
		server(store.MCPTransportHTTP, "http://localhost:8931/mcp"),
		server(store.MCPTransportHTTP, "http://127.0.0.1:8931/mcp"),
		server(store.MCPTransportStdio, ""),
	})
	want := []string{"mcp.tracker.example", "mcp.handbook.example:8443", "plain.example"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("egressAllow = %v, want %v", got, want)
	}
}
