package claude

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

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

// ResolveArgs is the argument list of a run that resolves model: a fresh
// throwaway session, which the run's empty config dir takes with it.
func ResolveArgs(model string) ([]string, error) {
	id := make([]byte, 16)
	if _, err := rand.Read(id); err != nil {
		return nil, err
	}
	id[6] = id[6]&0x0f | 0x40 // a version-4 uuid, which --session-id requires
	id[8] = id[8]&0x3f | 0x80
	h := hex.EncodeToString(id)
	session := h[:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
	return Args(Opts{Model: model, SessionID: session})
}

// ResolvedModel sends the run one message and returns the model its init
// event reports: the CLI resolves an alias locally and names the result
// there, before it needs any answer from the API (ADR-0033). Nothing after
// init is read. It fails when the run ends, or ctx does, without an init
// that names a model.
func ResolvedModel(ctx context.Context, stream *Stream) (string, error) {
	if err := stream.Send("resolve"); err != nil {
		return "", fmt.Errorf("claude: resolve: %w", err)
	}
	for {
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("claude: resolve: no init: %w", ctx.Err())
		case ev, ok := <-stream.Events():
			if !ok {
				return "", fmt.Errorf("claude: resolve: exited without an init (stderr: %s)", tailOf(stream.StderrTail(), 300))
			}
			if ev.Init == nil {
				continue
			}
			if ev.Init.Model == "" {
				return "", fmt.Errorf("claude: resolve: init named no model")
			}
			return ev.Init.Model, nil
		}
	}
}

func tailOf(s string, n int) string {
	if len(s) > n {
		return s[len(s)-n:]
	}
	return s
}
