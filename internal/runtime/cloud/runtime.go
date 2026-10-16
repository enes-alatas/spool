package cloud

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/enes-alatas/spool/internal/claude"
	"github.com/enes-alatas/spool/internal/runtime"
)

// HubTokenVar is the session environment variable that carries the loop's
// hub MCP token, which the environment's setup script names in the header
// it registers the hub's MCP server with (ADR-0056).
const HubTokenVar = "SPOOL_HUB_TOKEN"

// Runtime is the cloud runtime: each loop's claude runs in a cloud session
// in an environment of the loop's own, on Anthropic's machines. A wake
// sends into the session, and a sleep does nothing: Anthropic idles the
// machine and the next message brings it back (ADR-0056).
//
// It is hub-free, as the seam is: what it keeps of each loop, its
// environment, its session and how far its events have been read, is a
// file of its own under Dir.
type Runtime struct {
	Client *Client
	// Dir holds one state file per cloud loop. Its files carry no
	// credential.
	Dir string
	// AllowedHosts is the network allowlist each environment's sessions
	// are held to: the hub's own egress allowlist, as far as it translates,
	// and the host the loop reaches the hub at. Read at every wake, so an
	// edit reaches each environment by the loop's next one.
	AllowedHosts func(ctx context.Context) ([]string, error)
	// Bounds are a turn's read-back bounds; zero fields are the defaults.
	Bounds Bounds

	mu sync.Mutex // guards every state file
}

var _ runtime.Runtime = (*Runtime)(nil)

// state is what the runtime keeps of one loop, as the file under Dir.
type state struct {
	EnvironmentID string `json:"environment_id,omitempty"`
	// EnvironmentHash is the hash of what the environment was last written
	// with, so a wake writes it again only when that changed.
	EnvironmentHash string `json:"environment_hash,omitempty"`
	// SessionID is the cloud session the loop's current hub session runs
	// in, and ClaudeSession that hub session's id: a fresh hub session
	// gets a cloud session of its own.
	SessionID     string `json:"session_id,omitempty"`
	ClaudeSession string `json:"claude_session,omitempty"`
	// AfterID is the newest event read of the session.
	AfterID string `json:"after_id,omitempty"`
}

func (cloud *Runtime) path(loopID string) string {
	return filepath.Join(cloud.Dir, loopID+".json")
}

func (cloud *Runtime) load(loopID string) (state, error) {
	cloud.mu.Lock()
	defer cloud.mu.Unlock()
	return cloud.loadLocked(loopID)
}

func (cloud *Runtime) loadLocked(loopID string) (state, error) {
	var loaded state
	raw, err := os.ReadFile(cloud.path(loopID))
	if errors.Is(err, os.ErrNotExist) {
		return loaded, nil
	}
	if err != nil {
		return loaded, err
	}
	if err := json.Unmarshal(raw, &loaded); err != nil {
		return loaded, fmt.Errorf("cloud: state of loop %s: %w", loopID, err)
	}
	return loaded, nil
}

// update reads the loop's state, applies change and writes it back whole,
// through a rename so a crash leaves the old file or the new one.
func (cloud *Runtime) update(loopID string, change func(*state)) error {
	cloud.mu.Lock()
	defer cloud.mu.Unlock()
	current, err := cloud.loadLocked(loopID)
	if err != nil {
		return err
	}
	change(&current)
	raw, err := json.Marshal(current)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(cloud.Dir, 0o700); err != nil {
		return err
	}
	scratch, err := os.CreateTemp(cloud.Dir, loopID+".*.tmp")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(scratch.Name()) }() // gone already once renamed
	if _, err := scratch.Write(raw); err != nil {
		_ = scratch.Close()
		return err
	}
	if err := scratch.Close(); err != nil {
		return err
	}
	return os.Rename(scratch.Name(), cloud.path(loopID))
}

func (cloud *Runtime) Kind() string { return "cloud" }

// Preflight has nothing to check at boot: the runtime runs no binary of
// the operator's, and the account is read at each call.
func (cloud *Runtime) Preflight(context.Context) (string, error) { return "", nil }

// hubEndpoint is the hub's MCP server as the loop's spec names it: the
// address it reaches the hub at, and its token.
type hubEndpoint struct{ url, token string }

// hubFrom reads the hub's MCP server out of the spec's MCP config. A cloud
// session is created with an empty mcp_config, so of every server there,
// the hub's is the one that reaches it, through the setup script.
func hubFrom(spec runtime.Spec) (hubEndpoint, error) {
	var config struct {
		MCPServers map[string]struct {
			URL     string            `json:"url"`
			Headers map[string]string `json:"headers"`
		} `json:"mcpServers"`
	}
	if spec.MCPConfig == "" {
		return hubEndpoint{}, errors.New("cloud: the hub has no address a cloud session can reach it at")
	}
	if err := json.Unmarshal([]byte(spec.MCPConfig), &config); err != nil {
		return hubEndpoint{}, fmt.Errorf("cloud: reading the loop's MCP config: %w", err)
	}
	server, ok := config.MCPServers[claude.SpoolMCPServer]
	token, bearer := strings.CutPrefix(server.Headers["Authorization"], "Bearer ")
	if !ok || server.URL == "" || !bearer || token == "" {
		return hubEndpoint{}, errors.New("cloud: the loop's MCP config names no hub server")
	}
	return hubEndpoint{url: server.URL, token: token}, nil
}

// setupScript registers the hub's MCP server at user scope, for both users
// a session may run its claude as. The header names the token's variable
// and never its value: claude expands it from the session's environment
// (ADR-0056).
func setupScript(hubURL string) string {
	return "#!/bin/bash\n" +
		"for h in /root /home/user; do\n" +
		"  [ -d \"$h\" ] || continue\n" +
		"  HOME=\"$h\" claude mcp add --transport http --scope user " + claude.SpoolMCPServer + " " + shellQuote(hubURL) +
		" --header 'Authorization: Bearer ${" + HubTokenVar + "}' || exit 1\n" +
		"done\n" +
		"chown -R user:user /home/user/.claude* 2>/dev/null || true\n"
}

// shellQuote quotes value as one word for bash.
func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

func (cloud *Runtime) environmentFor(ctx context.Context, spec runtime.Spec, hub hubEndpoint) (EnvironmentSpec, error) {
	var hosts []string
	if cloud.AllowedHosts != nil {
		var err error
		if hosts, err = cloud.AllowedHosts(ctx); err != nil {
			return EnvironmentSpec{}, fmt.Errorf("cloud: reading the egress allowlist: %w", err)
		}
	}
	return EnvironmentSpec{
		Name:         "spool: " + spec.LoopName,
		Description:  "The Spool loop " + spec.LoopName + "; kept by its hub.",
		InitScript:   setupScript(hub.url),
		AllowedHosts: hosts,
	}, nil
}

func environmentHash(spec EnvironmentSpec) string {
	raw, _ := json.Marshal(spec.body())
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// Ensure makes the loop's environment exist, and writes it again when what
// it would be written with changed since: the hub's address or its
// allowlist.
func (cloud *Runtime) Ensure(ctx context.Context, spec runtime.Spec) error {
	hub, err := hubFrom(spec)
	if err != nil {
		return err
	}
	environment, err := cloud.environmentFor(ctx, spec, hub)
	if err != nil {
		return err
	}
	current, err := cloud.load(spec.LoopID)
	if err != nil {
		return err
	}
	hash := environmentHash(environment)
	switch {
	case current.EnvironmentID == "":
		id, err := cloud.Client.CreateEnvironment(ctx, environment)
		if err != nil {
			return err
		}
		return cloud.update(spec.LoopID, func(s *state) { s.EnvironmentID, s.EnvironmentHash = id, hash })
	case current.EnvironmentHash != hash:
		if err := cloud.Client.UpdateEnvironment(ctx, current.EnvironmentID, environment); err != nil {
			return err
		}
		return cloud.update(spec.LoopID, func(s *state) { s.EnvironmentHash = hash })
	}
	return nil
}

// Start hands back the loop's session as a Proc. A fresh hub session gets
// a cloud session of its own, created with its first message; a resumed one
// carries on in the cloud session it ran in. A resume of a session this
// runtime holds no cloud session for ends at once as the CLI's own does,
// so the runner starts a fresh one.
func (cloud *Runtime) Start(ctx context.Context, spec runtime.Spec) (runtime.Proc, error) {
	hub, err := hubFrom(spec)
	if err != nil {
		return nil, err
	}
	current, err := cloud.load(spec.LoopID)
	if err != nil {
		return nil, err
	}
	if current.EnvironmentID == "" {
		return nil, errors.New("cloud: the loop has no environment")
	}
	session := &proc{
		cloud:         cloud,
		loopID:        spec.LoopID,
		title:         "spool: " + spec.LoopName,
		model:         spec.Model,
		prompt:        spec.AppendSystemPrompt,
		hubToken:      hub.token,
		environmentID: current.EnvironmentID,
		bounds:        cloud.Bounds.withDefaults(),
	}
	switch {
	case spec.SessionID != "":
		session.claudeSession = spec.SessionID
		session.retired = current.SessionID
	case spec.ResumeID == current.ClaudeSession && current.SessionID != "":
		session.claudeSession = spec.ResumeID
		session.sessionID = current.SessionID
		session.afterID, session.keptID = current.AfterID, current.AfterID
	default:
		session.claudeSession = spec.ResumeID
		session.lost = true
	}
	session.start()
	return session, nil
}

// Reap has nothing to clean up: a cloud session is no process of the
// hub's, and the next wake reads its events on from where they were left.
func (cloud *Runtime) Reap(context.Context, string, int) error { return nil }

// Halt is unsupported: the machine is Anthropic's, which idles it itself.
func (cloud *Runtime) Halt(context.Context, string) error { return runtime.ErrUnsupported }

// Destroy archives the loop's session and its environment, and forgets
// both. Neither can be deleted; archived, nothing starts in either again.
// The loop's hub token is not this runtime's to revoke: the hub deletes it
// with the loop, after Destroy returns, until #866 has it revoked first.
func (cloud *Runtime) Destroy(ctx context.Context, loopID string) error {
	current, err := cloud.load(loopID)
	if err != nil {
		return err
	}
	if current.SessionID != "" {
		if err := cloud.Client.ArchiveSession(ctx, current.SessionID); err != nil && !isInactive(err) {
			return err
		}
	}
	if current.EnvironmentID != "" {
		if err := cloud.Client.ArchiveEnvironment(ctx, current.EnvironmentID); err != nil {
			return err
		}
	}
	cloud.mu.Lock()
	defer cloud.mu.Unlock()
	if err := os.Remove(cloud.path(loopID)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// HasWorkstation is false: there is nothing of the loop's own to halt or
// rebuild, so the power controls have nothing to act on.
func (cloud *Runtime) HasWorkstation() bool { return false }

// Health reads the session's status. A loop with no environment yet has no
// workstation at all; one with no session yet is up, since its first
// message creates one.
func (cloud *Runtime) Health(ctx context.Context, loopID string) (runtime.Health, error) {
	current, err := cloud.load(loopID)
	if err != nil {
		return runtime.Health{}, err
	}
	if current.EnvironmentID == "" {
		return runtime.Health{Missing: true}, nil
	}
	if current.SessionID == "" {
		return runtime.Health{Up: true}, nil
	}
	session, err := cloud.Client.GetSession(ctx, current.SessionID)
	if err != nil {
		return runtime.Health{Detail: err.Error()}, nil
	}
	if session.Status == StatusRequiresAction {
		return runtime.Health{Detail: "the cloud session is waiting for an answer the hub can't give"}, nil
	}
	return runtime.Health{Up: true}, nil
}

// PutFile is unsupported at first: a file sent to a cloud loop is recorded
// as not kept (ADR-0056).
func (cloud *Runtime) PutFile(context.Context, string, string, string) error {
	return runtime.ErrUnsupported
}

// GetFile is unsupported at first. It never reads the hub's own disk: a
// cloud loop's files are on Anthropic's machine.
func (cloud *Runtime) GetFile(context.Context, string, string, string, int64) ([]byte, error) {
	return nil, runtime.ErrUnsupported
}

// ResolveModel is unsupported: the runtime has no claude of its own to ask,
// and is never the hub's default.
func (cloud *Runtime) ResolveModel(context.Context, string) (string, error) {
	return "", runtime.ErrUnsupported
}

// CheckLogin is unsupported: a cloud session runs on the account's sign-in,
// never on the setup-token.
func (cloud *Runtime) CheckLogin(context.Context, string) (claude.LoginCheck, error) {
	return claude.LoginCheck{}, runtime.ErrUnsupported
}

func isInactive(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.Inactive()
}
