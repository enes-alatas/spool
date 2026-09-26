// Package sqlite implements store.Store on modernc.org/sqlite (pure Go).
package sqlite

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"sort"
	"strings"

	"github.com/enes-alatas/spool/internal/store"
	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

type DB struct {
	db *sql.DB
}

func Open(path string) (*DB, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// modernc/sqlite serializes writes; a single connection avoids
	// SQLITE_BUSY churn under concurrent goroutines.
	db.SetMaxOpenConns(1)
	database := &DB{db: db}
	if err := database.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return database, nil
}

func (database *DB) migrate() error {
	if _, err := database.db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (version TEXT PRIMARY KEY)`); err != nil {
		return err
	}
	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	for _, name := range names {
		var applied int
		if err := database.db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version=?`, name).Scan(&applied); err != nil {
			return err
		}
		if applied > 0 {
			continue
		}
		body, err := fs.ReadFile(migrationsFS, "migrations/"+name)
		if err != nil {
			return err
		}
		tx, err := database.db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(string(body)); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %s: %w", name, err)
		}
		if _, err := tx.Exec(`INSERT INTO schema_migrations (version) VALUES (?)`, name); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

func (database *DB) Close() error { return database.db.Close() }

func (database *DB) Loops() store.LoopStore             { return loops{database.db} }
func (database *DB) LoopSecrets() store.LoopSecretStore { return loopSecrets{database.db} }
func (database *DB) FleetRules() store.FleetRuleStore   { return fleetRules{database.db} }
func (database *DB) Sessions() store.SessionStore       { return sessions{database.db} }
func (database *DB) Messages() store.MessageStore       { return messages{database.db} }
func (database *DB) Turns() store.TurnStore             { return turns{database.db} }
func (database *DB) Events() store.EventStore           { return events{database.db} }
func (database *DB) Schedule() store.ScheduleStore      { return schedule{database.db} }
func (database *DB) Inbox() store.InboxStore            { return inbox{database.db} }
func (database *DB) Settings() store.SettingsStore      { return settings{database.db} }
func (database *DB) TGSenders() store.TGSenderStore     { return tgSenders{database.db} }
func (database *DB) Models() store.ModelStore           { return models{database.db} }

func toJSON(values []string) string {
	if values == nil {
		values = []string{}
	}
	encoded, _ := json.Marshal(values)
	return string(encoded)
}

func fromJSON(encoded string) []string {
	var out []string
	_ = json.Unmarshal([]byte(encoded), &out)
	return out
}

// --- loops ---

type loops struct{ db *sql.DB }

const loopCols = `id, name, mission, model, workspace_mode, workspace_path, repo_path,
	worktree_path, branch, tick_interval_sec, min_wake_sec, max_wake_sec, idle_timeout_sec,
	pacing, effort, tg_bot_token, tg_bot_username, tg_group_chat_id, tg_group_bound_at,
	owner_tg_user_id, owner_dm_chat_id,
	workstation_off, outside_fleet_channel, status, current_session_id, current_pid, created_at, updated_at,
	runtime, image, mem_mb, cpus, hub_mcp_token, rotate_pending, rotate_reason, handoff_note, prompt_hash, model_refusal`

func scanLoop(row interface{ Scan(...any) error }) (*store.Loop, error) {
	var loopRecord store.Loop
	err := row.Scan(&loopRecord.ID, &loopRecord.Name, &loopRecord.Mission, &loopRecord.Model, &loopRecord.WorkspaceMode, &loopRecord.WorkspacePath,
		&loopRecord.RepoPath, &loopRecord.WorktreePath, &loopRecord.Branch, &loopRecord.TickIntervalSec, &loopRecord.MinWakeSec,
		&loopRecord.MaxWakeSec, &loopRecord.IdleTimeoutSec, &loopRecord.Pacing, &loopRecord.Effort, &loopRecord.TGBotToken,
		&loopRecord.TGBotUsername, &loopRecord.TGGroupChatID, &loopRecord.TGGroupBoundAt,
		&loopRecord.OwnerTGUserID, &loopRecord.OwnerDMChatID, &loopRecord.WorkstationOff, &loopRecord.OutsideFleetChannel,
		&loopRecord.Status, &loopRecord.CurrentSessionID, &loopRecord.CurrentPID,
		&loopRecord.CreatedAt, &loopRecord.UpdatedAt, &loopRecord.Runtime, &loopRecord.Image, &loopRecord.MemMB, &loopRecord.CPUs, &loopRecord.HubMCPToken,
		&loopRecord.RotatePending, &loopRecord.RotateReason, &loopRecord.HandoffNote, &loopRecord.PromptHash, &loopRecord.ModelRefusal)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &loopRecord, nil
}

func (table loops) Create(ctx context.Context, loopRecord *store.Loop) error {
	// Workstation columns (runtime, image, mem_mb, cpus) and hub_mcp_token are
	// set here and deliberately absent from Update: immutable after creation
	// (ADR-0018, ADR-0026), the same enforcement-by-omission as
	// current_session_id/current_pid.
	if loopRecord.HubMCPToken == "" {
		loopRecord.HubMCPToken = store.NewHubMCPToken()
	}
	_, err := table.db.ExecContext(ctx, `INSERT INTO loops (`+loopCols+`) VALUES
		(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		loopRecord.ID, loopRecord.Name, loopRecord.Mission, loopRecord.Model, loopRecord.WorkspaceMode, loopRecord.WorkspacePath, loopRecord.RepoPath,
		loopRecord.WorktreePath, loopRecord.Branch, loopRecord.TickIntervalSec, loopRecord.MinWakeSec, loopRecord.MaxWakeSec,
		loopRecord.IdleTimeoutSec, loopRecord.Pacing, loopRecord.Effort, loopRecord.TGBotToken, loopRecord.TGBotUsername,
		loopRecord.TGGroupChatID, loopRecord.TGGroupBoundAt, loopRecord.OwnerTGUserID, loopRecord.OwnerDMChatID,
		loopRecord.WorkstationOff, loopRecord.OutsideFleetChannel, loopRecord.Status, loopRecord.CurrentSessionID, loopRecord.CurrentPID,
		loopRecord.CreatedAt, loopRecord.UpdatedAt,
		loopRecord.Runtime, loopRecord.Image, loopRecord.MemMB, loopRecord.CPUs, loopRecord.HubMCPToken, loopRecord.RotatePending, loopRecord.RotateReason,
		loopRecord.HandoffNote, loopRecord.PromptHash, loopRecord.ModelRefusal)
	if err != nil && strings.Contains(err.Error(), "UNIQUE") {
		return store.ErrDuplicate
	}
	return err
}

// Edit writes the named fields in one statement and reads the row back, so
// the caller's copy of the columns it did not write is the stored one rather
// than whatever it read before the edit (#164).
func (table loops) Edit(ctx context.Context, id string, edit store.LoopEdit) (*store.Loop, error) {
	sets := []string{"updated_at=?"}
	args := []any{edit.UpdatedAt}
	set := func(col string, val any) {
		sets = append(sets, col+"=?")
		args = append(args, val)
	}
	if edit.Mission != nil {
		set("mission", *edit.Mission)
	}
	if edit.Model != nil {
		set("model", *edit.Model)
		// a refusal was of the model being replaced; the next turn is the
		// new one's check (#289)
		set("model_refusal", "")
	}
	if edit.Effort != nil {
		set("effort", *edit.Effort)
	}
	if edit.Pacing != nil {
		set("pacing", *edit.Pacing)
	}
	if edit.TickIntervalSec != nil {
		set("tick_interval_sec", *edit.TickIntervalSec)
	}
	if edit.MinWakeSec != nil {
		set("min_wake_sec", *edit.MinWakeSec)
	}
	if edit.MaxWakeSec != nil {
		set("max_wake_sec", *edit.MaxWakeSec)
	}
	if edit.IdleTimeoutSec != nil {
		set("idle_timeout_sec", *edit.IdleTimeoutSec)
	}
	if edit.TGBotToken != nil {
		set("tg_bot_token", *edit.TGBotToken)
	}
	if edit.TGBotUsername != nil {
		set("tg_bot_username", *edit.TGBotUsername)
	}
	if edit.OutsideFleetChannel != nil {
		set("outside_fleet_channel", *edit.OutsideFleetChannel)
	}
	if edit.ClearGroupBinding {
		set("tg_group_chat_id", 0)
		set("tg_group_bound_at", 0)
	}
	if _, err := table.db.ExecContext(ctx,
		`UPDATE loops SET `+strings.Join(sets, ", ")+` WHERE id=?`, append(args, id)...); err != nil {
		return nil, err
	}
	return table.Get(ctx, id)
}

func (table loops) Delete(ctx context.Context, id string) error {
	_, err := table.db.ExecContext(ctx, `DELETE FROM loops WHERE id=?`, id)
	return err
}

func (table loops) Get(ctx context.Context, id string) (*store.Loop, error) {
	return scanLoop(table.db.QueryRowContext(ctx, `SELECT `+loopCols+` FROM loops WHERE id=?`, id))
}

func (table loops) GetByName(ctx context.Context, name string) (*store.Loop, error) {
	return scanLoop(table.db.QueryRowContext(ctx, `SELECT `+loopCols+` FROM loops WHERE name=?`, name))
}

func (table loops) GetByHubMCPToken(ctx context.Context, token string) (*store.Loop, error) {
	if token == "" {
		// Every pre-backfill row would match ''; an empty bearer is never valid.
		return nil, store.ErrNotFound
	}
	return scanLoop(table.db.QueryRowContext(ctx, `SELECT `+loopCols+` FROM loops WHERE hub_mcp_token=?`, token))
}

func (table loops) List(ctx context.Context) ([]*store.Loop, error) {
	rows, err := table.db.QueryContext(ctx, `SELECT `+loopCols+` FROM loops ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*store.Loop
	for rows.Next() {
		loopRecord, err := scanLoop(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, loopRecord)
	}
	return out, rows.Err()
}

func (table loops) SetRuntime(ctx context.Context, id, sessionID string, pid int) error {
	_, err := table.db.ExecContext(ctx,
		`UPDATE loops SET current_session_id=?, current_pid=? WHERE id=?`, sessionID, pid, id)
	return err
}

func (table loops) SetRotation(ctx context.Context, id string, pending bool, reason, note string) error {
	_, err := table.db.ExecContext(ctx,
		`UPDATE loops SET rotate_pending=?, rotate_reason=?, handoff_note=? WHERE id=?`, pending, reason, note, id)
	return err
}

func (table loops) SetPromptHash(ctx context.Context, id, hash string) error {
	_, err := table.db.ExecContext(ctx,
		`UPDATE loops SET prompt_hash=? WHERE id=?`, hash, id)
	return err
}

func (table loops) SetGroupBinding(ctx context.Context, id string, chatID, boundAt, updatedAt int64) error {
	_, err := table.db.ExecContext(ctx,
		`UPDATE loops SET tg_group_chat_id=?, tg_group_bound_at=?, updated_at=? WHERE id=?`,
		chatID, boundAt, updatedAt, id)
	return err
}

func (table loops) SetOwner(ctx context.Context, id string, tgUserID, dmChatID, updatedAt int64) error {
	res, err := table.db.ExecContext(ctx,
		`UPDATE loops SET owner_tg_user_id=?, owner_dm_chat_id=?, updated_at=? WHERE id=?`,
		tgUserID, dmChatID, updatedAt, id)
	if err != nil {
		return err
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (table loops) SetOwnerDMChat(ctx context.Context, id string, chatID, updatedAt int64) error {
	_, err := table.db.ExecContext(ctx,
		`UPDATE loops SET owner_dm_chat_id=?, updated_at=? WHERE id=?`, chatID, updatedAt, id)
	return err
}

func (table loops) SetStatus(ctx context.Context, id, status string, updatedAt int64) error {
	_, err := table.db.ExecContext(ctx,
		`UPDATE loops SET status=?, updated_at=? WHERE id=?`, status, updatedAt, id)
	return err
}

func (table loops) SetWorkstationOff(ctx context.Context, id string, off bool, updatedAt int64) error {
	_, err := table.db.ExecContext(ctx,
		`UPDATE loops SET workstation_off=?, updated_at=? WHERE id=?`, off, updatedAt, id)
	return err
}

func (table loops) SetModelRefusal(ctx context.Context, id, model, refusal string, updatedAt int64) error {
	_, err := table.db.ExecContext(ctx,
		`UPDATE loops SET model_refusal=?, updated_at=? WHERE id=? AND model=?`, refusal, updatedAt, id, model)
	return err
}

// --- sessions ---

type sessions struct{ db *sql.DB }

func (table sessions) Create(ctx context.Context, session *store.Session) error {
	_, err := table.db.ExecContext(ctx,
		`INSERT INTO sessions (id, loop_id, started_at) VALUES (?,?,?)`,
		session.ID, session.LoopID, session.StartedAt)
	return err
}

func (table sessions) End(ctx context.Context, id, reason string, endedAt int64) error {
	_, err := table.db.ExecContext(ctx,
		`UPDATE sessions SET ended_at=?, end_reason=? WHERE id=? AND ended_at=0`, endedAt, reason, id)
	return err
}

func (table sessions) ListByLoop(ctx context.Context, loopID string, limit int) ([]*store.Session, error) {
	rows, err := table.db.QueryContext(ctx, `SELECT id, loop_id, started_at, ended_at, end_reason
		FROM sessions WHERE loop_id=? ORDER BY started_at DESC LIMIT ?`, loopID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*store.Session
	for rows.Next() {
		var session store.Session
		if err := rows.Scan(&session.ID, &session.LoopID, &session.StartedAt, &session.EndedAt, &session.EndReason); err != nil {
			return nil, err
		}
		out = append(out, &session)
	}
	return out, rows.Err()
}

func (table sessions) EndDangling(ctx context.Context, reason string, endedAt int64) error {
	_, err := table.db.ExecContext(ctx,
		`UPDATE sessions SET ended_at=?, end_reason=? WHERE ended_at=0`, endedAt, reason)
	return err
}

// --- messages ---

type messages struct{ db *sql.DB }

func (table messages) Insert(ctx context.Context, message *store.Message) error {
	if message.Mirror == "" {
		// The column's promise is a spelled value on every row. A writer
		// that has no surface in view — a test fixture, a future path — has
		// written a message that is on the hub only.
		message.Mirror = store.MirrorNotMirrored
	}
	var tgChat, tgMsg any
	if message.TGChatID != 0 || message.TGMessageID != 0 {
		tgChat, tgMsg = message.TGChatID, message.TGMessageID
	}
	res, err := table.db.ExecContext(ctx, `INSERT INTO messages
		(ts, origin, author, from_loop_id, text, mentions, tg_chat_id, tg_message_id,
		 tg_bot_loop_id, delivered_to, conversation, conversation_loop_id, reply_to_id,
		 resends_id, tg_key, mirror)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		message.TS, message.Origin, message.Author, message.FromLoopID, message.Text, toJSON(message.Mentions), tgChat, tgMsg,
		message.TGBotLoopID, toJSON(message.DeliveredTo), message.Conversation, message.ConversationLoopID,
		message.ReplyToID, message.ResendsID, message.TGKey, message.Mirror)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return store.ErrDuplicate
		}
		return err
	}
	message.ID, _ = res.LastInsertId()
	return nil
}

// SetSendResult records a failed attempt at a message: when the bridge gave
// up and what it gave up on. A success does not come through here — it calls
// ResolveSend, which keeps the failure and marks it dealt with.
//
// It clears send_failure_told_at, so a fresh failure is fresh news: the pair
// can never read as "told about a failure the loop has not heard of". And
// the message is pending whatever it was: a failed send is not on the
// surface yet, and stays bound for it until a resolution says otherwise.
func (table messages) SetSendResult(ctx context.Context, id, failedAt int64, sendErr string) error {
	_, err := table.db.ExecContext(ctx,
		`UPDATE messages SET send_failed_at=?, send_error=?, send_failure_told_at=0, mirror=? WHERE id=?`,
		failedAt, sendErr, store.MirrorPending, id)
	return err
}

// UntoldSendFailures finds a loop's own lost messages, oldest first, so the
// loop is told in the order it said them.
//
// A failure whose words reached their reader in the end is not lost, so it is
// excluded by how it resolved rather than by send_resolved_at: told that a
// message it in fact delivered never arrived, a loop says it again and the
// human reads it twice — the doubling ADR-0026 leaves to the loop to avoid.
// Two resolutions mean the words arrived — an operator's retry that landed,
// and the loop's own resend (#270) — and a dismissed failure stays in: that
// message really did not arrive, and the operator setting it aside is news
// about their list, not about the send.
func (table messages) UntoldSendFailures(ctx context.Context, loopID string) ([]*store.Message, error) {
	if loopID == "" {
		// every message nobody authored would match; a loop is always named
		return nil, nil
	}
	return table.query(ctx, `SELECT `+messageCols+` FROM messages
		WHERE from_loop_id=? AND send_failed_at!=0 AND send_failure_told_at=0
		  AND send_resolution NOT IN (?,?)
		ORDER BY id`, loopID, store.SendResolutionDelivered, store.SendResolutionResent)
}

// unresolvedSendFailure is what "undelivered" means, written once: a send
// that ended in failure and that nobody has dealt with since. Both the Fleet
// page's per-loop count and the fleet-wide list below are this predicate with
// their own scope in front of it, so the badge and the page it opens cannot
// come to different answers (#263). The two indexes in migration 0022 are
// partial on exactly these terms.
const unresolvedSendFailure = `send_failed_at!=0 AND send_resolved_at=0`

func (table messages) UnresolvedSendFailures(ctx context.Context, loopID string) (int, error) {
	if loopID == "" {
		return 0, nil // as above: a loop is always named
	}
	var unresolved int
	err := table.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM messages WHERE from_loop_id=? AND `+unresolvedSendFailure,
		loopID).Scan(&unresolved)
	return unresolved, err
}

// ResolveSend marks a failure dealt with, and how. Deliberately not a clear
// of send_failed_at: the row still failed, and the timeline event that says
// so (#147) would otherwise describe a message the store claims got through.
//
// The predicate is the whole guard. A message that never failed has nothing
// to resolve — every successful send calls this, and most of them are the
// first attempt — and one already resolved must keep the time and the way it
// resolved, not the ones of whoever asked again.
func (table messages) ResolveSend(ctx context.Context, id int64, at int64, resolution string, resentAs int64) (bool, error) {
	res, err := table.db.ExecContext(ctx,
		`UPDATE messages SET send_resolved_at=?, send_resolution=?, send_resent_as=?, mirror=?
		 WHERE id=? AND `+unresolvedSendFailure,
		at, resolution, resentAs, mirrorAfter(resolution), id)
	if err != nil {
		return false, err
	}
	affected, err := res.RowsAffected()
	return affected > 0, err
}

// mirrorAfter is where a resolved failure leaves its message: on the surface
// if a retry of it got through, on the hub for good otherwise — a dismissed
// message never arrived, and a resent one arrived as another message.
func mirrorAfter(resolution string) string {
	if resolution == store.SendResolutionDelivered {
		return store.MirrorMirrored
	}
	return store.MirrorNotMirrored
}

func (table messages) SetMirror(ctx context.Context, id int64, mirror string) error {
	_, err := table.db.ExecContext(ctx, `UPDATE messages SET mirror=? WHERE id=?`, mirror, id)
	return err
}

func (table messages) FailInterruptedSends(ctx context.Context, failedAt int64, sendErr string) ([]*store.Message, error) {
	interrupted, err := table.query(ctx, `SELECT `+messageCols+` FROM messages
		WHERE mirror=? AND send_failed_at=0 ORDER BY id`, store.MirrorPending)
	if err != nil || len(interrupted) == 0 {
		return nil, err
	}
	for _, message := range interrupted {
		if err := table.SetSendResult(ctx, message.ID, failedAt, sendErr); err != nil {
			return nil, err
		}
		message.SendFailedAt, message.SendError = failedAt, sendErr
	}
	return interrupted, nil
}

// maxResendChain bounds the walk below. A chain is one loop saying the same
// words through an outage, so a handful of links is a long one; the bound is
// there because a walk over stored ids should not be able to run forever on
// a row that names itself, however it got that way.
const maxResendChain = 50

// ResolveResends resolves the failures this message was sent to replace.
//
// Usually one: the row its resends_id names. But a resend can fail too, and
// the loop is then told about that failure and resends it in turn — so what
// arrives is the end of a chain, and every link is an attempt to say the
// same words. They all resolve together, naming the message that actually
// got through rather than the next link, because that is the one an operator
// reading any of them wants to read.
//
// Each link is resolved through the same conditional update as any other
// resolution, so a link somebody already dismissed keeps their reason and
// the walk continues past it.
func (table messages) ResolveResends(ctx context.Context, messageID int64, at int64) (int, error) {
	resolved := 0
	id := messageID
	for i := 0; i < maxResendChain; i++ {
		message, err := table.Get(ctx, id)
		if err != nil {
			return resolved, err
		}
		if message.ResendsID == 0 {
			return resolved, nil
		}
		ok, err := table.ResolveSend(ctx, message.ResendsID, at, store.SendResolutionResent, messageID)
		if err != nil {
			return resolved, err
		}
		if ok {
			resolved++
		}
		id = message.ResendsID
	}
	return resolved, fmt.Errorf("resend chain from message %d is longer than %d", messageID, maxResendChain)
}

// Undelivered is the list behind the same predicate, newest failure first —
// the operator reads the most recent outage from the top. An empty loopID
// spans the fleet and requires only a non-empty from_loop_id, which keeps it
// to messages a loop sent: an inbound message has no sender to have failed.
// (Spelled in prose because gofmt rewrites a pair of single quotes in a doc
// comment into typographic ones, which would leave the SQL misquoted here.)
// A loop's id pins from_loop_id to it instead, which is the scope
// UnresolvedSendFailures counts — so the Fleet badge and the per-loop pane
// it opens are one predicate under one scope, not two queries that happen to
// agree (#281). An id and not a name, because that is what the rows carry;
// the route above is the one that speaks names.
//
// Sharing the predicate is also what keeps the partial index usable: it is
// declared on these two terms, so the planner only takes it when both are
// present.
func (table messages) Undelivered(ctx context.Context, loopID string) ([]*store.Message, error) {
	scope, args := `from_loop_id!=''`, []any(nil)
	if loopID != "" {
		scope, args = `from_loop_id=?`, []any{loopID}
	}
	return table.query(ctx, `SELECT `+messageCols+` FROM messages
		WHERE `+scope+` AND `+unresolvedSendFailure+`
		ORDER BY send_failed_at DESC, id DESC`, args...)
}

func (table messages) MarkSendFailuresTold(ctx context.Context, ids []int64, toldAt int64) error {
	if len(ids) == 0 {
		return nil
	}
	marks := strings.Repeat(",?", len(ids))[1:]
	args := make([]any, 0, len(ids)+1)
	args = append(args, toldAt)
	for _, id := range ids {
		args = append(args, id)
	}
	_, err := table.db.ExecContext(ctx,
		`UPDATE messages SET send_failure_told_at=? WHERE id IN (`+marks+`)`, args...)
	return err
}

func (table messages) SetDelivered(ctx context.Context, id int64, deliveredTo []string) error {
	_, err := table.db.ExecContext(ctx, `UPDATE messages SET delivered_to=? WHERE id=?`, toJSON(deliveredTo), id)
	return err
}

const messageCols = `id, ts, origin, author, from_loop_id, text,
	mentions, COALESCE(tg_chat_id,0), COALESCE(tg_message_id,0), tg_bot_loop_id, delivered_to,
	conversation, conversation_loop_id, reply_to_id, send_failed_at, send_error,
	send_failure_told_at, send_resolved_at, send_resolution, send_resent_as,
	resends_id, tg_key, mirror`

func (table messages) List(ctx context.Context, limit int) ([]*store.Message, error) {
	return table.query(ctx, `SELECT `+messageCols+` FROM messages ORDER BY id DESC LIMIT ?`, limit)
}

func (table messages) ListConversation(ctx context.Context, kind, loopID string, limit int) ([]*store.Message, error) {
	return table.query(ctx, `SELECT `+messageCols+` FROM messages
		WHERE conversation=? AND conversation_loop_id=?
		ORDER BY id DESC LIMIT ?`, kind, loopID, limit)
}

// Get resolves one message by id — how a reply target named in a send
// becomes the message it refers to.
func (table messages) Get(ctx context.Context, id int64) (*store.Message, error) {
	out, err := table.query(ctx, `SELECT `+messageCols+` FROM messages WHERE id=?`, id)
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, store.ErrNotFound
	}
	return out[0], nil
}

func (table messages) PutRef(ctx context.Context, ref *store.SurfaceRef) error {
	_, err := table.db.ExecContext(ctx, `INSERT INTO message_refs
		(message_id, bot_loop_id, tg_chat_id, tg_message_id) VALUES (?,?,?,?)
		ON CONFLICT (message_id, bot_loop_id) DO UPDATE SET
			tg_chat_id=excluded.tg_chat_id, tg_message_id=excluded.tg_message_id`,
		ref.MessageID, ref.BotLoopID, ref.TGChatID, ref.TGMessageID)
	return err
}

// Ref prefers the exact reference — an id this bot itself received or was
// given when it sent the message — and falls back to its sighting of the
// message, which is how a bot that did not ingest a group message still
// knows its own id for it.
func (table messages) Ref(ctx context.Context, messageID int64, botLoopID string) (*store.SurfaceRef, error) {
	ref := store.SurfaceRef{MessageID: messageID, BotLoopID: botLoopID}
	err := table.db.QueryRowContext(ctx, `SELECT tg_chat_id, tg_message_id FROM message_refs
		WHERE message_id=? AND bot_loop_id=?`, messageID, botLoopID).Scan(&ref.TGChatID, &ref.TGMessageID)
	if err == nil {
		return &ref, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	err = table.db.QueryRowContext(ctx, `SELECT s.tg_chat_id, s.tg_message_id
		FROM tg_sightings s JOIN messages m ON m.tg_key = s.tg_key
		WHERE m.id=? AND m.tg_key != '' AND s.bot_loop_id=?`,
		messageID, botLoopID).Scan(&ref.TGChatID, &ref.TGMessageID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	return &ref, err
}

// sightingRetention bounds tg_sightings: a reply to a message older than
// this renders without a native anchor rather than keeping every id forever.
const sightingRetention = 30 * 24 * 60 * 60 * 1000 // ms

func (table messages) RecordSighting(ctx context.Context, tgKey, botLoopID string, chatID, messageID, seenAt int64) error {
	if _, err := table.db.ExecContext(ctx, `INSERT INTO tg_sightings
		(tg_key, bot_loop_id, tg_chat_id, tg_message_id, seen_at) VALUES (?,?,?,?,?)
		ON CONFLICT (tg_key, bot_loop_id) DO NOTHING`,
		tgKey, botLoopID, chatID, messageID, seenAt); err != nil {
		return err
	}
	_, err := table.db.ExecContext(ctx, `DELETE FROM tg_sightings WHERE seen_at < ?`, seenAt-sightingRetention)
	return err
}

func (table messages) ByRef(ctx context.Context, botLoopID string, chatID, tgMessageID int64) (*store.Message, error) {
	var id int64
	err := table.db.QueryRowContext(ctx, `SELECT message_id FROM message_refs
		WHERE bot_loop_id=? AND tg_chat_id=? AND tg_message_id=?`,
		botLoopID, chatID, tgMessageID).Scan(&id)
	if err == nil {
		return table.Get(ctx, id)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	var key string
	err = table.db.QueryRowContext(ctx, `SELECT tg_key FROM tg_sightings
		WHERE bot_loop_id=? AND tg_chat_id=? AND tg_message_id=?`,
		botLoopID, chatID, tgMessageID).Scan(&key)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, store.ErrNotFound
	} else if err != nil {
		return nil, err
	}
	return table.ByTGKey(ctx, key)
}

func (table messages) ByTGKey(ctx context.Context, tgKey string) (*store.Message, error) {
	if tgKey == "" {
		return nil, store.ErrNotFound
	}
	out, err := table.query(ctx, `SELECT `+messageCols+` FROM messages WHERE tg_key=? ORDER BY id LIMIT 1`, tgKey)
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, store.ErrNotFound
	}
	return out[0], nil
}

func (table messages) LatestGroupPostBy(ctx context.Context, authorLoopID, text string) (*store.Message, error) {
	if authorLoopID == "" || text == "" {
		return nil, store.ErrNotFound
	}
	// Exact before longer, then newest. A prefix match exists for the one
	// shape that needs it — a message too long to send whole, quoted by its
	// first part — and a post that merely starts with the same words is a
	// different message. Ordering by id alone would let it win by being
	// newer, which is the reply filed against the wrong post.
	out, err := table.query(ctx, `SELECT `+messageCols+` FROM messages
		WHERE conversation=? AND from_loop_id=? AND (text=? OR text LIKE ? ESCAPE '\')
		ORDER BY (text=?) DESC, id DESC LIMIT 1`,
		store.ConversationGroup, authorLoopID, text, likePrefix(text), text)
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, store.ErrNotFound
	}
	return out[0], nil
}

// likePrefix turns text into a LIKE pattern matching rows that begin with
// it. The wildcards are escaped, so a message containing a literal % or _
// matches itself and nothing else.
func likePrefix(text string) string {
	esc := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return esc.Replace(text) + "%"
}

func (table messages) query(ctx context.Context, statement string, args ...any) ([]*store.Message, error) {
	rows, err := table.db.QueryContext(ctx, statement, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*store.Message
	for rows.Next() {
		var message store.Message
		var mentions, delivered string
		if err := rows.Scan(&message.ID, &message.TS, &message.Origin, &message.Author, &message.FromLoopID, &message.Text,
			&mentions, &message.TGChatID, &message.TGMessageID, &message.TGBotLoopID, &delivered,
			&message.Conversation, &message.ConversationLoopID, &message.ReplyToID,
			&message.SendFailedAt, &message.SendError, &message.SendFailureToldAt, &message.SendResolvedAt,
			&message.SendResolution, &message.SendResentAs, &message.ResendsID, &message.TGKey, &message.Mirror); err != nil {
			return nil, err
		}
		message.Mentions = fromJSON(mentions)
		message.DeliveredTo = fromJSON(delivered)
		out = append(out, &message)
	}
	return out, rows.Err()
}

// --- turns ---

type turns struct{ db *sql.DB }

func (table turns) Create(ctx context.Context, turn *store.Turn) error {
	_, err := table.db.ExecContext(ctx, `INSERT INTO turns
		(id, loop_id, session_id, trigger_kind, started_at) VALUES (?,?,?,?,?)`,
		turn.ID, turn.LoopID, turn.SessionID, turn.Trigger, turn.StartedAt)
	return err
}

func (table turns) Finish(ctx context.Context, turn *store.Turn) error {
	_, err := table.db.ExecContext(ctx, `UPDATE turns SET ended_at=?, is_error=?, result_text=?,
		cost_usd=?, session_cost_usd=?, input_tokens=?, output_tokens=?, cache_read_tokens=?,
		cache_write_tokens=?, context_tokens=?, duration_ms=?, model=? WHERE id=?`,
		turn.EndedAt, boolInt(turn.IsError), turn.ResultText, turn.CostUSD, turn.SessionCostUSD,
		turn.InputTokens, turn.OutputTokens,
		turn.CacheReadTokens, turn.CacheWriteTokens, turn.ContextTokens, turn.DurationMS, turn.Model, turn.ID)
	return err
}

const turnCols = `id, loop_id, session_id, trigger_kind, started_at, ended_at, is_error,
	result_text, cost_usd, session_cost_usd, input_tokens, output_tokens, cache_read_tokens,
	cache_write_tokens, context_tokens, duration_ms, model`

// Latest is the most recent turn the loop actually finished: an in-flight
// turn has no usage yet, and an errored one reports what it managed.
func (table turns) Latest(ctx context.Context, loopID string) (*store.Turn, error) {
	row := table.db.QueryRowContext(ctx, `SELECT `+turnCols+`
		FROM turns WHERE loop_id=? AND ended_at>0 ORDER BY ended_at DESC LIMIT 1`, loopID)
	var turn store.Turn
	var isErr int
	err := row.Scan(&turn.ID, &turn.LoopID, &turn.SessionID, &turn.Trigger, &turn.StartedAt, &turn.EndedAt,
		&isErr, &turn.ResultText, &turn.CostUSD, &turn.SessionCostUSD, &turn.InputTokens, &turn.OutputTokens,
		&turn.CacheReadTokens, &turn.CacheWriteTokens, &turn.ContextTokens, &turn.DurationMS, &turn.Model)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	turn.IsError = isErr != 0
	return &turn, nil
}

func (table turns) ListByLoop(ctx context.Context, loopID string, limit int) ([]*store.Turn, error) {
	rows, err := table.db.QueryContext(ctx, `SELECT `+turnCols+`
		FROM turns WHERE loop_id=? ORDER BY started_at DESC LIMIT ?`, loopID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*store.Turn
	for rows.Next() {
		var turn store.Turn
		var isErr int
		if err := rows.Scan(&turn.ID, &turn.LoopID, &turn.SessionID, &turn.Trigger, &turn.StartedAt,
			&turn.EndedAt, &isErr, &turn.ResultText, &turn.CostUSD, &turn.SessionCostUSD,
			&turn.InputTokens, &turn.OutputTokens,
			&turn.CacheReadTokens, &turn.CacheWriteTokens, &turn.ContextTokens, &turn.DurationMS, &turn.Model); err != nil {
			return nil, err
		}
		turn.IsError = isErr != 0
		out = append(out, &turn)
	}
	return out, rows.Err()
}

func (table turns) InterruptDangling(ctx context.Context, endedAt int64) error {
	_, err := table.db.ExecContext(ctx,
		`UPDATE turns SET ended_at=?, is_error=1 WHERE ended_at=0`, endedAt)
	return err
}

func (table turns) CostSince(ctx context.Context, loopID string, since int64) (float64, error) {
	var cost sql.NullFloat64
	err := table.db.QueryRowContext(ctx,
		`SELECT SUM(cost_usd) FROM turns WHERE loop_id=? AND started_at>=?`, loopID, since).Scan(&cost)
	return cost.Float64, err
}

// SessionCost is the highest total recorded for the session. The CLI's
// figure only grows within a session, so the highest is the latest — and
// MAX ignores the in-flight turn asking the question, whose own column is
// still zero.
func (table turns) SessionCost(ctx context.Context, loopID, sessionID string) (float64, error) {
	var cost sql.NullFloat64
	err := table.db.QueryRowContext(ctx,
		`SELECT MAX(session_cost_usd) FROM turns WHERE loop_id=? AND session_id=?`,
		loopID, sessionID).Scan(&cost)
	return cost.Float64, err
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

// --- events ---

type events struct{ db *sql.DB }

func (table events) Insert(ctx context.Context, event *store.Event) (int64, error) {
	res, err := table.db.ExecContext(ctx, `INSERT INTO events
		(loop_id, session_id, turn_id, ts, type, subtype, payload) VALUES (?,?,?,?,?,?,?)`,
		event.LoopID, event.SessionID, event.TurnID, event.TS, event.Type, event.Subtype, event.Payload)
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	event.ID = id
	return id, nil
}

func (table events) DeleteBefore(ctx context.Context, cutoff int64) (int64, error) {
	res, err := table.db.ExecContext(ctx, `DELETE FROM events WHERE ts < ?`, cutoff)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (table events) ListByLoop(ctx context.Context, loopID string, afterID int64, limit int) ([]*store.Event, error) {
	rows, err := table.db.QueryContext(ctx, `SELECT id, loop_id, session_id, turn_id, ts, type, subtype, payload
		FROM events WHERE loop_id=? AND id>? ORDER BY id LIMIT ?`, loopID, afterID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*store.Event
	for rows.Next() {
		var event store.Event
		if err := rows.Scan(&event.ID, &event.LoopID, &event.SessionID, &event.TurnID, &event.TS, &event.Type, &event.Subtype, &event.Payload); err != nil {
			return nil, err
		}
		out = append(out, &event)
	}
	return out, rows.Err()
}

// ListByLoopBefore reads the newest window: the rows are selected newest-first
// so the limit bites at the recent end, then reversed, because every caller —
// and the endpoint — reads a page oldest first.
func (table events) ListByLoopBefore(ctx context.Context, loopID string, beforeID int64, limit int) ([]*store.Event, error) {
	// 0 means "no cursor yet, start at the newest"; every real id is above it
	if beforeID <= 0 {
		beforeID = math.MaxInt64
	}
	rows, err := table.db.QueryContext(ctx, `SELECT id, loop_id, session_id, turn_id, ts, type, subtype, payload
		FROM events WHERE loop_id=? AND id<? ORDER BY id DESC LIMIT ?`, loopID, beforeID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*store.Event
	for rows.Next() {
		var event store.Event
		if err := rows.Scan(&event.ID, &event.LoopID, &event.SessionID, &event.TurnID, &event.TS, &event.Type, &event.Subtype, &event.Payload); err != nil {
			return nil, err
		}
		out = append(out, &event)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

// --- schedule ---

type schedule struct{ db *sql.DB }

func (table schedule) Set(ctx context.Context, loopID string, nextTickAt int64) error {
	_, err := table.db.ExecContext(ctx, `INSERT INTO schedule (loop_id, next_tick_at) VALUES (?,?)
		ON CONFLICT(loop_id) DO UPDATE SET next_tick_at=excluded.next_tick_at`, loopID, nextTickAt)
	return err
}

func (table schedule) Get(ctx context.Context, loopID string) (*store.ScheduleEntry, error) {
	var entry store.ScheduleEntry
	err := table.db.QueryRowContext(ctx,
		`SELECT loop_id, next_tick_at, last_tick_at FROM schedule WHERE loop_id=?`, loopID).
		Scan(&entry.LoopID, &entry.NextTickAt, &entry.LastTickAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &entry, nil
}

func (table schedule) SetLastTick(ctx context.Context, loopID string, at int64) error {
	_, err := table.db.ExecContext(ctx, `UPDATE schedule SET last_tick_at=? WHERE loop_id=?`, at, loopID)
	return err
}

func (table schedule) All(ctx context.Context) ([]*store.ScheduleEntry, error) {
	rows, err := table.db.QueryContext(ctx, `SELECT loop_id, next_tick_at, last_tick_at FROM schedule`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*store.ScheduleEntry
	for rows.Next() {
		var entry store.ScheduleEntry
		if err := rows.Scan(&entry.LoopID, &entry.NextTickAt, &entry.LastTickAt); err != nil {
			return nil, err
		}
		out = append(out, &entry)
	}
	return out, rows.Err()
}

func (table schedule) Delete(ctx context.Context, loopID string) error {
	_, err := table.db.ExecContext(ctx, `DELETE FROM schedule WHERE loop_id=?`, loopID)
	return err
}

// --- inbox ---

type inbox struct{ db *sql.DB }

func (table inbox) Push(ctx context.Context, loopID, envelope string, queuedAt int64) error {
	_, err := table.db.ExecContext(ctx,
		`INSERT INTO inbox (loop_id, envelope, queued_at) VALUES (?,?,?)`, loopID, envelope, queuedAt)
	return err
}

func (table inbox) Drain(ctx context.Context, loopID string) ([]string, error) {
	tx, err := table.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx,
		`SELECT id, envelope FROM inbox WHERE loop_id=? ORDER BY id`, loopID)
	if err != nil {
		return nil, err
	}
	var ids []int64
	var out []string
	for rows.Next() {
		var id int64
		var env string
		if err := rows.Scan(&id, &env); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
		out = append(out, env)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, id := range ids {
		if _, err := tx.ExecContext(ctx, `DELETE FROM inbox WHERE id=?`, id); err != nil {
			return nil, err
		}
	}
	return out, tx.Commit()
}

// --- settings ---

type settings struct{ db *sql.DB }

func (table settings) Get(ctx context.Context, key string) (string, error) {
	var value string
	err := table.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key=?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", store.ErrNotFound
	}
	return value, err
}

func (table settings) Set(ctx context.Context, key, value string) error {
	_, err := table.db.ExecContext(ctx, `INSERT INTO settings (key, value) VALUES (?,?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value)
	return err
}

// --- loop secrets ---

type loopSecrets struct{ db *sql.DB }

func (table loopSecrets) Set(ctx context.Context, loopID, name, value string, updatedAt int64) error {
	_, err := table.db.ExecContext(ctx, `INSERT INTO loop_secrets (loop_id, name, value, updated_at) VALUES (?,?,?,?)
		ON CONFLICT(loop_id, name) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`,
		loopID, name, value, updatedAt)
	if err != nil && strings.Contains(err.Error(), "FOREIGN KEY") {
		return store.ErrNotFound
	}
	return err
}

func (table loopSecrets) Delete(ctx context.Context, loopID, name string) error {
	_, err := table.db.ExecContext(ctx, `DELETE FROM loop_secrets WHERE loop_id=? AND name=?`, loopID, name)
	return err
}

func (table loopSecrets) List(ctx context.Context, loopID string) ([]*store.LoopSecret, error) {
	rows, err := table.db.QueryContext(ctx, `SELECT loop_id, name, value, updated_at
		FROM loop_secrets WHERE loop_id=? ORDER BY name`, loopID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*store.LoopSecret
	for rows.Next() {
		var secret store.LoopSecret
		if err := rows.Scan(&secret.LoopID, &secret.Name, &secret.Value, &secret.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, &secret)
	}
	return out, rows.Err()
}

// --- fleet rules ---

type fleetRules struct{ db *sql.DB }

const ruleCols = `id, title, body, enabled, created_at, updated_at`

func scanRule(row interface{ Scan(...any) error }) (*store.FleetRule, error) {
	var rule store.FleetRule
	var enabled int
	if err := row.Scan(&rule.ID, &rule.Title, &rule.Body, &enabled, &rule.CreatedAt, &rule.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, store.ErrNotFound
		}
		return nil, err
	}
	rule.Enabled = enabled == 1
	return &rule, nil
}

func (table fleetRules) Create(ctx context.Context, rule *store.FleetRule) error {
	_, err := table.db.ExecContext(ctx, `INSERT INTO fleet_rules (`+ruleCols+`) VALUES (?,?,?,?,?,?)`,
		rule.ID, rule.Title, rule.Body, boolInt(rule.Enabled), rule.CreatedAt, rule.UpdatedAt)
	return err
}

func (table fleetRules) Update(ctx context.Context, rule *store.FleetRule) error {
	res, err := table.db.ExecContext(ctx, `UPDATE fleet_rules SET title=?, body=?, enabled=?, updated_at=? WHERE id=?`,
		rule.Title, rule.Body, boolInt(rule.Enabled), rule.UpdatedAt, rule.ID)
	if err != nil {
		return err
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (table fleetRules) Delete(ctx context.Context, id string) error {
	_, err := table.db.ExecContext(ctx, `DELETE FROM fleet_rules WHERE id=?`, id)
	return err
}

func (table fleetRules) Get(ctx context.Context, id string) (*store.FleetRule, error) {
	return scanRule(table.db.QueryRowContext(ctx, `SELECT `+ruleCols+` FROM fleet_rules WHERE id=?`, id))
}

// List returns every rule in creation order: ids are creation-ordered, and
// a stable order keeps the rendered section identical between wakes.
func (table fleetRules) List(ctx context.Context) ([]*store.FleetRule, error) {
	rows, err := table.db.QueryContext(ctx, `SELECT `+ruleCols+` FROM fleet_rules ORDER BY created_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*store.FleetRule
	for rows.Next() {
		rule, err := scanRule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, rule)
	}
	return out, rows.Err()
}

// --- tg senders ---

type tgSenders struct{ db *sql.DB }

const senderCols = `tg_user_id, username, display, status, pair_code, first_seen_via, created_at, updated_at`

func (table tgSenders) Get(ctx context.Context, id int64) (*store.TGSender, error) {
	var sender store.TGSender
	err := table.db.QueryRowContext(ctx, `SELECT `+senderCols+` FROM tg_senders WHERE tg_user_id=?`, id).
		Scan(&sender.TGUserID, &sender.Username, &sender.Display, &sender.Status, &sender.PairCode, &sender.FirstSeenVia,
			&sender.CreatedAt, &sender.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &sender, nil
}

func (table tgSenders) Create(ctx context.Context, sender *store.TGSender) error {
	_, err := table.db.ExecContext(ctx, `INSERT INTO tg_senders (`+senderCols+`) VALUES (?,?,?,?,?,?,?,?)`,
		sender.TGUserID, sender.Username, sender.Display, sender.Status, sender.PairCode, sender.FirstSeenVia,
		sender.CreatedAt, sender.UpdatedAt)
	if err != nil && strings.Contains(err.Error(), "UNIQUE") {
		return store.ErrDuplicate
	}
	return err
}

func (table tgSenders) SetStatus(ctx context.Context, id int64, status string, updatedAt int64) error {
	res, err := table.db.ExecContext(ctx, `UPDATE tg_senders SET status=?, updated_at=? WHERE tg_user_id=?`,
		status, updatedAt, id)
	if err != nil {
		return err
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (table tgSenders) List(ctx context.Context) ([]*store.TGSender, error) {
	rows, err := table.db.QueryContext(ctx, `SELECT `+senderCols+` FROM tg_senders ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*store.TGSender
	for rows.Next() {
		var sender store.TGSender
		if err := rows.Scan(&sender.TGUserID, &sender.Username, &sender.Display, &sender.Status, &sender.PairCode,
			&sender.FirstSeenVia, &sender.CreatedAt, &sender.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, &sender)
	}
	return out, rows.Err()
}

func (table tgSenders) Delete(ctx context.Context, id int64) error {
	_, err := table.db.ExecContext(ctx, `DELETE FROM tg_senders WHERE tg_user_id=?`, id)
	return err
}
