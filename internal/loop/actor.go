package loop

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"
	"time"

	"github.com/enes-alatas/spool/internal/bus"
	"github.com/enes-alatas/spool/internal/claude"
	"github.com/enes-alatas/spool/internal/store"
)

// Loop states (exposed to the UI via loop_status bus items).
const (
	StateAsleep   = "asleep"
	StateWaking   = "waking"
	StateBusy     = "busy"
	StateIdle     = "idle"
	StateDraining = "draining"
	StatePaused   = "paused"
)

const (
	maxInbox        = 100
	backoffStart    = 10 * time.Second
	backoffCap      = 5 * time.Minute
	recentTurnCount = 5
)

// Deps wires an actor to the rest of the system without import cycles.
type Deps struct {
	Store           store.Store
	Bus             *bus.Bus
	ClaudeBin       string
	PartialMessages bool
	Logger          *slog.Logger

	// SystemPrompt builds the --append-system-prompt for a loop (peers are
	// resolved at call time so every wake sees the current fleet).
	SystemPrompt func(l *store.Loop) string
	// OnReply is called after each successful turn with the final reply text
	// and the DM chats that triggered this turn (for reply-context mirroring).
	OnReply func(l *store.Loop, resultText string, replyDMChats []int64)
	// OnTurnDone reschedules the loop's next tick after any completed turn.
	OnTurnDone func(l *store.Loop, trailer time.Duration, hasTrailer bool)
}

type cmd struct {
	kind string // deliver|tick|pause|resume|kill|update|shutdown
	env  Envelope
	loop *store.Loop
	done chan struct{} // for shutdown
}

// Actor owns one loop: a single goroutine serializing every command,
// event and timer for that loop, and the claude process it drives.
type Actor struct {
	deps      Deps
	cmds      chan cmd
	stateSnap atomic.Value // string; last published state, for REST reads

	// goroutine-owned state below
	loop         store.Loop
	state        string
	paused       bool
	proc         claude.Proc
	procEvents   <-chan claude.Event
	inbox        []Envelope
	currentBatch []Envelope // in-flight batch, kept for redelivery on session loss
	pendingDMs   []int64    // DM chats of the in-flight turn
	turn         *store.Turn
	freshSpawn   bool // current process was started with --session-id (not resume)
	backoff      time.Duration

	idleTimer  *time.Timer
	retryTimer *time.Timer
}

func NewActor(deps Deps, l *store.Loop) *Actor {
	actor := &Actor{
		deps:  deps,
		cmds:  make(chan cmd, 32),
		loop:  *l,
		state: StateAsleep,
	}
	actor.paused = l.Status == store.StatusPaused
	actor.idleTimer = time.NewTimer(time.Hour)
	actor.idleTimer.Stop()
	actor.retryTimer = time.NewTimer(time.Hour)
	actor.retryTimer.Stop()
	go actor.run()
	return actor
}

// --- public API (thread-safe; commands are serialized onto the actor) ---

func (actor *Actor) Deliver(env Envelope)     { actor.cmds <- cmd{kind: "deliver", env: env} }
func (actor *Actor) Tick()                    { actor.cmds <- cmd{kind: "tick"} }
func (actor *Actor) Pause()                   { actor.cmds <- cmd{kind: "pause"} }
func (actor *Actor) Resume()                  { actor.cmds <- cmd{kind: "resume"} }
func (actor *Actor) Kill()                    { actor.cmds <- cmd{kind: "kill"} }
func (actor *Actor) UpdateLoop(l *store.Loop) { actor.cmds <- cmd{kind: "update", loop: l} }

// Shutdown stops the actor, killing any live process. Blocks until done.
func (actor *Actor) Shutdown() {
	done := make(chan struct{})
	actor.cmds <- cmd{kind: "shutdown", done: done}
	<-done
}

// --- actor loop ---

func (actor *Actor) run() {
	actor.publishState()
	for {
		select {
		case command := <-actor.cmds:
			if command.kind == "shutdown" {
				actor.handleShutdown()
				close(command.done)
				return
			}
			actor.handleCmd(command)
		case ev, ok := <-actor.procEvents:
			if !ok {
				actor.handleProcExit()
				continue
			}
			actor.handleEvent(ev)
		case <-actor.idleTimer.C:
			actor.handleIdleTimeout()
		case <-actor.retryTimer.C:
			if len(actor.inbox) > 0 && actor.state == StateAsleep {
				actor.wake()
			}
		}
	}
}

func (actor *Actor) handleCmd(command cmd) {
	switch command.kind {
	case "deliver":
		if actor.paused {
			if b, err := json.Marshal(command.env); err == nil {
				_ = actor.deps.Store.Inbox().Push(context.Background(), actor.loop.ID, string(b), now())
			}
			return
		}
		actor.enqueue(command.env)
		actor.pump()
	case "tick":
		if actor.paused {
			return
		}
		if actor.state == StateBusy || actor.state == StateWaking {
			// mission already being worked; scheduler will reschedule
			return
		}
		actor.enqueue(TickEnvelope(time.Now(), &actor.loop))
		actor.pump()
	case "pause":
		actor.paused = true
		actor.publishState()
	case "resume":
		actor.paused = false
		envs, err := actor.deps.Store.Inbox().Drain(context.Background(), actor.loop.ID)
		if err != nil {
			actor.log().Error("inbox drain", "err", err)
		}
		for _, raw := range envs {
			var env Envelope
			if json.Unmarshal([]byte(raw), &env) == nil {
				actor.enqueue(env)
			}
		}
		actor.publishState()
		actor.pump()
	case "kill":
		if actor.proc != nil {
			_ = actor.proc.Kill()
			// exit handled via procEvents close
		}
	case "update":
		token := actor.loop // keep runtime-only fields
		actor.loop = *command.loop
		actor.loop.CurrentSessionID = token.CurrentSessionID
		actor.paused = actor.loop.Status == store.StatusPaused
		actor.publishState()
	}
}

func (actor *Actor) enqueue(env Envelope) {
	if len(actor.inbox) >= maxInbox {
		actor.log().Warn("inbox overflow, dropping oldest")
		actor.inbox = actor.inbox[1:]
	}
	actor.inbox = append(actor.inbox, env)
}

// pump advances the state machine when there is queued work.
func (actor *Actor) pump() {
	if len(actor.inbox) == 0 {
		return
	}
	switch actor.state {
	case StateAsleep:
		actor.wake()
	case StateIdle:
		actor.startTurn()
	case StateWaking, StateBusy, StateDraining:
		// queued; picked up on init / result / exit respectively
	}
}

func (actor *Actor) wake() {
	ctx := context.Background()
	fresh := actor.loop.CurrentSessionID == ""
	var opts claude.Opts
	if fresh {
		sid := newUUID()
		actor.loop.CurrentSessionID = sid
		_ = actor.deps.Store.Sessions().Create(ctx, &store.Session{ID: sid, LoopID: actor.loop.ID, StartedAt: now()})
		opts.SessionID = sid
	} else {
		opts.ResumeID = actor.loop.CurrentSessionID
	}
	opts.Model = actor.loop.Model
	opts.Effort = actor.loop.Effort
	opts.AppendSystemPrompt = actor.deps.SystemPrompt(&actor.loop)
	opts.PartialMessages = actor.deps.PartialMessages

	proc, err := claude.Spawn(ctx, actor.deps.ClaudeBin, actor.loop.WorkspacePath, opts)
	if err != nil {
		actor.log().Error("spawn failed", "err", err)
		actor.crashBackoff()
		return
	}
	actor.proc = proc
	actor.procEvents = proc.Events()
	actor.freshSpawn = fresh
	actor.state = StateWaking
	_ = actor.deps.Store.Loops().SetRuntime(ctx, actor.loop.ID, actor.loop.CurrentSessionID, proc.PID())
	actor.storeSpoolEvent("proc_spawn", fmt.Sprintf(`{"pid":%d,"resume":%v}`, proc.PID(), !fresh))
	actor.publishState()
	// The CLI emits nothing (not even init) until the first user message, so
	// send queued work immediately rather than waiting for init.
	if len(actor.inbox) > 0 {
		actor.startTurn()
	}
}

// startTurn sends everything queued as one batched user message.
func (actor *Actor) startTurn() {
	batch := actor.inbox
	actor.inbox = nil
	actor.currentBatch = batch

	texts := make([]string, 0, len(batch))
	trigger := store.TriggerTick
	actor.pendingDMs = nil
	for _, env := range batch {
		texts = append(texts, env.Text)
		if env.Trigger != store.TriggerTick {
			trigger = env.Trigger
		}
		if env.TGChatID != 0 {
			actor.pendingDMs = append(actor.pendingDMs, env.TGChatID)
		}
	}
	text := strings.Join(texts, "\n\n---\n\n")
	if actor.freshSpawn && actor.loopHadHistory() {
		recent := actor.recentReplies()
		text = SessionLostPreamble(&actor.loop, recent) + "\n\n---\n\n" + text
	}

	turn := &store.Turn{
		ID:        newUUID(),
		LoopID:    actor.loop.ID,
		SessionID: actor.loop.CurrentSessionID,
		Trigger:   trigger,
		StartedAt: now(),
	}
	if err := actor.deps.Store.Turns().Create(context.Background(), turn); err != nil {
		actor.log().Error("turn create", "err", err)
	}
	actor.turn = turn

	for _, env := range batch {
		if b, err := json.Marshal(env); err == nil {
			actor.storeEventFull("envelope", env.Trigger, string(b))
		}
	}

	if err := actor.proc.Send(text); err != nil {
		actor.log().Error("send failed", "err", err)
		_ = actor.proc.Kill()
		// exit path will requeue currentBatch if the session was lost
		return
	}
	actor.idleTimer.Stop()
	actor.state = StateBusy
	actor.publishState()
}

func (actor *Actor) handleEvent(ev claude.Event) {
	switch {
	case ev.Type == "system" && ev.Subtype == "init":
		if ev.Init != nil && ev.Init.SessionID != "" && ev.Init.SessionID != actor.loop.CurrentSessionID {
			// authoritative id from the CLI (shouldn't differ, but trust it)
			actor.loop.CurrentSessionID = ev.Init.SessionID
			_ = actor.deps.Store.Loops().SetRuntime(context.Background(), actor.loop.ID, ev.Init.SessionID, actor.proc.PID())
		}
		actor.storeClaudeEvent(ev)
		if actor.state == StateWaking && actor.turn == nil {
			// spawned with no work (shouldn't normally happen)
			actor.state = StateIdle
			actor.armIdleTimer()
			actor.publishState()
		}
	case ev.Type == "stream_event":
		// live deltas: publish only, never persist
		actor.deps.Bus.Publish(bus.Item{Kind: bus.KindAgentEvent, LoopID: actor.loop.ID, Payload: json.RawMessage(ev.Raw)})
	case ev.Type == "result":
		actor.storeClaudeEvent(ev)
		actor.finishTurn(ev)
	default:
		actor.storeClaudeEvent(ev)
	}
}

func (actor *Actor) finishTurn(ev claude.Event) {
	actor.backoff = 0
	actor.freshSpawn = false
	res := ev.Result
	if actor.turn != nil && res != nil {
		t := actor.turn
		t.EndedAt = now()
		t.IsError = res.IsError
		t.ResultText = res.ResultText
		t.CostUSD = res.CostUSD
		t.InputTokens = res.Usage.InputTokens
		t.OutputTokens = res.Usage.OutputTokens
		t.CacheReadTokens = res.Usage.CacheReadTokens
		t.CacheWriteTokens = res.Usage.CacheCreationTokens
		t.DurationMS = res.DurationMS
		if err := actor.deps.Store.Turns().Finish(context.Background(), t); err != nil {
			actor.log().Error("turn finish", "err", err)
		}
		actor.deps.Bus.Publish(bus.Item{Kind: bus.KindTurnResult, LoopID: actor.loop.ID, Payload: t})

		var trailer time.Duration
		var hasTrailer bool
		if !res.IsError {
			trailer, hasTrailer = ParseTrailer(res.ResultText)
			if actor.deps.OnReply != nil && strings.TrimSpace(res.ResultText) != "" {
				actor.deps.OnReply(&actor.loop, res.ResultText, actor.pendingDMs)
			}
		}
		if actor.deps.OnTurnDone != nil {
			actor.deps.OnTurnDone(&actor.loop, trailer, hasTrailer)
		}
	}
	actor.turn = nil
	actor.pendingDMs = nil
	actor.currentBatch = nil

	if len(actor.inbox) > 0 && !actor.paused {
		actor.startTurn()
		return
	}
	actor.state = StateIdle
	actor.armIdleTimer()
	actor.publishState()
}

func (actor *Actor) armIdleTimer() {
	d := time.Duration(actor.loop.IdleTimeoutSec) * time.Second
	if d <= 0 {
		d = 90 * time.Second
	}
	actor.idleTimer.Reset(d)
}

func (actor *Actor) handleIdleTimeout() {
	if actor.state != StateIdle || actor.proc == nil {
		return
	}
	actor.state = StateDraining
	actor.publishState()
	_ = actor.proc.CloseStdin()
	// exit arrives via procEvents close
}

func (actor *Actor) handleProcExit() {
	exit := actor.proc.Wait()
	pid := actor.proc.PID()
	actor.proc = nil
	actor.procEvents = nil
	actor.idleTimer.Stop()
	_ = actor.deps.Store.Loops().SetRuntime(context.Background(), actor.loop.ID, actor.loop.CurrentSessionID, 0)
	actor.storeSpoolEvent("proc_exit", fmt.Sprintf(`{"pid":%d,"code":%d}`, pid, exit.Code))

	wasDraining := actor.state == StateDraining
	inTurn := actor.turn != nil
	sessionLost := claude.IsSessionNotFound(exit)

	if inTurn {
		// process died mid-turn
		t := actor.turn
		t.EndedAt = now()
		t.IsError = true
		_ = actor.deps.Store.Turns().Finish(context.Background(), t)
		actor.turn = nil
		actor.pendingDMs = nil
	}

	switch {
	case wasDraining || (exit.Code == 0 && !inTurn):
		// clean idle exit
		actor.currentBatch = nil
		actor.state = StateAsleep
		actor.publishState()
		if len(actor.inbox) > 0 && !actor.paused {
			actor.wake() // message raced the idle close
		}
	case sessionLost:
		// resume failed: mint a fresh session, requeue the undelivered batch,
		// and retry immediately
		old := actor.loop.CurrentSessionID
		_ = actor.deps.Store.Sessions().End(context.Background(), old, store.EndReasonLost, now())
		actor.storeSpoolEvent("session_lost", fmt.Sprintf(`{"old_session":%q}`, old))
		actor.loop.CurrentSessionID = ""
		if len(actor.currentBatch) > 0 {
			actor.inbox = append(actor.currentBatch, actor.inbox...)
			actor.currentBatch = nil
		}
		actor.state = StateAsleep
		actor.publishState()
		if len(actor.inbox) > 0 && !actor.paused {
			actor.wake()
		}
	default:
		actor.currentBatch = nil
		actor.storeSpoolEvent("crash", fmt.Sprintf(`{"code":%d,"stderr":%q}`, exit.Code, tail(exit.Stderr, 2000)))
		actor.log().Warn("claude process crashed", "code", exit.Code, "stderr", tail(exit.Stderr, 500))
		actor.crashBackoff()
	}
}

func (actor *Actor) crashBackoff() {
	if actor.backoff == 0 {
		actor.backoff = backoffStart
	} else {
		actor.backoff *= 2
		if actor.backoff > backoffCap {
			actor.backoff = backoffCap
		}
	}
	actor.state = StateAsleep
	actor.publishState()
	if len(actor.inbox) > 0 {
		actor.retryTimer.Reset(actor.backoff)
	}
}

func (actor *Actor) handleShutdown() {
	if actor.proc != nil {
		_ = actor.proc.Kill()
		actor.proc.Wait()
		_ = actor.deps.Store.Loops().SetRuntime(context.Background(), actor.loop.ID, actor.loop.CurrentSessionID, 0)
	}
}

// --- helpers ---

func (actor *Actor) loopHadHistory() bool {
	turns, err := actor.deps.Store.Turns().ListByLoop(context.Background(), actor.loop.ID, 1)
	return err == nil && len(turns) > 0
}

func (actor *Actor) recentReplies() []string {
	turns, err := actor.deps.Store.Turns().ListByLoop(context.Background(), actor.loop.ID, recentTurnCount)
	if err != nil {
		return nil
	}
	// ListByLoop is newest-first; preamble wants oldest-first
	var out []string
	for i := len(turns) - 1; i >= 0; i-- {
		if turns[i].ResultText != "" {
			out = append(out, turns[i].ResultText)
		}
	}
	return out
}

func (actor *Actor) storeClaudeEvent(ev claude.Event) {
	e := &store.Event{
		LoopID:    actor.loop.ID,
		SessionID: actor.loop.CurrentSessionID,
		TS:        now(),
		Type:      ev.Type,
		Subtype:   ev.Subtype,
		Payload:   string(ev.Raw),
	}
	if actor.turn != nil {
		e.TurnID = actor.turn.ID
	}
	if _, err := actor.deps.Store.Events().Insert(context.Background(), e); err != nil {
		actor.log().Error("event insert", "err", err)
	}
	actor.deps.Bus.Publish(bus.Item{Kind: bus.KindAgentEvent, LoopID: actor.loop.ID, Payload: e})
}

func (actor *Actor) storeSpoolEvent(subtype, payload string) {
	actor.storeEventFull("spool", subtype, payload)
}

func (actor *Actor) storeEventFull(typ, subtype, payload string) {
	e := &store.Event{
		LoopID:    actor.loop.ID,
		SessionID: actor.loop.CurrentSessionID,
		TS:        now(),
		Type:      typ,
		Subtype:   subtype,
		Payload:   payload,
	}
	if actor.turn != nil {
		e.TurnID = actor.turn.ID
	}
	if _, err := actor.deps.Store.Events().Insert(context.Background(), e); err != nil {
		actor.log().Error("event insert", "err", err)
	}
	actor.deps.Bus.Publish(bus.Item{Kind: bus.KindAgentEvent, LoopID: actor.loop.ID, Payload: e})
}

// State returns the last published loop state (safe from any goroutine).
func (actor *Actor) State() string {
	if s, ok := actor.stateSnap.Load().(string); ok {
		return s
	}
	return StateAsleep
}

func (actor *Actor) publishState() {
	state := actor.state
	if actor.paused {
		state = StatePaused
	}
	actor.stateSnap.Store(state)
	actor.deps.Bus.Publish(bus.Item{Kind: bus.KindLoopStatus, LoopID: actor.loop.ID, Payload: map[string]any{
		"loop_id": actor.loop.ID,
		"name":    actor.loop.Name,
		"state":   state,
	}})
}

func (actor *Actor) log() *slog.Logger {
	if actor.deps.Logger == nil {
		return slog.Default()
	}
	return actor.deps.Logger.With("loop", actor.loop.Name)
}

func now() int64 { return time.Now().UnixMilli() }

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

// newUUID returns a random v4 UUID (claude --session-id requires valid UUIDs).
func newUUID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	dst := make([]byte, 32)
	hex.Encode(dst, b[:])
	return fmt.Sprintf("%s-%s-%s-%s-%s", dst[0:8], dst[8:12], dst[12:16], dst[16:20], dst[20:32])
}
