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
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/enes-alatas/spool/internal/claude"
	"github.com/enes-alatas/spool/internal/runtime"
)

// Account is the claude.ai account whose plan cloud loops spend: the
// OAuth access token of its sign-in, the organization it acts in, and the
// account's own ID, "" for a sign-in kept before the hub read it. Never
// logged.
type Account struct {
	Token       string
	OrgUUID     string
	AccountUUID string
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
	// HubKey keys what a state file keeps of a session's connections, so a
	// backup of the state files alone tests no guess at their values (#1293).
	// Without one no session is carried on; a replaced hub key only means
	// the next start makes a new one.
	HubKey []byte
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
	// Misplaced, when set, is told of a start the runtime failed because
	// the CLI picked an environment other than the loop's, with what to
	// say: every later start would do the same, so the loop must hold
	// until its setup check passes again (#1232).
	Misplaced func(loopID, detail string)

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
	// Connections is connectionsKey of the connections SessionID was
	// handed at its start. A state written before #1278 has none, so its
	// session isn't carried on: the hub can't tell what its shell holds.
	Connections string `json:"connections,omitempty"`
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
// from, under the account whose ID is account: a session started from
// another repo, branch or environment, under another login, or under
// another account the login signed in as since (#1230), is not resumed.
// The default login adds nothing, and nor does a sign-in kept before the
// hub read its account's ID, so a key kept before either still matches.
func sourceKey(source runtime.CloudStart, account string) string {
	key := source.Repo + "\n" + source.Branch + "\n" + source.EnvironmentID
	if source.Login != "" {
		key += "\n" + source.Login
	}
	if account != "" {
		key += "\naccount " + account
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
// started from another source than the loop's now, or under another
// account of its login's, and of a session the sessions client created.
//
// A cloud session is carried on only as it was left (#1278): its last
// turn's Stop post came, and the loop's connections are the ones it was
// handed at its start. A resume of one that isn't ends as lost, and says
// why. A fresh hub session that succeeds one rotated by choice carries on
// in the cloud session the last one ran in when it can, cleared first
// with /clear, so a rotation leaves no session behind on claude.ai.
func (cloud *Runtime) Start(ctx context.Context, spec runtime.Spec) (runtime.Proc, error) {
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
	resuming := spec.SessionID == "" && spec.ResumeID == current.ClaudeSession
	var account, unkept string
	if resuming || spec.Rotated {
		account, unkept, err = cloud.carriedOn(ctx, current, source, spec.Env)
		if err != nil {
			return nil, err
		}
	}
	cloud.waits.setPrompt(spec.LoopID, spec.AppendSystemPrompt)
	switch {
	case spec.SessionID != "" && spec.Rotated && unkept == "":
		session.claudeSession = spec.SessionID
		session.sessionID = current.SessionID
		session.account = account
		session.clearFirst = true
	case spec.SessionID != "":
		session.claudeSession = spec.SessionID
		if spec.Rotated {
			slog.Default().Info("cloud: a rotation starts a session of its own", "loop", spec.LoopID, "why", unkept)
		}
	case resuming && unkept == "":
		session.claudeSession = spec.ResumeID
		session.sessionID = current.SessionID
		session.account = account
	default:
		session.claudeSession = spec.ResumeID
		session.lost = true
		session.lostWhy = unkept
	}
	session.start()
	return session, nil
}

// carriedOn says whether the loop's cloud session in current can be
// carried on for a start from source with the connections env: unkept is
// why not, "" when it can, and account the account it runs under then.
func (cloud *Runtime) carriedOn(ctx context.Context, current state, source runtime.CloudStart, env map[string]string) (account, unkept string, err error) {
	switch {
	case current.SessionID == "":
		return "", "the loop holds no cloud session", nil
	case current.EnvironmentID != "":
		return "", "its session was made by the sessions client", nil
	case current.Turn != nil && current.Turn.SessionURL == current.SessionURL:
		// a turn the hub didn't hear end may have changed the session
		// out of its sight
		return "", "its session's last turn was cut off before its Stop post came", nil
	case current.Connections == "" || current.Connections != cloud.connectionsKey(env):
		// a connection detached since the session started is still
		// exported in its shell
		return "", "the loop's connections changed since its session started", nil
	}
	// a login signed in again as another account doesn't own the session
	// its last one started (#1230)
	signedIn, err := cloud.Account(ctx, source.Login)
	if err != nil {
		return "", "", err
	}
	if current.Source != sourceKey(source, signedIn.AccountUUID) {
		return "", "its session started from another repo, branch, environment or account", nil
	}
	return signedIn.AccountUUID, "", nil
}

// connectionsKey is what a state file keeps of the connections a session
// was handed: a MAC of their export lines under a key derived from the
// hub key, enough to tell a changed set, and no credential nor anything
// a guess at one can be tested against offline. "" without a hub key,
// which carries no session on.
func (cloud *Runtime) connectionsKey(env map[string]string) string {
	if len(cloud.HubKey) == 0 {
		return ""
	}
	derive := hmac.New(sha256.New, cloud.HubKey)
	derive.Write([]byte("spool cloud connections"))
	mac := hmac.New(sha256.New, derive.Sum(nil))
	mac.Write(exportLines(env))
	return hex.EncodeToString(mac.Sum(nil))
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
