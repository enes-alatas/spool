// Package store defines Spool's persistence interfaces and domain types.
// Implementations must stick to portable SQL (SQLite today, Postgres later):
// TEXT uuids, unix-milli INTEGER timestamps, JSON-in-TEXT for lists.
package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// NewHubMCPToken mints the bearer token a loop presents to the hub's own MCP
// endpoint — hub-scoped, unrelated to any MCP connection a loop may attach
// later (ADR-0026). Implementations
// call it from Create when the caller left the token empty, so every loop
// holds one no matter which path created it.
func NewHubMCPToken() string {
	random := make([]byte, 24)
	if _, err := rand.Read(random); err != nil {
		panic("store: crypto/rand unavailable: " + err.Error())
	}
	return hex.EncodeToString(random)
}

const (
	WorkspaceNone     = "none"
	WorkspaceDir      = "dir"
	WorkspaceWorktree = "worktree"

	StatusActive   = "active"
	StatusPaused   = "paused"
	StatusArchived = "archived"

	EndReasonIdle    = "idle"
	EndReasonKilled  = "killed"
	EndReasonCrash   = "crash"
	EndReasonLost    = "lost"
	EndReasonRotated = "rotated" // retired by a deliberate context rotation (ADR-0022)
	EndReasonPaused  = "paused"  // ended by a pause, as a cloud loop's session is (ADR-0059)

	TriggerTick     = "tick"
	TriggerMessage  = "message"
	TriggerManual   = "manual"
	TriggerRotation = "rotation" // the handoff turn a context rotation injects

	// Why a context rotation was taken, which decides what the handoff turn
	// and the fresh session after it are told (#272).
	RotationReasonFill     = "fill"     // the context window crossed a rotation threshold
	RotationReasonOperator = "operator" // the operator asked for one outright
	RotationReasonMission  = "mission"  // the operator rewrote the loop's mission
	// RotationReasonConnection is a value replaced on a connection the loop
	// holds: the session that ran with the old one ends (#609).
	RotationReasonConnection = "connection"
	// RotationReasonRevoke is a connection the loop held revoked: the
	// session that ran with its value ends, and the next runs without it
	// (#610).
	RotationReasonRevoke = "revoke"
	// RotationReasonRehome is the operator moving a bare loop into a docker
	// workstation: the session that ran on the host ends, and the next runs
	// in the workstation (#624).
	RotationReasonRehome = "rehome"

	OriginWeb           = "web"
	OriginTelegramGroup = "telegram-group"
	OriginTelegramDM    = "telegram-dm"
	OriginSlackChannel  = "slack-channel"
	OriginSlackDM       = "slack-dm"
	OriginLoop          = "loop"

	// A message's conversation: the unit of privacy and addressing
	// (ADR-0026). The per-loop kinds are keyed to a loop; group is shared.
	ConversationOwnerDM     = "owner_dm"     // a loop's Telegram DM with its owner
	ConversationGroup       = "group"        // a channel: the fleet channel, or another by Message.Channel
	ConversationControlRoom = "control_room" // a loop's web thread, read by every hub user
	// A loop's private conversation with an allowed sender who is not its
	// owner is dm:<name> (ADR-0049): see DMDestinationPrefix.

	PacingFixed = "fixed" // orchestrator interval; trailer optional
	PacingSelf  = "self"  // the loop schedules itself via trailers; interval is a fallback

	SubagentsAllowed = "allowed" // a turn may spawn subagents (the CLI's default)
	// SubagentsOff denies every turn the Agent tool (#855). It is a cost
	// guard on how far the CLI fans out, not containment: a loop with a
	// shell and a claude login can still run claude itself.
	SubagentsOff = "off"

	RuntimeBare   = "bare"   // host subprocess, uncontained (ADR-0017)
	RuntimeDocker = "docker" // long-lived container + volume workstation
	RuntimeCloud  = "cloud"  // a Claude Code cloud session (ADR-0056)

	// A loop's surface: the chat platform it has an identity on, if any
	// (ADR-0029: at most one, attached after the loop exists).
	SurfaceTelegram = "telegram"
	SurfaceSlack    = "slack"

	SenderPending = "pending"
	SenderAllowed = "allowed"
	SenderBlocked = "blocked"
)

// SettingClaudeOAuthToken is the settings key holding the operator's
// `claude setup-token` output. Until a Claude login is signed in, the Claude
// proxy adds it to contained loops' model requests, so they run under the
// operator's own Claude plan without holding it (ADR-0058). Write-only
// through the API; never logged.
const SettingClaudeOAuthToken = "claude_oauth_token"

// DefaultClaudeLogin names the default Claude login: the one a loop that
// names no login runs on, and the one cloud loops run on (ADR-0060). The
// upgrade made today's sign-in a login of this name.
const DefaultClaudeLogin = "default"

// ClaudeLogin is one claude.ai sign-in the hub holds (ADR-0060). Account
// is sealed at rest (ADR-0046), and its zero value is a login signed out,
// which still exists so the loops on it keep a valid name.
type ClaudeLogin struct {
	Name    string
	Account CloudAccount
	// OwnerUserID is the user who added it, "" for the one the upgrade made.
	OwnerUserID string
	CreatedAt   int64
	// Check is its last login check (ADR-0044), nil before the first. The
	// default login's is kept in SettingLoginCheck instead.
	Check *LoginCheckRecord
	// Unreadable is its opened sign-in when that doesn't read as an
	// account, "" otherwise. The login reads as signed out, and the
	// redactor masks the value whole.
	Unreadable string
	// PlanUsage is its plan usage as last observed (ADR-0060), the zero
	// record before the first observation. The default login's is kept
	// in SettingPlanUsage instead.
	PlanUsage PlanUsageRecord
}

// SignedIn reports whether the login holds a sign-in.
func (login *ClaudeLogin) SignedIn() bool { return login.Account.Token != "" }

// SignedInAccount reads the sign-in of the login called name. ok is false
// when the hub holds no such login or it is signed out.
func SignedInAccount(ctx context.Context, logins ClaudeLoginStore, name string) (account CloudAccount, ok bool, err error) {
	login, err := logins.Get(ctx, name)
	if errors.Is(err, ErrNotFound) {
		return CloudAccount{}, false, nil
	}
	if err != nil {
		return CloudAccount{}, false, err
	}
	return login.Account, login.SignedIn(), nil
}

// SignInsOf is the logins the user with userID owns that hold a sign-in,
// one that reads or one that doesn't: what signing them out clears.
func SignInsOf(ctx context.Context, logins ClaudeLoginStore, userID string) ([]*ClaudeLogin, error) {
	all, err := logins.List(ctx)
	if err != nil {
		return nil, err
	}
	var held []*ClaudeLogin
	for _, login := range all {
		if login.OwnerUserID == userID && (login.SignedIn() || login.Unreadable != "") {
			held = append(held, login)
		}
	}
	return held, nil
}

// CloudAccount is a Claude login's sign-in: the OAuth access token, the
// refresh token the sign-in gave with it, and the account it signed in
// to.
type CloudAccount struct {
	Token        string `json:"token"`
	RefreshToken string `json:"refresh_token,omitempty"`
	// ExpiresAt is when Token expires, in Unix ms, 0 when unknown.
	ExpiresAt int64  `json:"expires_at,omitempty"`
	OrgUUID   string `json:"org_uuid"`
	// Email is the address the account signed in with, "" when the CLI
	// didn't say, as for a sign-in made before it was kept.
	Email string `json:"email,omitempty"`
	// Scopes are what the sign-in granted, handed back to the CLI when it
	// refreshes the token (ADR-0058).
	Scopes []string `json:"scopes,omitempty"`
}

// ClaudeLoginStore keeps the hub's Claude logins.
type ClaudeLoginStore interface {
	// List returns every login, by name.
	List(ctx context.Context) ([]*ClaudeLogin, error)
	// Get is ErrNotFound for a name the hub doesn't hold.
	Get(ctx context.Context, name string) (*ClaudeLogin, error)
	// Create adds a login; ErrDuplicate for a name held already.
	Create(ctx context.Context, login *ClaudeLogin) error
	// SetAccount stores the login's sign-in, the zero CloudAccount to sign
	// it out. ErrNotFound for a name the hub doesn't hold.
	SetAccount(ctx context.Context, name string, account CloudAccount) error
	// SetCheck stores the login's last check, nil to forget it.
	// ErrNotFound for a name the hub doesn't hold.
	SetCheck(ctx context.Context, name string, check *LoginCheckRecord) error
	// SetPlanUsage stores the login's plan usage. ErrNotFound for a name
	// the hub doesn't hold.
	SetPlanUsage(ctx context.Context, name string, usage PlanUsageRecord) error
	// Delete removes a login; ErrNotFound for a name the hub doesn't hold.
	Delete(ctx context.Context, name string) error
}

// SettingCloudAddress holds the public HTTPS address in front of the cloud
// listener, the one cloud sessions reach the hub at (ADR-0056). Unset, no
// cloud loop wakes.
const SettingCloudAddress = "cloud_address"

// SettingCloudListen holds where Settings → Cloud opened the cloud
// listener (ADR-0056), so the hub opens it there again at its next
// start. Unset or "", the listener is off unless --cloud-listen opens it.
const SettingCloudListen = "cloud_listen"

// SettingCloudEnvironment holds the environment every cloud loop starts
// in, which the operator sets up on claude.ai once (ADR-0059), a
// CloudEnvironment as JSON. Unset, no cloud loop wakes.
const SettingCloudEnvironment = "cloud_environment"

// CloudEnvironment is the environment Settings → Cloud chose, and the
// allowlist text the operator last confirmed pasting into it.
type CloudEnvironment struct {
	ID string `json:"id"`
	// Name is what the account's list called it when it was chosen, ""
	// for an ID the list didn't show.
	Name string `json:"name,omitempty"`
	// ConfirmedDigest is the SHA-256, in hex, of the text confirmed, ""
	// before a confirm. A list that has moved since is no longer
	// confirmed.
	ConfirmedDigest string `json:"confirmed_digest,omitempty"`
	// ConfirmedAt is when, in Unix ms.
	ConfirmedAt int64 `json:"confirmed_at,omitempty"`
}

// ValidCloudEnvironmentID reports whether id has an environment ID's
// shape: env_ and 1–100 letters and digits. It is checked before the ID
// goes into the --settings JSON a start runs with (ADR-0059).
func ValidCloudEnvironmentID(id string) bool {
	rest, found := strings.CutPrefix(id, "env_")
	if !found || rest == "" || len(rest) > 100 {
		return false
	}
	for _, char := range rest {
		if (char < 'a' || char > 'z') && (char < 'A' || char > 'Z') && (char < '0' || char > '9') {
			return false
		}
	}
	return true
}

// SettingLoginCheck holds the last login check's outcome (ADR-0044), a
// LoginCheckRecord as JSON. Saving a setup-token resets it to pending.
const SettingLoginCheck = "login_check"

// LoginCheckRecord is a login check as the hub remembers it: its status and,
// for a refusal, the sentence the CLI gave. At is when the check began.
type LoginCheckRecord struct {
	Status  string `json:"status"`
	Refusal string `json:"refusal,omitempty"`
	At      int64  `json:"at"`
}

// How a login check ended, or that it has not yet.
const (
	LoginCheckPending      = "pending"
	LoginCheckOK           = "ok"
	LoginCheckRefused      = "refused"
	LoginCheckInconclusive = "inconclusive"
)

// SettingPlanUsage holds the default Claude login's plan usage, a
// PlanUsageRecord as JSON: the usage of every loop that names no login.
// Unset until a first observation. Another login keeps its own on its
// ClaudeLogin (ADR-0060).
const SettingPlanUsage = "plan_usage"

// PlanUsageRecord is the Claude plan's usage as last observed: how much of
// each limit window is used and when that window resets. A Claude login
// has one, which every loop on it reports into, not one per loop. A
// window the observation did not carry is nil.
type PlanUsageRecord struct {
	FiveHour *PlanUsageWindow `json:"five_hour,omitempty"`
	SevenDay *PlanUsageWindow `json:"seven_day,omitempty"`
	At       int64            `json:"at"`     // when it was observed
	Source   string           `json:"source"` // where it was read from
}

// PlanUsageSourceStream is plan usage read from a loop's own stream: the
// rate-limit event its claude process emits after an API response.
const PlanUsageSourceStream = "stream"

// PlanUsageWindow is one limit window: Utilization is the fraction used,
// which can run past 1, and ResetsAt when the window starts over.
type PlanUsageWindow struct {
	Utilization float64 `json:"utilization"`
	ResetsAt    int64   `json:"resets_at"`
}

// SettingOnboardingCompleted is set the first time the hub finds
// every onboarding pillar done (#580), or when the operator skips the chat
// surface (#798). It is what keeps the first-run page aside once the
// operator is through it, whatever a pillar reads later, and deleting the
// fleet's last loop clears it, in the same transaction.
const SettingOnboardingCompleted = "onboarding_completed"

// SettingEgressHosts holds the operator's extra egress hosts (#542), an
// EgressHosts as JSON. Unset until the hub first starts with it, which
// seeds it from --egress-allow; the stored list wins from then on.
const SettingEgressHosts = "egress_hosts"

// SettingHealthErrorTurns is how many errored turns in a row open a
// turn_errors problem with a loop (ADR-0053), an integer; 0 turns that
// symptom off. Unset means loop.DefaultHealthErrorTurns.
const SettingHealthErrorTurns = "health_error_turns"

// EgressHosts is the hosts every docker loop may reach on top of the
// built-in allowlist (ADR-0028), each canonical "host" or "host:port", in
// the order they were added. ChangedAt is when the list last changed, in
// Unix milliseconds.
type EgressHosts struct {
	Hosts     []string `json:"hosts"`
	ChangedAt int64    `json:"changed_at"`
}

// Context-rotation thresholds (ADR-0022), stored as integer percentages of
// the model's context window. A loop arms rotation at the first and stops
// waiting for a quiet boundary at the second.
const (
	SettingContextArmPercent   = "context_arm_percent"
	SettingContextForcePercent = "context_force_percent"
)

// Plan-cap settings (ADR-0047). The thresholds are integer percentages of
// each plan window, 0 for off, and the default while unset. The resume
// setting holds when the operator's last Resume now stops holding the cap
// off, in Unix milliseconds.
const (
	SettingPlanCapFiveHourPercent = "plan_cap_five_hour_percent"
	SettingPlanCapSevenDayPercent = "plan_cap_seven_day_percent"
	SettingPlanCapResumedUntil    = "plan_cap_resumed_until"
)

// MaxDoing is the most a loop's doing holds, in characters; the store cuts
// a longer one, after the redactor has seen it whole (#751).
const MaxDoing = 60

type Loop struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Mission string `json:"mission"`
	Model   string `json:"model"`

	WorkspaceMode string `json:"workspace_mode"` // none|dir|worktree
	WorkspacePath string `json:"workspace_path"` // effective cwd for the claude process, in the runtime's filesystem
	RepoPath      string `json:"repo_path"`
	WorktreePath  string `json:"worktree_path"`
	Branch        string `json:"branch"`

	// Workstation config (ADR-0017): set at creation, immutable after.
	Runtime string  `json:"runtime"` // bare|docker|cloud
	Image   string  `json:"image"`   // workstation image; "" = the server default
	MemMB   int     `json:"mem_mb"`  // workstation memory limit
	CPUs    float64 `json:"cpus"`    // workstation CPU limit

	TickIntervalSec int    `json:"tick_interval_sec"`
	MinWakeSec      int    `json:"min_wake_sec"`
	MaxWakeSec      int    `json:"max_wake_sec"`
	IdleTimeoutSec  int    `json:"idle_timeout_sec"`
	Pacing          string `json:"pacing"`    // fixed|self
	Effort          string `json:"effort"`    // ""|low|medium|high|xhigh|max
	Subagents       string `json:"subagents"` // allowed|off
	// DailyCapUSD is the loop's own daily USD cap, 0 for none (#652).
	DailyCapUSD float64 `json:"daily_cap_usd"`
	// ClaudeLogin names the Claude login a contained loop runs on, "" for
	// the default (ADR-0060). A bare loop runs on the host's own login and a
	// cloud loop on the default, so theirs is always "".
	ClaudeLogin string `json:"claude_login"`

	TGBotToken    string `json:"-"`
	TGBotUsername string `json:"tg_bot_username"`
	// TGGroupChatID is the Telegram group the fleet channel's room is, read
	// from the loop's rooms (0 = none bound).
	TGGroupChatID int64 `json:"tg_group_chat_id"`
	// HubMCPToken is the bearer token this loop's claude process presents to
	// the hub's MCP endpoint (ADR-0026). Minted at creation, immutable, and
	// secret: json:"-" keeps it out of every API response, like TGBotToken.
	HubMCPToken string `json:"-"`
	// WorkstationOff records that the operator switched this loop's
	// workstation off. Intent, not observation: a health poll cannot tell a
	// halted workstation from a dead one (ADR-0021). Surfaced to the UI as
	// down_reason, not as a field of its own.
	WorkstationOff bool `json:"-"`
	// OutsideFleetChannel says the operator took this loop out of the fleet
	// channel: it has no group (ADR-0032). Stored as the exception so
	// the zero value is the ordinary loop, which is in it. Surfaced to the
	// API as in_fleet_channel, the way round a reader asks the question.
	OutsideFleetChannel bool `json:"-"`
	// TGGroupBoundAt is when this loop's bot bound to that group, read with
	// it. A bot only ingests group messages Telegram dated after it — see
	// ADR-0020.
	TGGroupBoundAt int64 `json:"-"`
	// OwnerTGUserID is the allowlisted Telegram sender configured as this
	// loop's owner (0 = none). Being known to Spool does not make someone
	// an owner; the operator chooses (#73).
	OwnerTGUserID int64 `json:"owner_tg_user_id,omitempty"`
	// OwnerDMChatID is the private chat between that owner and this loop's
	// own bot, captured when they write to it (0 = not captured yet). A bot
	// cannot open a private chat, so until the owner writes once there is
	// nowhere to send — reported, never substituted (ADR-0025).
	OwnerDMChatID int64 `json:"-"`

	// The loop's identity on Slack (#230), written together by LoopEdit.Slack
	// from what the bot token's auth.test answered. Both tokens are secret,
	// like TGBotToken. A loop has at most one surface (ADR-0029): the
	// hub keeps these empty while TGBotToken is set, and the other way round.
	// The store accepts both.
	SlackAppToken  string `json:"-"`
	SlackBotToken  string `json:"-"`
	SlackBotUserID string `json:"slack_bot_user_id,omitempty"`
	SlackBotName   string `json:"slack_bot_name,omitempty"`
	SlackTeamID    string `json:"slack_team_id,omitempty"`
	SlackTeamName  string `json:"slack_team_name,omitempty"`
	// SlackChannelID is the Slack channel the fleet channel's room is, and
	// SlackChannelBoundAt when the app bound to it, both read from the loop's
	// rooms ("" = none bound), as TGGroupChatID and TGGroupBoundAt are.
	SlackChannelID      string `json:"-"`
	SlackChannelBoundAt int64  `json:"-"`
	// OwnerSlackUserID is the allowlisted Slack sender configured as this
	// loop's owner ("" = none). OwnerSlackDMChannel is the DM the bot opened
	// with them. Unlike a Telegram bot, a Slack bot can open that DM itself,
	// so it is a cache of conversations.open, not a precondition for sending.
	OwnerSlackUserID    string `json:"owner_slack_user_id,omitempty"`
	OwnerSlackDMChannel string `json:"-"`

	Status           string `json:"status"`
	CurrentSessionID string `json:"current_session_id"`
	CurrentPID       int    `json:"current_pid"`

	// Rotation state that has to outlive the orchestrator (#66, ADR-0022).
	// RotatePending says a handoff turn has been asked for and the session it
	// ran on must not be resumed; HandoffNote is the note that session wrote,
	// waiting for its successor's first turn. RotateReason is why the
	// rotation was taken (a store.RotationReason* value), kept with the note
	// because the successor is told both. All three are engine bookkeeping
	// rather than loop configuration, and the note is the loop's own words —
	// json:"-" keeps them out of every API response.
	RotatePending bool   `json:"-"`
	RotateReason  string `json:"-"`
	HandoffNote   string `json:"-"`
	// PromptHash identifies the system prompt the current session was created
	// with. A resumed session keeps that prompt whatever Spool passes on the
	// next spawn (#162), so this is the only record of what the live session
	// actually reads; comparing it with the wake's rendered prompt is how a
	// rule or catalog change is noticed. Engine bookkeeping like the pair
	// above, and json:"-" for the same reason.
	PromptHash string `json:"-"`
	// ModelRefusal is what the CLI said when the API did not recognize the
	// loop's model: a sentence naming it, "" while the model has not been
	// refused (#289). A loop with one takes no turns until its model is
	// edited, which clears it. Always present in the API, so a client can
	// tell "not refused" from a server too old to say.
	ModelRefusal string `json:"model_refusal"`
	// Doing is the one line the loop last set with a [doing: …] trailer to
	// say what it is working on, "" when it has set none or cleared it, and
	// DoingAt when it set it, in Unix ms (#751). Always present in the API,
	// like ModelRefusal.
	Doing   string `json:"doing"`
	DoingAt int64  `json:"doing_at"`

	CreatedAt int64 `json:"created_at"`
	UpdatedAt int64 `json:"updated_at"`
	// CreatedBy and UpdatedBy name who made the loop and who last changed
	// it through the API (ADR-0048): a user, or "operator" for the
	// operator token. Only the loop's change record writes them, so Create
	// and Update leave them alone, and a loop older than the record has
	// neither.
	CreatedBy string `json:"created_by"`
	UpdatedBy string `json:"updated_by"`
}

// Surface names the platform the loop has an identity on, or "" when it has
// none. It is read off which credential is stored, so it cannot disagree
// with them.
func (loopRecord *Loop) Surface() string {
	switch {
	case loopRecord.TGBotToken != "":
		return SurfaceTelegram
	case loopRecord.SlackBotToken != "":
		return SurfaceSlack
	}
	return ""
}

// OwnerConfigured reports whether the loop has an owner on its own surface.
// Every loop carries the hub's default Telegram owner once a Telegram sender
// is allowed, whatever it is attached to, so a Slack loop's owner is only
// ever its Slack one.
func (loopRecord *Loop) OwnerConfigured() bool {
	switch loopRecord.Surface() {
	case SurfaceTelegram:
		return loopRecord.OwnerTGUserID != 0
	case SurfaceSlack:
		return loopRecord.OwnerSlackUserID != ""
	}
	return false
}

// OwnerDMReady reports whether the loop can write to its owner privately
// now. On Telegram that takes the owner's own first message, since a bot
// cannot open a private chat; a Slack app opens the DM itself, so an owner
// is enough.
func (loopRecord *Loop) OwnerDMReady() bool {
	switch loopRecord.Surface() {
	case SurfaceTelegram:
		return loopRecord.OwnerTGUserID != 0 && loopRecord.OwnerDMChatID != 0
	case SurfaceSlack:
		return loopRecord.OwnerSlackUserID != ""
	}
	return false
}

// FleetRule is one operator-defined rule every loop follows. Enabled rules
// render into every loop's system prompt as the FLEET RULES section, ahead of
// its mission (ADR-0024). A rule's text is data; only the section is contract.
type FleetRule struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Body      string `json:"body"`
	Enabled   bool   `json:"enabled"`
	CreatedAt int64  `json:"created_at"`
	UpdatedAt int64  `json:"updated_at"`
}

// ModelResolution is what a model name runs as on this hub (ADR-0033): a
// family alias, or an entry on the custom model list. Resolved is "" until
// something has said.
type ModelResolution struct {
	Model    string `json:"model"`
	Resolved string `json:"resolved"`
	// Source is ResolutionProbe or ResolutionTurn: the hub's own run, or
	// what a real turn on the default runtime reported at init.
	Source string `json:"source"`
	// CLIVersion is the version the probe ran, "" for a turn.
	CLIVersion string `json:"cli_version"`
	ResolvedAt int64  `json:"resolved_at"`
}

// Where a resolution came from.
const (
	ResolutionProbe = "probe"
	ResolutionTurn  = "turn"
)

// CustomModel is an operator-added entry on the model list, offered in the
// dropdowns after the family aliases (#332). Label is optional.
type CustomModel struct {
	ID        string `json:"id"`
	Model     string `json:"model"`
	Label     string `json:"label"`
	CreatedAt int64  `json:"created_at"`
}

type Session struct {
	ID        string `json:"id"` // the claude session uuid (minted by Spool)
	LoopID    string `json:"loop_id"`
	StartedAt int64  `json:"started_at"`
	EndedAt   int64  `json:"ended_at"`
	EndReason string `json:"end_reason"`
}

type Message struct {
	ID          int64    `json:"id"`
	TS          int64    `json:"ts"`
	Origin      string   `json:"origin"`
	Author      string   `json:"author"`
	FromLoopID  string   `json:"from_loop_id,omitempty"`
	Text        string   `json:"text"`
	Mentions    []string `json:"mentions"`
	TGChatID    int64    `json:"tg_chat_id,omitempty"`
	TGMessageID int64    `json:"tg_message_id,omitempty"`
	// TGBotLoopID is the loop whose bot received this message. Telegram
	// numbers message_id per bot conversation, so it identifies a message
	// only together with the bot that saw it. Storage detail, not surfaced.
	TGBotLoopID string   `json:"-"`
	DeliveredTo []string `json:"delivered_to"`
	// Conversation is the unit of privacy and addressing this message
	// belongs to: one of the Conversation* constants.
	Conversation string `json:"conversation"`
	// ConversationLoopID keys the per-loop conversation kinds (owner_dm,
	// control_room, dm:<name>) to their loop; empty for the shared group.
	ConversationLoopID string `json:"conversation_loop_id,omitempty"`
	// DMPerson is who a private conversation is with, as TGPerson or
	// SlackPerson key them: on dm:<name> the name can change, the person
	// cannot, and on owner_dm it is the owner the message was with, so a
	// later owner is not them (ADR-0050). Empty for every other
	// conversation, and on an owner_dm older than #703. Storage detail,
	// not surfaced.
	DMPerson string `json:"-"`
	// Channel names the channel a group message was said in, FleetChannel
	// for the fleet channel; empty for the per-loop kinds, which are in none
	// (ADR-0038).
	Channel string `json:"channel,omitempty"`
	// ReplyToID is the message this one explicitly replies to (0 = none).
	// A reply is always chosen, never inferred from ordering (ADR-0025).
	ReplyToID int64 `json:"reply_to_id,omitempty"`
	// SendFailedAt and SendError record a surface send that did not get
	// through: when the bridge gave up, and what it gave up on. Both are
	// zero for a message that was delivered, and for one nobody sent —
	// the control room tells those apart by the direction of the message
	// rather than by these (#147).
	SendFailedAt int64  `json:"send_failed_at,omitempty"`
	SendError    string `json:"send_error,omitempty"`
	// SendResolvedAt is when a failure stopped being the operator's
	// business: a retry of this row got through, or they dismissed it.
	// Zero while it is still unresolved, which is what the undelivered
	// count and the Undelivered pane ask for (#269). The failure itself is
	// not erased — send_failed_at and send_error stay, because what failed
	// and why is history, and a resolved row that looked like a delivered
	// one would lose it.
	SendResolvedAt int64 `json:"send_resolved_at,omitempty"`
	// SendResolution is how it resolved, one of the SendResolution*
	// constants; empty while unresolved. The ways mean different things
	// about the message — delivered says it did arrive in the end,
	// dismissed says it never did and someone is done looking — and readers
	// that only ask "is it still on my list" should use SendResolvedAt
	// instead of comparing this.
	SendResolution string `json:"send_resolution,omitempty"`
	// SendResentAs is the message that carried these words the second time,
	// when the loop itself said them again against this failure (#270).
	// Zero for every other resolution. The reason says a resend happened;
	// this says which message it was, so an operator reading the failure can
	// read what was actually said rather than take it on trust. When a
	// resend failed and was itself resent, this is the message that finally
	// got through, not the next attempt — the words that arrived are what
	// the operator wants from a failure that is closed.
	SendResentAs int64 `json:"send_resent_as,omitempty"`
	// ResendsID is the failed message these words were said again for: the
	// sender's claim, made when the send was asked for and kept whatever
	// becomes of it (#270). Zero for a message that resends nothing. A
	// resend that fails too is itself resent, so this is a chain, and the
	// send that finally arrives resolves all of it.
	ResendsID int64 `json:"resends_id,omitempty"`
	// SendFailureToldAt is when the loop that sent this message was last
	// told the send failed (0 = not yet), and SendFailureTellings how many
	// turns have told it: the first telling, then the reminders (#154,
	// #561). Engine bookkeeping, so json:"-" keeps both out of every API
	// response — the operator reads SendFailedAt, which is the fact itself,
	// and SendLeftByLoop, which is what the count comes to.
	SendFailureToldAt   int64 `json:"-"`
	SendFailureTellings int   `json:"-"`
	// SendLeftByLoop says the loop was told of this failure and reminded
	// of it as often as it will be, and neither resent nor dismissed it: it
	// left it for the operator (#561). Only an unresolved failure is left;
	// the operator's own dismissal or retry ends that.
	SendLeftByLoop bool `json:"send_left_by_loop,omitempty"`
	// Mirror says whether this message exists on the surface too: one of the
	// Mirror* constants, never empty once stored and never omitted, so an
	// absent field means an older server rather than an answer (#285). Its
	// failures are not here — a mirror that failed is a send that failed,
	// and carries SendFailedAt like any other (ADR-0032).
	Mirror string `json:"mirror"`
	// TGKey identifies a telegram message by what every bot observing it
	// sees alike — chat, sender, date, text — so one bot's message can be
	// matched to another bot's sighting of it. Storage detail, not surfaced.
	TGKey string `json:"-"`
	// SlackChannelID and SlackTS identify a message on Slack: the channel
	// it is in and Slack's ts for it. Slack numbers ts per channel, not per
	// app, so unlike a Telegram message_id the pair names the message for
	// every loop's app alike, and is unique. Storage detail, not surfaced.
	SlackChannelID string `json:"-"`
	SlackTS        string `json:"-"`
	// EditOf is the message this one corrects, 0 for none: a person
	// changed or deleted what they said on a surface, and this is what
	// they now stand behind (ADR-0055). EditKind says which, one of the
	// Edit* constants, and a deletion carries no text.
	EditOf   int64  `json:"edit_of,omitempty"`
	EditKind string `json:"edit_kind,omitempty"`
	// EditedAt and DeletedAt are when the person last edited this message,
	// and when they deleted it, 0 for never. The text stays as it was
	// said; the correction carries what changed.
	EditedAt  int64 `json:"edited_at,omitempty"`
	DeletedAt int64 `json:"deleted_at,omitempty"`
	// EditKey names one edit as every identity that heard it sees it
	// alike, so a shared room's edit is stored once whichever loop's bot
	// or app reports it first. Storage detail, not surfaced.
	EditKey string `json:"-"`
}

// What a correction does to the message it corrects (ADR-0055).
const (
	EditEdited  = "edited"
	EditDeleted = "deleted"
)

// Attachment is a file that crossed a chat surface with a message (#123):
// kept once by the hub, referenced from the message, and presented to a
// loop as a path it can read. The row outlives the file: retention removes
// the file and sets RemovedAt, and the message still says what was sent.
type Attachment struct {
	ID        int64  `json:"id"`
	MessageID int64  `json:"message_id"`
	Name      string `json:"name"`
	MIME      string `json:"mime"`
	Kind      string `json:"kind"` // AttachmentImage | AttachmentFile
	Size      int64  `json:"size"`
	SHA256    string `json:"-"`
	Width     int    `json:"width,omitempty"`
	Height    int    `json:"height,omitempty"`
	// Path is the kept file's location relative to the hub's files
	// directory. Storage detail, not surfaced.
	Path string `json:"-"`
	// NotKept says why a file never arrived (NotKept*); "" when it did.
	NotKept   string `json:"not_kept,omitempty"`
	CreatedAt int64  `json:"created_at"`
	RemovedAt int64  `json:"removed_at,omitempty"` // 0 = the file is kept
	// UploadedBy is the user who uploaded the file from the control room,
	// "" for the operator token, a surface's file and a loop's. Until a
	// message claims it, only they or an admin may read or send it.
	UploadedBy string `json:"-"`
}

// Attachment kinds: an image is presented with its dimensions and sent as
// native media where the surface has it; anything else is a file.
const (
	AttachmentImage = "image"
	AttachmentFile  = "file"
)

// Why an attachment has no file (Attachment.NotKept).
const (
	NotKeptTooLarge     = "too_large"     // over the size limit; never downloaded
	NotKeptFailed       = "fetch_failed"  // the surface would not hand it over
	NotKeptMissingScope = "missing_scope" // the surface's app may not read files (#480)
)

// How a send failure resolved (Message.SendResolution).
const (
	// SendResolutionDelivered: a later attempt at the same row got through,
	// so the message did arrive. Its sender must not be told it was lost —
	// a loop told that says the message again, and the human gets it twice
	// (ADR-0026, 2026-09-18 amendment).
	SendResolutionDelivered = "delivered"
	// SendResolutionDismissed: the operator read the failure and set it
	// aside. The message still never arrived, so its sender is still owed
	// the news; this only takes it off the operator's list.
	SendResolutionDismissed = "dismissed"
	// SendResolutionResent: the loop said the words again itself, naming
	// this failure in the send that carried them, and that send got
	// through (#270). Like a delivered resolution and unlike a dismissed
	// one, it means the words reached their reader — so the sender is not
	// told about this failure either; it is the one that dealt with it.
	SendResolutionResent = "resent"
	// SendResolutionDismissedByLoop: the loop read the failure and decided
	// the words are no longer worth saying (#561). The message never
	// arrived, as with the operator's dismissal, but the sender is the one
	// that decided, so it is not told again either.
	SendResolutionDismissedByLoop = "dismissed_by_loop"
)

// SendFailureTellings is how many turns carry a lost send's news while it
// is unresolved: the first telling and two reminders (#561). After that the
// failure is the operator's, and the loop has left it to them.
const SendFailureTellings = 3

// Whether a message exists on the surface too (Message.Mirror). It is a
// state, not a direction: which way a message crossed is its Origin.
const (
	// MirrorNotMirrored: on the hub only, and staying there — the
	// operator's words (ADR-0032), a control_room message, a loop
	// send when the loop has no surface to carry it, and a failed send
	// the operator dismissed or the loop said again in another message.
	MirrorNotMirrored = "not_mirrored"
	// MirrorPending: bound for the surface and not there yet. With
	// SendFailedAt set, the send failed and the failure fields say why.
	// Without it, the send is in flight: a running hub settles every send
	// it accepted, as landed or as failed (#302), and a stopping one fails
	// what it still held before its store closes (ADR-0036). Only a hub
	// that crashes leaves one unsettled, and its next start marks that
	// failed, so no such row outlives a restart.
	MirrorPending = "pending"
	// MirrorMirrored: on the surface too — a loop's send that got through,
	// and everything that came in from the surface in the first place.
	MirrorMirrored = "mirrored"
)

// SurfaceTraffic is what one chat surface has carried (#580).
type SurfaceTraffic struct {
	Surface  string // SurfaceTelegram or SurfaceSlack
	Sent     bool   // a loop's send got through to it
	Received bool   // a person's message came in from it
}

// SurfaceRef is one bot's own id for a message on a surface: what its poller
// received, or what Telegram returned when it sent it. Telegram numbers
// message_id per bot conversation (ADR-0020), so a reference is only ever
// valid for the bot that holds it.
type SurfaceRef struct {
	MessageID   int64
	BotLoopID   string
	TGChatID    int64
	TGMessageID int64
}

// Spend is a sum of turn costs. USD leaves out every turn whose cost is
// unknown, so a total that does says so (ADR-0059).
type Spend struct {
	USD float64
	// Estimated is whether any cost in USD is the hub's estimate.
	Estimated bool
	// UnknownTurns counts the turns USD leaves out.
	UnknownTurns int
}

type Turn struct {
	ID        string `json:"id"`
	LoopID    string `json:"loop_id"`
	SessionID string `json:"session_id"`
	Trigger   string `json:"trigger"`
	// Model the turn actually ran on, as the CLI reported it at init — not
	// the loop's configured string, which may be empty or an alias.
	Model      string `json:"model,omitempty"`
	StartedAt  int64  `json:"started_at"`
	EndedAt    int64  `json:"ended_at"`
	IsError    bool   `json:"is_error"`
	ResultText string `json:"result_text"`
	// CostUSD is what this turn cost. The CLI reports the session's running
	// total instead, so the actor records the difference from the previous
	// turn of the same session (#191) — which makes this column safe to sum,
	// and SessionCostUSD the one that is not. Nil on an estimated turn when
	// the cost is unknown: a model the hub has no price for (ADR-0059).
	CostUSD *float64 `json:"cost_usd"`
	// CostEstimated marks a cost the hub priced from the turn's tokens,
	// a cloud turn's, rather than one the CLI reported.
	CostEstimated bool `json:"cost_estimated"`
	// SessionCostUSD is the CLI's total_cost_usd verbatim: the session's
	// spend up to and including this turn. Kept because it is the figure we
	// were given, so a later turn can be priced against it after a restart,
	// and because an accounting change upstream is only visible here.
	SessionCostUSD   float64 `json:"session_cost_usd"`
	InputTokens      int     `json:"input_tokens"`
	OutputTokens     int     `json:"output_tokens"`
	CacheReadTokens  int     `json:"cache_read_tokens"`
	CacheWriteTokens int     `json:"cache_write_tokens"`
	// ContextTokens is the occupancy of the turn's last API call — what the
	// session's next prompt would carry into the model's window. The usage
	// fields above are summed across the turn's API steps (each step rereads
	// the cached prefix), so they price the turn but cannot measure context.
	ContextTokens int   `json:"context_tokens"`
	DurationMS    int64 `json:"duration_ms"`
}

type Event struct {
	ID        int64  `json:"id"`
	LoopID    string `json:"loop_id"`
	SessionID string `json:"session_id"`
	TurnID    string `json:"turn_id"`
	TS        int64  `json:"ts"`
	Type      string `json:"type"`
	Subtype   string `json:"subtype"`
	Payload   string `json:"payload"` // raw JSON line (or envelope JSON)
}

type ScheduleEntry struct {
	LoopID     string `json:"loop_id"`
	NextTickAt int64  `json:"next_tick_at"` // 0 = no tick scheduled (paused)
	LastTickAt int64  `json:"last_tick_at"`
}

type InboxItem struct {
	ID       int64  `json:"id"`
	LoopID   string `json:"loop_id"`
	Envelope string `json:"envelope"` // JSON-encoded loop.Envelope
	QueuedAt int64  `json:"queued_at"`
}

// LoopEdit is an operator's edit to a loop's settings: the fields they named,
// and only those. A nil field is one the request did not mention and the
// write does not touch.
//
// Sparse rather than a whole Loop because the alternative loses writes it
// never meant to make. PATCH used to read the row, apply the request, and
// write all twenty-three columns back — holding that copy across a live
// GetMe call to api.telegram.org when the edit carried a token. The poller
// re-learns the group binding and the owner's DM chat in exactly that
// window, and both were reverted from the stale copy, silently, leaving the
// loop's group traffic dead with nothing in the control room to explain it
// (#164, and the same mechanism as #161).
type LoopEdit struct {
	Mission     *string
	Model       *string
	Effort      *string
	Pacing      *string
	Subagents   *string
	DailyCapUSD *float64
	ClaudeLogin *string

	TickIntervalSec *int
	MinWakeSec      *int
	MaxWakeSec      *int
	IdleTimeoutSec  *int

	// TGBotToken and TGBotUsername move together: the username is what the
	// token's own GetMe answered, so writing one without the other would
	// name a bot the token does not open.
	TGBotToken    *string
	TGBotUsername *string
	// ClearTelegramRooms forgets every Telegram room this loop's bot knew,
	// bound or not: with the token cleared there is no bot in any of them.
	// Set only when the token is cleared, never as a side effect of an edit
	// that did not mention it, which is the revert this type exists to
	// prevent.
	ClearTelegramRooms bool
	// Slack writes the loop's whole Slack identity at once, for the same
	// reason TGBotToken and TGBotUsername move together: every field but the
	// tokens is what the tokens answered. nil leaves it alone; a zero
	// SlackIdentity detaches the app. Any write also drops the owner's DM
	// channel, which belonged to the bot it replaces; the owner stays unless
	// ClearSlackOwner says otherwise.
	Slack *SlackIdentity
	// ClearSlackRooms forgets every Slack room this loop's app knew, bound
	// or not. Set when the app is detached, never as a side effect, like
	// ClearTelegramRooms.
	ClearSlackRooms bool
	// ClearSlackOwner drops the loop's Slack owner. Set when an app from
	// another workspace is attached: the kept owner is not a sender there,
	// so its bot could never reach them.
	ClearSlackOwner bool
	// OutsideFleetChannel moves the loop out of the fleet channel (true) or
	// back in (false); nil leaves it where it is.
	OutsideFleetChannel *bool

	UpdatedAt int64
}

// SlackIdentity is a Slack app as a loop holds it: the token pair the
// operator pasted and what Slack said the bot token names.
type SlackIdentity struct {
	AppToken  string
	BotToken  string
	BotUserID string
	BotName   string
	TeamID    string
	TeamName  string
}

type LoopStore interface {
	Create(ctx context.Context, loopRecord *Loop) error
	// Edit writes the fields an operator named and nothing else, returning
	// the row as it stands afterwards. There is deliberately no whole-row
	// writer: every column of a loop has an owner, and several of them are
	// written by the Telegram poller while the hub is mid-edit (#164).
	Edit(ctx context.Context, id string, edit LoopEdit) (*Loop, error)
	// Delete removes the loop and the connections only it held, so a value
	// the operator gave one loop goes with it (ADR-0043); a connection
	// another loop holds stays, detached from this one. Deleting the last
	// loop also clears SettingOnboardingCompleted.
	Delete(ctx context.Context, id string, at int64) error
	Get(ctx context.Context, id string) (*Loop, error)
	GetByName(ctx context.Context, name string) (*Loop, error)
	// GetByHubMCPToken resolves the loop presenting a bearer token to the
	// hub's MCP endpoint; ErrNotFound for an unknown token.
	GetByHubMCPToken(ctx context.Context, token string) (*Loop, error)
	List(ctx context.Context) ([]*Loop, error)
	// RotateHubMCPToken gives the loop a fresh hub token, which revokes the
	// one it held at once, and returns it. A cloud loop gets one for each
	// session it starts, and on a stop (ADR-0059).
	RotateHubMCPToken(ctx context.Context, id string) (string, error)
	SetRuntime(ctx context.Context, id, sessionID string, pid int) error
	// SetRotation persists a rotation in progress: whether the current session
	// has been retired by a handoff turn, why the rotation was taken, and the
	// note it wrote for its successor. Written at the points the actor changes
	// its mind, so a restart reads the decision rather than starting the loop
	// over.
	SetRotation(ctx context.Context, id string, pending bool, reason, note string) error
	// SetPromptHash records the system prompt the loop's current session was
	// created with, so a later wake can tell whether the prompt it renders is
	// the one that session is running (#162). Narrow for the same reason as
	// the setters below: the actor writes it while other writers touch other
	// columns of the same row.
	SetPromptHash(ctx context.Context, id, hash string) error
	// SetOwner records who the loop may message privately, and the chat its
	// bot reaches them in. Changing the owner passes 0 for the chat: the
	// captured one belonged to the previous owner. Narrow rather than a
	// whole-row Update because a surface writes the loop's columns while the
	// hub writes others of the same row: two read-modify-write cycles
	// interleave and the later one silently reverts the earlier one's
	// column (#161). ErrNotFound if the loop is gone.
	SetOwner(ctx context.Context, id string, tgUserID, dmChatID, updatedAt int64) error
	// AdoptOwner makes tgUserID the owner of every loop that has none and is
	// not archived: the first person allowed, by an admin or by an owner's
	// link, owns the fleet by default (#73, ADR-0051), and can be replaced
	// per loop.
	AdoptOwner(ctx context.Context, tgUserID, updatedAt int64) error
	// SetOwnerDMChat records the private chat the owner has written from,
	// which is the only way a bot learns an address it cannot open itself.
	SetOwnerDMChat(ctx context.Context, id string, chatID, updatedAt int64) error
	// SetSlackOwner records the loop's Slack owner and the DM channel with
	// them; changing the owner passes "" for the channel, which belonged to
	// the previous owner. ErrNotFound if the loop is gone.
	SetSlackOwner(ctx context.Context, id, userID, dmChannel string, updatedAt int64) error
	// SetSlackOwnerDM records the DM channel the bot opened with its owner.
	SetSlackOwnerDM(ctx context.Context, id, dmChannel string, updatedAt int64) error
	// SetStatus records whether a loop is active, paused or archived.
	SetStatus(ctx context.Context, id, status string, updatedAt int64) error
	// SetWorkstationOff records the operator's power intent for a loop's
	// workstation. Written from the actor goroutine, which runs alongside the
	// Telegram poller — the same reason the columns above are narrow.
	SetWorkstationOff(ctx context.Context, id string, off bool, updatedAt int64) error
	// Rehome makes a bare loop a docker one whose workspace is workspacePath,
	// the workstation's home, sized memMB and cpus. The host workspace it
	// leaves is no longer the loop's: repo, worktree and branch are cleared.
	// Written from the actor goroutine, like the power intent (#624).
	// ErrNotFound if the loop is gone.
	Rehome(ctx context.Context, id, workspacePath string, memMB int, cpus float64, updatedAt int64) error
	// SetModelRefusal records that the API refused model, with the CLI's
	// sentence, if model is still the loop's: a refusal of a model the
	// operator has already replaced is not written. Written from the actor
	// goroutine; an edit of the model clears it in the same statement (#289).
	SetModelRefusal(ctx context.Context, id, model, refusal string, updatedAt int64) error
	// SetDoing records the loop's doing, cut to MaxDoing characters, and
	// when it was set; "" clears it. A doing the loop already holds keeps the
	// time it was first set, and changed is false. Written from the actor
	// goroutine at a turn's end, narrow like the setters above (#751). It
	// is not an edit, so updated_at is untouched.
	SetDoing(ctx context.Context, id, doing string, at int64) (changed bool, err error)
}

// FleetRuleStore holds the fleet's rules. List returns them in creation
// order, enabled or not, so the rendered section stays stable between wakes
// and the API can show disabled rules alongside the live ones.
type FleetRuleStore interface {
	Create(ctx context.Context, rule *FleetRule) error
	Update(ctx context.Context, rule *FleetRule) error
	Delete(ctx context.Context, id string) error
	Get(ctx context.Context, id string) (*FleetRule, error)
	List(ctx context.Context) ([]*FleetRule, error)
}

// ModelStore holds the model list's own state: what each name resolves to,
// and the operator's custom entries.
type ModelStore interface {
	// Resolutions returns every stored resolution.
	Resolutions(ctx context.Context) ([]*ModelResolution, error)
	// SetResolution stores r for r.Model, replacing any before it.
	SetResolution(ctx context.Context, resolution *ModelResolution) error
	// ListCustom returns the custom entries in the order they were added.
	ListCustom(ctx context.Context) ([]*CustomModel, error)
	// AddCustom inserts an entry; ErrDuplicate if its model is already on
	// the list.
	AddCustom(ctx context.Context, model *CustomModel) error
	// SetCustomLabel relabels an entry and returns it; ErrNotFound if there
	// is none with that id.
	SetCustomLabel(ctx context.Context, id, label string) (*CustomModel, error)
	// DeleteCustom removes an entry and its resolution; ErrNotFound if there
	// is none with that id.
	DeleteCustom(ctx context.Context, id string) error
}

type SessionStore interface {
	Create(ctx context.Context, session *Session) error
	End(ctx context.Context, id, reason string, endedAt int64) error
	ListByLoop(ctx context.Context, loopID string, limit int) ([]*Session, error)
	// EndDangling closes any sessions left open (orchestrator crash).
	EndDangling(ctx context.Context, reason string, endedAt int64) error
}

// User roles (ADR-0048). Stored and served; a later slice enforces what
// each may do.
const (
	RoleOwner  = "owner"
	RoleAdmin  = "admin"
	RoleMember = "member"
)

// User is a person who signs in to the control room (ADR-0048).
// PasswordHash is self-describing (algorithm, iterations, salt, key), and
// never leaves the hub.
type User struct {
	ID                 string
	Name               string
	Role               string
	PasswordHash       string
	MustChangePassword bool
	CreatedAt          int64
}

type UserStore interface {
	// Create inserts a user; ErrDuplicate if the name is taken.
	Create(ctx context.Context, user *User) error
	Get(ctx context.Context, id string) (*User, error)
	GetByName(ctx context.Context, name string) (*User, error)
	// List is every user, by name.
	List(ctx context.Context) ([]*User, error)
	// SetPassword stores a new hash and whether a change is due.
	// ErrNotFound for an unknown user.
	SetPassword(ctx context.Context, id, hash string, mustChange bool) error
	// SetRole changes a user's role. ErrNotFound for an unknown user, and
	// ErrLastOwner when it would demote the hub's only owner.
	SetRole(ctx context.Context, id, role string) error
	// Delete removes a user, every session they hold, and their links with
	// the senders those links allowed (ADR-0051). ErrNotFound for an unknown
	// user, and ErrLastOwner for the hub's only owner.
	Delete(ctx context.Context, id string) error
}

// UserSession is one sign-in to the control room (ADR-0048), kept by the
// SHA-256 of the ID its cookie holds. UserID is "" for a session the
// operator token opened, which belongs to no user; TokenHash is then the
// SHA-256 of that token, so the session ends when the token changes.
type UserSession struct {
	IDHash     string
	UserID     string
	TokenHash  string
	CreatedAt  int64
	LastSeenAt int64
}

type UserSessionStore interface {
	Create(ctx context.Context, session *UserSession) error
	// Get is ErrNotFound for a session that does not exist or has ended.
	Get(ctx context.Context, idHash string) (*UserSession, error)
	// Touch records the session's use at the time at.
	Touch(ctx context.Context, idHash string, at int64) error
	// Delete ends one session; ending one that is gone is no error.
	Delete(ctx context.Context, idHash string) error
	// DeleteForUser ends every session userID holds but the one keepIDHash
	// names, or all of them when it is "".
	DeleteForUser(ctx context.Context, userID, keepIDHash string) error
	// DeleteExpired ends every session last used before lastSeenBefore or
	// created before createdBefore, and returns how many.
	DeleteExpired(ctx context.Context, lastSeenBefore, createdBefore int64) (int64, error)
}

// SignInThrottle is the failures counted against one name tried at sign-in
// (ADR-0048). It is kept by the name tried, whether or not a user has it,
// so a lock does not say which names exist. Times are Unix milliseconds;
// LockedUntil is 0 for a name never locked.
type SignInThrottle struct {
	Name          string
	Failures      int
	LockedUntil   int64
	LastFailureAt int64
}

type SignInThrottleStore interface {
	// Get is ErrNotFound for a name with no failures counted.
	Get(ctx context.Context, name string) (*SignInThrottle, error)
	// Fail counts one failure against name at the time at, and returns the
	// count. It adds in one statement, so failures that arrive together
	// each count.
	Fail(ctx context.Context, name string, at int64) (int, error)
	// Lock holds name until until, unless it is held longer already.
	Lock(ctx context.Context, name string, until int64) error
	// Clear forgets the failures counted against name.
	Clear(ctx context.Context, name string) error
	// DeleteBefore forgets every name whose last failure was before cutoff,
	// and returns how many.
	DeleteBefore(ctx context.Context, cutoff int64) (int64, error)
}

type MessageStore interface {
	// Insert persists a message. For telegram-sourced messages, (tgChatID,
	// tgMessageID, tgBotLoopID) is unique, and for slack-sourced ones
	// (slackChannelID, slackTS); a duplicate returns ErrDuplicate.
	Insert(ctx context.Context, message *Message) error
	SetDelivered(ctx context.Context, id int64, deliveredTo []string) error
	// Traffic reports, for each chat surface, whether a loop still in the
	// fleet has carried a message each way there: its send got through,
	// and a person wrote in to it. Only an allowlisted person's message is
	// ever stored, so any one counts.
	Traffic(ctx context.Context) ([]SurfaceTraffic, error)
	List(ctx context.Context, limit int) ([]*Message, error)
	// ListConversation returns one conversation's messages, newest first.
	// A per-loop kind — ConversationOwnerDM or ConversationControlRoom — is
	// keyed to its loop. ConversationGroup is every channel's, the hub's
	// rather than any loop's (ADR-0032), so its rows carry no loop key and
	// it is asked for with loopID ""; ListChannel reads one channel.
	ListConversation(ctx context.Context, kind, loopID string, limit int) ([]*Message, error)
	// ListDM returns a loop's dm:<name> messages with person, newest first,
	// under every name the person had: the name is what changes, the
	// person is not (ADR-0049).
	ListDM(ctx context.Context, loopID, person string, limit int) ([]*Message, error)
	// ListPrivateThreads returns who a loop's private messages are with,
	// newest first: one thread per person for owner_dm, and one for their
	// dm:<name> messages under any name. An owner_dm older than #703
	// records nobody, and those make one thread with DMPerson "".
	ListPrivateThreads(ctx context.Context, loopID string) ([]PrivateThread, error)
	// ListChannel returns the messages said in one channel, newest first:
	// the fleet channel's under FleetChannel, and no other's (ADR-0038).
	ListChannel(ctx context.Context, name string, limit int) ([]*Message, error)
	// Get returns one message by id, or ErrNotFound. Reply targets are
	// resolved through it.
	Get(ctx context.Context, id int64) (*Message, error)
	// MarkEdited records that the person who said message id edited it at
	// at, or deleted it when kind is EditDeleted (ADR-0055). An edit leaves
	// its text as it was. A deletion blanks it, and the text of every
	// correction that edited it, in the same write. ErrNotFound when there
	// is no such message.
	MarkEdited(ctx context.Context, id int64, kind string, at int64) error
	// BySlackTS returns the message Slack knows as ts in channelID, or
	// ErrNotFound.
	BySlackTS(ctx context.Context, channelID, ts string) (*Message, error)
	// SetSlackRef records the channel and ts Slack gave a message a loop's
	// app posted, so a reply in its thread can be traced back to it.
	SetSlackRef(ctx context.Context, id int64, channelID, ts string) error
	// SetSendResult records how a surface send ended: an error and the time
	// the bridge gave up, or empty and 0 when a later attempt got through.
	// A message nobody tried to send carries neither (#147).
	SetSendResult(ctx context.Context, id int64, failedAt int64, sendErr string) error
	// SetMirror records whether a message is on the surface, one of the
	// Mirror* constants. A resolution sets it too — see ResolveSend — so
	// this is for the answers that come with no failure attached: a send
	// that got through first time, or one the surface declined to carry.
	SetMirror(ctx context.Context, id int64, mirror string) error
	// FailInterruptedSends marks every unsettled send — pending, no
	// failure — as failed with sendErr, and returns them. Called at
	// startup, before anything can send, and at shutdown, once every
	// surface has stopped: a send queue lives in the process, so a pending
	// row with no process left to send it would otherwise read as in
	// flight forever. Once failed, it is an undelivered message
	// like any other, which the operator can retry and its loop is told of.
	FailInterruptedSends(ctx context.Context, failedAt int64, sendErr string) ([]*Message, error)
	// SendFailuresToTell returns the messages a loop sent that never got
	// through and that its next turn tells it of, oldest first: those it
	// has not been told of, and the unresolved ones it has been told of
	// fewer than SendFailureTellings times (#561). The sender, not the
	// recipient: this is the loop's own news about its own words.
	//
	// A failure whose words reached their reader in the end is not among
	// them — an operator's retry that landed, or the loop's own resend —
	// because a loop told a delivered message was lost says it again and
	// the human reads it twice; nor is one the loop dismissed itself. One
	// the operator dismissed is told once: dismissal is the operator done
	// looking, not the message delivered.
	SendFailuresToTell(ctx context.Context, loopID string) ([]*Message, error)
	// UnresolvedSendFailures counts the messages a loop sent that never got
	// through and that nothing has resolved — no successful retry, no
	// dismissal (#269). Unlike SendFailuresToTell this ignores whether the
	// loop has been told: it answers the operator's question, not the
	// loop's, and an operator who was not looking is the reason the count
	// exists. There is no age limit: a failure stops counting when someone
	// deals with it, not when a clock decides they are done looking.
	UnresolvedSendFailures(ctx context.Context, loopID string) (int, error)
	// Undelivered is the same question asked as a list rather than a count:
	// unresolved failures, newest failure first. An empty loopID asks it of
	// the whole fleet; a loop's id asks it of that loop alone, and then it
	// is UnresolvedSendFailures' own scope, so the badge and the list it
	// opens cannot disagree about what they are counting (#263, #281).
	Undelivered(ctx context.Context, loopID string) ([]*Message, error)
	// ResolveSend marks a failure dealt with, at the given time, and
	// reports whether there was one to mark. Called when a retry of the row
	// gets through, and when the operator or the loop dismisses it; the row
	// keeps what failed and why either way. A row that never failed, or
	// whose failure is already resolved, is left alone and answers false —
	// which is a no-op on the send path and a 404 on the operator's.
	// resolution is one of the SendResolution* constants, saying which
	// happened; readers that care about the difference — notably
	// SendFailuresToTell — ask it rather than re-deriving it. resentAs
	// names the message that carried the words again, and is zero for
	// every resolution but a resend. The row's Mirror follows: a delivered
	// resolution put it on the surface, and the others leave it on the hub
	// for good.
	ResolveSend(ctx context.Context, id int64, at int64, resolution string, resentAs int64) (bool, error)
	// ResolveResends resolves every failure the given message was sent to
	// replace — the one its ResendsID names, the one that one named, and
	// so on — as resent by it, and reports how many it resolved. Called
	// when a send gets through: the words arrived, so every failure that
	// was an attempt to say them is dealt with, however many attempts it
	// took. A message that resends nothing resolves nothing.
	ResolveResends(ctx context.Context, messageID int64, at int64) (int, error)
	// MarkSendFailuresTold records that the loop has now been told about
	// these messages once more, counting the telling. Called once the turn
	// carrying the news has completed, so a turn that dies before it still
	// owes the news.
	MarkSendFailuresTold(ctx context.Context, ids []int64, toldAt int64) error
	// PutRef records a bot's own surface id for a message.
	PutRef(ctx context.Context, ref *SurfaceRef) error
	// Ref returns the bot's own id for a message, or ErrNotFound when that
	// bot never saw or sent it — the case where no native reply can be
	// rendered. A sighting recorded by a non-ingesting poller counts.
	Ref(ctx context.Context, messageID int64, botLoopID string) (*SurfaceRef, error)
	// Refs returns every bot's id for a message that bot sent or ingested,
	// oldest first: empty when no bot did.
	Refs(ctx context.Context, messageID int64) ([]*SurfaceRef, error)
	// RecordSighting stores a poller's own id for a telegram message it
	// observed, whether or not it was the bot that ingested it, and what
	// that poller resolved the message to be a reply to (0 = nothing).
	RecordSighting(ctx context.Context, tgKey, botLoopID string, chatID, messageID, replyToID, seenAt int64) error
	// SightedReplyTarget returns the reply target any poller's sighting of
	// a telegram message resolved, or ErrNotFound when none did (#424).
	SightedReplyTarget(ctx context.Context, tgKey string) (int64, error)
	// AdoptReplyTarget gives the ingested message with this tg_key the reply
	// target it was ingested without, and returns the updated row. It
	// changes nothing and returns ErrNotFound when there is no such row yet
	// or the row already has a target: of the two pollers that might both
	// learn the target, exactly one adopts it.
	AdoptReplyTarget(ctx context.Context, tgKey string, replyToID int64) (*Message, error)
	// ByRef resolves a surface id back to the message it belongs to, from
	// the perspective of one bot: what that bot sent, or what it saw.
	// ErrNotFound when it maps to nothing.
	ByRef(ctx context.Context, botLoopID string, chatID, tgMessageID int64) (*Message, error)
	// ByTGKey resolves a telegram message to the ingested row that shares
	// its identity, whichever bot ingested it. ErrNotFound when it was
	// never ingested.
	ByTGKey(ctx context.Context, tgKey string) (*Message, error)
	// LatestGroupPostBy finds the group message authored by one loop whose
	// text is, or begins with, the given text — the last resort for
	// identifying a reply target that no bot holds an id for, such as
	// another loop's post. "Begins with" is what a message too long for one
	// Telegram message needs, and only for its first part: the row holds
	// the whole message, so a reply to the first part is a prefix of it and
	// a reply to any later part is not text this can find. Such a reply
	// resolves to nothing and is delivered as an ordinary message — the
	// same outcome as before it was split, not a new failure.
	//
	// A message whose text is exactly this wins over one that merely starts
	// with it, however much newer that one is: they are different messages,
	// and only the first is the one quoted back. Among equals the newest
	// wins. The author is already known by then, so that choice decides
	// which message the reply is filed against, not which loop is woken.
	LatestGroupPostBy(ctx context.Context, authorLoopID, text string) (*Message, error)
}

// AttachmentStore records the files kept for messages (#123).
type AttachmentStore interface {
	// Insert records an attachment and fills in its ID.
	Insert(ctx context.Context, attachment *Attachment) error
	Get(ctx context.Context, id int64) (*Attachment, error)
	// ByMessage returns a message's attachments, in the order they came.
	ByMessage(ctx context.Context, messageID int64) ([]*Attachment, error)
	// Expire marks every attachment created before cutoff and still kept
	// as removed at removedAt, and returns them, so the caller can delete
	// their files. The rows stay.
	Expire(ctx context.Context, cutoff, removedAt int64) ([]*Attachment, error)
	// ByMessages returns the attachments of each message in messageIDs
	// that has any, by message, each in the order they came.
	ByMessages(ctx context.Context, messageIDs []int64) (map[int64][]*Attachment, error)
	// Claim gives the operator's upload id (MessageID 0) to messageID. An
	// upload that is not waiting, because it is already sent, removed or
	// unknown, is ErrNotFound.
	Claim(ctx context.Context, id, messageID int64) error
	// RemoveByMessage is Expire for the attachments of one message, kept
	// whatever their age, for a message its author deleted (ADR-0055).
	RemoveByMessage(ctx context.Context, messageID, removedAt int64) ([]*Attachment, error)
	// ExpireUnsent is Expire for uploads no message has claimed.
	ExpireUnsent(ctx context.Context, cutoff, removedAt int64) ([]*Attachment, error)
}

type TurnStore interface {
	Create(ctx context.Context, turn *Turn) error
	Finish(ctx context.Context, turn *Turn) error
	ListByLoop(ctx context.Context, loopID string, limit int) ([]*Turn, error)
	// Latest returns the loop's most recent finished turn, or ErrNotFound
	// when it has none. Its token counts are the freshest measure of how
	// full the loop's context is.
	Latest(ctx context.Context, loopID string) (*Turn, error)
	// LastCompleted returns when the newest turn any loop finished without
	// an error ended, 0 when none has: a loop has woken and done its work,
	// and the login it ran under was accepted then.
	LastCompleted(ctx context.Context) (int64, error)
	// InterruptDangling marks unfinished turns as errored (orchestrator crash).
	InterruptDangling(ctx context.Context, endedAt int64) error
	// CostSince sums the cost of the loop's turns started since then.
	CostSince(ctx context.Context, loopID string, since int64) (Spend, error)
	// SessionCost returns the session's spend so far — the highest
	// session_cost_usd recorded for it, or 0 when it has no finished turn.
	// It is how a turn is priced: the CLI hands us a running total, and the
	// difference from this is what the turn itself cost. Read from the store
	// rather than remembered, so a restart mid-session still prices the next
	// turn correctly.
	SessionCost(ctx context.Context, loopID, sessionID string) (float64, error)
}

type EventStore interface {
	Insert(ctx context.Context, event *Event) (int64, error)
	// ListByLoop returns up to limit events after afterID, oldest first —
	// the tail-following form: afterID 0 is the oldest end of the timeline.
	ListByLoop(ctx context.Context, loopID string, afterID int64, limit int) ([]*Event, error)
	// ListByLoopBefore returns the newest window instead: up to limit events
	// older than beforeID, still oldest first, so a caller reads one page the
	// same way whichever end it asked from. beforeID 0 is the newest end of
	// the timeline — the mirror of afterID 0 — which is what a page opening
	// on a long-lived loop wants (#119).
	ListByLoopBefore(ctx context.Context, loopID string, beforeID int64, limit int) ([]*Event, error)
	// DeleteBefore prunes raw events older than cutoff (retention,
	// docs/QUALITY.md). Messages and turns are never pruned.
	DeleteBefore(ctx context.Context, cutoff int64) (int64, error)
}

type ScheduleStore interface {
	Set(ctx context.Context, loopID string, nextTickAt int64) error
	Get(ctx context.Context, loopID string) (*ScheduleEntry, error)
	SetLastTick(ctx context.Context, loopID string, at int64) error
	All(ctx context.Context) ([]*ScheduleEntry, error)
	Delete(ctx context.Context, loopID string) error
}

type InboxStore interface {
	Push(ctx context.Context, loopID, envelope string, queuedAt int64) error
	// Drain returns and removes all queued envelopes for a loop, oldest first.
	Drain(ctx context.Context, loopID string) ([]string, error)
}

type SettingsStore interface {
	Get(ctx context.Context, key string) (string, error)
	Set(ctx context.Context, key, value string) error
}

// TGSender is a Telegram account known to Spool. Only 'allowed' senders can
// reach loops; everyone else is dropped. A link makes a person one
// (ADR-0051); PairCode is a pending sender's, from before it.
type TGSender struct {
	TGUserID     int64  `json:"tg_user_id"`
	Username     string `json:"username"`
	Display      string `json:"display"`
	Status       string `json:"status"` // pending|allowed|blocked
	PairCode     string `json:"pair_code"`
	FirstSeenVia string `json:"first_seen_via"` // "dm:<loop>" | "group:<loop>"
	CreatedAt    int64  `json:"created_at"`
	UpdatedAt    int64  `json:"updated_at"`
}

type TGSenderStore interface {
	Get(ctx context.Context, tgUserID int64) (*TGSender, error)
	// Create inserts a new (pending) sender; ErrDuplicate if already known.
	Create(ctx context.Context, sender *TGSender) error
	// SetStatus clears the sender's pairing code: a sender no longer
	// pending has nothing left to pair.
	SetStatus(ctx context.Context, tgUserID int64, status string, updatedAt int64) error
	List(ctx context.Context) ([]*TGSender, error)
	Delete(ctx context.Context, tgUserID int64) error
}

// SlackSender is a Slack user known to Spool: the allowlist that TGSender
// is for Telegram (operator, 2026-09-22 on #230). Only 'allowed'
// senders reach loops; workspace membership alone admits nobody.
type SlackSender struct {
	SlackUserID  string `json:"slack_user_id"`
	TeamID       string `json:"team_id"`
	Username     string `json:"username"`
	Display      string `json:"display"`
	Status       string `json:"status"` // pending|allowed|blocked
	PairCode     string `json:"pair_code"`
	FirstSeenVia string `json:"first_seen_via"` // "dm:<loop>" | "group:<loop>"
	CreatedAt    int64  `json:"created_at"`
	UpdatedAt    int64  `json:"updated_at"`
}

// The access stream carries Telegram and Slack senders alike, so each frame
// is marked with its surface (#230). Every publisher builds its frame with
// Frame, the bridge's included, so no frame goes out without the mark.

// TGSenderFrame is a Telegram sender on the access stream.
type TGSenderFrame struct {
	*TGSender
	Surface string `json:"surface"`
}

// Frame marks the sender for the access stream. It carries no pairing
// code: the stream reaches members, who read none (#689).
func (sender *TGSender) Frame() TGSenderFrame {
	framed := *sender
	framed.PairCode = ""
	return TGSenderFrame{&framed, SurfaceTelegram}
}

// SlackSenderFrame is a Slack sender on the access stream.
type SlackSenderFrame struct {
	*SlackSender
	Surface string `json:"surface"`
}

// Frame marks the sender for the access stream, with no pairing code, as
// a Telegram sender's frame has none.
func (sender *SlackSender) Frame() SlackSenderFrame {
	framed := *sender
	framed.PairCode = ""
	return SlackSenderFrame{&framed, SurfaceSlack}
}

type SlackSenderStore interface {
	Get(ctx context.Context, slackUserID string) (*SlackSender, error)
	// Create inserts a new (pending) sender; ErrDuplicate if already known.
	Create(ctx context.Context, sender *SlackSender) error
	// SetStatus is ErrNotFound for an unknown sender, and clears a known
	// one's pairing code, as TGSenderStore's does.
	SetStatus(ctx context.Context, slackUserID, status string, updatedAt int64) error
	List(ctx context.Context) ([]*SlackSender, error)
	Delete(ctx context.Context, slackUserID string) error
}

type Store interface {
	Loops() LoopStore
	Channels() ChannelStore
	Rooms() RoomStore
	Reactions() ReactionStore
	Polls() PollStore
	Connections() ConnectionStore
	LoopChanges() LoopChangeStore
	PrivateChats() PrivateChatStore
	Problems() ProblemStore
	ClaudeLogins() ClaudeLoginStore
	UserLinks() UserLinkStore
	FleetRules() FleetRuleStore
	Sessions() SessionStore
	Messages() MessageStore
	Attachments() AttachmentStore
	Turns() TurnStore
	Events() EventStore
	Schedule() ScheduleStore
	Inbox() InboxStore
	Settings() SettingsStore
	TGSenders() TGSenderStore
	SlackSenders() SlackSenderStore
	Models() ModelStore
	Users() UserStore
	UserSessions() UserSessionStore
	SignInThrottles() SignInThrottleStore
	Close() error
}

// FleetChannel is the channel every fleet starts with: the group
// conversation of ADR-0032, under the name everything already used for it
// (ADR-0038). It cannot be created or deleted, and a loop is in it unless
// the operator took it out.
const FleetChannel = "group"

// Channel is a conversation several loops and people share (ADR-0038).
type Channel struct {
	Name        string
	Description string
	CreatedAt   int64
	// LoopIDs are the loops in the channel, in no promised order.
	LoopIDs []string
}

// ValidChannelName reports whether a name may name a channel: 1–32 of
// a-z, 0-9 and '-', not starting with '-'. Underscores are left out so a
// channel can never be spelled like a private destination (owner_dm).
func ValidChannelName(name string) bool { return validHandle(name) }

// validHandle is the alphabet the operator names hub objects in: 1–32 of
// a-z, 0-9 and '-', not starting with '-'.
func validHandle(name string) bool {
	if name == "" || len(name) > 32 || name[0] == '-' {
		return false
	}
	for _, r := range name {
		lower, digit := r >= 'a' && r <= 'z', r >= '0' && r <= '9'
		if !lower && !digit && r != '-' {
			return false
		}
	}
	return true
}

// ChannelDestinationPrefix is how send_message names a channel other than
// the fleet channel: channel:<name>. The fleet channel keeps its plain name,
// group, so no prompt or row written before channels changes (ADR-0038).
const ChannelDestinationPrefix = "channel:"

// DMDestinationPrefix is how a loop's private conversation with an allowed
// sender other than its owner is named: dm:<name>, the name being how the
// identity catalog lists the person (ADR-0049). It is the message's
// conversation as stored, keyed to the loop by ConversationLoopID and to the
// person by DMPerson.
const DMDestinationPrefix = "dm:"

// PrivateThread is one person's share of a loop's private messages:
// Conversation is ConversationOwnerDM, or DMDestinationPrefix standing for
// their dm:<name> under every name, and LastAt is the newest message's TS.
type PrivateThread struct {
	Conversation string
	DMPerson     string
	LastAt       int64
}

// IsDM reports whether a conversation or destination is a private one with
// a person other than the owner.
func IsDM(conversation string) bool { return strings.HasPrefix(conversation, DMDestinationPrefix) }

// DMName is how a person is named in dm:<name>: their handle on the surface,
// or, for one with none, their user id there. Telegram handles ignore case,
// and Slack names are lowercase, so the name is folded.
func DMName(username, userID string) string {
	if username != "" {
		return strings.ToLower(username)
	}
	return strings.ToLower(userID)
}

// TGPerson and SlackPerson are a sender's stable key across surfaces, as
// private_chats and a dm message carry it.
func TGPerson(tgUserID int64) string        { return "tg:" + strconv.FormatInt(tgUserID, 10) }
func SlackPerson(slackUserID string) string { return "slack:" + slackUserID }

// PersonSurface is the surface a TGPerson or SlackPerson key belongs to.
func PersonSurface(person string) string {
	switch {
	case strings.HasPrefix(person, "tg:"):
		return SurfaceTelegram
	case strings.HasPrefix(person, "slack:"):
		return SurfaceSlack
	}
	return ""
}

// ChannelDestination names a channel as a send destination.
func ChannelDestination(name string) string {
	if name == FleetChannel {
		return ConversationGroup
	}
	return ChannelDestinationPrefix + name
}

// Destination names the conversation a message is in the way a loop sends
// to it: its kind, or channel:<name> for a channel other than the fleet
// channel.
func (message *Message) Destination() string {
	if message.Conversation == ConversationGroup && message.Channel != "" {
		return ChannelDestination(message.Channel)
	}
	return message.Conversation
}

// ChannelStore holds the hub's channels and who is in them. The fleet
// channel's membership is the loops' own OutsideFleetChannel, read and
// written through here as any other channel's is, so a caller never asks
// which channel it holds.
type ChannelStore interface {
	// List returns every channel, the fleet channel first, then by name.
	List(ctx context.Context) ([]*Channel, error)
	// Get returns one channel, or ErrNotFound.
	Get(ctx context.Context, name string) (*Channel, error)
	// Create adds a channel with no loops in it; ErrDuplicate if the name
	// is taken.
	Create(ctx context.Context, channel *Channel) error
	// SetDescription rewrites a channel's description and returns it;
	// ErrNotFound if there is none by that name.
	SetDescription(ctx context.Context, name, description string) (*Channel, error)
	// Delete removes a channel and its membership, and unbinds every room
	// bound to it; ErrNotFound if there is none. Messages said in it keep
	// its name. The fleet channel is not deletable, and the caller refuses
	// it before asking.
	Delete(ctx context.Context, name string) error
	// AddLoop puts a loop in a channel and RemoveLoop takes it out; both
	// are no-ops when it already is, or is not, there, and ErrNotFound when
	// the channel is not. at stamps the write. Taking a loop out of a
	// channel other than the fleet channel unbinds its room for it; the
	// fleet channel's room stays bound, as it did before rooms, so a loop
	// put back in finds its group where it was.
	AddLoop(ctx context.Context, name, loopID string, at int64) error
	RemoveLoop(ctx context.Context, name, loopID string, at int64) error
}

// Room is a surface's chat as one loop's bot knows it (ADR-0038): a
// Telegram group, bound to one of the loop's channels or, while Channel is
// "", to none. A bound room carries its channel both ways for that loop; an
// unbound one carries nothing and waits for the operator to bind it.
type Room struct {
	LoopID  string
	Surface string
	// RoomID is the surface's id for the chat, as text so every surface's
	// fits: a Telegram chat id in decimal.
	RoomID string
	// Title is the chat's name as the surface last reported it, "" when
	// the room was bound by id before it was ever heard from.
	Title       string
	Channel     string
	FirstSeenAt int64
	// BoundAt is when the room was bound to its channel, 0 while unbound.
	// The ingest election reads it, as it read TGGroupBoundAt (ADR-0020).
	BoundAt int64
}

// RoomStore holds every loop's rooms. A room carries one channel: per loop,
// by the (loop, channel) uniqueness, and across loops, by Bind's refusal.
type RoomStore interface {
	// List returns a loop's rooms, bound or not, newest first.
	List(ctx context.Context, loopID string) ([]*Room, error)
	// ListByRoom returns every loop's row for one chat.
	ListByRoom(ctx context.Context, surface, roomID string) ([]*Room, error)
	// Sight records a chat the loop's bot heard from: added unbound if
	// the loop had no row for it, its title refreshed if it had. It
	// returns the row as stored and whether it was added.
	Sight(ctx context.Context, room *Room) (*Room, bool, error)
	// Bind binds a loop's room to a channel, adding the room if the loop
	// had none by that id, and returns it. The room the loop had for that
	// channel, if another, goes back to unbound. Binding a room to the
	// channel it already carries keeps its BoundAt. ErrRoomInUse when
	// another loop's row binds the room to another channel.
	Bind(ctx context.Context, loopID, surface, roomID, channel string, at int64) (*Room, error)
	// Forget removes a loop's room; ErrNotFound if it had none.
	Forget(ctx context.Context, loopID, surface, roomID string) error
	// Move carries every loop's room for a chat over to the chat's new id,
	// as a Telegram group upgraded to a supergroup gets one: channel,
	// title and BoundAt kept, so the binding and the ingest election stand.
	// The unbound row a loop may already have for the new id gives way; a
	// loop whose row for it is bound keeps that one. It returns the loops
	// whose room moved, and ErrRoomInUse when the new id carries another
	// channel already.
	Move(ctx context.Context, surface, fromRoomID, toRoomID string) ([]string, error)
}

// Reaction is one reactor's emoji on one hub message (ADR-0040). It is not a
// message: it has no conversation, no recipients and no mirror state of its
// own.
type Reaction struct {
	ID        int64 `json:"id"`
	MessageID int64 `json:"message_id"`
	// ReactorKey says who reacted, the same across every bot that reported
	// it: LoopReactor's for a loop, PersonReactor's for a person.
	ReactorKey string `json:"reactor_key"`
	// Reactor is the reactor's name as it was shown when they reacted.
	Reactor string `json:"reactor"`
	// Emoji is the Unicode the surface reported, or :name: for a custom
	// emoji that has none.
	Emoji string `json:"emoji"`
	TS    int64  `json:"ts"`
	// ToldAt is when the loop that wrote the message was told of it, 0
	// until then. Engine bookkeeping, like SendFailureToldAt.
	ToldAt int64 `json:"-"`
}

// LoopReactor is the reactor key of a loop.
func LoopReactor(loopID string) string { return "loop:" + loopID }

// ReactorLoop is the loop a reactor key names, and false for a person.
func ReactorLoop(reactorKey string) (string, bool) { return strings.CutPrefix(reactorKey, "loop:") }

// PersonReactor is the reactor key of a person, by their user id on a
// surface.
func PersonReactor(surface, userID string) string { return surface + ":" + userID }

// OwnerReactor is the reactor key of the loop's owner on its own surface, ""
// when it has none.
func (loopRecord *Loop) OwnerReactor() string {
	switch {
	case !loopRecord.OwnerConfigured():
		return ""
	case loopRecord.Surface() == SurfaceTelegram:
		return PersonReactor(SurfaceTelegram, strconv.FormatInt(loopRecord.OwnerTGUserID, 10))
	default:
		return PersonReactor(loopRecord.Surface(), loopRecord.OwnerSlackUserID)
	}
}

// ReactionStore holds the hub's reactions.
type ReactionStore interface {
	// Add records a reaction, and reports whether it was new: the same
	// reactor's same emoji on the same message is one row, however many
	// bots report it. ErrNotFound when the message is not the hub's.
	Add(ctx context.Context, reaction *Reaction) (bool, error)
	// Remove deletes a reaction, and reports whether there was one.
	Remove(ctx context.Context, messageID int64, reactorKey, emoji string) (bool, error)
	// ListByMessages returns the reactions on the given messages, oldest
	// first.
	ListByMessages(ctx context.Context, messageIDs []int64) ([]*Reaction, error)
	// Untold returns the reactions on a loop's own messages, by anyone but
	// the loop itself, that it has not been told of, oldest first.
	Untold(ctx context.Context, loopID string) ([]*Reaction, error)
	// MarkTold records that the loop was told of these reactions.
	MarkTold(ctx context.Context, ids []int64, toldAt int64) error
	// Seen reports whether a person's reaction with this emoji is on any
	// message: how the hub tells a custom :name: a surface reported from
	// one a loop made up.
	Seen(ctx context.Context, emoji string) (bool, error)
}

// Poll is the ballot a poll message carries (ADR-0041). The message's text
// is the question; the ballot is kept beside it, keyed by it, and never
// changes after it is sent, except that it closes.
type Poll struct {
	MessageID int64 `json:"message_id"`
	// Options are two to ten, in order. A vote names them by index.
	Options  []string `json:"options"`
	Multiple bool     `json:"multiple"`
	// ClosesAt is when the hub closes the poll, 0 for one its author
	// closes. ClosedAt is when it closed, 0 while it is open.
	ClosesAt int64 `json:"closes_at"`
	ClosedAt int64 `json:"closed_at"`
	// CloseToldAt is when the author was told the result, 0 until then.
	// Engine bookkeeping, like Reaction.ToldAt.
	CloseToldAt int64 `json:"-"`
	// TGPollID is the id Telegram gave the poll the loop's bot sent, by
	// which Telegram reports each vote; "" for a poll no bot sent.
	TGPollID string `json:"-"`
}

// Vote is one voter's whole choice in one poll (ADR-0041). A new vote
// replaces the voter's previous one, and an empty choice retracts it.
type Vote struct {
	ID     int64 `json:"id"`
	PollID int64 `json:"poll_id"`
	// VoterKey says who voted, keyed as a reactor is: LoopReactor's for a
	// loop, PersonReactor's for a person.
	VoterKey string `json:"voter_key"`
	// Voter is the voter's name as it was shown when they voted.
	Voter string `json:"voter"`
	// Choice is the indexes of the options picked, ascending; empty once
	// retracted.
	Choice []int `json:"choice"`
	TS     int64 `json:"ts"`
	// ToldAt is when the poll's author was told of this choice, 0 until
	// then. Engine bookkeeping, like Reaction.ToldAt.
	ToldAt int64 `json:"-"`
}

// PollStore holds the hub's polls and their votes.
type PollStore interface {
	// Create records the ballot of a poll message. ErrNotFound when the
	// message is not the hub's, ErrDuplicate when it already carries one.
	Create(ctx context.Context, poll *Poll) error
	// Get returns a message's ballot. ErrNotFound when it carries none.
	Get(ctx context.Context, messageID int64) (*Poll, error)
	// ListByMessages returns the ballots the given messages carry.
	ListByMessages(ctx context.Context, messageIDs []int64) ([]*Poll, error)
	// Vote records a voter's whole choice, replacing their previous one,
	// and reports whether it changed. ErrNotFound when there is no such
	// poll, ErrPollClosed once it has closed.
	Vote(ctx context.Context, vote *Vote) (bool, error)
	// Votes returns the votes in the given polls, oldest first, retracted
	// ones included.
	Votes(ctx context.Context, pollIDs []int64) ([]*Vote, error)
	// Close closes an open poll, and reports whether it was open.
	Close(ctx context.Context, messageID, closedAt int64) (bool, error)
	// Due returns the open polls whose close time is at or before now.
	Due(ctx context.Context, now int64) ([]*Poll, error)
	// UntoldVotes returns the votes in a loop's own polls, by anyone but
	// the loop itself, whose current choice it has not been told of,
	// oldest first.
	UntoldVotes(ctx context.Context, loopID string) ([]*Vote, error)
	// MarkVotesTold records that the loop was told of these votes.
	MarkVotesTold(ctx context.Context, ids []int64, toldAt int64) error
	// UntoldCloses returns a loop's own closed polls whose result it has
	// not been told, oldest close first.
	UntoldCloses(ctx context.Context, loopID string) ([]*Poll, error)
	// MarkClosesTold records that the loop was told these polls' results.
	MarkClosesTold(ctx context.Context, messageIDs []int64, toldAt int64) error
	// SetTGPollID records the id Telegram gave the poll a bot sent.
	SetTGPollID(ctx context.Context, messageID int64, tgPollID string) error
	// ByTGPollID returns the poll Telegram knows by tgPollID. ErrNotFound
	// when no bot sent one by that id.
	ByTGPollID(ctx context.Context, tgPollID string) (*Poll, error)
}

// ErrNotFound / ErrDuplicate are sentinel errors shared by implementations.
type sentinelError string

func (sentinel sentinelError) Error() string { return string(sentinel) }

const (
	ErrNotFound  = sentinelError("store: not found")
	ErrDuplicate = sentinelError("store: duplicate")
	// ErrRoomInUse is a room that carries another channel already: a chat
	// with two channels in it would leave a person's message in neither.
	ErrRoomInUse = sentinelError("store: room carries another channel")
	// ErrConnectionAttached refuses deleting a connection a loop still
	// holds: detaching first is the operator saying the loop can do
	// without it (ADR-0043).
	ErrConnectionAttached = sentinelError("store: connection attached")
	// ErrConnectionPrivate refuses attaching a loop's private connection
	// to another loop (ADR-0043): sharing it is a step of its own.
	ErrConnectionPrivate = sentinelError("store: connection private")
	// ErrConnectionRevoked refuses attaching, sharing, setting a value on,
	// or revoking again a connection the operator revoked (ADR-0043): it
	// is gone for good, and a new one takes its place.
	ErrConnectionRevoked = sentinelError("store: connection revoked")
	// ErrPollClosed refuses a vote in a poll that has closed (ADR-0041).
	ErrPollClosed = sentinelError("store: poll closed")
	// ErrLastOwner refuses removing or demoting the hub's only owner: a
	// hub with no owner has nobody who may make one (ADR-0048).
	ErrLastOwner = sentinelError("store: last owner")
)

// Connection is an org-level tool credential or config, defined once under
// a name and attachable to loops (ADR-0043). Secret is write-only: json:"-"
// keeps it out of every API response, the rule a loop's bot token follows.
type Connection struct {
	Name      string
	Kind      string
	Config    ConnectionConfig
	Secret    string `json:"-"`
	CreatedAt int64
	// UpdatedAt is when the secret was last set: CreatedAt until it is
	// replaced.
	UpdatedAt int64
	// RotatedAt is when the secret was last replaced, 0 for never.
	RotatedAt int64
	// RevokedAt is when the connection was revoked, 0 for never. A revoked
	// one holds no value and no loop.
	RevokedAt int64
	// LoopIDs are the loops the connection is attached to, in no promised
	// order.
	LoopIDs []string
	// OwnerLoopID is the loop a private connection belongs to, and "" for
	// one the fleet shares. A private one is attached to its owner when it
	// is created, and to no other loop until it is shared.
	OwnerLoopID string
}

// The kinds a connection can be (ADR-0043).
const (
	// ConnectionEnvVar is one env var a loop's tools read, its value kept
	// like any secret whether or not it is one: a GitHub token, an API
	// key, a username.
	ConnectionEnvVar = "env-var"
	// ConnectionMCPServer is an MCP server a loop's claude can be given.
	ConnectionMCPServer = "mcp-server"
	// ConnectionGitHubApp is a GitHub App's installation: the hub holds the
	// app's private key and mints the loop short-lived installation tokens
	// on demand (#887).
	ConnectionGitHubApp = "github-app"
)

// The transports an mcp-server connection reaches its server by.
const (
	MCPTransportHTTP  = "http"
	MCPTransportStdio = "stdio"
)

// ConnectionConfig is what a connection says about itself besides its
// secret. Which fields it uses is its kind's: Env for an env-var;
// Transport, and URL or Command with Args, for an mcp-server; AppID and
// InstallationID for a github-app. It is read back in full, so nothing
// secret belongs in it.
type ConnectionConfig struct {
	Env       string   `json:"env,omitempty"`
	Transport string   `json:"transport,omitempty"`
	URL       string   `json:"url,omitempty"`
	Command   string   `json:"command,omitempty"`
	Args      []string `json:"args,omitempty"`
	// AppID is a GitHub App's App ID or its Client ID: GitHub takes either
	// as the issuer of the app's JWT.
	AppID string `json:"app_id,omitempty"`
	// InstallationID is the app's installation the tokens are minted for.
	InstallationID int64 `json:"installation_id,omitempty"`
}

// Cleartext reports whether an http mcp-server's URL would carry its secret
// unencrypted off the machine: plain http to a host that isn't loopback. A
// local server on http is fine; the bearer token never leaves the host.
func (config ConnectionConfig) Cleartext() bool {
	parsed, err := url.Parse(config.URL)
	return err == nil && parsed.Scheme == "http" && !config.Loopback()
}

// Loopback reports whether an http mcp-server's URL names this machine:
// localhost, a loopback address, or an unspecified one (0.0.0.0, ::), which
// a connect reaches this machine by too.
func (config ConnectionConfig) Loopback() bool {
	parsed, err := url.Parse(config.URL)
	if err != nil {
		return false
	}
	host := parsed.Hostname()
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && (ip.IsLoopback() || ip.IsUnspecified())
}

// Brokered reports whether the hub stands between a loop on the given
// runtime and an mcp-server connection's server (#622): every http server
// the hub can reach as the loop would. A loopback server is one only for a
// bare loop, which shares the hub's machine; for a workstation it names a
// place inside it.
func (connection *Connection) Brokered(runtime string) bool {
	return connection.Kind == ConnectionMCPServer &&
		connection.Config.Transport == MCPTransportHTTP &&
		(!connection.Config.Loopback() || runtime == RuntimeBare)
}

// ValidConnectionName reports whether a name may name a connection: the
// alphabet a channel is named in.
func ValidConnectionName(name string) bool { return validHandle(name) }

// ConnectionStore holds the hub's connections (ADR-0043).
type ConnectionStore interface {
	// List returns every connection name-sorted, secrets included: the
	// redactor redacts by them, and the API drops them.
	List(ctx context.Context) ([]*Connection, error)
	// Get is ErrNotFound for an unknown name.
	Get(ctx context.Context, name string) (*Connection, error)
	// Create is ErrDuplicate when the name is taken. A private one is
	// attached to its owner in the same step, and is ErrNotFound for an
	// unknown owner.
	Create(ctx context.Context, connection *Connection) error
	// Delete is ErrNotFound for an unknown name, and ErrConnectionAttached
	// while any loop but a private one's owner holds it. A private one is
	// detached from its owner in the same step. Its value is retired.
	Delete(ctx context.Context, name string, at int64) error
	// Share makes a private connection the fleet's, for good: its value may
	// be in another loop's env from then on. Sharing a shared one changes
	// nothing. ErrNotFound for an unknown name, ErrConnectionRevoked for a
	// revoked one.
	Share(ctx context.Context, name string, at int64) error
	// SetSecret replaces the secret, stamps UpdatedAt and RotatedAt, and
	// retires the value it replaced; setting the value it holds changes
	// nothing. ErrNotFound for an unknown name, ErrConnectionRevoked for a
	// revoked one.
	SetSecret(ctx context.Context, name, secret string, at int64) error
	// Attach gives a loop the connection; attaching it again changes
	// nothing. ErrNotFound for an unknown connection or loop,
	// ErrConnectionPrivate for another loop's private one, and
	// ErrConnectionRevoked for a revoked one.
	Attach(ctx context.Context, name, loopID string, at int64) error
	// Detach takes it away again; detaching what is not attached changes
	// nothing. ErrNotFound for an unknown connection.
	Detach(ctx context.Context, name, loopID string, at int64) error
	// Revoke detaches the connection from every loop, retires its value,
	// and stamps RevokedAt, in one step. It returns the loops it was
	// detached from. ErrNotFound for an unknown name, and
	// ErrConnectionRevoked for one revoked already.
	Revoke(ctx context.Context, name string, at int64) ([]string, error)
	// ListByLoop returns the connections attached to one loop, name-sorted,
	// secrets included for the injector.
	ListByLoop(ctx context.Context, loopID string) ([]*Connection, error)
	// Events returns the record of changes the filter matches, newest
	// first: in the order they were made, whatever their timestamps say.
	// Every write above records its change in the same step, and one that
	// changes nothing records nothing.
	Events(ctx context.Context, filter ConnectionEventFilter) ([]*ConnectionEvent, error)
	// Retired returns every value a connection has let go of, replaced or
	// deleted, for the redactor alone: no route reads it, and nothing
	// injects it.
	Retired(ctx context.Context) ([]RetiredSecret, error)
}

// RetiredSecret is a connection's value it no longer holds. RedactName is
// the name the redactor showed it under while it was live: an env-var's
// variable, or "connection:" and the connection's name.
type RetiredSecret struct {
	Connection string
	RedactName string
	Value      string
	RetiredAt  int64
}

// RedactName is the name the redactor shows a connection's value under:
// an env-var's variable, as the per-loop secret it replaced was, or
// "connection:" and the connection's name.
func (connection *Connection) RedactName() string {
	if connection.Kind == ConnectionEnvVar {
		return connection.Config.Env
	}
	return "connection:" + connection.Name
}

// ConnectionEvent is one change to a connection, on its append-only
// record (ADR-0043). It names the connection and the loop, the loop's name
// as it was, so it outlives both. LoopID and LoopName are "" for a change
// no loop is part of.
type ConnectionEvent struct {
	ID         int64
	Action     string
	Connection string
	LoopID     string
	LoopName   string
	At         int64
}

// The changes a connection's record holds.
const (
	ConnectionEventCreate = "create"
	ConnectionEventDelete = "delete"
	ConnectionEventAttach = "attach"
	ConnectionEventDetach = "detach"
	ConnectionEventShare  = "share"
	// ConnectionEventRotate is a connection's value replaced.
	ConnectionEventRotate = "rotate"
	// ConnectionEventRevoke is a connection revoked, after a detach for
	// each loop that held it.
	ConnectionEventRevoke = "revoke"
)

// ConnectionEventFilter picks a connection's record, or a loop's: by the
// loop's id while it exists, by its name once it is gone. A zero field
// matches everything.
type ConnectionEventFilter struct {
	Connection string
	LoopID     string
	LoopName   string
}

// LoopChange is one change a user or the operator token made to a loop
// through the API, on the hub's append-only record of them (ADR-0048). By
// is the user's name, or "operator" for the token. It names the loop, with
// its name as it was, so it outlives the loop.
type LoopChange struct {
	ID       int64
	At       int64
	By       string
	Action   string
	LoopID   string
	LoopName string
}

// The changes a loop's record holds: one per API route that changes a
// loop or acts on it.
const (
	LoopChangeCreate      = "create"
	LoopChangeEdit        = "edit"
	LoopChangeDelete      = "delete"
	LoopChangePause       = "pause"
	LoopChangeResume      = "resume"
	LoopChangeWake        = "wake"
	LoopChangeKill        = "kill"
	LoopChangeRotate      = "rotate"
	LoopChangeRehome      = "rehome"
	LoopChangeRestart     = "workstation_restart"
	LoopChangePowerOff    = "workstation_poweroff"
	LoopChangePowerOn     = "workstation_poweron"
	LoopChangeRecreate    = "workstation_recreate"
	LoopChangeAcknowledge = "acknowledge"
	LoopChangeOwner       = "owner"
	LoopChangeRoom        = "room"
	LoopChangeUnroom      = "unroom"
	LoopChangeAttach      = "attach"
	LoopChangeDetach      = "detach"
	LoopChangeJoin        = "join"
	LoopChangeLeave       = "leave"
)

// LoopChangeFilter narrows the record to one loop, by its id while it
// exists or by its name once it is gone; empty is every loop's. Limit 0 is
// no limit.
type LoopChangeFilter struct {
	LoopID   string
	LoopName string
	Limit    int
}

type LoopChangeStore interface {
	// Record appends a change, and names its actor on the loop: as who last
	// changed it, and for a create as who made it too. A wake changes
	// nothing about the loop, so it is recorded without touching either.
	Record(ctx context.Context, change *LoopChange) error
	// List is the record, newest first.
	List(ctx context.Context, filter LoopChangeFilter) ([]*LoopChange, error)
}

// PrivateChat is where a person other than a loop's owner messaged the
// loop privately: their chat with its Telegram bot, or their DM with its
// Slack app (ADR-0049). A loop writes first only to someone who has one.
type PrivateChat struct {
	LoopID string
	// Person is the sender, as TGPerson or SlackPerson key them.
	Person string
	// Chat is the Telegram chat id in decimal, or the Slack DM channel.
	Chat       string
	CapturedAt int64
}

type PrivateChatStore interface {
	// Capture records the chat, or moves it if the person's chat with the
	// loop changed.
	Capture(ctx context.Context, chat *PrivateChat) error
	// Get is the person's chat with the loop; ErrNotFound if they have
	// never messaged it privately.
	Get(ctx context.Context, loopID, person string) (*PrivateChat, error)
	// List is every private chat the loop has, oldest first.
	List(ctx context.Context, loopID string) ([]*PrivateChat, error)
}

// Problem is a problem the hub's health check found with a loop, open from
// Since until the operator acts on the loop or the signal clears (ADR-0053,
// #764). A loop has at most one open per symptom.
type Problem struct {
	// Symptom is SymptomWorkstationDown, SymptomTurnErrors,
	// SymptomCloudAddress or SymptomCloudConnections.
	Symptom string `json:"symptom"`
	// Since is when the problem opened, in Unix ms.
	Since int64 `json:"since"`
}

// The symptoms the health check opens a problem for, and who clears one.
const (
	// SymptomWorkstationDown: the workstation has stayed down past a grace
	// period, for a reason nobody else tells the owner about.
	SymptomWorkstationDown = "workstation_down"
	// SymptomTurnErrors: the loop's last turns all ended in an error.
	SymptomTurnErrors = "turn_errors"
	// SymptomCloudAddress: a cloud loop is held because the hub's public
	// address fails its check (ADR-0056).
	SymptomCloudAddress = "cloud_address"
	// SymptomCloudConnections: a cloud loop's session asked for its
	// connections and the hub refused, so its shell may run without them
	// (ADR-0059).
	SymptomCloudConnections = "cloud_connections"

	ClearedByOperator = "operator"
	ClearedByRecovery = "recovery"
)

type ProblemStore interface {
	// Open records an open problem since since, unless the loop already has
	// one open for symptom; opened says whether it did.
	Open(ctx context.Context, loopID, symptom string, since int64) (opened bool, err error)
	// Clear closes the loop's open problem for symptom at at, by
	// ClearedByOperator or ClearedByRecovery; cleared says whether there
	// was one.
	Clear(ctx context.Context, loopID, symptom string, at int64, by string) (cleared bool, err error)
	// ListOpen is the loop's open problems, oldest first.
	ListOpen(ctx context.Context, loopID string) ([]Problem, error)
}

// UserLink is a hub user's link to the person they are on a surface
// (#703): proved by the user sending a code from the control room to a
// loop's bot or app in a DM, and confirmed by the person (ADR-0051). A
// user has at most one per surface, and a person is linked to at most one
// user. What a member reads of loops' private conversations follows from
// their links, and a link is what makes its person an allowed sender.
type UserLink struct {
	UserID string
	// Surface is the surface Person belongs to.
	Surface string
	// Person is the sender, as TGPerson or SlackPerson key them.
	Person    string
	CreatedAt int64
}

// LinkConfirmation is a link a person is asked to confirm: a code its user
// minted reached them, and nothing is linked until they reply YES
// (ADR-0051). A person holds at most one.
type LinkConfirmation struct {
	Person string
	UserID string
	// LoopID is the loop whose bot or app the code reached.
	LoopID string
	// Username and Display are how the surface named the person, and
	// TeamID is their Slack workspace, kept for the sender a link allows.
	Username  string
	Display   string
	TeamID    string
	ExpiresAt int64
}

// LinkChange is one link made or removed, on the hub's append-only record
// of them for admins (ADR-0051). It names the user, and the loop whose bot
// or app a link's code reached, as they were, so a row outlives both.
type LinkChange struct {
	ID       int64  `json:"id"`
	At       int64  `json:"at"`
	Action   string `json:"action"`
	User     string `json:"user"`
	Surface  string `json:"surface"`
	Person   string `json:"person"`
	LoopName string `json:"loop,omitempty"`
}

// The changes the record of links holds.
const (
	LinkChangeLink   = "link"
	LinkChangeUnlink = "unlink"
)

type UserLinkStore interface {
	// SetCode records the SHA-256 of a link code a user asked for, to link
	// a person on surface, good until expiresAt, in place of any code they
	// asked for before. Times here are Unix milliseconds.
	SetCode(ctx context.Context, userID, surface, codeHash string, expiresAt int64) error
	// Claim spends the code codeHash names and asks confirmation.Person to
	// confirm a link to the code's user, in place of anything they were
	// asked before, and fills in confirmation.UserID. It is ErrNotFound for
	// a code that does not exist, expired before now or was minted for
	// another surface, and ErrDuplicate when the person is linked to
	// another user; the code is spent either way.
	Claim(ctx context.Context, codeHash string, confirmation *LinkConfirmation, now int64) error
	// Confirm links person to the user they were asked to confirm, in
	// place of that user's link on the person's surface, and makes them an
	// allowed sender there. It is ErrNotFound when the person holds no
	// confirmation open at now, and ErrDuplicate when they were linked to
	// another user meanwhile; the confirmation is spent either way.
	Confirm(ctx context.Context, person string, now int64) (*UserLink, error)
	// Waiting is the user's confirmations open at now.
	Waiting(ctx context.Context, userID string, now int64) ([]*LinkConfirmation, error)
	// List is the user's links, by surface.
	List(ctx context.Context, userID string) ([]*UserLink, error)
	// LinkedUsers is every linked person, as TGPerson or SlackPerson key
	// them, with the name of the user they are linked to. A sender missing
	// from it is not linked: allowed before ADR-0051, and kept (#731).
	LinkedUsers(ctx context.Context) (map[string]string, error)
	// Delete removes the user's link on surface, and the person's sender
	// with it, so they are a stranger again, and answers with the link it
	// removed; ErrNotFound if the user has none there.
	Delete(ctx context.Context, userID, surface string, now int64) (*UserLink, error)
	// Changes is the record of links made and removed, newest first: at
	// most limit of them when limit is above 0.
	Changes(ctx context.Context, limit int) ([]*LinkChange, error)
}

// OwnerPerson is the loop's owner on its own surface, as TGPerson or
// SlackPerson key them, or "" when it has none.
func (loopRecord *Loop) OwnerPerson() string {
	switch {
	case !loopRecord.OwnerConfigured():
		return ""
	case loopRecord.Surface() == SurfaceTelegram:
		return TGPerson(loopRecord.OwnerTGUserID)
	default:
		return SlackPerson(loopRecord.OwnerSlackUserID)
	}
}
