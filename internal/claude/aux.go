package claude

// An auxiliary run is the CLI started for something other than a loop's
// turns: its version at preflight, or what a model alias resolves to
// (ADR-0033). It holds no credential and reaches no API.

// auxAPIKey gets the CLI past its login check and authenticates nothing.
// It is not an API key in ADR-0001's sense, and it names itself so that no
// one mistakes it for one.
const auxAPIKey = "not-a-key"

// AuxEnv is an auxiliary run's whole environment, built from nothing so that
// nothing the hub was started with (a Claude token, an API key, a loop's
// secrets) reaches it: path to find programs, home as both HOME and the
// parent of an empty config dir the operator's login is not in, and baseURL
// as the only API the CLI is told of. An empty path leaves PATH to the
// image, for a run inside a workstation; an empty baseURL leaves it unset,
// for a run that makes no request at all.
func AuxEnv(path, home, baseURL string) []string {
	var env []string
	if path != "" {
		env = append(env, "PATH="+path)
	}
	env = append(env,
		"HOME="+home,
		"CLAUDE_CONFIG_DIR="+home+"/config",
		"ANTHROPIC_API_KEY="+auxAPIKey,
	)
	if baseURL != "" {
		env = append(env, "ANTHROPIC_BASE_URL="+baseURL)
	}
	return env
}
