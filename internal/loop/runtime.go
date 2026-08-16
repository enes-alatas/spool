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

// Runtime states (exposed to the UI via loop_status bus items).
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

// Deps wires a runtime to the rest of the system without import cycles.
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

// Runtime is the single-goroutine actor owning one loop's claude process.
type Runtime struct {
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

func NewRuntime(deps Deps, l *store.Loop) *Runtime {
	r := &Runtime{
		deps:  deps,
		cmds:  make(chan cmd, 32),
		loop:  *l,
		state: StateAsleep,
	}
	r.paused = l.Status == store.StatusPaused
	r.idleTimer = time.NewTimer(time.Hour)
	r.idleTimer.Stop()
	r.retryTimer = time.NewTimer(time.Hour)
	r.retryTimer.Stop()
	go r.run()
	return r
}

// --- public API (thread-safe; commands are serialized onto the actor) ---

func (r *Runtime) Deliver(env Envelope)     { r.cmds <- cmd{kind: "deliver", env: env} }
func (r *Runtime) Tick()                    { r.cmds <- cmd{kind: "tick"} }
func (r *Runtime) Pause()                   { r.cmds <- cmd{kind: "pause"} }
func (r *Runtime) Resume()                  { r.cmds <- cmd{kind: "resume"} }
func (r *Runtime) Kill()                    { r.cmds <- cmd{kind: "kill"} }
func (r *Runtime) UpdateLoop(l *store.Loop) { r.cmds <- cmd{kind: "update", loop: l} }

// Shutdown stops the runtime, killing any live process. Blocks until done.
func (r *Runtime) Shutdown() {
	done := make(chan struct{})
	r.cmds <- cmd{kind: "shutdown", done: done}
	<-done
}

// --- actor loop ---

func (r *Runtime) run() {
	r.publishState()
	for {
		select {
		case c := <-r.cmds:
			if c.kind == "shutdown" {
				r.handleShutdown()
				close(c.done)
				return
			}
			r.handleCmd(c)
		case ev, ok := <-r.procEvents:
			if !ok {
				r.handleProcExit()
				continue
			}
			r.handleEvent(ev)
		case <-r.idleTimer.C:
			r.handleIdleTimeout()
		case <-r.retryTimer.C:
			if len(r.inbox) > 0 && r.state == StateAsleep {
				r.wake()
			}
		}
	}
}

func (r *Runtime) handleCmd(c cmd) {
	switch c.kind {
	case "deliver":
		if r.paused {
			if b, err := json.Marshal(c.env); err == nil {
				_ = r.deps.Store.Inbox().Push(context.Background(), r.loop.ID, string(b), now())
			}
			return
		}
		r.enqueue(c.env)
		r.pump()
	case "tick":
		if r.paused {
			return
		}
		if r.state == StateBusy || r.state == StateWaking {
			// mission already being worked; scheduler will reschedule
			return
		}
		r.enqueue(TickEnvelope(time.Now(), &r.loop))
		r.pump()
	case "pause":
		r.paused = true
		r.publishState()
	case "resume":
		r.paused = false
		envs, err := r.deps.Store.Inbox().Drain(context.Background(), r.loop.ID)
		if err != nil {
			r.log().Error("inbox drain", "err", err)
		}
		for _, raw := range envs {
			var env Envelope
			if json.Unmarshal([]byte(raw), &env) == nil {
				r.enqueue(env)
			}
		}
		r.publishState()
		r.pump()
	case "kill":
		if r.proc != nil {
			_ = r.proc.Kill()
			// exit handled via procEvents close
		}
	case "update":
		token := r.loop // keep runtime-only fields
		r.loop = *c.loop
		r.loop.CurrentSessionID = token.CurrentSessionID
		r.paused = r.loop.Status == store.StatusPaused
		r.publishState()
	}
}

func (r *Runtime) enqueue(env Envelope) {
	if len(r.inbox) >= maxInbox {
		r.log().Warn("inbox overflow, dropping oldest")
		r.inbox = r.inbox[1:]
	}
	r.inbox = append(r.inbox, env)
}

// pump advances the state machine when there is queued work.
func (r *Runtime) pump() {
	if len(r.inbox) == 0 {
		return
	}
	switch r.state {
	case StateAsleep:
		r.wake()
	case StateIdle:
		r.startTurn()
	case StateWaking, StateBusy, StateDraining:
		// queued; picked up on init / result / exit respectively
	}
}

func (r *Runtime) wake() {
	ctx := context.Background()
	fresh := r.loop.CurrentSessionID == ""
	var opts claude.Opts
	if fresh {
		sid := newUUID()
		r.loop.CurrentSessionID = sid
		_ = r.deps.Store.Sessions().Create(ctx, &store.Session{ID: sid, LoopID: r.loop.ID, StartedAt: now()})
		opts.SessionID = sid
	} else {
		opts.ResumeID = r.loop.CurrentSessionID
	}
	opts.Bin = r.deps.ClaudeBin
	opts.WorkDir = r.loop.WorkspacePath
	opts.Model = r.loop.Model
	opts.Effort = r.loop.Effort
	opts.AppendSystemPrompt = r.deps.SystemPrompt(&r.loop)
	opts.PartialMessages = r.deps.PartialMessages

	proc, err := claude.Spawn(ctx, opts)
	if err != nil {
		r.log().Error("spawn failed", "err", err)
		r.crashBackoff()
		return
	}
	r.proc = proc
	r.procEvents = proc.Events()
	r.freshSpawn = fresh
	r.state = StateWaking
	_ = r.deps.Store.Loops().SetRuntime(ctx, r.loop.ID, r.loop.CurrentSessionID, proc.PID())
	r.storeSpoolEvent("proc_spawn", fmt.Sprintf(`{"pid":%d,"resume":%v}`, proc.PID(), !fresh))
	r.publishState()
	// The CLI emits nothing (not even init) until the first user message, so
	// send queued work immediately rather than waiting for init.
	if len(r.inbox) > 0 {
		r.startTurn()
	}
}

// startTurn sends everything queued as one batched user message.
func (r *Runtime) startTurn() {
	batch := r.inbox
	r.inbox = nil
	r.currentBatch = batch

	texts := make([]string, 0, len(batch))
	trigger := store.TriggerTick
	r.pendingDMs = nil
	for _, env := range batch {
		texts = append(texts, env.Text)
		if env.Trigger != store.TriggerTick {
			trigger = env.Trigger
		}
		if env.TGChatID != 0 {
			r.pendingDMs = append(r.pendingDMs, env.TGChatID)
		}
	}
	text := strings.Join(texts, "\n\n---\n\n")
	if r.freshSpawn && r.loopHadHistory() {
		recent := r.recentReplies()
		text = SessionLostPreamble(&r.loop, recent) + "\n\n---\n\n" + text
	}

	turn := &store.Turn{
		ID:        newUUID(),
		LoopID:    r.loop.ID,
		SessionID: r.loop.CurrentSessionID,
		Trigger:   trigger,
		StartedAt: now(),
	}
	if err := r.deps.Store.Turns().Create(context.Background(), turn); err != nil {
		r.log().Error("turn create", "err", err)
	}
	r.turn = turn

	for _, env := range batch {
		if b, err := json.Marshal(env); err == nil {
			r.storeEventFull("envelope", env.Trigger, string(b))
		}
	}

	if err := r.proc.Send(text); err != nil {
		r.log().Error("send failed", "err", err)
		_ = r.proc.Kill()
		// exit path will requeue currentBatch if the session was lost
		return
	}
	r.idleTimer.Stop()
	r.state = StateBusy
	r.publishState()
}

func (r *Runtime) handleEvent(ev claude.Event) {
	switch {
	case ev.Type == "system" && ev.Subtype == "init":
		if ev.Init != nil && ev.Init.SessionID != "" && ev.Init.SessionID != r.loop.CurrentSessionID {
			// authoritative id from the CLI (shouldn't differ, but trust it)
			r.loop.CurrentSessionID = ev.Init.SessionID
			_ = r.deps.Store.Loops().SetRuntime(context.Background(), r.loop.ID, ev.Init.SessionID, r.proc.PID())
		}
		r.storeClaudeEvent(ev)
		if r.state == StateWaking && r.turn == nil {
			// spawned with no work (shouldn't normally happen)
			r.state = StateIdle
			r.armIdleTimer()
			r.publishState()
		}
	case ev.Type == "stream_event":
		// live deltas: publish only, never persist
		r.deps.Bus.Publish(bus.Item{Kind: bus.KindAgentEvent, LoopID: r.loop.ID, Payload: json.RawMessage(ev.Raw)})
	case ev.Type == "result":
		r.storeClaudeEvent(ev)
		r.finishTurn(ev)
	default:
		r.storeClaudeEvent(ev)
	}
}

func (r *Runtime) finishTurn(ev claude.Event) {
	r.backoff = 0
	r.freshSpawn = false
	res := ev.Result
	if r.turn != nil && res != nil {
		t := r.turn
		t.EndedAt = now()
		t.IsError = res.IsError
		t.ResultText = res.ResultText
		t.CostUSD = res.CostUSD
		t.InputTokens = res.Usage.InputTokens
		t.OutputTokens = res.Usage.OutputTokens
		t.CacheReadTokens = res.Usage.CacheReadTokens
		t.CacheWriteTokens = res.Usage.CacheCreationTokens
		t.DurationMS = res.DurationMS
		if err := r.deps.Store.Turns().Finish(context.Background(), t); err != nil {
			r.log().Error("turn finish", "err", err)
		}
		r.deps.Bus.Publish(bus.Item{Kind: bus.KindTurnResult, LoopID: r.loop.ID, Payload: t})

		var trailer time.Duration
		var hasTrailer bool
		if !res.IsError {
			trailer, hasTrailer = ParseTrailer(res.ResultText)
			if r.deps.OnReply != nil && strings.TrimSpace(res.ResultText) != "" {
				r.deps.OnReply(&r.loop, res.ResultText, r.pendingDMs)
			}
		}
		if r.deps.OnTurnDone != nil {
			r.deps.OnTurnDone(&r.loop, trailer, hasTrailer)
		}
	}
	r.turn = nil
	r.pendingDMs = nil
	r.currentBatch = nil

	if len(r.inbox) > 0 && !r.paused {
		r.startTurn()
		return
	}
	r.state = StateIdle
	r.armIdleTimer()
	r.publishState()
}

func (r *Runtime) armIdleTimer() {
	d := time.Duration(r.loop.IdleTimeoutSec) * time.Second
	if d <= 0 {
		d = 90 * time.Second
	}
	r.idleTimer.Reset(d)
}

func (r *Runtime) handleIdleTimeout() {
	if r.state != StateIdle || r.proc == nil {
		return
	}
	r.state = StateDraining
	r.publishState()
	_ = r.proc.CloseStdin()
	// exit arrives via procEvents close
}

func (r *Runtime) handleProcExit() {
	exit := r.proc.Wait()
	pid := r.proc.PID()
	r.proc = nil
	r.procEvents = nil
	r.idleTimer.Stop()
	_ = r.deps.Store.Loops().SetRuntime(context.Background(), r.loop.ID, r.loop.CurrentSessionID, 0)
	r.storeSpoolEvent("proc_exit", fmt.Sprintf(`{"pid":%d,"code":%d}`, pid, exit.Code))

	wasDraining := r.state == StateDraining
	inTurn := r.turn != nil
	sessionLost := claude.IsSessionNotFound(exit)

	if inTurn {
		// process died mid-turn
		t := r.turn
		t.EndedAt = now()
		t.IsError = true
		_ = r.deps.Store.Turns().Finish(context.Background(), t)
		r.turn = nil
		r.pendingDMs = nil
	}

	switch {
	case wasDraining || (exit.Code == 0 && !inTurn):
		// clean idle exit
		r.currentBatch = nil
		r.state = StateAsleep
		r.publishState()
		if len(r.inbox) > 0 && !r.paused {
			r.wake() // message raced the idle close
		}
	case sessionLost:
		// resume failed: mint a fresh session, requeue the undelivered batch,
		// and retry immediately
		old := r.loop.CurrentSessionID
		_ = r.deps.Store.Sessions().End(context.Background(), old, store.EndReasonLost, now())
		r.storeSpoolEvent("session_lost", fmt.Sprintf(`{"old_session":%q}`, old))
		r.loop.CurrentSessionID = ""
		if len(r.currentBatch) > 0 {
			r.inbox = append(r.currentBatch, r.inbox...)
			r.currentBatch = nil
		}
		r.state = StateAsleep
		r.publishState()
		if len(r.inbox) > 0 && !r.paused {
			r.wake()
		}
	default:
		r.currentBatch = nil
		r.storeSpoolEvent("crash", fmt.Sprintf(`{"code":%d,"stderr":%q}`, exit.Code, tail(exit.Stderr, 2000)))
		r.log().Warn("claude process crashed", "code", exit.Code, "stderr", tail(exit.Stderr, 500))
		r.crashBackoff()
	}
}

func (r *Runtime) crashBackoff() {
	if r.backoff == 0 {
		r.backoff = backoffStart
	} else {
		r.backoff *= 2
		if r.backoff > backoffCap {
			r.backoff = backoffCap
		}
	}
	r.state = StateAsleep
	r.publishState()
	if len(r.inbox) > 0 {
		r.retryTimer.Reset(r.backoff)
	}
}

func (r *Runtime) handleShutdown() {
	if r.proc != nil {
		_ = r.proc.Kill()
		r.proc.Wait()
		_ = r.deps.Store.Loops().SetRuntime(context.Background(), r.loop.ID, r.loop.CurrentSessionID, 0)
	}
}

// --- helpers ---

func (r *Runtime) loopHadHistory() bool {
	turns, err := r.deps.Store.Turns().ListByLoop(context.Background(), r.loop.ID, 1)
	return err == nil && len(turns) > 0
}

func (r *Runtime) recentReplies() []string {
	turns, err := r.deps.Store.Turns().ListByLoop(context.Background(), r.loop.ID, recentTurnCount)
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

func (r *Runtime) storeClaudeEvent(ev claude.Event) {
	e := &store.Event{
		LoopID:    r.loop.ID,
		SessionID: r.loop.CurrentSessionID,
		TS:        now(),
		Type:      ev.Type,
		Subtype:   ev.Subtype,
		Payload:   string(ev.Raw),
	}
	if r.turn != nil {
		e.TurnID = r.turn.ID
	}
	if _, err := r.deps.Store.Events().Insert(context.Background(), e); err != nil {
		r.log().Error("event insert", "err", err)
	}
	r.deps.Bus.Publish(bus.Item{Kind: bus.KindAgentEvent, LoopID: r.loop.ID, Payload: e})
}

func (r *Runtime) storeSpoolEvent(subtype, payload string) {
	r.storeEventFull("spool", subtype, payload)
}

func (r *Runtime) storeEventFull(typ, subtype, payload string) {
	e := &store.Event{
		LoopID:    r.loop.ID,
		SessionID: r.loop.CurrentSessionID,
		TS:        now(),
		Type:      typ,
		Subtype:   subtype,
		Payload:   payload,
	}
	if r.turn != nil {
		e.TurnID = r.turn.ID
	}
	if _, err := r.deps.Store.Events().Insert(context.Background(), e); err != nil {
		r.log().Error("event insert", "err", err)
	}
	r.deps.Bus.Publish(bus.Item{Kind: bus.KindAgentEvent, LoopID: r.loop.ID, Payload: e})
}

// State returns the last published runtime state (safe from any goroutine).
func (r *Runtime) State() string {
	if s, ok := r.stateSnap.Load().(string); ok {
		return s
	}
	return StateAsleep
}

func (r *Runtime) publishState() {
	state := r.state
	if r.paused {
		state = StatePaused
	}
	r.stateSnap.Store(state)
	r.deps.Bus.Publish(bus.Item{Kind: bus.KindLoopStatus, LoopID: r.loop.ID, Payload: map[string]any{
		"loop_id": r.loop.ID,
		"name":    r.loop.Name,
		"state":   state,
	}})
}

func (r *Runtime) log() *slog.Logger {
	if r.deps.Logger == nil {
		return slog.Default()
	}
	return r.deps.Logger.With("loop", r.loop.Name)
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
