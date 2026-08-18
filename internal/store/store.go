// Package store defines Spool's persistence interfaces and domain types.
// Implementations must stick to portable SQL (SQLite today, Postgres later):
// TEXT uuids, unix-milli INTEGER timestamps, JSON-in-TEXT for lists.
package store

import (
	"context"
)

const (
	WorkspaceNone     = "none"
	WorkspaceDir      = "dir"
	WorkspaceWorktree = "worktree"

	StatusActive   = "active"
	StatusPaused   = "paused"
	StatusArchived = "archived"

	EndReasonIdle   = "idle"
	EndReasonKilled = "killed"
	EndReasonCrash  = "crash"
	EndReasonLost   = "lost"

	TriggerTick    = "tick"
	TriggerMessage = "message"
	TriggerManual  = "manual"

	OriginWeb           = "web"
	OriginTelegramGroup = "telegram-group"
	OriginTelegramDM    = "telegram-dm"
	OriginLoop          = "loop"

	PacingFixed = "fixed" // orchestrator interval; trailer optional
	PacingSelf  = "self"  // the loop schedules itself via trailers; interval is a fallback

	RuntimeBare   = "bare"   // host subprocess, uncontained (ADR-0017)
	RuntimeDocker = "docker" // long-lived container + volume workstation

	SenderPending = "pending"
	SenderAllowed = "allowed"
	SenderBlocked = "blocked"
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

	Status           string `json:"status"`
	CurrentSessionID string `json:"current_session_id"`
	CurrentPID       int    `json:"current_pid"`
	CreatedAt        int64  `json:"created_at"`
	UpdatedAt        int64  `json:"updated_at"`
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
	DeliveredTo []string `json:"delivered_to"`
}

type Turn struct {
	ID               string  `json:"id"`
	LoopID           string  `json:"loop_id"`
	SessionID        string  `json:"session_id"`
	Trigger          string  `json:"trigger"`
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
	List(ctx context.Context) ([]*Loop, error)
	SetRuntime(ctx context.Context, id, sessionID string, pid int) error
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
}

type TurnStore interface {
	Create(ctx context.Context, t *Turn) error
	Finish(ctx context.Context, t *Turn) error
	ListByLoop(ctx context.Context, loopID string, limit int) ([]*Turn, error)
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
