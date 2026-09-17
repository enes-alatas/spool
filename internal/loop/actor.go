package loop

import (
	"context"
	crand "crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"strings"
	"sync/atomic"
	"time"

	"github.com/enes-alatas/spool/internal/bus"
	"github.com/enes-alatas/spool/internal/claude"
	"github.com/enes-alatas/spool/internal/runtime"
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
	// StateWorkstationDown overlays every other state — pause included —
	// while the loop's workstation is unreachable for a reason nobody chose
	// (ADR-0017): it is the alert the operator must act on. Ensure
	// self-heals it at next wake.
	StateWorkstationDown = "workstation_down"
	// StateWorkstationOff is the calm counterpart: the workstation is down
	// because the operator switched it off (ADR-0021). It is equally
	// not-running, so the fleet says so, but it is expected rather than
	// wrong — and it yields to pause, which is a statement about the loop
	// rather than its machine.
	StateWorkstationOff = "workstation_off"
)

// Why a workstation is down, for the control room: nothing when it is up,
// and otherwise whether the operator meant it.
const (
	DownReasonPoweredOff  = "powered_off"
	DownReasonUnreachable = "unreachable"
)

// The operator's power controls, as passed to Power.
const (
	PowerRestart  = "restart"
	PowerOff      = "poweroff"
	PowerOn       = "poweron"
	PowerRecreate = "recreate"
)

const (
	maxInbox        = 100
	backoffStart    = 10 * time.Second
	backoffCap      = 5 * time.Minute
	recentTurnCount = 5
	// deadResumeLimit is how many resumes may die before the CLI announces
	// itself before the session is written off as unusable. Two, so a single
	// transient failure still gets its ordinary retry.
	deadResumeLimit = 2
	// drainGrace bounds a stdin-close drain; a process that ignores EOF is
	// killed rather than left parking the loop (and any queued work) forever.
	drainGrace = 30 * time.Second

	defaultHealthInterval = 45 * time.Second
	healthCheckTimeout    = 10 * time.Second
	// powerTimeout bounds one power control. Generous because recreate on a
	// cold image pulls and builds before it can answer.
	powerTimeout = 10 * time.Minute
	// procStopGrace is how long a power control waits for the claude process
	// it killed to close its stdout before carrying on regardless.
	procStopGrace = 10 * time.Second
)

// poweredOffDetail is what the control room shows for a workstation the
// operator switched off; down_reason carries the machine-readable half.
const poweredOffDetail = "powered off by the operator"

// Deps wires an actor to the rest of the system without import cycles.
type Deps struct {
	Store store.Store
	Bus   *bus.Bus
	// Runtimes holds the available SandboxRuntime implementations by kind;
	// each loop runs on the one its runtime field names (ADR-0018).
	Runtimes        map[string]runtime.Runtime
	PartialMessages bool
	Logger          *slog.Logger

	// WorkstationHealthInterval paces each loop's liveness poll; 0 means
	// the default. The docker runtime answers from one batched sweep behind
	// a short TTL, so the interval is about UI freshness, not cost.
	WorkstationHealthInterval time.Duration

	// SystemPrompt builds the --append-system-prompt for a loop (peers are
	// resolved at call time so every wake sees the current fleet).
	SystemPrompt func(l *store.Loop) string
	// MCPEndpoint returns the hub MCP URL reachable from this loop's
	// runtime ("" = don't connect the tool). Wired in cmd, which knows the
	// listen address and each runtime's network path to it (ADR-0026).
	MCPEndpoint func(l *store.Loop) string
	// OnTurnStart opens the loop's per-turn send budget (ADR-0026).
	OnTurnStart func(l *store.Loop)
	// SendsThisTurn summarizes the messages the loop has sent since its
	// budget last opened — what a redelivered batch's fresh session is told,
	// so it can identify the lost turn's sends and not repeat them
	// (ADR-0026 decision 5).
	SendsThisTurn func(loopID string) []string
	// OnTurnDone reschedules the loop's next tick after any completed turn.
	OnTurnDone func(l *store.Loop, trailer time.Duration, hasTrailer bool)
	// ClaudeToken returns the operator's stored setup-token, or "" when none is
	// configured. Workstation (contained) runtimes inject it as
	// CLAUDE_CODE_OAUTH_TOKEN; bare loops use the host login and never call it.
	ClaudeToken func(ctx context.Context) (string, error)
}

// runtimeFor picks the runtime a loop runs on. Unknown kinds return nil —
// callers surface that as a workstation problem rather than crashing the
// server (a DB written by a newer Spool could name a kind this build lacks).
func (deps *Deps) runtimeFor(kind string) runtime.Runtime {
	if kind == "" {
		kind = store.RuntimeBare
	}
	return deps.Runtimes[kind]
}

type cmd struct {
	kind  string // deliver|tick|pause|resume|kill|update|power|shutdown
	env   Envelope
	loop  *store.Loop
	verb  string        // for power
	reply chan error    // for power: the caller waits on the result
	done  chan struct{} // for shutdown
}

// Actor owns one loop: a single goroutine serializing every command,
// event and timer for that loop, and the claude process it drives.
type Actor struct {
	deps       Deps
	cmds       chan cmd
	stateSnap  atomic.Value // string; last published state, for REST reads
	healthSnap atomic.Value // runtime.Health; last workstation poll, for REST reads
	offSnap    atomic.Bool  // the operator's power-off intent, for REST reads

	// goroutine-owned state below
	loop         store.Loop
	state        string
	paused       bool
	wsDown       bool   // workstation unreachable; overlays the state
	wsDetail     string // why, when wsDown
	proc         runtime.Proc
	procEvents   <-chan claude.Event
	inbox        []Envelope
	currentBatch []Envelope // in-flight batch, kept for redelivery on session loss
	turn         *store.Turn
	redelivered  bool         // next batch repeats a turn whose session was lost mid-flight
	lostSends    []string     // sends made by every lost attempt of the turn being redelivered
	freshSpawn   bool         // current process was started with --session-id (not resume)
	sawInit      bool         // current process got as far as announcing itself
	deadResumes  int          // consecutive resumes that died before announcing
	activeModel  string       // model the CLI reported at init, for the turn record
	lastCall     claude.Usage // usage of the in-flight turn's latest API call (assistant event)
	backoff      time.Duration

	// Context rotation (ADR-0022): the loop sheds its context proactively,
	// before the window's degradation zone, by writing a handoff note as its
	// session's last turn and starting fresh from it.
	fillPct int // context fill of the last measured turn, percent of the model's window (0 = unknown)
	// armPct/forcePct are the rotation thresholds this wake runs on. Read
	// once at the start of each wake rather than on every measurement: they
	// are operator settings that change between wakes, not within one, and
	// the actor goroutine was querying the store up to four times a turn
	// for an answer that could not have moved.
	armPct       int
	forcePct     int
	armed        bool   // fill crossed the arm threshold; rotate at the next quiet boundary
	rotateAsked  bool   // operator asked for a rotation at the next quiet boundary
	handoffTurn  bool   // the in-flight turn is the rotation's handoff request
	rotateOnExit bool   // rotate to a fresh session once the draining process exits
	handoffNote  string // captured handoff reply, carried into the next fresh session's preamble
	powering     bool   // a power verb is running; nothing may spawn a process under it

	idleTimer   *time.Timer
	retryTimer  *time.Timer
	healthTimer *time.Timer
}

func NewActor(deps Deps, l *store.Loop) *Actor {
	actor := &Actor{
		deps:  deps,
		cmds:  make(chan cmd, 32),
		loop:  *l,
		state: StateAsleep,
		// the defaults hold until the first wake reads the operator's:
		// a zero force threshold would read as "every turn is over it"
		armPct:   DefaultContextArmPercent,
		forcePct: DefaultContextForcePercent,
	}
	actor.paused = l.Status == store.StatusPaused
	// A rotation the previous process completed left its note in the store;
	// the successor session has not had its first turn yet, so this actor is
	// the one that owes it the preamble (#66).
	actor.handoffNote = l.HandoffNote
	actor.offSnap.Store(l.WorkstationOff)
	actor.healthSnap.Store(runtime.Health{Up: true}) // optimistic until the first poll
	if l.WorkstationOff {
		// switched off before the orchestrator restarted: it is still off,
		// and still calmly so
		actor.healthSnap.Store(runtime.Health{Up: false, Detail: poweredOffDetail})
	}
	actor.idleTimer = time.NewTimer(time.Hour)
	actor.idleTimer.Stop()
	actor.retryTimer = time.NewTimer(time.Hour)
	actor.retryTimer.Stop()
	actor.healthTimer = time.NewTimer(actor.healthInterval())
	go actor.run()
	return actor
}

// --- public API (thread-safe; commands are serialized onto the actor) ---

func (actor *Actor) Deliver(env Envelope) { actor.cmds <- cmd{kind: "deliver", env: env} }
func (actor *Actor) Tick()                { actor.cmds <- cmd{kind: "tick"} }
func (actor *Actor) Pause()               { actor.cmds <- cmd{kind: "pause"} }
func (actor *Actor) Resume()              { actor.cmds <- cmd{kind: "resume"} }
func (actor *Actor) Kill()                { actor.cmds <- cmd{kind: "kill"} }

// Rotate asks the loop to shed its context through the ADR-0022 handoff flow
// at the next quiet boundary, and blocks for the immediate verdict: an error
// means there is no session to rotate.
func (actor *Actor) Rotate() error {
	reply := make(chan error, 1)
	actor.cmds <- cmd{kind: "rotate", reply: reply}
	return <-reply
}

// Power runs one of the operator's power controls against the loop's
// workstation and blocks until it is done, so the caller can answer with the
// state it produced rather than a promise (ADR-0021). Serialized onto the
// actor like every other command, which is what keeps it from racing a wake.
func (actor *Actor) Power(verb string) error {
	reply := make(chan error, 1)
	actor.cmds <- cmd{kind: "power", verb: verb, reply: reply}
	return <-reply
}
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
			if len(actor.inbox) > 0 && actor.state == StateAsleep && !actor.loop.WorkstationOff {
				actor.wake()
			}
		case <-actor.healthTimer.C:
			actor.checkWorkstation()
			actor.healthTimer.Reset(actor.healthInterval())
		}
	}
}

func (actor *Actor) handleCmd(command cmd) {
	switch command.kind {
	case "deliver":
		if actor.paused || actor.loop.WorkstationOff {
			if b, err := json.Marshal(command.env); err == nil {
				_ = actor.deps.Store.Inbox().Push(context.Background(), actor.loop.ID, string(b), now())
			}
			return
		}
		actor.enqueue(command.env)
		actor.pump()
	case "tick":
		if actor.paused || actor.loop.WorkstationOff {
			// a switched-off workstation stays off: ticks are skipped, not
			// queued, so power-on isn't met by a backlog of stale wakes
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
		actor.drainStoredInbox()
		actor.publishState()
		actor.pump()
	case "power":
		command.reply <- actor.power(command.verb)
	case "rotate":
		command.reply <- actor.requestRotation()
	case "kill":
		if actor.proc != nil {
			_ = actor.proc.Kill()
			// exit handled via procEvents close
		}
	case "update":
		token := actor.loop // keep runtime-only fields
		actor.loop = *command.loop
		actor.loop.CurrentSessionID = token.CurrentSessionID
		actor.loop.WorkstationOff = token.WorkstationOff // power intent is the actor's, not the editor's
		// likewise a rotation in flight: an edit carries the row as it was
		// read, which may already be a wake behind the actor
		actor.loop.RotatePending = token.RotatePending
		actor.loop.HandoffNote = token.HandoffNote
		actor.paused = actor.loop.Status == store.StatusPaused
		actor.publishState()
	}
}

// drainStoredInbox re-queues the messages that arrived while the loop was
// not accepting work — paused, or its workstation switched off. Whatever was
// said to the loop meanwhile is said again the moment it can hear it.
func (actor *Actor) drainStoredInbox() {
	envs, err := actor.deps.Store.Inbox().Drain(context.Background(), actor.loop.ID)
	if err != nil {
		actor.log().Error("inbox drain", "err", err)
		return
	}
	for _, raw := range envs {
		var env Envelope
		if json.Unmarshal([]byte(raw), &env) == nil {
			actor.enqueue(env)
		}
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
	if len(actor.inbox) == 0 || actor.loop.WorkstationOff {
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
	// the thresholds this wake will judge its turns by, read once
	actor.armPct, actor.forcePct = RotationThresholds(ctx, actor.deps.Store.Settings(), actor.log())
	fresh := actor.loop.CurrentSessionID == ""
	if fresh {
		actor.mintSession(ctx)
	}
	spec := actor.wakeSpec(fresh)

	loopRuntime := actor.deps.runtimeFor(actor.loop.Runtime)
	if loopRuntime == nil {
		actor.log().Error("workstation not ready", "err", fmt.Errorf("no %q runtime available", actor.loop.Runtime))
		actor.setWorkstationDown(fmt.Sprintf("no %q runtime available", actor.loop.Runtime))
		actor.crashBackoff()
		return
	}
	system := map[string]string{}
	if needsClaudeToken(loopRuntime) {
		token, err := actor.claudeToken(ctx)
		switch {
		case err != nil:
			actor.log().Error("workstation not ready", "err", err)
			actor.setWorkstationDown(err.Error())
			actor.crashBackoff()
			return
		case token == "":
			actor.setWorkstationDown(noClaudeTokenDetail)
			actor.crashBackoff()
			return
		}
		system["CLAUDE_CODE_OAUTH_TOKEN"] = token
	}
	// Per-loop secrets are read fresh each wake, so an edit lands on the next
	// wake. A read failure fails closed: a loop running without its expected
	// credentials could act on the wrong ones.
	secrets, err := actor.deps.Store.LoopSecrets().List(ctx, actor.loop.ID)
	if err != nil {
		actor.log().Error("workstation not ready", "err", err)
		actor.setWorkstationDown(err.Error())
		actor.crashBackoff()
		return
	}
	spec.Env = buildExecEnv(system, secrets)
	if err := loopRuntime.Ensure(ctx, spec); err != nil {
		actor.log().Error("workstation not ready", "err", err)
		actor.setWorkstationDown(err.Error())
		actor.crashBackoff()
		return
	}
	proc, err := loopRuntime.Start(ctx, spec)
	if err != nil {
		actor.log().Error("spawn failed", "err", err)
		actor.setWorkstationDown(err.Error())
		actor.crashBackoff()
		return
	}
	actor.setWorkstationUp()
	actor.proc = proc
	actor.procEvents = proc.Events()
	actor.freshSpawn = fresh
	actor.sawInit = false
	actor.state = StateWaking
	_ = actor.deps.Store.Loops().SetRuntime(ctx, actor.loop.ID, actor.loop.CurrentSessionID, proc.PID())
	actor.storeSpoolEvent("proc_spawn", fmt.Sprintf(`{"pid":%d,"resume":%v}`, proc.PID(), !fresh))
	actor.publishState()
	// The CLI emits nothing (not even init) until the first user message, so
	// send queued work immediately rather than waiting for init.
	if len(actor.inbox) > 0 {
		actor.startTurn()
	} else if actor.rotateAsked {
		// woken for nothing but an operator-asked rotation: the handoff
		// turn is the session's only business
		actor.startHandoffTurn()
	}
}

// mintSession gives the loop a fresh claude session id. The id is ours to
// choose (claude accepts --session-id), so it is recorded before the process
// exists and survives a spawn failure.
func (actor *Actor) mintSession(ctx context.Context) {
	sessionID := newUUID()
	actor.loop.CurrentSessionID = sessionID
	_ = actor.deps.Store.Sessions().Create(ctx, &store.Session{
		ID:        sessionID,
		LoopID:    actor.loop.ID,
		StartedAt: now(),
	})
}

// wakeSpec describes this wake to the runtime. Peers are resolved here rather
// than cached, so every wake sees the current fleet in its system prompt.
func (actor *Actor) wakeSpec(fresh bool) runtime.Spec {
	spec := runtime.Spec{
		LoopID:             actor.loop.ID,
		LoopName:           actor.loop.Name,
		WorkDir:            actor.loop.WorkspacePath,
		Model:              actor.loop.Model,
		Effort:             actor.loop.Effort,
		Image:              actor.loop.Image,
		MemMB:              actor.loop.MemMB,
		CPUs:               actor.loop.CPUs,
		AppendSystemPrompt: actor.deps.SystemPrompt(&actor.loop),
		PartialMessages:    actor.deps.PartialMessages,
	}
	if actor.deps.MCPEndpoint != nil && actor.loop.HubMCPToken != "" {
		if url := actor.deps.MCPEndpoint(&actor.loop); url != "" {
			spec.MCPConfig = claude.MCPConfigJSON(url, actor.loop.HubMCPToken)
		}
	}
	if fresh {
		spec.SessionID = actor.loop.CurrentSessionID
	} else {
		spec.ResumeID = actor.loop.CurrentSessionID
	}
	return spec
}

// startTurn sends the queue's oldest conversation as one batched user
// message — never more than one conversation per turn, so a private and a
// group exchange cannot blend into one answer (ADR-0026). Whatever else is
// queued stays put and runs as the following turns. Past the force ceiling
// the handoff turn runs first and the queue is delivered to the fresh
// session after rotation.
func (actor *Actor) startTurn() {
	if actor.needsForcedRotation() {
		actor.startHandoffTurn()
		return
	}
	key := actor.inbox[0].conversationKey()
	var batch, rest []Envelope
	for _, env := range actor.inbox {
		if env.conversationKey() == key {
			batch = append(batch, env)
		} else {
			rest = append(rest, env)
		}
	}
	actor.inbox = rest
	actor.sendBatch(batch)
}

// needsForcedRotation reports whether the context is past the force ceiling.
// Rotation then stops waiting for a quiet boundary: a loop that is never idle
// must still not ride into the CLI's end-of-window compaction.
func (actor *Actor) needsForcedRotation() bool {
	if actor.handoffTurn || actor.fillPct == 0 {
		return false
	}
	return actor.fillPct >= actor.forcePct
}

// requestRotation latches an operator-asked rotation: ADR-0022's handoff
// flow, on demand instead of at a fill threshold. Queued work still runs
// first — the handoff turn takes the next quiet boundary — while an idle
// loop rotates now, and an asleep one is woken just to write its note.
func (actor *Actor) requestRotation() error {
	if actor.loop.CurrentSessionID == "" {
		return fmt.Errorf("the loop has no session to rotate")
	}
	actor.rotateAsked = true
	if actor.paused || actor.loop.WorkstationOff || len(actor.inbox) > 0 {
		return nil // latched; the next quiet boundary takes it
	}
	switch actor.state {
	case StateIdle:
		actor.startHandoffTurn()
	case StateAsleep:
		actor.wake()
	}
	return nil
}

// startHandoffTurn asks the loop, as this session's last turn, to write the
// handoff note its successor starts from. The inbox stays put; it is
// delivered to the fresh session after rotation.
func (actor *Actor) startHandoffTurn() {
	actor.handoffTurn = true
	// Recorded before the turn runs, not after it: from this moment the
	// session has been told it ends here, and a restart in the window that
	// follows — mid-turn, or mid-drain after it — must retire the session
	// rather than resume one its own last turn retired (#66).
	actor.setRotationState(true, actor.handoffNote)
	actor.sendBatch([]Envelope{RotationEnvelope(time.Now())})
}

// sendBatch runs one turn over the given envelopes.
func (actor *Actor) sendBatch(batch []Envelope) {
	actor.currentBatch = batch

	texts := make([]string, 0, len(batch))
	trigger := store.TriggerTick
	for _, env := range batch {
		texts = append(texts, env.Text)
		if env.Trigger != store.TriggerTick {
			trigger = env.Trigger
		}
	}
	text := strings.Join(texts, "\n\n---\n\n")
	if actor.redelivered {
		// The lost attempts' sends are facts (immediate delivery, ADR-0026);
		// the retry must know what they were before its budget reopens
		// below. They accumulate in lostSends because each attempt's log
		// restarts empty — the last attempt's alone would forget what the
		// attempts before it sent.
		actor.redelivered = false
		if actor.deps.SendsThisTurn != nil {
			actor.lostSends = append(actor.lostSends, actor.deps.SendsThisTurn(actor.loop.ID)...)
		}
		if len(actor.lostSends) > 0 {
			var note strings.Builder
			note.WriteString("[system note · a previous attempt at this turn already sent the following; do not send them again]")
			for _, s := range actor.lostSends {
				note.WriteString("\n- " + s)
			}
			text = note.String() + "\n\n---\n\n" + text
		}
	}
	if actor.deps.OnTurnStart != nil {
		actor.deps.OnTurnStart(&actor.loop)
	}
	if actor.freshSpawn {
		switch {
		case actor.handoffNote != "":
			// The note is not cleared here: a send that fails or a process
			// that dies before this turn completes must not cost it. It is
			// cleared when a turn finishes, by which point the fresh session
			// has the preamble in its history.
			text = RotationPreamble(&actor.loop, actor.handoffNote, actor.recentReplies()) + "\n\n---\n\n" + text
		case actor.loopHadHistory():
			text = SessionLostPreamble(&actor.loop, actor.recentReplies()) + "\n\n---\n\n" + text
		}
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
	actor.lastCall = claude.Usage{}

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
		actor.sawInit = true
		actor.deadResumes = 0
		if ev.Init != nil && ev.Init.Model != "" {
			// what the loop is really running on: its configured model may
			// be empty or an alias that floats between releases
			actor.activeModel = ev.Init.Model
			if ContextLimit(actor.activeModel) == 0 {
				// otherwise rotation stays off in total silence, and the loop
				// rides to the CLI's end-of-window compaction unnoticed
				actor.log().Warn("context window unknown; rotation will not trigger", "model", actor.activeModel)
			}
		}
		actor.storeClaudeEvent(ev)
		if actor.state == StateWaking && actor.turn == nil {
			// spawned with no work (shouldn't normally happen)
			actor.state = StateIdle
			actor.armIdleTimer()
			actor.publishState()
		}
	case ev.Type == "assistant":
		// each API call of the turn reports its own usage here; the latest
		// one is the context-occupancy measure (the result's is summed)
		if ev.Assistant != nil && ev.Assistant.Usage != (claude.Usage{}) {
			actor.lastCall = ev.Assistant.Usage
		}
		actor.storeClaudeEvent(ev)
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
	if claude.IsPromptTooLong(res) {
		actor.recordOversizedBatch()
	}
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
		t.ContextTokens = contextOccupancy(actor.lastCall)
		t.DurationMS = res.DurationMS
		t.Model = actor.activeModel
		if err := actor.deps.Store.Turns().Finish(context.Background(), t); err != nil {
			actor.log().Error("turn finish", "err", err)
		}
		actor.deps.Bus.Publish(bus.Item{Kind: bus.KindTurnResult, LoopID: actor.loop.ID, Payload: t})

		// The final reply is the turn's status note (ADR-0026): stored and
		// published above, delivered to no conversation — a loop that wants
		// to say something sends it explicitly. Only the trailer is read
		// (the handoff turn's reply is a note to the loop's successor and
		// is not even that).
		var trailer time.Duration
		var hasTrailer bool
		if !res.IsError && !actor.handoffTurn {
			trailer, hasTrailer = ParseTrailer(res.ResultText)
		}
		if actor.deps.OnTurnDone != nil && !actor.handoffTurn {
			actor.deps.OnTurnDone(&actor.loop, trailer, hasTrailer)
		}
	}
	actor.turn = nil
	actor.currentBatch = nil
	actor.lostSends = nil
	// A completed turn means any pending handoff note reached its session;
	// finishHandoff below sets the next one after this clears the old.
	if actor.handoffNote != "" {
		actor.setRotationState(actor.loop.RotatePending, "")
	}
	actor.handoffNote = ""

	if actor.handoffTurn {
		actor.finishHandoff(res)
		return
	}
	actor.measureContext()

	if len(actor.inbox) > 0 && !actor.paused {
		actor.startTurn()
		return
	}
	if (actor.armed || actor.rotateAsked) && !actor.paused {
		// quiet boundary: the wake left no queued work, so this is the
		// cheapest moment to shed the context (ADR-0022) — whether the
		// fill armed it or the operator asked for it
		actor.startHandoffTurn()
		return
	}
	actor.state = StateIdle
	actor.armIdleTimer()
	actor.publishState()
}

// finishHandoff ends the rotation's handoff turn: the reply is the note the
// next session starts from (an errored turn donates none — the rotation still
// happens with the summary-less carry), and the process is drained so the
// rotation lands on a dead process rather than under a live one.
func (actor *Actor) finishHandoff(res *claude.ResultInfo) {
	actor.handoffTurn = false
	if res != nil && !res.IsError {
		actor.handoffNote = strings.TrimSpace(res.ResultText)
	}
	actor.rotateOnExit = true
	actor.setRotationState(true, actor.handoffNote)
	actor.state = StateDraining
	actor.publishState()
	_ = actor.proc.CloseStdin()
	// the timer becomes the drain's deadline: a process that ignores EOF
	// would otherwise park the rotation — and any queued work — forever
	actor.idleTimer.Reset(drainGrace)
}

// setRotationState mirrors the actor's rotation bookkeeping into the store so
// a restart inherits the decision instead of the loop paying for it twice
// (#66). Best effort, like the other runtime writes: a failed write costs the
// old behaviour, never the rotation.
func (actor *Actor) setRotationState(pending bool, note string) {
	actor.loop.RotatePending = pending
	actor.loop.HandoffNote = note
	if err := actor.deps.Store.Loops().SetRotation(context.Background(), actor.loop.ID, pending, note); err != nil {
		actor.log().Error("persist rotation state", "err", err)
	}
}

// contextOccupancy is what a session's next prompt would carry into the
// window after this API call: the prompt it sent, the cached prefix it
// reread, and the suffix it just wrote to the cache (the next call rereads
// that too). The same formula backs the CLI's own /context gauge.
func contextOccupancy(u claude.Usage) int {
	return u.InputTokens + u.CacheReadTokens + u.CacheCreationTokens
}

// measureContext records how full the model's window was when the turn that
// just finished made its last API call, and arms rotation once it crosses
// the arm threshold. The last call, not the result event's usage: the CLI
// sums usage across the turn's API steps, and every step rereads the cached
// prefix, so a long turn's sum reaches multiples of the window (#94). An
// unknown window never arms: a ratio against a guess would rotate on
// fiction. Zero usage (an errored turn) keeps the previous measurement
// rather than reading as empty.
func (actor *Actor) measureContext() {
	window := ContextLimit(actor.activeModel)
	tokens := contextOccupancy(actor.lastCall)
	if window <= 0 || tokens == 0 {
		return
	}
	actor.fillPct = FillPercent(tokens, window)
	// Recomputed, not latched: a drop below the threshold — the CLI compacted
	// after all, or the operator raised the bar — disarms a pointless rotation.
	actor.armed = actor.fillPct >= actor.armPct
}

func (actor *Actor) armIdleTimer() {
	d := time.Duration(actor.loop.IdleTimeoutSec) * time.Second
	if d <= 0 {
		d = 90 * time.Second
	}
	actor.idleTimer.Reset(d)
}

func (actor *Actor) handleIdleTimeout() {
	if actor.proc == nil {
		return
	}
	switch actor.state {
	case StateIdle:
		actor.state = StateDraining
		actor.publishState()
		_ = actor.proc.CloseStdin()
		// exit arrives via procEvents close; the timer re-arms as the
		// drain's deadline
		actor.idleTimer.Reset(drainGrace)
	case StateDraining:
		// the process ignored EOF past the grace: kill it — the exit path
		// still carries out whatever the drain was for (sleep or rotation)
		actor.log().Warn("drain timed out; killing process")
		_ = actor.proc.Kill()
	}
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
	wasHandoff := actor.handoffTurn
	actor.handoffTurn = false
	sessionLost := claude.IsSessionNotFound(exit)
	// A resume that dies before the CLI announces itself never loaded the
	// session at all. One can be a blip; a run of them means this session
	// cannot be resumed any more, whatever the CLI printed about it — which
	// is why this is classified by what the process did rather than by
	// matching a message.
	deadOnResume := !actor.freshSpawn && !actor.sawInit && exit.Code != 0
	if deadOnResume {
		actor.deadResumes++
	}

	if inTurn {
		// process died mid-turn
		t := actor.turn
		t.EndedAt = now()
		t.IsError = true
		_ = actor.deps.Store.Turns().Finish(context.Background(), t)
		actor.turn = nil
	}

	switch {
	case wasHandoff:
		// the handoff turn died with the process; rotate anyway — the crash
		// costs the note, not the rotation. Its envelope is not requeued: the
		// rotation it asked for is happening.
		actor.currentBatch = nil
		actor.rotateContext(tail(exit.Stderr, 500))
	case wasDraining || (exit.Code == 0 && !inTurn):
		// clean idle exit
		actor.currentBatch = nil
		if actor.rotateOnExit {
			actor.rotateContext("")
			return
		}
		actor.state = StateAsleep
		actor.publishState()
		if len(actor.inbox) > 0 && !actor.paused && !actor.loop.WorkstationOff && !actor.powering {
			actor.wake() // message raced the idle close
		}
	case sessionLost:
		// the CLI said outright that the session is gone
		actor.rotateSession("session_lost", "")
	case deadOnResume:
		// The session never loaded. One failure gets an ordinary retry — it
		// may have been a blip — but a session that cannot be resumed twice
		// running will not be resumable later either. Retrying it forever
		// would leave a perfectly healthy loop reading as dead, so rotate
		// onto a fresh session and carry the mission across.
		actor.storeSpoolEvent("resume_failed",
			fmt.Sprintf(`{"session":%q,"attempt":%d,"stderr":%q}`,
				actor.loop.CurrentSessionID, actor.deadResumes, tail(exit.Stderr, 500)))
		if actor.deadResumes >= deadResumeLimit {
			actor.rotateSession("session_unusable", tail(exit.Stderr, 500))
			return
		}
		// keep the undelivered work: the retry is what earns the second
		// data point, and the generic crash path would drop it
		if len(actor.currentBatch) > 0 {
			actor.inbox = append(actor.currentBatch, actor.inbox...)
			actor.currentBatch = nil
			actor.redelivered = true
		}
		actor.crashBackoff()
	default:
		// the batch is dropped, so any lost-send tally for it dies with it
		actor.currentBatch = nil
		actor.lostSends = nil
		actor.storeSpoolEvent("crash", fmt.Sprintf(`{"code":%d,"stderr":%q}`, exit.Code, tail(exit.Stderr, 2000)))
		actor.log().Warn("claude process crashed", "code", exit.Code, "stderr", tail(exit.Stderr, 500))
		actor.crashBackoff()
	}
}

// recordOversizedBatch notes that the turn's payload did not fit the model's
// context window. The batch dies with the turn either way — what this adds is
// a verdict the operator can act on: an ordinary errored turn says a loop
// failed, while this says a specific message was too big to deliver and no
// retry will change that. The text is not recorded: it is the thing
// that was too large, and the timeline is not the place for it.
func (actor *Actor) recordOversizedBatch() {
	chars := 0
	for _, env := range actor.currentBatch {
		chars += len(env.Text)
	}
	actor.storeSpoolEvent("message_too_long", fmt.Sprintf(
		`{"messages":%d,"chars":%d,"model":%q,"window_tokens":%d}`,
		len(actor.currentBatch), chars, actor.activeModel, ContextLimit(actor.activeModel)))
	actor.log().Warn("message too long for the model's context window",
		"messages", len(actor.currentBatch), "chars", chars, "model", actor.activeModel)
}

// rotateSession abandons the loop's claude session and starts a fresh one,
// carrying the mission and the loop's recent replies across through the
// handoff preamble. Undelivered work is requeued, so nothing said to the loop
// is lost — only the model's own recollection of the conversation is.
func (actor *Actor) rotateSession(reason, detail string) {
	old := actor.loop.CurrentSessionID
	_ = actor.deps.Store.Sessions().End(context.Background(), old, store.EndReasonLost, now())
	if detail == "" {
		actor.storeSpoolEvent(reason, fmt.Sprintf(`{"old_session":%q}`, old))
	} else {
		actor.storeSpoolEvent(reason, fmt.Sprintf(`{"old_session":%q,"stderr":%q}`, old, detail))
		actor.log().Warn("session cannot be resumed; rotating", "old_session", old, "stderr", detail)
	}
	if len(actor.currentBatch) > 0 {
		actor.inbox = append(actor.currentBatch, actor.inbox...)
		actor.currentBatch = nil
		actor.redelivered = true
	}
	actor.retireSession()
	// The retry, not an immediate wake: a rotation forced by failure is no
	// evidence that a spawn will work now, and waking straight into one
	// respawns a permanently broken loop every few seconds (#89). The
	// ladder it climbs is the same one an ordinary crash climbs, so the
	// two interleave into one sequence instead of resetting each other.
	actor.crashBackoff()
}

// rotateContext is the deliberate counterpart of rotateSession (ADR-0022):
// the context crossed its threshold and the old session is retired by choice,
// its handoff note (when one was captured) waiting to seed the next session's
// first turn.
func (actor *Actor) rotateContext(detail string) {
	actor.rotateOnExit = false
	// A rotation taken by choice starts the new session unburdened: the old
	// one was working, it just filled up.
	actor.backoff = 0
	old := actor.loop.CurrentSessionID
	_ = actor.deps.Store.Sessions().End(context.Background(), old, store.EndReasonRotated, now())
	if detail == "" {
		actor.storeSpoolEvent("context_rotated", fmt.Sprintf(`{"old_session":%q,"fill_pct":%d}`, old, actor.fillPct))
	} else {
		actor.storeSpoolEvent("context_rotated", fmt.Sprintf(`{"old_session":%q,"fill_pct":%d,"stderr":%q}`, old, actor.fillPct, detail))
	}
	actor.log().Info("context rotated", "old_session", old, "fill_pct", actor.fillPct)
	actor.startFreshSession()
}

// retireSession forgets the current session without deciding what happens
// next; the callers differ on that. The cleared id is persisted immediately:
// a loop that goes quiet after rotating may not wake again before a server
// restart, and a restart must not resurrect the retired session.
//
// The retry ladder is deliberately left alone here. A rotation the loop was
// driven to by failure is no evidence that anything works, and clearing the
// backoff on that path let a loop that could not spawn at all respawn every
// few seconds forever (#89). The paths that know better clear it: a turn
// that completed, and a rotation taken by choice.
func (actor *Actor) retireSession() {
	actor.loop.CurrentSessionID = ""
	_ = actor.deps.Store.Loops().SetRuntime(context.Background(), actor.loop.ID, "", 0)
	actor.deadResumes = 0
	actor.armed = false
	actor.rotateAsked = false
	// The pending rotation is spent with the session it was pending on,
	// whichever path retired it — a rotation by choice, or a failure that
	// overtook one. Left set, it is a latch: the next restart would read it
	// against a healthy session, retire that one as rotated, and re-apply a
	// note it has already carried. The note itself is not cleared here — it
	// is owed to the next session's first turn, restart or no.
	actor.setRotationState(false, actor.handoffNote)
	actor.fillPct = 0
	actor.state = StateAsleep
	actor.publishState()
}

// startFreshSession retires the session and wakes straight onto a new one:
// the path for a loop that is working — a finished turn, or a rotation taken
// by choice.
func (actor *Actor) startFreshSession() {
	actor.retireSession()
	// Never wake while a power verb is underway: stopProcess can land here
	// mid-verb via a draining rotation, and a process spawned now would run
	// on the workstation the verb is about to halt or destroy. The queued
	// work stays put; the verb's end delivers it.
	if len(actor.inbox) > 0 && !actor.paused && !actor.loop.WorkstationOff && !actor.powering {
		actor.wake()
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
		// The wait is the only visible sign that a loop is failing rather
		// than idle, and the ladder it climbs is what says "this is not
		// getting better" — so record it where the operator reads the
		// loop's story, next to the crash it follows.
		actor.storeSpoolEvent("retry_scheduled", fmt.Sprintf(`{"in_ms":%d}`, actor.backoff.Milliseconds()))
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

// --- workstation liveness (ADR-0017: a seam duty, surfaced as a state) ---

// noClaudeTokenDetail is the workstation-down reason shown when a contained
// loop has no operator setup-token to run claude under.
const noClaudeTokenDetail = "Claude token not configured. Please add a setup-token in Settings"

// needsClaudeToken reports whether a runtime is a contained workstation that
// must carry the operator's setup-token. Bare loops use the host login.
func needsClaudeToken(loopRuntime runtime.Runtime) bool {
	return loopRuntime.Kind() != store.RuntimeBare
}

// buildExecEnv assembles a wake's env: the system-injected vars first, then the
// loop's own secrets overlaid on top. Secrets are applied last on purpose — a
// loop secret wins a name collision with a system var, the operator's
// deliberate escape hatch (a per-loop CLAUDE_CODE_OAUTH_TOKEN, say). Returns
// nil when there is nothing to inject, so spec.Env stays unset.
func buildExecEnv(system map[string]string, secrets []*store.LoopSecret) map[string]string {
	if len(system) == 0 && len(secrets) == 0 {
		return nil
	}
	env := make(map[string]string, len(system)+len(secrets))
	for k, v := range system {
		env[k] = v
	}
	for _, s := range secrets {
		env[s.Name] = s.Value
	}
	return env
}

// claudeToken reads the operator's stored setup-token, treating a missing
// wiring as "unconfigured" rather than panicking a loop's goroutine.
func (actor *Actor) claudeToken(ctx context.Context) (string, error) {
	if actor.deps.ClaudeToken == nil {
		return "", nil
	}
	return actor.deps.ClaudeToken(ctx)
}

// power carries out one operator power control, in the actor's goroutine.
// Every verb that takes the workstation away ends the running turn first, so
// a turn is never silently truncated, and the caller gets the resulting
// state rather than a promise.
func (actor *Actor) power(verb string) error {
	loopRuntime := actor.deps.runtimeFor(actor.loop.Runtime)
	if loopRuntime == nil {
		return fmt.Errorf("no %q runtime available", actor.loop.Runtime)
	}
	if !loopRuntime.HasWorkstation() {
		return runtime.ErrUnsupported
	}
	ctx, cancel := context.WithTimeout(context.Background(), powerTimeout)
	defer cancel()
	actor.powering = true
	defer func() { actor.powering = false }()

	if verb != PowerOn {
		// poweron is documented idempotent: it must not end a turn that is
		// running on a workstation which is already up
		actor.stopProcess(verb)
	}
	actor.publishWorkstationVerb(verb)
	spec := actor.wakeSpec(actor.loop.CurrentSessionID == "")

	// The operator's intent is recorded only once the verb it describes has
	// actually happened. A verb that fails leaves everything as it found it:
	// a failed halt must not suspend a loop whose workstation is still up,
	// and a failed power-on must not discard an off-intent that still holds.
	var err error
	switch verb {
	case PowerOff:
		if err = loopRuntime.Halt(ctx, actor.loop.ID); err == nil {
			actor.setWorkstationOff(true)
		}
	case PowerOn:
		if err = loopRuntime.Ensure(ctx, spec); err == nil {
			actor.setWorkstationOff(false)
		}
	case PowerRestart:
		if err = loopRuntime.Halt(ctx, actor.loop.ID); err == nil {
			err = loopRuntime.Ensure(ctx, spec)
		}
		if err == nil {
			actor.setWorkstationOff(false)
		}
	case PowerRecreate:
		if err = loopRuntime.Destroy(ctx, actor.loop.ID); err == nil {
			actor.forgetSession(ctx)
			err = loopRuntime.Ensure(ctx, spec)
		}
		if err == nil {
			actor.setWorkstationOff(false)
		}
	default:
		return fmt.Errorf("unknown power verb %q", verb)
	}

	actor.storeSpoolEvent("workstation_power", fmt.Sprintf(`{"verb":%q,"ok":%v}`, verb, err == nil))
	if err != nil {
		if actor.loop.WorkstationOff {
			// still switched off, and still calmly so: the failed verb
			// changed nothing, and the caller gets the error
			actor.checkWorkstation()
		} else {
			actor.setWorkstationDown(err.Error())
		}
		return err
	}
	if verb == PowerOff {
		actor.checkWorkstation() // publishes the calm off state
		return nil
	}
	actor.setWorkstationUp()
	// the machine is back: deliver what was said while it was away
	actor.drainStoredInbox()
	actor.pump()
	return nil
}

// stopProcess ends the claude process a power verb is about to pull the
// ground from under. It waits for stdout to close so the turn is recorded as
// it happened; if the process outlives the grace period the ordinary exit
// path picks it up later, by which time the verb has already run.
func (actor *Actor) stopProcess(verb string) {
	if actor.proc == nil {
		return
	}
	actor.log().Info("power control ends the running turn", "verb", verb)
	_ = actor.proc.Kill()
	grace := time.NewTimer(procStopGrace)
	defer grace.Stop()
	for {
		select {
		case _, open := <-actor.procEvents:
			if !open {
				actor.handleProcExit()
				return
			}
		case <-grace.C:
			return
		}
	}
}

// forgetSession drops the loop's claude session: recreate destroys the state
// it lived in, so the next wake starts fresh instead of failing to resume.
func (actor *Actor) forgetSession(ctx context.Context) {
	old := actor.loop.CurrentSessionID
	if old == "" {
		return
	}
	_ = actor.deps.Store.Sessions().End(ctx, old, store.EndReasonLost, now())
	actor.loop.CurrentSessionID = ""
	_ = actor.deps.Store.Loops().SetRuntime(ctx, actor.loop.ID, "", 0)
	// the fill measurement described the forgotten session; left standing it
	// would force a pointless rotation as the new empty session's first turn
	actor.fillPct = 0
	actor.armed = false
	actor.rotateAsked = false
	// and the rotation this session was asked for: recreate answers the
	// question it was asked — the session is gone — and a pending flag left
	// standing would have the next restart retire the healthy session that
	// replaced it
	actor.setRotationState(false, actor.handoffNote)
	actor.storeSpoolEvent("session_forgotten", fmt.Sprintf(`{"old_session":%q}`, old))
}

// setWorkstationOff records the operator's intent, in memory and in the DB,
// so it survives a restart — a health poll can't tell "switched off" from
// "died" (ADR-0021).
func (actor *Actor) setWorkstationOff(off bool) {
	actor.loop.WorkstationOff = off
	actor.offSnap.Store(off)
	l, err := actor.deps.Store.Loops().Get(context.Background(), actor.loop.ID)
	if err != nil {
		actor.log().Error("power intent not persisted", "err", err)
		return
	}
	l.WorkstationOff = off
	l.UpdatedAt = now()
	if err := actor.deps.Store.Loops().Update(context.Background(), l); err != nil {
		actor.log().Error("power intent not persisted", "err", err)
	}
}

// checkWorkstation polls the loop's workstation and maintains the
// workstation_down overlay. Bare loops answer statically up, so the uniform
// poll costs them nothing.
func (actor *Actor) checkWorkstation() {
	if actor.loop.WorkstationOff {
		// don't poll something the operator switched off: it is down, and
		// the reason is already known
		actor.wsDown = false
		actor.wsDetail = ""
		actor.healthSnap.Store(runtime.Health{Up: false, Detail: poweredOffDetail})
		actor.publishState()
		actor.publishWorkstation()
		return
	}
	loopRuntime := actor.deps.runtimeFor(actor.loop.Runtime)
	if loopRuntime == nil {
		actor.setWorkstationDown(fmt.Sprintf("no %q runtime available", actor.loop.Runtime))
		return
	}
	if needsClaudeToken(loopRuntime) {
		token, err := actor.claudeToken(context.Background())
		switch {
		case err != nil:
			actor.setWorkstationDown(err.Error())
			return
		case token == "":
			// A contained loop can't run without the operator token; say so
			// here too, so the poll doesn't overwrite it with "not found".
			actor.setWorkstationDown(noClaudeTokenDetail)
			return
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), healthCheckTimeout)
	defer cancel()
	health, err := loopRuntime.Health(ctx, actor.loop.ID)
	switch {
	case err != nil:
		actor.setWorkstationDown(err.Error())
	case health.Up:
		actor.setWorkstationUp()
	default:
		actor.setWorkstationDown(health.Detail)
	}
}

// healthInterval jitters the poll so a fleet of actors spreads over the
// runtime's cache window instead of thundering against it at once.
func (actor *Actor) healthInterval() time.Duration {
	interval := actor.deps.WorkstationHealthInterval
	if interval <= 0 {
		interval = defaultHealthInterval
	}
	jitter := time.Duration((rand.Float64()*0.4 - 0.2) * float64(interval))
	return interval + jitter
}

func (actor *Actor) setWorkstationDown(detail string) {
	actor.healthSnap.Store(runtime.Health{Up: false, Detail: detail})
	if actor.wsDown && actor.wsDetail == detail {
		return
	}
	wasUp := !actor.wsDown
	actor.wsDown = true
	actor.wsDetail = detail
	if wasUp {
		actor.storeSpoolEvent("workstation_down", fmt.Sprintf(`{"detail":%q}`, detail))
		actor.log().Warn("workstation down", "detail", detail)
	}
	actor.publishState()
	actor.publishWorkstation()
}

func (actor *Actor) setWorkstationUp() {
	actor.healthSnap.Store(runtime.Health{Up: true})
	if !actor.wsDown {
		return
	}
	actor.wsDown = false
	actor.wsDetail = ""
	actor.storeSpoolEvent("workstation_up", "{}")
	actor.log().Info("workstation up")
	actor.publishState()
	actor.publishWorkstation()
}

// DownReason says why the workstation is not up: nothing when it is,
// powered_off when the operator switched it off, unreachable otherwise.
func (actor *Actor) DownReason() string {
	if actor.WorkstationHealth().Up {
		return ""
	}
	if actor.PoweredOff() {
		return DownReasonPoweredOff
	}
	return DownReasonUnreachable
}

// PoweredOff reports the operator's power-off intent (safe from any
// goroutine).
func (actor *Actor) PoweredOff() bool { return actor.offSnap.Load() }

// publishWorkstation announces the workstation's liveness on the bus. Sent
// when the verdict changes, not on every poll: the control room renders a
// state, not a heartbeat.
func (actor *Actor) publishWorkstation() {
	health := actor.WorkstationHealth()
	actor.deps.Bus.Publish(bus.Item{Kind: bus.KindWorkstation, LoopID: actor.loop.ID, Payload: map[string]any{
		"loop_id":     actor.loop.ID,
		"up":          health.Up,
		"down_reason": actor.DownReason(),
		"detail":      health.Detail,
	}})
}

// publishWorkstationVerb announces that a power control has started, so the
// control room can show work in progress rather than a frozen button — a
// cold recreate can run for minutes and outlive the request's connection.
// The frame is the ordinary workstation one plus the verb under way.
func (actor *Actor) publishWorkstationVerb(verb string) {
	health := actor.WorkstationHealth()
	actor.deps.Bus.Publish(bus.Item{Kind: bus.KindWorkstation, LoopID: actor.loop.ID, Payload: map[string]any{
		"loop_id":     actor.loop.ID,
		"up":          health.Up,
		"down_reason": actor.DownReason(),
		"detail":      health.Detail,
		"verb":        verb,
	}})
}

// WorkstationHealth returns the last observed workstation health (safe from
// any goroutine; REST reads never touch the runtime).
func (actor *Actor) WorkstationHealth() runtime.Health {
	if health, ok := actor.healthSnap.Load().(runtime.Health); ok {
		return health
	}
	return runtime.Health{Up: true}
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
	if actor.loop.WorkstationOff {
		// switched off: really not running, so say so — but calmly, and
		// pause still outranks it as a fact about the loop itself
		state = StateWorkstationOff
	}
	if actor.paused {
		state = StatePaused
	}
	if actor.wsDown {
		// the alert outranks everything: a dead workstation needs the operator
		state = StateWorkstationDown
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
	if _, err := crand.Read(b[:]); err != nil {
		panic(err)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	dst := make([]byte, 32)
	hex.Encode(dst, b[:])
	return fmt.Sprintf("%s-%s-%s-%s-%s", dst[0:8], dst[8:12], dst[12:16], dst[16:20], dst[20:32])
}
