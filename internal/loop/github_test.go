package loop

import (
	"maps"
	"testing"

	"github.com/enes-alatas/spool/internal/store"
)

// A loop holding a github-app on a runtime with a helper is handed the
// token route, its hub token and git's helper, quoted for the shell git
// runs it through, after an empty helper that clears any configured
// earlier; PATH only where the runtime gives one. A loop holding none, one
// holding a revoked app, and one on a runtime with no helper get nothing.
func TestGitHubEnvHandsALoopHoldingAnAppItsHelper(t *testing.T) {
	actor := &Actor{
		deps: Deps{
			MCPEndpoint: func(*store.Loop) string { return "http://host.docker.internal:7777/mcp" },
			GitHubHelpers: map[string]GitHubHelper{
				store.RuntimeDocker: {Path: "/usr/local/bin/spool-github"},
				store.RuntimeBare:   {Path: "/opt/spool's bin/spool-github", PATH: "/data/github-bin:/usr/bin"},
			},
		},
		loop: store.Loop{HubMCPToken: "spool_fixtureHUB"},
	}
	app := []*store.Connection{{Kind: store.ConnectionGitHubApp}}

	docker := map[string]string{}
	actor.githubEnv(docker, store.RuntimeDocker, app)
	want := map[string]string{
		"SPOOL_GITHUB_TOKEN_URL": "http://host.docker.internal:7777/github/token",
		"SPOOL_HUB_TOKEN":        "spool_fixtureHUB",
		"GIT_CONFIG_COUNT":       "2",
		"GIT_CONFIG_KEY_0":       "credential.https://github.com.helper",
		"GIT_CONFIG_VALUE_0":     "",
		"GIT_CONFIG_KEY_1":       "credential.https://github.com.helper",
		"GIT_CONFIG_VALUE_1":     "!'/usr/local/bin/spool-github'",
	}
	if !maps.Equal(docker, want) {
		t.Errorf("a docker loop's env: %v\nwant %v", docker, want)
	}

	bare := map[string]string{}
	actor.githubEnv(bare, store.RuntimeBare, app)
	if bare["PATH"] != "/data/github-bin:/usr/bin" || bare["GIT_CONFIG_VALUE_1"] != `!'/opt/spool'\''s bin/spool-github'` {
		t.Errorf("a bare loop's env: %v; want the link's PATH and the helper quoted", bare)
	}

	for name, row := range map[string]struct {
		kind        string
		connections []*store.Connection
	}{
		"no app":          {store.RuntimeDocker, []*store.Connection{{Kind: store.ConnectionEnvVar}}},
		"a revoked app":   {store.RuntimeDocker, []*store.Connection{{Kind: store.ConnectionGitHubApp, RevokedAt: 1}}},
		"a cloud runtime": {store.RuntimeCloud, app},
	} {
		env := map[string]string{}
		actor.githubEnv(env, row.kind, row.connections)
		if len(env) != 0 {
			t.Errorf("%s: handed %v, want nothing", name, env)
		}
	}
}
