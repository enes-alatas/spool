package loop

import (
	"strings"

	"github.com/enes-alatas/spool/internal/githubhelper"
	"github.com/enes-alatas/spool/internal/store"
)

// GitHubTokenPath is where the loop listener serves a fresh installation
// token to a loop that holds a github-app connection (#887).
const GitHubTokenPath = "/github/token"

// GitHubHelper is spool-github as one runtime's workstation runs it
// (#1054): git's credential helper for github.com, and the gh link that
// sets GH_TOKEN, both asking the hub's token route.
type GitHubHelper struct {
	// Path is the helper inside the workstation.
	Path string
	// PATH, when set, is the PATH the loop runs with: the directory of the
	// helper's gh link ahead of the hub's own PATH, which a bare loop
	// inherits. An image carries its link ahead of the real gh already.
	PATH string
}

// githubEnv adds to a wake's system env what a loop holding a github-app
// needs for its git and gh to fetch their token from the hub (#1054): the
// route and the loop's own hub token, and the helper registered for
// https://github.com through git's env config, so the loop's own gitconfig
// is untouched. The empty helper first clears any configured before it, so
// git asks this one. A loop holding no app, or on a runtime with no helper,
// gets none of it.
func (actor *Actor) githubEnv(system map[string]string, kind string, connections []*store.Connection) {
	helper, ok := actor.deps.GitHubHelpers[kind]
	if !ok || !holdsGitHubApp(connections) {
		return
	}
	url := actor.loopListenerURL(GitHubTokenPath)
	if url == "" {
		return
	}
	system[githubhelper.EnvTokenURL] = url
	system[githubhelper.EnvHubToken] = actor.loop.HubMCPToken
	key := "credential.https://" + githubhelper.Host + ".helper"
	system["GIT_CONFIG_COUNT"] = "2"
	system["GIT_CONFIG_KEY_0"], system["GIT_CONFIG_VALUE_0"] = key, ""
	system["GIT_CONFIG_KEY_1"], system["GIT_CONFIG_VALUE_1"] = key, "!"+shellWord(helper.Path)
	if helper.PATH != "" {
		system["PATH"] = helper.PATH
	}
}

// ShadowsGitHubEnv reports whether an env-var on name would undo what
// githubEnv sets, since an attached env-var wins a clash with the system
// env (#1060): the route and hub token, git's env config, or PATH, which
// carries a bare loop's gh link. GIT_CONFIG_PARAMETERS too: githubEnv
// doesn't set it, but git reads it after the GIT_CONFIG_COUNT pairs, so an
// empty helper there clears the hub's. Beside a github-app any of them
// would leave git or gh unauthenticated with nothing pointing at why.
func ShadowsGitHubEnv(name string) bool {
	switch name {
	case githubhelper.EnvTokenURL, githubhelper.EnvHubToken, "GIT_CONFIG_COUNT", "GIT_CONFIG_PARAMETERS", "PATH":
		return true
	}
	for _, prefix := range []string{"GIT_CONFIG_KEY_", "GIT_CONFIG_VALUE_"} {
		if index, ok := strings.CutPrefix(name, prefix); ok && index != "" && strings.Trim(index, "0123456789") == "" {
			return true
		}
	}
	return false
}

func holdsGitHubApp(connections []*store.Connection) bool {
	for _, connection := range connections {
		if connection.Kind == store.ConnectionGitHubApp && connection.RevokedAt == 0 {
			return true
		}
	}
	return false
}

// shellWord quotes value as one word for a shell: git runs a helper given
// as "!…" through one.
func shellWord(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}
