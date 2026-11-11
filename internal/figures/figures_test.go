package figures

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/enes-alatas/spool/internal/loop"
	"github.com/enes-alatas/spool/internal/store"
	"github.com/enes-alatas/spool/internal/store/sqlite"
)

// pagedSource serves fixed pages of the observations started at the
// read's from or later, or fails when down.
type pagedSource struct {
	pages [][]Observation
	down  bool
	froms []time.Time
}

func (source *pagedSource) Observations(_ context.Context, _ store.Langfuse, from time.Time, cursor string) (ObservationPage, error) {
	if source.down {
		return ObservationPage{}, errors.New("langfuse is down")
	}
	source.froms = append(source.froms, from)
	index := 0
	if cursor != "" {
		index = int(cursor[0] - '0')
	}
	if index >= len(source.pages) {
		return ObservationPage{}, nil
	}
	// as Langfuse does, a page holds only what started at from or later
	page := ObservationPage{Observations: []Observation{}}
	for _, observation := range source.pages[index] {
		if !observation.Start.Before(from) {
			page.Observations = append(page.Observations, observation)
		}
	}
	if index+1 < len(source.pages) {
		page.Next = string(rune('0' + index + 1))
	}
	return page, nil
}

// loopsStub is a loop store that lists the loops named by ID, and
// nothing else of a store.
type loopsStub struct {
	store.LoopStore
	ids []string
}

func knownLoopsOf(ids ...string) *loopsStub { return &loopsStub{ids: ids} }

func (stub *loopsStub) List(context.Context) ([]*store.Loop, error) {
	var loops []*store.Loop
	for _, id := range stub.ids {
		loops = append(loops, &store.Loop{ID: id})
	}
	return loops, nil
}

// turnsStub is the hub's own turns, which price the figures: only the
// two reads the Reader makes.
type turnsStub struct {
	store.TurnStore
	turns []*store.Turn
}

func (stub *turnsStub) CostSince(_ context.Context, loopID string, since int64) (store.Spend, error) {
	var spend store.Spend
	for _, turn := range stub.turns {
		if turn.LoopID == loopID && turn.StartedAt >= since && turn.CostUSD != nil {
			spend.USD += *turn.CostUSD
			if turn.EndedAt > 0 {
				spend.Turns++
			}
		}
	}
	return spend, nil
}

func (stub *turnsStub) ListByLoop(_ context.Context, loopID string, limit int) ([]*store.Turn, error) {
	var out []*store.Turn
	for _, turn := range slices.Backward(stub.turns) {
		if turn.LoopID == loopID && len(out) < limit {
			out = append(out, turn)
		}
	}
	return out, nil
}

func cost(usd float64) *float64 { return &usd }

// syntheticKey is an obviously fake Langfuse key pair.
const syntheticKey = "pk-lf-test:sk-lf-test"

func newTestReader(t *testing.T, source ObservationSource, langfuse store.Langfuse) (*Reader, *time.Time) {
	t.Helper()
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := loop.StoreLangfuse(context.Background(), db.Settings(), langfuse); err != nil {
		t.Fatal(err)
	}
	reader := NewReader(db.Settings(), knownLoopsOf("L1", "L2"), &turnsStub{}, source, 5*time.Minute, slog.New(slog.NewTextHandler(io.Discard, nil)))
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	reader.now = func() time.Time { return now }
	return reader, &now
}

var readableLangfuse = store.Langfuse{ReadAddress: "http://127.0.0.1:3000", ReadKey: syntheticKey}

func at(now time.Time, ago time.Duration) time.Time { return now.Add(-ago) }

// A loop's traces sum to its turns, failures, tokens, durations, tool mix
// and per-turn rows, in the shape #1300's probe found: tokens on the
// model requests, each tool call a span named after the tool with an
// execution child that says whether it succeeded. Spend is the hub's own,
// its turns', joined to a row by session. Another loop's observations and
// turns, and unstamped observations, count for no one, and a page read
// twice counts once.
func TestFiguresSumALoopsTraces(t *testing.T) {
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	turnOne := []Observation{
		{ID: "a1", TraceID: "t1", LoopID: "L1", Type: "AGENT", Name: "claude_code.interaction", SessionID: "s1",
			Start: at(now, 2*time.Hour), End: at(now, 2*time.Hour-40*time.Second)},
		{ID: "a2", TraceID: "t1", ParentID: "a1", LoopID: "L1", Type: "GENERATION", Name: modelRequest, Start: at(now, 2*time.Hour), End: at(now, 2*time.Hour-10*time.Second),
			Tokens: Tokens{Input: 100, Output: 40, CacheRead: 5000, CacheWrite: 200}},
		{ID: "a3", TraceID: "t1", ParentID: "a1", LoopID: "L1", Type: "TOOL", Name: "Bash", Start: at(now, 2*time.Hour-10*time.Second), End: at(now, 2*time.Hour-11*time.Second)},
		{ID: "a3x", TraceID: "t1", ParentID: "a3", LoopID: "L1", Type: "TOOL", Name: toolExecution, Start: at(now, 2*time.Hour-10*time.Second), End: at(now, 2*time.Hour-11*time.Second)},
		{ID: "a4", TraceID: "t1", ParentID: "a1", LoopID: "L1", Type: "TOOL", Name: "Bash", Start: at(now, 2*time.Hour-12*time.Second), End: at(now, 2*time.Hour-15*time.Second)},
		{ID: "a4x", TraceID: "t1", ParentID: "a4", LoopID: "L1", Type: "TOOL", Name: toolExecution, Start: at(now, 2*time.Hour-12*time.Second), End: at(now, 2*time.Hour-15*time.Second), Failed: true},
	}
	turnTwo := []Observation{
		{ID: "b1", TraceID: "t2", LoopID: "L1", Type: "AGENT", SessionID: "s2", Start: at(now, 30*time.Minute), End: at(now, 30*time.Minute-20*time.Second)},
		{ID: "b2", TraceID: "t2", ParentID: "b1", LoopID: "L1", Type: "GENERATION", Name: modelRequest, Start: at(now, 30*time.Minute), End: at(now, 30*time.Minute-5*time.Second),
			Tokens: Tokens{Input: 10, Output: 4}, Failed: true},
		{ID: "b3", TraceID: "t2", ParentID: "b1", LoopID: "L1", Type: "TOOL", Name: "send_message", Start: at(now, 30*time.Minute-6*time.Second), End: at(now, 30*time.Minute-6*time.Second-100*time.Millisecond)},
	}
	others := []Observation{
		{ID: "c1", TraceID: "t3", LoopID: "L2", Type: "GENERATION", Start: at(now, time.Hour), Tokens: Tokens{Input: 9}},
		{ID: "c2", TraceID: "t4", Type: "GENERATION", Start: at(now, time.Hour), Tokens: Tokens{Input: 9}},
	}
	// the hub stamps a turn a moment before the CLI opens its span
	turns := &turnsStub{turns: []*store.Turn{
		{LoopID: "L1", SessionID: "s1", StartedAt: at(now, 2*time.Hour+time.Second).UnixMilli(), EndedAt: at(now, 2*time.Hour-41*time.Second).UnixMilli(), CostUSD: cost(0.05)},
		{LoopID: "L2", SessionID: "s9", StartedAt: at(now, time.Hour).UnixMilli(), EndedAt: at(now, time.Hour-time.Second).UnixMilli(), CostUSD: cost(9)},
		{LoopID: "L1", SessionID: "s2", StartedAt: at(now, 30*time.Minute+time.Second).UnixMilli(), EndedAt: at(now, 30*time.Minute-21*time.Second).UnixMilli(), CostUSD: cost(0.02)},
		// a turn Langfuse has no trace of yet, and one still running
		{LoopID: "L1", SessionID: "s3", StartedAt: at(now, 10*time.Minute).UnixMilli(), EndedAt: at(now, 9*time.Minute).UnixMilli(), CostUSD: cost(0)},
		{LoopID: "L1", SessionID: "s4", StartedAt: at(now, time.Minute).UnixMilli()},
	}}
	source := &pagedSource{pages: [][]Observation{turnOne, append(turnTwo, others...), turnOne}}
	reader, _ := newTestReader(t, source, readableLangfuse)
	reader.turns = turns
	if err := reader.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	answer, err := reader.Of(context.Background(), &store.Loop{ID: "L1"}, Windows["24h"])
	if err != nil {
		t.Fatal(err)
	}
	if answer.State != StateOK || answer.FetchedAt != now.UnixMilli() {
		t.Fatalf("answer = %+v, want ok fetched now", answer)
	}
	got := answer.Figures
	if got.Turns != 2 || got.FailedTurns != 2 {
		t.Errorf("turns = %d, failed %d; want 2 and 2", got.Turns, got.FailedTurns)
	}
	if got.CostUSD < 0.0699 || got.CostUSD > 0.0701 {
		t.Errorf("cost = %v, want 0.07 (the hub's L1 turns; L2's excluded)", got.CostUSD)
	}
	if got.CostTurns != 3 {
		t.Errorf("cost turns = %d, want 3: the hub's ended L1 turns, the untraced one too, the running one not", got.CostTurns)
	}
	if want := (Tokens{Input: 110, Output: 44, CacheRead: 5000, CacheWrite: 200}); got.Tokens != want {
		t.Errorf("tokens = %+v, want %+v", got.Tokens, want)
	}
	if want := (Durations{P50: 20000, P95: 40000, Max: 40000}); got.TurnDurationMS != want {
		t.Errorf("durations = %+v, want %+v", got.TurnDurationMS, want)
	}
	wantTools := []Tool{{Name: "Bash", Calls: 2, Failures: 1, DurationMSP50: 1000}, {Name: "send_message", Calls: 1, DurationMSP50: 100}}
	if len(got.Tools) != 2 || got.Tools[0] != wantTools[0] || got.Tools[1] != wantTools[1] {
		t.Errorf("tools = %+v, want %+v (an execution span is no call of its own)", got.Tools, wantTools)
	}
	wantFailures := []Failure{{Class: "model_request", Count: 1}, {Class: "tool", Count: 1}}
	if len(got.Failures) != 2 || got.Failures[0] != wantFailures[0] || got.Failures[1] != wantFailures[1] {
		t.Errorf("failures = %+v, want %+v", got.Failures, wantFailures)
	}
	if len(got.PerTurn) != 2 || got.PerTurn[0].StartedAt != at(now, 2*time.Hour).UnixMilli() || got.PerTurn[0].ToolCalls != 2 || !got.PerTurn[1].Failed {
		t.Errorf("per-turn = %+v, want t1 then t2, oldest first", got.PerTurn)
	}
	if len(got.PerTurn) == 2 && (got.PerTurn[0].CostUSD != 0.05 || got.PerTurn[1].CostUSD != 0.02) {
		t.Errorf("per-turn cost = %v and %v, want each turn's own: 0.05 and 0.02", got.PerTurn[0].CostUSD, got.PerTurn[1].CostUSD)
	}

	hour, err := reader.Of(context.Background(), &store.Loop{ID: "L1"}, Windows["1h"])
	if err != nil {
		t.Fatal(err)
	}
	if hour.Figures.Turns != 1 || hour.Figures.PerTurn[0].StartedAt != at(now, 30*time.Minute).UnixMilli() || hour.Figures.CostUSD != 0.02 || hour.Figures.CostTurns != 2 {
		t.Errorf("1h window = %+v, want t2 alone, at 0.02 over the hub's 2 ended turns", hour.Figures)
	}
}

// The states: off with no read key or a read address the key may not go
// to, unsupported for a cloud loop, pending before the first read, stale
// once Langfuse stops answering, and a changed key starts over.
func TestFiguresStates(t *testing.T) {
	ctx := context.Background()
	for name, langfuse := range map[string]store.Langfuse{
		"no read key":       {ReadAddress: "http://127.0.0.1:3000"},
		"no read address":   {ReadKey: syntheticKey},
		"http off loopback": {ReadAddress: "http://langfuse.example", ReadKey: syntheticKey},
	} {
		reader, _ := newTestReader(t, &pagedSource{}, langfuse)
		if err := reader.Refresh(ctx); err != nil {
			t.Fatalf("%s: refresh: %v", name, err)
		}
		if answer, _ := reader.Of(ctx, &store.Loop{ID: "L1"}, time.Hour); answer.State != StateOff || answer.Figures != nil {
			t.Errorf("%s: answer = %+v, want off with no figures", name, answer)
		}
	}

	source := &pagedSource{pages: [][]Observation{{{ID: "a", TraceID: "t", LoopID: "L1", Start: time.Date(2026, 11, 10, 11, 0, 0, 0, time.UTC)}}}}
	reader, now := newTestReader(t, source, readableLangfuse)
	if answer, _ := reader.Of(ctx, &store.Loop{ID: "C1", Runtime: store.RuntimeCloud}, time.Hour); answer.State != StateUnsupported {
		t.Errorf("cloud loop = %+v, want unsupported", answer)
	}
	if answer, _ := reader.Of(ctx, &store.Loop{ID: "L1"}, time.Hour); answer.State != StatePending || answer.Figures != nil {
		t.Errorf("before a read = %+v, want pending", answer)
	}
	if err := reader.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	source.down = true
	if err := reader.Refresh(ctx); err == nil {
		t.Error("a down Langfuse's refresh reported no error")
	}
	fetched := now.UnixMilli()
	*now = now.Add(11 * time.Minute)
	answer, _ := reader.Of(ctx, &store.Loop{ID: "L1"}, 24*time.Hour)
	if answer.State != StateStale || answer.FetchedAt != fetched || answer.Figures == nil || answer.Figures.Turns != 1 {
		t.Errorf("Langfuse down = %+v, want the last figures, stale, as of the last read", answer)
	}

	other := readableLangfuse
	other.ReadKey = "pk-lf-other:sk-lf-other"
	if err := loop.StoreLangfuse(ctx, reader.settings, other); err != nil {
		t.Fatal(err)
	}
	if answer, _ := reader.Of(ctx, &store.Loop{ID: "L1"}, 24*time.Hour); answer.State != StatePending {
		t.Errorf("after a key change = %+v, want pending: the figures came from another key", answer)
	}
}

// Each read starts a little before the newest observation it has seen,
// and never further back than the widest window.
func TestFiguresReadOnFromWhereTheyLeftOff(t *testing.T) {
	ctx := context.Background()
	newest := time.Date(2026, 11, 10, 11, 50, 0, 0, time.UTC)
	source := &pagedSource{pages: [][]Observation{{{ID: "a", TraceID: "t", LoopID: "L1", Start: newest}}}}
	reader, now := newTestReader(t, source, readableLangfuse)
	for range 2 {
		if err := reader.Refresh(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if first := now.Add(-kept); !source.froms[0].Equal(first) {
		t.Errorf("first read from %v, want %v", source.froms[0], first)
	}
	if second := newest.Add(-overlap); !source.froms[1].Equal(second) {
		t.Errorf("second read from %v, want %v", source.froms[1], second)
	}
}

// A served answer holds numbers and names only: no key, nothing but the
// documented fields.
func TestFiguresHoldNoKey(t *testing.T) {
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	source := &pagedSource{pages: [][]Observation{{{ID: "a", TraceID: "t", LoopID: "L1", Type: "TOOL", Name: "Bash", Start: now.Add(-time.Minute)}}}}
	reader, _ := newTestReader(t, source, readableLangfuse)
	if err := reader.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	answer, _ := reader.Of(context.Background(), &store.Loop{ID: "L1"}, time.Hour)
	raw, err := json.Marshal(answer.Figures)
	if err != nil {
		t.Fatal(err)
	}
	for _, half := range strings.Split(syntheticKey, ":") {
		if strings.Contains(string(raw), half) {
			t.Fatalf("figures carry the read key: %s", raw)
		}
	}
}

// One observation dated past now, which a bare loop can post, moves no
// read past now: the next refresh still folds what arrived since.
func TestAFutureDatedObservationStopsNoRead(t *testing.T) {
	ctx := context.Background()
	source := &pagedSource{}
	reader, now := newTestReader(t, source, readableLangfuse)
	source.pages = [][]Observation{{
		{ID: "f", TraceID: "future", LoopID: "L2", Type: "AGENT", Start: time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC)},
		{ID: "a", TraceID: "t1", LoopID: "L1", Type: "AGENT", Start: now.Add(-20 * time.Minute)},
	}}
	if err := reader.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(5 * time.Minute)
	source.pages[0] = append(source.pages[0], Observation{ID: "b", TraceID: "t2", LoopID: "L1", Type: "AGENT", Start: now.Add(-time.Minute)})
	if err := reader.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if second := source.froms[1]; second.After(*now) {
		t.Fatalf("the second read started at %v, past now %v", second, *now)
	}
	answer, _ := reader.Of(ctx, &store.Loop{ID: "L1"}, time.Hour)
	if answer.Figures == nil || answer.Figures.Turns != 2 {
		t.Fatalf("L1's figures = %+v, want both turns, the one after the future-dated span too", answer.Figures)
	}
}

// A turn's root span reaches Langfuse when the turn ends, after children
// that started later than it and were read already. Until it arrives,
// the read stays back at the turn's start, so the root is still read
// however long the turn ran, and its end sets the turn's duration.
func TestALongTurnsRootIsStillRead(t *testing.T) {
	ctx := context.Background()
	source := &pagedSource{}
	reader, now := newTestReader(t, source, readableLangfuse)
	turnStart := now.Add(-45 * time.Minute)
	source.pages = [][]Observation{{
		{ID: "g1", TraceID: "long", LoopID: "L1", Type: "GENERATION", Start: turnStart.Add(time.Second), End: turnStart.Add(time.Minute)},
		{ID: "b1", TraceID: "long", LoopID: "L1", Type: "TOOL", Name: "Bash", Start: now.Add(-2 * time.Minute), End: now.Add(-time.Minute)},
		{ID: "x1", TraceID: "short", LoopID: "L1", Type: "AGENT", Start: now.Add(-time.Minute), End: now.Add(-30 * time.Second)},
	}}
	if err := reader.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(5 * time.Minute)
	source.pages[0] = append(source.pages[0], Observation{ID: "root", TraceID: "long", LoopID: "L1", Type: "AGENT", Start: turnStart, End: now.Add(-time.Minute)})
	if err := reader.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	answer, _ := reader.Of(ctx, &store.Loop{ID: "L1"}, time.Hour)
	if answer.Figures == nil || len(answer.Figures.PerTurn) != 2 {
		t.Fatalf("figures = %+v, want two turns", answer.Figures)
	}
	long := answer.Figures.PerTurn[0]
	if long.StartedAt != turnStart.UnixMilli() || long.DurationMS != now.Add(-time.Minute).Sub(turnStart).Milliseconds() {
		t.Errorf("the long turn = %+v, want it to start and end with its root, read 5m after its children", long)
	}

	// once the root is read, the read moves on to the newest observation
	if err := reader.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if third, want := source.froms[2], turnStart.Add(-overlap); !third.After(want) {
		t.Errorf("the third read started at %v, still held back for a root already read", third)
	}
}

// endlessSource answers every page with one more cursor, as a Langfuse
// whose cursor never ends would.
type endlessSource struct{ pages int }

func (source *endlessSource) Observations(_ context.Context, _ store.Langfuse, from time.Time, _ string) (ObservationPage, error) {
	source.pages++
	return ObservationPage{
		Observations: []Observation{{ID: fmt.Sprint(source.pages), TraceID: fmt.Sprint(source.pages), LoopID: "L1", Type: "AGENT", Start: from.Add(time.Second)}},
		Next:         "more",
	}, nil
}

// A refresh reads at most maxPages pages: one whose cursor never ends
// fails and leaves the figures pending rather than reading on.
func TestARefreshReadsBoundedPages(t *testing.T) {
	source := &endlessSource{}
	reader, _ := newTestReader(t, source, readableLangfuse)
	if err := reader.Refresh(context.Background()); err == nil {
		t.Fatal("a cursor that never ends read with no error")
	}
	if source.pages != maxPages {
		t.Errorf("read %d pages, want %d", source.pages, maxPages)
	}
	if answer, _ := reader.Of(context.Background(), &store.Loop{ID: "L1"}, time.Hour); answer.State != StatePending {
		t.Errorf("after a bounded read = %+v, want pending", answer)
	}
}

// An observation stamped with an ID no loop on the hub has is not kept,
// so spans posted under made-up IDs grow nothing.
func TestAnUnknownLoopsObservationsAreNotKept(t *testing.T) {
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	source := &pagedSource{pages: [][]Observation{{
		{ID: "a", TraceID: "t1", LoopID: "L1", Type: "AGENT", Start: now.Add(-time.Minute)},
		{ID: "b", TraceID: "t2", LoopID: "made-up", Type: "AGENT", Start: now.Add(-time.Minute)},
	}}}
	reader, _ := newTestReader(t, source, readableLangfuse)
	if err := reader.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, kept := reader.traces["made-up"]; kept || len(reader.traces) != 1 {
		t.Errorf("traces kept for %d loops, want L1's alone", len(reader.traces))
	}
}

// A failure counts once for what failed, however many of its spans say
// so: a tool call failed on its own span and its execution's is one
// "tool", the root failing with it adds nothing, and only a turn nothing
// explains counts an "error". An execution whose call wasn't read still
// counts its call's failure.
func TestAFailureCountsOnce(t *testing.T) {
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	start := now.Add(-time.Minute)
	source := &pagedSource{pages: [][]Observation{{
		{ID: "a1", TraceID: "t1", LoopID: "L1", Type: "AGENT", Start: start, Failed: true},
		{ID: "a2", TraceID: "t1", ParentID: "a1", LoopID: "L1", Type: "TOOL", Name: "Bash", Start: start, Failed: true},
		{ID: "a2x", TraceID: "t1", ParentID: "a2", LoopID: "L1", Type: "TOOL", Name: toolExecution, Start: start, Failed: true},
		{ID: "b1", TraceID: "t2", LoopID: "L1", Type: "AGENT", Start: start, Failed: true},
		{ID: "c1", TraceID: "t3", LoopID: "L1", Type: "AGENT", Start: start},
		{ID: "c2x", TraceID: "t3", ParentID: "c-unread", LoopID: "L1", Type: "TOOL", Name: toolExecution, Start: start, Failed: true},
	}}}
	reader, _ := newTestReader(t, source, readableLangfuse)
	if err := reader.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	answer, err := reader.Of(context.Background(), &store.Loop{ID: "L1"}, Windows["1h"])
	if err != nil {
		t.Fatal(err)
	}
	got := answer.Figures
	want := []Failure{{Class: "tool", Count: 2}, {Class: "error", Count: 1}}
	if len(got.Failures) != 2 || got.Failures[0] != want[0] || got.Failures[1] != want[1] {
		t.Errorf("failures = %+v, want %+v", got.Failures, want)
	}
	if len(got.Tools) != 1 || got.Tools[0].Calls != 1 || got.Tools[0].Failures != 1 {
		t.Errorf("tools = %+v, want one Bash call failed once", got.Tools)
	}
	if got.FailedTurns != 3 {
		t.Errorf("failed turns = %d, want 3", got.FailedTurns)
	}
}
