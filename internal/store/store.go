// Package store defines Spool's persistence interfaces and domain types.
// Implementations must stick to portable SQL (SQLite today, Postgres later):
// TEXT uuids, unix-milli INTEGER timestamps, JSON-in-TEXT for lists.
package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
)

// NewHubMCPToken mints the bearer token a loop presents to the hub's own MCP
// endpoint — hub-scoped, unrelated to any MCP connection a loop may attach
// later (ADR-0026). Implementations
// call it from Create when the caller left the token empty, so every loop
// holds one no matter which path created it.
func NewHubMCPToken() string {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		panic("store: crypto/rand unavailable: " + err.Error())
	}
	return hex.EncodeToString(b)
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

	OriginWeb           = "web"
	OriginTelegramGroup = "telegram-group"
	OriginTelegramDM    = "telegram-dm"
	OriginLoop          = "loop"

	// A message's conversation: the unit of privacy and addressing
	// (ADR-0026). The private kinds are keyed to a loop; group is shared.
	ConversationOwnerDM     = "owner_dm"     // a loop's Telegram DM with its owner
	ConversationGroup       = "group"        // the loop's bound Telegram group
	ConversationControlRoom = "control_room" // a loop's private web thread

	PacingFixed = "fixed" // orchestrator interval; trailer optional
	PacingSelf  = "self"  // the loop schedules itself via trailers; interval is a fallback

	RuntimeBare   = "bare"   // host subprocess, uncontained (ADR-0017)
	RuntimeDocker = "docker" // long-lived container + volume workstation

	SenderPending = "pending"
	SenderAllowed = "allowed"
	SenderBlocked = "blocked"
)

// SettingClaudeOAuthToken is the settings key holding the operator's
// `claude setup-token` output. It is injected into every workstation exec as
// CLAUDE_CODE_OAUTH_TOKEN so contained loops run under the operator's own
// Claude login. Write-only through the API; never logged.
const SettingClaudeOAuthToken = "claude_oauth_token"

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
	TGGroupChatID int64  `json:"tg_group_chat_id"`
	// HubMCPToken is the bearer token this loop's claude process presents to
	// the hub's MCP endpoint (ADR-0026). Minted at creation, immutable, and
	// secret: json:"-" keeps it out of every API response, like TGBotToken.
	HubMCPToken string `json:"-"`
	// WorkstationOff records that the operator switched this loop's
	// workstation off. Intent, not observation: a health poll cannot tell a
	// halted workstation from a dead one (ADR-0021). Surfaced to the UI as
	// down_reason, not as a field of its own.
	WorkstationOff bool `json:"-"`
	// TGGroupBoundAt is when this loop's bot bound to that group. A bot only
	// ingests group messages Telegram dated after it — see ADR-0020.
	TGGroupBoundAt int64 `json:"-"`

	Status           string `json:"status"`
	CurrentSessionID string `json:"current_session_id"`
	CurrentPID       int    `json:"current_pid"`
	CreatedAt        int64  `json:"created_at"`
	UpdatedAt        int64  `json:"updated_at"`
}

// LoopSecret is one per-loop secret env var: a name/value pair injected into
// every workstation exec as a tool credential. Value is write-only — json:"-"
// keeps it out of every API response, the same rule a loop's bot token follows.
type LoopSecret struct {
	LoopID    string `json:"loop_id"`
	Name      string `json:"name"`
	Value     string `json:"-"`
	UpdatedAt int64  `json:"updated_at"`
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
}

type Turn struct {
	ID        string `json:"id"`
	LoopID    string `json:"loop_id"`
	SessionID string `json:"session_id"`
	Trigger   string `json:"trigger"`
	// Model the turn actually ran on, as the CLI reported it at init — not
	// the loop's configured string, which may be empty or an alias.
	Model            string  `json:"model,omitempty"`
	StartedAt        int64   `json:"started_at"`
	EndedAt          int64   `json:"ended_at"`
	IsError          bool    `json:"is_error"`
	ResultText       string  `json:"result_text"`
	CostUSD          float64 `json:"cost_usd"`
	InputTokens      int     `json:"input_tokens"`
	OutputTokens     int     `json:"output_tokens"`
	CacheReadTokens  int     `json:"cache_read_tokens"`
	CacheWriteTokens int     `json:"cache_write_tokens"`
	DurationMS       int64   `json:"duration_ms"`
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

type LoopStore interface {
	Create(ctx context.Context, l *Loop) error
	Update(ctx context.Context, l *Loop) error
	Delete(ctx context.Context, id string) error
	Get(ctx context.Context, id string) (*Loop, error)
	GetByName(ctx context.Context, name string) (*Loop, error)
	// GetByHubMCPToken resolves the loop presenting a bearer token to the
	// hub's MCP endpoint; ErrNotFound for an unknown token.
	GetByHubMCPToken(ctx context.Context, token string) (*Loop, error)
	List(ctx context.Context) ([]*Loop, error)
	SetRuntime(ctx context.Context, id, sessionID string, pid int) error
}

// LoopSecretStore holds a loop's secret env vars. Callers pass the timestamp
// (the store never reads the clock), matching the rest of the interfaces.
type LoopSecretStore interface {
	// Set upserts one secret by (loopID, name).
	Set(ctx context.Context, loopID, name, value string, updatedAt int64) error
	Delete(ctx context.Context, loopID, name string) error
	// List returns a loop's secrets name-sorted, values included: the injector
	// needs the values; the API maps these rows to names only.
	List(ctx context.Context, loopID string) ([]*LoopSecret, error)
}

// FleetRuleStore holds the fleet's rules. List returns them in creation
// order, enabled or not, so the rendered section stays stable between wakes
// and the API can show disabled rules alongside the live ones.
type FleetRuleStore interface {
	Create(ctx context.Context, r *FleetRule) error
	Update(ctx context.Context, r *FleetRule) error
	Delete(ctx context.Context, id string) error
	Get(ctx context.Context, id string) (*FleetRule, error)
	List(ctx context.Context) ([]*FleetRule, error)
}

type SessionStore interface {
	Create(ctx context.Context, s *Session) error
	End(ctx context.Context, id, reason string, endedAt int64) error
	ListByLoop(ctx context.Context, loopID string, limit int) ([]*Session, error)
	// EndDangling closes any sessions left open (orchestrator crash).
	EndDangling(ctx context.Context, reason string, endedAt int64) error
}

type MessageStore interface {
	// Insert persists a message. For telegram-sourced messages, (tgChatID,
	// tgMessageID) is unique; a duplicate returns ErrDuplicate.
	Insert(ctx context.Context, m *Message) error
	SetDelivered(ctx context.Context, id int64, deliveredTo []string) error
	List(ctx context.Context, limit int) ([]*Message, error)
	// OwnerDMChat returns the telegram chat of the loop's owner_dm
	// conversation, captured from its latest inbound DM; ErrNotFound when no
	// DM was ever ingested. Interim owner-DM address until a configured
	// owner identity replaces the capture (tracked as part of ADR-0025/0026
	// follow-up work).
	OwnerDMChat(ctx context.Context, loopID string) (int64, error)
}

type TurnStore interface {
	Create(ctx context.Context, t *Turn) error
	Finish(ctx context.Context, t *Turn) error
	ListByLoop(ctx context.Context, loopID string, limit int) ([]*Turn, error)
	// Latest returns the loop's most recent finished turn, or ErrNotFound
	// when it has none. Its token counts are the freshest measure of how
	// full the loop's context is.
	Latest(ctx context.Context, loopID string) (*Turn, error)
	// InterruptDangling marks unfinished turns as errored (orchestrator crash).
	InterruptDangling(ctx context.Context, endedAt int64) error
	CostSince(ctx context.Context, loopID string, since int64) (float64, error)
}

type EventStore interface {
	Insert(ctx context.Context, e *Event) (int64, error)
	ListByLoop(ctx context.Context, loopID string, afterID int64, limit int) ([]*Event, error)
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
	Create(ctx context.Context, s *TGSender) error
	SetStatus(ctx context.Context, tgUserID int64, status string, updatedAt int64) error
	List(ctx context.Context) ([]*TGSender, error)
	Delete(ctx context.Context, tgUserID int64) error
}

type Store interface {
	Loops() LoopStore
	LoopSecrets() LoopSecretStore
	FleetRules() FleetRuleStore
	Sessions() SessionStore
	Messages() MessageStore
	Turns() TurnStore
	Events() EventStore
	Schedule() ScheduleStore
	Inbox() InboxStore
	Settings() SettingsStore
	TGSenders() TGSenderStore
	Close() error
}

// ErrNotFound / ErrDuplicate are sentinel errors shared by implementations.
type sentinelError string

func (e sentinelError) Error() string { return string(e) }

const (
	ErrNotFound  = sentinelError("store: not found")
	ErrDuplicate = sentinelError("store: duplicate")
)
