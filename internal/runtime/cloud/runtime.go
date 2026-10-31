// Package cloud is the cloud runtime (ADR-0059): a loop whose claude runs
// in a Claude Code cloud session on Anthropic's machines, driven through
// the claude CLI on documented surfaces. `claude --cloud` under a
// pseudo-terminal starts a session from the loop's GitHub repo in its own
// environment (ADR-0061), `claude -p --cloud` sends each wake into it,
// and the hooks the environment's setup script writes post the session's
// id and each turn back to the cloud listener.
//
// What the hooks post is untrusted: it carries text the loop wrote, and
// every post is read under a size bound.
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
	"time"

	"github.com/enes-alatas/spool/internal/claude"
	"github.com/enes-alatas/spool/internal/runtime"
)

// Account is the claude.ai account whose plan cloud loops spend: the
// OAuth access token of its sign-in, and the organization it acts in.
// Never logged.
type Account struct {
	Token   string
	OrgUUID string
}

// ErrNoAccount is what a run returns when the hub holds no signed-in
// account, which is every hub until the operator signs in (ADR-0056).
var ErrNoAccount = errors.New("cloud: no Claude account is signed in")

// Runtime is the cloud runtime: each loop's claude runs in a cloud session
// on Anthropic's machines. A wake sends into the session, and a sleep does
// nothing: Anthropic idles the machine and the next message brings it back
// (ADR-0059).
//
// It is hub-free, as the seam is: what it keeps of each loop, its session
// and a hash of the source the session started from, is a file of
// its own under Dir.
type Runtime struct {
	// CLI is the claude CLI every session runs through.
	CLI *CLI
	// Account returns the signed-in account of the Claude login called
	// login, "" for the default, or ErrNoAccount. Read at every CLI run,
	// so a sign-in or a removal takes effect at once.
	Account func(ctx context.Context, login string) (Account, error)
	// APIBase is Anthropic's API, which Environments lists from;
	// DefaultAPIBase when empty. Tests point it at a stand-in.
	APIBase string
	// Dir holds one state file per cloud loop. Its files carry no
	// credential.
	Dir string
	// Bounds are a turn's read-back bounds; zero fields are the defaults.
	Bounds Bounds
	// HandOff, when set, is told of each SessionStart post the runtime
	// answered with a loop's connections, and of each it refused from the
	// session the loop recorded: that session may have lost them, which must
	// not go unsaid (ADR-0059).
	HandOff func(loopID string, answered bool)
	// Unreached, when set, is told of a start whose session never reached
	// the hub: the CLI exited cleanly, so the session was created, and no
	// SessionStart post of its session came within the start's bound,
	// answered or refused. The session posts to the public address, so
	// this is its word that it can't reach the hub there (ADR-0056).
	Unreached func(loopID string, within time.Duration)

	mu sync.Mutex // guards every state file
	// waits are the hook posts sessions are waiting on.
	waits hookWaits
}

var _ runtime.Runtime = (*Runtime)(nil)

// state is what the runtime keeps of one loop, as the file under Dir.
type state struct {
	// SessionID is the cloud session the loop's current hub session runs
	// in, and ClaudeSession that hub session's id: a fresh hub session
	// gets a cloud session of its own.
	SessionID     string `json:"session_id,omitempty"`
	ClaudeSession string `json:"claude_session,omitempty"`
	// SessionURL is where SessionID is read on claude.ai.
	SessionURL string `json:"session_url,omitempty"`
	// Source is sourceKey of the repo, branch and environment SessionID
	// started from: a session started from another needs one of its own.
	// A state written before ADR-0061 has none, so its session isn't
	// resumed: it held a token of its own, which the hub no longer keeps.
	Source string `json:"source,omitempty"`
	// EnvironmentID is set only on a state the sessions client wrote
	// (ADR-0056). Its session runs in an environment of the loop's own,
	// with a token the CLI never handed it, so it is never resumed.
	EnvironmentID string `json:"environment_id,omitempty"`
	// Turn is the turn that may be running in the loop's session, nil
	// when the hub knows of none (ADR-0059).
	Turn *turnState `json:"turn,omitempty"`
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

// Preflight has nothing to check at boot: the CLI is looked for at each
// run, as the sign-in does (ADR-0058), and the account is read at each
// run too.
func (cloud *Runtime) Preflight(context.Context) (string, error) { return "", nil }

// startFrom is the loop's source as the spec names it. A loop whose repo,
// branch or environment isn't set has none, and starts no session.
func startFrom(spec runtime.Spec) (runtime.CloudStart, error) {
	source := spec.Cloud
	if source.Repo == "" || source.Branch == "" || source.EnvironmentID == "" {
		return source, errors.New("cloud: the loop's repo, branch and environment aren't all set")
	}
	return source, nil
}

// sourceKey is what a state file keeps of the source a session started
// from: a session started from another repo, branch or environment, or
// under another login's account, is not resumed. The default login adds
// nothing, so a key kept before logins were named still matches.
func sourceKey(source runtime.CloudStart) string {
	key := source.Repo + "\n" + source.Branch + "\n" + source.EnvironmentID
	if source.Login != "" {
		key += "\n" + source.Login
	}
	return tokenHash(key)
}

// shellQuote quotes value as one word for sh.
func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

// Ensure checks the loop has a source to start from. There is nothing of
// the loop's own to build: its session starts from its repo, in its
// environment (ADR-0061).
func (cloud *Runtime) Ensure(_ context.Context, spec runtime.Spec) error {
	_, err := startFrom(spec)
	return err
}

// Start hands back the loop's session as a Proc. A fresh hub session gets
// a cloud session of its own, started with its first message; a resumed
// one carries on in the cloud session it ran in. A resume of a session
// this runtime holds no cloud session for ends at once as the CLI's own
// does, so the runner starts a fresh one. So does a resume of a session
// started from another source than the loop's now, and of a session the
// sessions client created.
func (cloud *Runtime) Start(_ context.Context, spec runtime.Spec) (runtime.Proc, error) {
	source, err := startFrom(spec)
	if err != nil {
		return nil, err
	}
	current, err := cloud.load(spec.LoopID)
	if err != nil {
		return nil, err
	}
	session := &cliSession{
		cloud:  cloud,
		loopID: spec.LoopID,
		prompt: spec.AppendSystemPrompt,
		source: source,
		bounds: cloud.Bounds.withDefaults(),
		env:    spec.Env,
	}
	cloud.waits.setPrompt(spec.LoopID, spec.AppendSystemPrompt)
	switch {
	case spec.SessionID != "":
		session.claudeSession = spec.SessionID
	case spec.ResumeID == current.ClaudeSession && current.SessionID != "" &&
		current.Source == sourceKey(source) && current.EnvironmentID == "":
		session.claudeSession = spec.ResumeID
		session.sessionID = current.SessionID
	default:
		session.claudeSession = spec.ResumeID
		session.lost = true
	}
	session.start()
	return session, nil
}

// Reap has nothing to clean up: a cloud session is no process of the
// hub's.
func (cloud *Runtime) Reap(context.Context, string, int) error { return nil }

// Halt is unsupported: the machine is Anthropic's, which idles it itself.
func (cloud *Runtime) Halt(context.Context, string) error { return runtime.ErrUnsupported }

// Destroy forgets the loop's session. The CLI can't archive one, so it is
// left for the operator to archive on claude.ai. The loop's hub token is
// not this runtime's to revoke: the hub deletes it with the loop before
// Destroy runs.
func (cloud *Runtime) Destroy(_ context.Context, loopID string) error {
	cloud.mu.Lock()
	defer cloud.mu.Unlock()
	if err := os.Remove(cloud.path(loopID)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// sessionPages is where claude.ai shows a cloud session. The session id
// comes from a hook post, which is untrusted, so a link anywhere else is
// never served.
const sessionPages = "https://claude.ai/code/"

// SessionURL is the claude.ai page of the loop's current cloud session,
// "" while it has none.
func (cloud *Runtime) SessionURL(loopID string) string {
	current, err := cloud.load(loopID)
	if err != nil || !strings.HasPrefix(current.SessionURL, sessionPages) {
		return ""
	}
	return current.SessionURL
}

// HasWorkstation is false: there is nothing of the loop's own to halt or
// rebuild, so the power controls have nothing to act on.
func (cloud *Runtime) HasWorkstation() bool { return false }

// Health is always up: the CLI can't read a session's status, and a loop
// with no session yet is up too, since its first message starts one.
func (cloud *Runtime) Health(context.Context, string) (runtime.Health, error) {
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

// tokenHash is what a state file keeps of a hub token: enough to tell a
// replaced one, and no credential.
func tokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
