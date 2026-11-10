package figures

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/enes-alatas/spool/internal/loop"
	"github.com/enes-alatas/spool/internal/store"
)

// The hub reads each loop's figures back from the operator's Langfuse
// (ADR-0064, #1300): it pages through the observations since its last
// read, on a slow cadence and never on a loop's turn, folds each into
// the trace it belongs to, and answers a window from what it has folded.
// It keeps numbers and names only. An observation is attributed to a
// loop by its spool.loop.id resource stamp alone, which the relay sets
// for a docker loop and nothing the loop writes can override.

// Windows are the windows the figures answer, by name.
var Windows = map[string]time.Duration{
	"1h":  time.Hour,
	"24h": 24 * time.Hour,
	"7d":  7 * 24 * time.Hour,
	"30d": 30 * 24 * time.Hour,
}

// kept is how far back the hub keeps folded traces: the widest
// window.
const kept = 30 * 24 * time.Hour

// perTurnMax caps a window's per-turn series at its newest rows.
const perTurnMax = 200

// The states a loop's figures are in, as the route reports them.
const (
	StateOK          = "ok"          // fresh
	StateStale       = "stale"       // Langfuse didn't answer lately: the last figures
	StatePending     = "pending"     // nothing read yet
	StateOff         = "off"         // no read address or read key
	StateUnsupported = "unsupported" // a cloud loop, which exports nothing
)

// Observation is one Langfuse observation as the hub keeps it: what it
// is, when, its loop's stamp, and its numbers. No content.
type Observation struct {
	ID      string
	TraceID string
	// ParentID is the observation this one is a child of, "" for a root.
	ParentID string
	// LoopID is the spool.loop.id resource stamp, "" for an observation
	// no loop's stamp is on, which counts for no loop.
	LoopID string
	// Type is Langfuse's: AGENT, GENERATION, SPAN, TOOL, …
	Type string
	// Name is the span's or tool's name.
	Name  string
	Start time.Time
	End   time.Time
	// Failed is a level of ERROR, or a tool reporting no success.
	Failed bool
	// ErrorClass is the error_class attribute, "" for none.
	ErrorClass string
	// SessionID is the Claude session the span is of, which joins a turn
	// to the hub's own row of it for its cost: Langfuse holds none.
	SessionID string
	Tokens    Tokens
}

// Tokens are a window's or a turn's tokens.
type Tokens struct {
	Input      int64 `json:"input"`
	Output     int64 `json:"output"`
	CacheRead  int64 `json:"cache_read"`
	CacheWrite int64 `json:"cache_write"`
}

func (tokens *Tokens) add(more Tokens) {
	tokens.Input += more.Input
	tokens.Output += more.Output
	tokens.CacheRead += more.CacheRead
	tokens.CacheWrite += more.CacheWrite
}

// ObservationPage is one page of observations, in the order Langfuse
// gives them, newest first today, and the cursor for the next, "" on the
// last. Nothing that folds a page depends on its order.
type ObservationPage struct {
	Observations []Observation
	Next         string
}

// ObservationSource reads observations started at from or later, a page
// at a time; cursor is "" for the first page.
type ObservationSource interface {
	Observations(ctx context.Context, langfuse store.Langfuse, from time.Time, cursor string) (ObservationPage, error)
}

// Summary is one loop's numbers over a window.
type Summary struct {
	Turns          int       `json:"turns"`
	FailedTurns    int       `json:"failed_turns"`
	CostUSD        float64   `json:"cost_usd"`
	Tokens         Tokens    `json:"tokens"`
	TurnDurationMS Durations `json:"turn_duration_ms"`
	Failures       []Failure `json:"failures"`
	Tools          []Tool    `json:"tools"`
	PerTurn        []TurnRow `json:"per_turn"`
}

// Durations are a window's turn durations.
type Durations struct {
	P50 int64 `json:"p50"`
	P95 int64 `json:"p95"`
	Max int64 `json:"max"`
}

// Failure counts a window's failures of one class, one for each thing
// that failed: a tool call, a model request, or a turn nothing explains.
type Failure struct {
	Class string `json:"class"`
	Count int    `json:"count"`
}

// Tool is one tool's calls over a window.
type Tool struct {
	Name          string `json:"name"`
	Calls         int    `json:"calls"`
	Failures      int    `json:"failures"`
	DurationMSP50 int64  `json:"duration_ms_p50"`
}

// TurnRow is one trace in a window's series.
type TurnRow struct {
	StartedAt        int64   `json:"started_at"`
	DurationMS       int64   `json:"duration_ms"`
	CostUSD          float64 `json:"cost_usd"`
	InputTokens      int64   `json:"input_tokens"`
	OutputTokens     int64   `json:"output_tokens"`
	CacheReadTokens  int64   `json:"cache_read_tokens"`
	CacheWriteTokens int64   `json:"cache_write_tokens"`
	ToolCalls        int     `json:"tool_calls"`
	Failed           bool    `json:"failed"`
	// sessionID joins the row to the hub's turn for its cost.
	sessionID string
}

// Answer is what the hub can say of a loop's figures: the state,
// when it last read Langfuse, and the figures unless the state has none.
type Answer struct {
	State     string
	FetchedAt int64
	Figures   *Summary
}

// foldedTrace is one trace as far as the hub has read it.
type foldedTrace struct {
	start, end time.Time
	sessionID  string
	tokens     Tokens
	failed     bool
	// tools are the turn's tool calls by observation ID, and failedTools
	// the calls whose execution, a child span, reported no success, with
	// the class it failed as
	tools       map[string]foldedTool
	failedTools map[string]string
	// failures are the classes of the turn's failed model requests and
	// other spans with a class of their own, one each. A failed tool call
	// is counted from tools and failedTools instead, once per call however
	// many of its spans say it failed.
	failures []string
	// unexplained is whether a span failed, the turn's root for one, with
	// no class of its own. It counts as one "error" only when nothing else
	// in the turn says what failed.
	unexplained bool
	seen        map[string]bool // observation IDs, so a re-read page counts once
	// rooted is whether the turn's own span, its AGENT observation, has
	// been read. Langfuse takes a span when it ends, so the root of a turn
	// arrives last, after children that started later than it.
	rooted bool
}

type foldedTool struct {
	name       string
	durationMS int64
	// failedAs is the class the call's own span failed as, "" for none
	failedAs string
}

// Reader keeps every loop's folded traces and refreshes them.
type Reader struct {
	settings store.SettingsStore
	loops    store.LoopStore
	turns    store.TurnStore
	source   ObservationSource
	every    time.Duration
	now      func() time.Time
	log      *slog.Logger

	mu        sync.Mutex
	traces    map[string]map[string]*foldedTrace // loop ID → trace ID → trace
	fetchedAt time.Time                          // the last read that answered, zero before any
	from      time.Time                          // where the next read starts
	readFor   string                             // the read address and key the folded traces came from
}

// NewReader returns figures read from source every period.
func NewReader(settings store.SettingsStore, loops store.LoopStore, turns store.TurnStore, source ObservationSource, every time.Duration, log *slog.Logger) *Reader {
	return &Reader{settings: settings, loops: loops, turns: turns, source: source, every: every, now: time.Now, log: log,
		traces: map[string]map[string]*foldedTrace{}}
}

// RefreshEvery is the cadence the figures are read at.
func (reader *Reader) RefreshEvery() time.Duration { return reader.every }

// Run refreshes the figures until ctx ends: once at the start, then
// every period. A Langfuse that doesn't answer stops nothing but the
// read.
func (reader *Reader) Run(ctx context.Context) {
	tick := time.NewTicker(reader.every)
	defer tick.Stop()
	for {
		if err := reader.Refresh(ctx); err != nil && ctx.Err() == nil {
			reader.log.Warn("langfuse figures: read failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// overlap is how far before an observation already read the next read
// starts again, for one that Langfuse ingests late; the IDs keep a
// re-read one counted once.
const overlap = 10 * time.Minute

// maxPages bounds one refresh at 500 pages of 1000 observations, about
// 26,000 turns, so a Langfuse whose cursor never ends can't hold the
// read, or the hub's memory, open.
const maxPages = 500

// openKept is how long a trace whose root hasn't been read holds the next
// read back at its start. A span reaches Langfuse when it ends, so a
// turn's root arrives once the turn is over, however long it ran, and
// only a read from before its start finds it. A turn longer than this, or
// one whose root never arrives, is counted from its children alone.
const openKept = 24 * time.Hour

// Refresh reads every observation since the last read and folds it in.
// A changed read address or key starts over, since the traces folded so
// far came from another Langfuse.
func (reader *Reader) Refresh(ctx context.Context) error {
	langfuse, err := loop.LoadLangfuse(ctx, reader.settings)
	if err != nil {
		return err
	}
	if !readable(langfuse) {
		reader.reset("")
		return nil
	}
	reader.reset(langfuse.ReadAddress + "\x00" + langfuse.ReadKey)
	reader.mu.Lock()
	from := reader.from
	reader.mu.Unlock()
	now := reader.now()
	if earliest := now.Add(-kept); from.Before(earliest) {
		from = earliest
	}
	known, err := reader.knownLoops(ctx)
	if err != nil {
		return err
	}
	newest := from
	var cursor string
	for pages := 1; ; pages++ {
		page, err := reader.source.Observations(ctx, langfuse, from, cursor)
		if err != nil {
			return err
		}
		reader.mu.Lock()
		for _, observation := range page.Observations {
			if known[observation.LoopID] {
				reader.fold(observation)
			}
			// a span dated past now, which a bare loop or a skewed clock can
			// post, moves no read past now
			if observation.Start.After(newest) && !observation.Start.After(now) {
				newest = observation.Start
			}
		}
		reader.mu.Unlock()
		if page.Next == "" {
			break
		}
		if pages >= maxPages {
			// what was read stays folded; the figures go stale, and the next
			// refresh reads from where this one began
			return fmt.Errorf("langfuse observations: over %d pages in one read", maxPages)
		}
		cursor = page.Next
	}
	reader.mu.Lock()
	defer reader.mu.Unlock()
	reader.from = reader.nextFrom(newest, now)
	reader.fetchedAt = now
	reader.prune(now.Add(-kept))
	return nil
}

// nextFrom is where the next read starts: before the newest observation
// read, and before every trace still waiting on its root, unless that
// trace started over openKept ago. Held under mu.
func (reader *Reader) nextFrom(newest, now time.Time) time.Time {
	next := newest.Add(-overlap)
	giveUp := now.Add(-openKept)
	for _, traces := range reader.traces {
		for _, trace := range traces {
			if !trace.rooted && trace.start.After(giveUp) && trace.start.Add(-overlap).Before(next) {
				next = trace.start.Add(-overlap)
			}
		}
	}
	return next
}

// knownLoops is the IDs of the loops the hub has: an observation stamped
// with any other counts for no loop and isn't kept, so spans posted
// under made-up IDs grow nothing.
func (reader *Reader) knownLoops(ctx context.Context) (map[string]bool, error) {
	loops, err := reader.loops.List(ctx)
	if err != nil {
		return nil, err
	}
	known := make(map[string]bool, len(loops))
	for _, loopRecord := range loops {
		known[loopRecord.ID] = true
	}
	return known, nil
}

// readable reports whether the hub can read Langfuse back: a read
// address it may send the read key to, and the key.
func readable(langfuse store.Langfuse) bool {
	return langfuse.ReadKey != "" && loop.ValidLangfuseReadAddress(langfuse.ReadAddress) == nil
}

// reset forgets everything folded when the Langfuse read from changes.
func (reader *Reader) reset(readFor string) {
	reader.mu.Lock()
	defer reader.mu.Unlock()
	if reader.readFor == readFor {
		return
	}
	reader.readFor = readFor
	reader.traces = map[string]map[string]*foldedTrace{}
	reader.fetchedAt, reader.from = time.Time{}, time.Time{}
}

// fold adds one observation to its trace. Held under mu.
func (reader *Reader) fold(observation Observation) {
	if observation.LoopID == "" || observation.TraceID == "" {
		return
	}
	traces := reader.traces[observation.LoopID]
	if traces == nil {
		traces = map[string]*foldedTrace{}
		reader.traces[observation.LoopID] = traces
	}
	trace := traces[observation.TraceID]
	if trace == nil {
		trace = &foldedTrace{start: observation.Start, end: observation.End, seen: map[string]bool{},
			tools: map[string]foldedTool{}, failedTools: map[string]string{}}
		traces[observation.TraceID] = trace
	}
	if observation.ID != "" {
		if trace.seen[observation.ID] {
			return
		}
		trace.seen[observation.ID] = true
	}
	if !observation.Start.IsZero() && (trace.start.IsZero() || observation.Start.Before(trace.start)) {
		trace.start = observation.Start
	}
	if observation.End.After(trace.end) {
		trace.end = observation.End
	}
	trace.tokens.add(observation.Tokens)
	failedAs := ""
	if observation.Failed {
		trace.failed = true
		failedAs = failureClass(observation)
	}
	if observation.Type == "AGENT" {
		trace.rooted = true
		if observation.SessionID != "" {
			trace.sessionID = observation.SessionID
		}
	}
	switch {
	case observation.Type != "TOOL":
		switch failedAs {
		case "":
		case unexplainedFailure:
			trace.unexplained = true
		default:
			trace.failures = append(trace.failures, failedAs)
		}
	case observation.Name == toolExecution:
		// a call's execution is a child of the call, and says whether it
		// succeeded; it is not a call of its own
		if failedAs != "" {
			parent := observation.ParentID
			if parent == "" {
				parent = "#exec:" + observation.ID
			}
			trace.failedTools[parent] = cmp.Or(trace.failedTools[parent], failedAs)
		}
	default:
		id := observation.ID
		if id == "" {
			id = fmt.Sprintf("#%d", len(trace.tools))
		}
		trace.tools[id] = foldedTool{name: observation.Name,
			durationMS: durationMS(observation.Start, observation.End), failedAs: failedAs}
	}
}

// unexplainedFailure is the class of a failed span that says nothing of
// what failed.
const unexplainedFailure = "error"

// Claude Code's span names (#1300's probe): each tool call is a TOOL span
// named after the tool, with a toolExecution child that carries its
// success, and each model request is a modelRequest generation.
const (
	toolExecution = "claude_code.tool.execution"
	modelRequest  = "claude_code.llm_request"
)

// failureClass names what failed: the span's error_class when it has one,
// which Claude Code doesn't set today, or else a tool or a model request.
func failureClass(observation Observation) string {
	switch {
	case observation.ErrorClass != "":
		return observation.ErrorClass
	case observation.Name == toolExecution, observation.Type == "TOOL":
		return "tool"
	case observation.Name == modelRequest:
		return "model_request"
	}
	return unexplainedFailure
}

// prune drops the traces started before cutoff. Held under mu.
func (reader *Reader) prune(cutoff time.Time) {
	for loopID, traces := range reader.traces {
		for traceID, trace := range traces {
			if trace.start.Before(cutoff) {
				delete(traces, traceID)
			}
		}
		if len(traces) == 0 {
			delete(reader.traces, loopID)
		}
	}
}

// Of answers a loop's figures over window, which Windows names.
func (reader *Reader) Of(ctx context.Context, loopRecord *store.Loop, window time.Duration) (Answer, error) {
	if loopRecord.Runtime == store.RuntimeCloud {
		return Answer{State: StateUnsupported}, nil
	}
	langfuse, err := loop.LoadLangfuse(ctx, reader.settings)
	if err != nil {
		return Answer{}, err
	}
	if !readable(langfuse) {
		return Answer{State: StateOff}, nil
	}
	answer, since := reader.folded(loopRecord.ID, langfuse, window)
	if answer.Figures == nil {
		return answer, nil
	}
	if err := reader.price(ctx, loopRecord.ID, since, answer.Figures); err != nil {
		return Answer{}, err
	}
	return answer, nil
}

// folded answers the loop's figures from the traces folded so far, with
// no cost yet, and the window's start.
func (reader *Reader) folded(loopID string, langfuse store.Langfuse, window time.Duration) (Answer, time.Time) {
	reader.mu.Lock()
	defer reader.mu.Unlock()
	if reader.fetchedAt.IsZero() || reader.readFor != langfuse.ReadAddress+"\x00"+langfuse.ReadKey {
		return Answer{State: StatePending}, time.Time{}
	}
	now := reader.now()
	answer := Answer{State: StateOK, FetchedAt: reader.fetchedAt.UnixMilli()}
	if now.Sub(reader.fetchedAt) > 2*reader.every {
		answer.State = StateStale
	}
	since := now.Add(-window)
	answer.Figures = summaryOver(reader.traces[loopID], since)
	return answer, since
}

// price puts the hub's own cost on figures. Langfuse holds no cost for
// Claude Code's spans (#1300's probe), and the hub prices each turn
// already (ADR-0059): the window's spend is the loop's turns started in
// it, and a row's is the turn of the same session whose span holds the
// row's start. A row no turn holds costs 0.
func (reader *Reader) price(ctx context.Context, loopID string, since time.Time, figures *Summary) error {
	spend, err := reader.turns.CostSince(ctx, loopID, since.UnixMilli())
	if err != nil {
		return err
	}
	figures.CostUSD = spend.USD
	if len(figures.PerTurn) == 0 {
		return nil
	}
	turns, err := reader.turns.ListByLoop(ctx, loopID, 2*perTurnMax)
	if err != nil {
		return err
	}
	now := reader.now().UnixMilli()
	for index := range figures.PerTurn {
		row := &figures.PerTurn[index]
		for _, turn := range turns {
			ended := turn.EndedAt
			if ended == 0 {
				ended = now
			}
			if row.sessionID == "" || turn.SessionID != row.sessionID || turn.CostUSD == nil ||
				row.StartedAt < turn.StartedAt-turnSlackMS || row.StartedAt > ended+turnSlackMS {
				continue
			}
			row.CostUSD = *turn.CostUSD
			break
		}
	}
	return nil
}

// turnSlackMS is how far a trace's start may sit outside its turn's
// span and still be that turn's: the hub stamps a turn as it hands the
// message over, and the CLI starts its span a moment later.
const turnSlackMS = 5_000

// summaryOver sums the traces started at since or later.
func summaryOver(traces map[string]*foldedTrace, since time.Time) *Summary {
	out := &Summary{Failures: []Failure{}, Tools: []Tool{}, PerTurn: []TurnRow{}}
	var inWindow []*foldedTrace
	for _, trace := range traces {
		if !trace.start.Before(since) {
			inWindow = append(inWindow, trace)
		}
	}
	slices.SortFunc(inWindow, func(earlier, later *foldedTrace) int { return earlier.start.Compare(later.start) })
	var durations []int64
	failures := map[string]int{}
	tools := map[string]*Tool{}
	toolDurations := map[string][]int64{}
	for _, trace := range inWindow {
		duration := durationMS(trace.start, trace.end)
		durations = append(durations, duration)
		out.Turns++
		if trace.failed {
			out.FailedTurns++
		}
		out.Tokens.add(trace.tokens)
		explained := len(trace.failures)
		for _, class := range trace.failures {
			failures[class]++
		}
		for id, call := range trace.tools {
			tool := tools[call.name]
			if tool == nil {
				tool = &Tool{Name: call.name}
				tools[call.name] = tool
			}
			tool.Calls++
			// one failure per call, whether its own span, its execution's or
			// both say so
			if failedAs := cmp.Or(call.failedAs, trace.failedTools[id]); failedAs != "" {
				tool.Failures++
				failures[failedAs]++
				explained++
			}
			toolDurations[call.name] = append(toolDurations[call.name], call.durationMS)
		}
		// an execution whose call wasn't read still failed a call
		for id, failedAs := range trace.failedTools {
			if _, read := trace.tools[id]; !read {
				failures[failedAs]++
				explained++
			}
		}
		if trace.unexplained && explained == 0 {
			failures[unexplainedFailure]++
		}
		out.PerTurn = append(out.PerTurn, TurnRow{
			StartedAt: trace.start.UnixMilli(), DurationMS: duration, sessionID: trace.sessionID,
			InputTokens: trace.tokens.Input, OutputTokens: trace.tokens.Output,
			CacheReadTokens: trace.tokens.CacheRead, CacheWriteTokens: trace.tokens.CacheWrite,
			ToolCalls: len(trace.tools), Failed: trace.failed,
		})
	}
	if len(out.PerTurn) > perTurnMax {
		out.PerTurn = out.PerTurn[len(out.PerTurn)-perTurnMax:]
	}
	out.TurnDurationMS = Durations{P50: percentile(durations, 50), P95: percentile(durations, 95), Max: percentile(durations, 100)}
	for class, count := range failures {
		out.Failures = append(out.Failures, Failure{Class: class, Count: count})
	}
	slices.SortFunc(out.Failures, func(first, second Failure) int {
		return cmp.Or(cmp.Compare(second.Count, first.Count), cmp.Compare(first.Class, second.Class))
	})
	for name, tool := range tools {
		tool.DurationMSP50 = percentile(toolDurations[name], 50)
		out.Tools = append(out.Tools, *tool)
	}
	slices.SortFunc(out.Tools, func(first, second Tool) int {
		return cmp.Or(cmp.Compare(second.Calls, first.Calls), cmp.Compare(first.Name, second.Name))
	})
	return out
}

// percentile is the nearest-rank percentile of values, 0 for none.
func percentile(values []int64, rank int) int64 {
	if len(values) == 0 {
		return 0
	}
	sorted := slices.Clone(values)
	slices.Sort(sorted)
	index := (rank*len(sorted)+99)/100 - 1
	return sorted[max(index, 0)]
}

// durationMS is end − start in milliseconds, 0 for an unended span.
func durationMS(start, end time.Time) int64 {
	if start.IsZero() || end.Before(start) {
		return 0
	}
	return end.Sub(start).Milliseconds()
}
