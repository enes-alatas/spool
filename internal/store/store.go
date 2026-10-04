// Package store defines Spool's persistence interfaces and domain types.
// Implementations must stick to portable SQL (SQLite today, Postgres later):
// TEXT uuids, unix-milli INTEGER timestamps, JSON-in-TEXT for lists.
package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
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

	TriggerTick     = "tick"
	TriggerMessage  = "message"
	TriggerManual   = "manual"
	TriggerRotation = "rotation" // the handoff turn a context rotation injects

	// Why a context rotation was taken, which decides what the handoff turn
	// and the fresh session after it are told (#272).
	RotationReasonFill     = "fill"     // the context window crossed a rotation threshold
	RotationReasonOperator = "operator" // the operator asked for one outright
	RotationReasonMission  = "mission"  // the operator rewrote the loop's mission

	OriginWeb           = "web"
	OriginTelegramGroup = "telegram-group"
	OriginTelegramDM    = "telegram-dm"
	OriginSlackChannel  = "slack-channel"
	OriginSlackDM       = "slack-dm"
	OriginLoop          = "loop"

	// A message's conversation: the unit of privacy and addressing
	// (ADR-0026). The private kinds are keyed to a loop; group is shared.
	ConversationOwnerDM     = "owner_dm"     // a loop's Telegram DM with its owner
	ConversationGroup       = "group"        // a channel: the fleet channel, or another by Message.Channel
	ConversationControlRoom = "control_room" // a loop's private web thread

	PacingFixed = "fixed" // orchestrator interval; trailer optional
	PacingSelf  = "self"  // the loop schedules itself via trailers; interval is a fallback

	RuntimeBare   = "bare"   // host subprocess, uncontained (ADR-0017)
	RuntimeDocker = "docker" // long-lived container + volume workstation

	// A loop's surface: the chat platform it has an identity on, if any
	// (ADR-0029: at most one, attached after the loop exists).
	SurfaceTelegram = "telegram"
	SurfaceSlack    = "slack"

	SenderPending = "pending"
	SenderAllowed = "allowed"
	SenderBlocked = "blocked"
)

// SettingClaudeOAuthToken is the settings key holding the operator's
// `claude setup-token` output. It is injected into every workstation exec as
// CLAUDE_CODE_OAUTH_TOKEN so contained loops run under the operator's own
// Claude login. Write-only through the API; never logged.
const SettingClaudeOAuthToken = "claude_oauth_token"

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

// SettingOnboardingCompleted is set the first time the hub finds
// every onboarding pillar done (#580). It is what keeps the first-run page
// aside once the operator is through it, whatever a pillar reads later, and
// deleting the fleet's last loop clears it, in the same transaction.
const SettingOnboardingCompleted = "onboarding_completed"

// Context-rotation thresholds (ADR-0022), stored as integer percentages of
// the model's context window. A loop arms rotation at the first and stops
// waiting for a quiet boundary at the second.
const (
	SettingContextArmPercent   = "context_arm_percent"
	SettingContextForcePercent = "context_force_percent"
)

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
	Runtime string  `json:"runtime"` // bare|docker
	Image   string  `json:"image"`   // workstation image; "" = the server default
	MemMB   int     `json:"mem_mb"`  // workstation memory limit
	CPUs    float64 `json:"cpus"`    // workstation CPU limit

	TickIntervalSec int    `json:"tick_interval_sec"`
	MinWakeSec      int    `json:"min_wake_sec"`
	MaxWakeSec      int    `json:"max_wake_sec"`
	IdleTimeoutSec  int    `json:"idle_timeout_sec"`
	Pacing          string `json:"pacing"` // fixed|self
	Effort          string `json:"effort"` // ""|low|medium|high|xhigh|max

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

	CreatedAt int64 `json:"created_at"`
	UpdatedAt int64 `json:"updated_at"`
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
	// ConversationLoopID keys the private conversation kinds (owner_dm,
	// control_room) to their loop; empty for the shared group.
	ConversationLoopID string `json:"conversation_loop_id,omitempty"`
	// Channel names the channel a group message was said in, FleetChannel
	// for the fleet channel; empty for the private kinds, which are in none
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
}

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
}

// Attachment kinds: an image is presented with its dimensions and sent as
// native media where the surface has it; anything else is a file.
const (
	AttachmentImage = "image"
	AttachmentFile  = "file"
)

// Why an attachment has no file (Attachment.NotKept).
const (
	NotKeptTooLarge = "too_large"    // over the size limit; never downloaded
	NotKeptFailed   = "fetch_failed" // the surface would not hand it over
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
	// and SessionCostUSD the one that is not.
	CostUSD float64 `json:"cost_usd"`
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
	Mission *string
	Model   *string
	Effort  *string
	Pacing  *string

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
	Delete(ctx context.Context, id string) error
	Get(ctx context.Context, id string) (*Loop, error)
	GetByName(ctx context.Context, name string) (*Loop, error)
	// GetByHubMCPToken resolves the loop presenting a bearer token to the
	// hub's MCP endpoint; ErrNotFound for an unknown token.
	GetByHubMCPToken(ctx context.Context, token string) (*Loop, error)
	List(ctx context.Context) ([]*Loop, error)
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
	// SetModelRefusal records that the API refused model, with the CLI's
	// sentence, if model is still the loop's: a refusal of a model the
	// operator has already replaced is not written. Written from the actor
	// goroutine; an edit of the model clears it in the same statement (#289).
	SetModelRefusal(ctx context.Context, id, model, refusal string, updatedAt int64) error
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
	// A private kind — ConversationOwnerDM or ConversationControlRoom — is
	// keyed to its loop. ConversationGroup is every channel's, the hub's
	// rather than any loop's (ADR-0032), so its rows carry no loop key and
	// it is asked for with loopID ""; ListChannel reads one channel.
	ListConversation(ctx context.Context, kind, loopID string, limit int) ([]*Message, error)
	// ListChannel returns the messages said in one channel, newest first:
	// the fleet channel's under FleetChannel, and no other's (ADR-0038).
	ListChannel(ctx context.Context, name string, limit int) ([]*Message, error)
	// Get returns one message by id, or ErrNotFound. Reply targets are
	// resolved through it.
	Get(ctx context.Context, id int64) (*Message, error)
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
	CostSince(ctx context.Context, loopID string, since int64) (float64, error)
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
// reach loops; everyone else is dropped (with a pairing code offered on DM).
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
	SetStatus(ctx context.Context, tgUserID int64, status string, updatedAt int64) error
	List(ctx context.Context) ([]*TGSender, error)
	Delete(ctx context.Context, tgUserID int64) error
}

// SlackSender is a Slack user known to Spool: the pairing allowlist that
// TGSender is for Telegram (operator, 2026-09-22 on #230). Only 'allowed'
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

// Frame marks the sender for the access stream.
func (sender *TGSender) Frame() TGSenderFrame { return TGSenderFrame{sender, SurfaceTelegram} }

// SlackSenderFrame is a Slack sender on the access stream.
type SlackSenderFrame struct {
	*SlackSender
	Surface string `json:"surface"`
}

// Frame marks the sender for the access stream.
func (sender *SlackSender) Frame() SlackSenderFrame { return SlackSenderFrame{sender, SurfaceSlack} }

type SlackSenderStore interface {
	Get(ctx context.Context, slackUserID string) (*SlackSender, error)
	// Create inserts a new (pending) sender; ErrDuplicate if already known.
	Create(ctx context.Context, sender *SlackSender) error
	// SetStatus is ErrNotFound for an unknown sender.
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
	// ErrPollClosed refuses a vote in a poll that has closed (ADR-0041).
	ErrPollClosed = sentinelError("store: poll closed")
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
	// LoopIDs are the loops the connection is attached to, in no promised
	// order.
	LoopIDs []string
}

// The kinds a connection can be (ADR-0043).
const (
	// ConnectionEnvVar is one env var a loop's tools read, its value kept
	// like any secret whether or not it is one: a GitHub token, an API
	// key, a username.
	ConnectionEnvVar = "env-var"
	// ConnectionMCPServer is an MCP server a loop's claude can be given.
	ConnectionMCPServer = "mcp-server"
)

// The transports an mcp-server connection reaches its server by.
const (
	MCPTransportHTTP  = "http"
	MCPTransportStdio = "stdio"
)

// ConnectionConfig is what a connection says about itself besides its
// secret. Which fields it uses is its kind's: Env for an env-var;
// Transport, and URL or Command with Args, for an mcp-server. It is read
// back in full, so nothing secret belongs in it.
type ConnectionConfig struct {
	Env       string   `json:"env,omitempty"`
	Transport string   `json:"transport,omitempty"`
	URL       string   `json:"url,omitempty"`
	Command   string   `json:"command,omitempty"`
	Args      []string `json:"args,omitempty"`
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
	// Create is ErrDuplicate when the name is taken.
	Create(ctx context.Context, connection *Connection) error
	// Delete is ErrNotFound for an unknown name, and ErrConnectionAttached
	// while any loop holds it.
	Delete(ctx context.Context, name string) error
	// SetSecret replaces the secret and stamps UpdatedAt. ErrNotFound for
	// an unknown name.
	SetSecret(ctx context.Context, name, secret string, at int64) error
	// Attach gives a loop the connection; attaching it again changes
	// nothing. ErrNotFound for an unknown connection or loop.
	Attach(ctx context.Context, name, loopID string, at int64) error
	// Detach takes it away again; detaching what is not attached changes
	// nothing. ErrNotFound for an unknown connection.
	Detach(ctx context.Context, name, loopID string) error
	// ListByLoop returns the connections attached to one loop, name-sorted,
	// secrets included for the injector.
	ListByLoop(ctx context.Context, loopID string) ([]*Connection, error)
}
