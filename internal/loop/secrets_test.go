package loop

import (
	"testing"

	"github.com/enes-alatas/spool/internal/store"
)

// TestBuildExecEnv pins how a wake's env is assembled: nothing to inject
// stays nil, system vars and loop secrets merge, and a loop secret wins a name
// collision with a system var — the deliberate operator escape hatch.
func TestBuildExecEnv(t *testing.T) {
	sec := func(name, value string) *store.LoopSecret {
		return &store.LoopSecret{Name: name, Value: value}
	}

	t.Run("empty is nil", func(t *testing.T) {
		if env := buildExecEnv(nil, nil); env != nil {
			t.Fatalf("got %v, want nil so spec.Env stays unset", env)
		}
	})

	t.Run("system and secrets merge", func(t *testing.T) {
		env := buildExecEnv(
			map[string]string{"CLAUDE_CODE_OAUTH_TOKEN": "sk-ant-tok"},
			[]*store.LoopSecret{sec("GH_TOKEN", "ghp_x")},
		)
		if env["CLAUDE_CODE_OAUTH_TOKEN"] != "sk-ant-tok" || env["GH_TOKEN"] != "ghp_x" {
			t.Fatalf("merge dropped a var: %v", env)
		}
	})

	t.Run("secret overrides a colliding system var", func(t *testing.T) {
		env := buildExecEnv(
			map[string]string{"CLAUDE_CODE_OAUTH_TOKEN": "operator-token"},
			[]*store.LoopSecret{sec("CLAUDE_CODE_OAUTH_TOKEN", "loop-token")},
		)
		if env["CLAUDE_CODE_OAUTH_TOKEN"] != "loop-token" {
			t.Fatalf("got %q, want the loop secret to win the collision", env["CLAUDE_CODE_OAUTH_TOKEN"])
		}
	})
}
