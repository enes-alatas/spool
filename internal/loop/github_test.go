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

// Every variable githubEnv sets is one an env-var beside a github-app is
// refused (#1060), so a name added to one can't be missed in the other.
// So is GIT_CONFIG_PARAMETERS, which git reads after the counted pairs.
// Other names stay attachable.
func TestShadowsGitHubEnvCoversEverythingGitHubEnvSets(t *testing.T) {
	actor := &Actor{
		deps: Deps{
			MCPEndpoint:   func(*store.Loop) string { return "http://127.0.0.1:7777/mcp" },
			GitHubHelpers: map[string]GitHubHelper{store.RuntimeBare: {Path: "/bin/spool-github", PATH: "/data/github-bin:/usr/bin"}},
		},
		loop: store.Loop{HubMCPToken: "spool_fixtureHUB"},
	}
	env := map[string]string{}
	actor.githubEnv(env, store.RuntimeBare, []*store.Connection{{Kind: store.ConnectionGitHubApp}})
	if len(env) == 0 {
		t.Fatal("githubEnv set nothing")
	}
	for name := range env {
		if !ShadowsGitHubEnv(name) {
			t.Errorf("githubEnv sets %s, which ShadowsGitHubEnv lets an env-var replace", name)
		}
	}
	if !ShadowsGitHubEnv("GIT_CONFIG_PARAMETERS") {
		t.Error("an env-var on GIT_CONFIG_PARAMETERS, which git reads after the COUNT pairs, is let through")
	}
	for _, name := range []string{"GH_TOKEN", "GIT_CONFIG_KEY_", "GIT_CONFIG_KEY_X", "GIT_CONFIG_GLOBAL", "PATHS", "HOME"} {
		if ShadowsGitHubEnv(name) {
			t.Errorf("ShadowsGitHubEnv(%q) = true, want false", name)
		}
	}
}
