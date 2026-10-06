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

// An attached mcp-server's secret never reaches the loop when the hub can
// broker it: every http server a workstation's hub can reach is named at
// the hub, under the loop's own hub MCP token, whatever its secret (#622).
// A loopback http server, inside the workstation, gets its secret as a
// bearer token, and a stdio server in the env
// var its config names. An env-var is no server, and a stdio server with no
// env var named is given no secret (ADR-0043).
func TestMCPServersCarryTheirSecrets(t *testing.T) {
	const hub, token = "http://host.docker.internal:7781/mcp", "fixture-hub-token"
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
		{Name: "any4", Kind: store.ConnectionMCPServer, Secret: "fixture-any4",
			Config: store.ConnectionConfig{Transport: store.MCPTransportHTTP, URL: "http://0.0.0.0:8931/mcp"}},
		{Name: "any6", Kind: store.ConnectionMCPServer, Secret: "fixture-any6",
			Config: store.ConnectionConfig{Transport: store.MCPTransportHTTP, URL: "http://[::]:8931/mcp"}},
	}, store.RuntimeDocker, hub, token)
	atHub := map[string]string{"Authorization": "Bearer " + token}
	want := []claude.MCPServerConfig{
		{Name: "tracker", Transport: "http", URL: "http://host.docker.internal:7781/mcp/connections/tracker", Headers: atHub},
		{Name: "handbook", Transport: "stdio", Command: "handbook-mcp", Env: map[string]string{"HANDBOOK_KEY": "fixture-handbook"}},
		{Name: "open-docs", Transport: "http", URL: "http://host.docker.internal:7781/mcp/connections/open-docs", Headers: atHub},
		{Name: "legacy", Transport: "stdio", Command: "legacy-mcp"},
		{Name: "cleartext", Transport: "http", URL: "http://host.docker.internal:7781/mcp/connections/cleartext", Headers: atHub},
		{Name: "local", Transport: "http", URL: "http://127.0.0.1:8931/mcp",
			Headers: map[string]string{"Authorization": "Bearer fixture-local"}},
		{Name: "any4", Transport: "http", URL: "http://0.0.0.0:8931/mcp",
			Headers: map[string]string{"Authorization": "Bearer fixture-any4"}},
		{Name: "any6", Transport: "http", URL: "http://[::]:8931/mcp",
			Headers: map[string]string{"Authorization": "Bearer fixture-any6"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("mcpServers =\n%+v\nwant\n%+v", got, want)
	}
}

// A bare loop shares the hub's machine, so the hub brokers its loopback
// http server too, however the URL names this machine (#622).
func TestABareLoopsLoopbackServerIsBrokered(t *testing.T) {
	var connections []*store.Connection
	var want []claude.MCPServerConfig
	for name, url := range map[string]string{"local": "http://127.0.0.1:8931/mcp", "any4": "http://0.0.0.0:8931/mcp", "any6": "http://[::]:8931/mcp"} {
		connections = append(connections, &store.Connection{Name: name, Kind: store.ConnectionMCPServer, Secret: "fixture-" + name,
			Config: store.ConnectionConfig{Transport: store.MCPTransportHTTP, URL: url}})
		want = append(want, claude.MCPServerConfig{Name: name, Transport: "http", URL: "http://127.0.0.1:7781/mcp/connections/" + name,
			Headers: map[string]string{"Authorization": "Bearer fixture-hub-token"}})
	}
	got := mcpServers(connections, store.RuntimeBare, "http://127.0.0.1:7781/mcp", "fixture-hub-token")
	if !reflect.DeepEqual(got, want) {
		t.Errorf("mcpServers =\n%+v\nwant\n%+v", got, want)
	}
}
