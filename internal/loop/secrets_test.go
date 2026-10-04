package loop

import (
	"testing"

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
