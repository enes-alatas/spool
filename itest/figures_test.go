//go:build integration

package itest

import (
	"encoding/base64"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// figuresJSON is GET /api/loops/{name}/figures as the test reads it.
type figuresJSON struct {
	Loop       string `json:"loop"`
	Window     string `json:"window"`
	Langfuse   string `json:"langfuse"`
	FetchedAt  int64  `json:"fetched_at"`
	RefreshSec int    `json:"refresh_sec"`
	Figures    *struct {
		Turns   int     `json:"turns"`
		CostUSD float64 `json:"cost_usd"`
		Tokens  struct {
			Input  int64 `json:"input"`
			Output int64 `json:"output"`
		} `json:"tokens"`
		Tools []struct {
			Name     string `json:"name"`
			Calls    int    `json:"calls"`
			Failures int    `json:"failures"`
		} `json:"tools"`
		Failures []struct {
			Class string `json:"class"`
			Count int    `json:"count"`
		} `json:"failures"`
		PerTurn []struct {
			CostUSD float64 `json:"cost_usd"`
		} `json:"per_turn"`
	} `json:"figures"`
}

// langfuseTurn is the turn a stand-in Langfuse reports a trace of.
type langfuseTurn struct {
	loopID, sessionID string
	start             time.Time
}

// A stand-in Langfuse serving one turn of a loop's observations, in the
// shape #1300's probe read on a live hub: newest first, attributes
// flattened, tokens as attributes with no usage and no cost, and a Bash
// call whose execution failed. It answers a caller with the read key, and
// is down once told so.
func fakeLangfuse(t *testing.T, of *atomic.Pointer[langfuseTurn], down *atomic.Bool) *httptest.Server {
	t.Helper()
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte(syntheticReadKey))
	lf := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if down.Load() {
			http.Error(w, "down", http.StatusServiceUnavailable)
			return
		}
		if r.URL.Path != "/api/public/v2/observations" || r.Header.Get("Authorization") != want {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		turn := of.Load()
		stamp := func(more string) string {
			return fmt.Sprintf(`{"resourceAttributes.spool.loop.id": %q, "attributes.session.id": %q%s}`, turn.loopID, turn.sessionID, more)
		}
		at := func(offset time.Duration) string { return turn.start.Add(offset).UTC().Format(time.RFC3339Nano) }
		fmt.Fprintf(w, `{"data": [
		  {"id": "x1", "traceId": "t1", "parentObservationId": "b1", "type": "TOOL", "name": "claude_code.tool.execution", "startTime": %q, "endTime": %q, "metadata": %s},
		  {"id": "b1", "traceId": "t1", "parentObservationId": "a1", "type": "TOOL", "name": "Bash", "startTime": %q, "endTime": %q, "metadata": %s},
		  {"id": "g1", "traceId": "t1", "parentObservationId": "a1", "type": "GENERATION", "name": "claude_code.llm_request", "startTime": %q, "endTime": %q,
		   "usageDetails": {}, "costDetails": {}, "totalCost": null, "metadata": %s},
		  {"id": "a1", "traceId": "t1", "type": "AGENT", "name": "claude_code.interaction", "startTime": %q, "endTime": %q, "metadata": %s}
		], "meta": {}}`,
			at(600*time.Millisecond), at(700*time.Millisecond), stamp(`, "attributes.success": false`),
			at(600*time.Millisecond), at(700*time.Millisecond), stamp(""),
			at(100*time.Millisecond), at(500*time.Millisecond), stamp(`, "attributes.input_tokens": 120, "attributes.output_tokens": 30`),
			at(0), at(800*time.Millisecond), stamp(""))
	}))
	t.Cleanup(lf.Close)
	return lf
}

// The figures route answers off with no Langfuse read side set, refuses a
// window it doesn't answer, and once a read address and key are set
// answers the loop's figures from the hub's read, numbers and names only:
// tokens and tools from Langfuse, and spend from the hub's own turn, which
// Langfuse holds none of. With Langfuse down it keeps answering them,
// stale (#1300).
func TestFiguresReadBackFromLangfuse(t *testing.T) {
	t.Parallel()
	hub := startServerArgs(t, t.TempDir(), "--runtime", "bare", "--figures-refresh-sec", "1")
	hub.createLoop("gauge", map[string]any{"fleet": false})
	var gauge loopView
	hub.mustJSON("GET", "/api/loops/gauge", nil, &gauge)
	hub.message("gauge", "count this")
	ran := hub.waitTurn("gauge", firstTurnWait, func(tn turn) bool { return strings.Contains(tn.ResultText, "count this") })
	hub.waitState("gauge", "asleep", 30*time.Second)
	var spent float64 // every turn of the loop, which the 24h window holds
	for _, tn := range hub.turns("gauge") {
		spent += tn.CostUSD
	}
	if ran.CostUSD <= 0 || ran.SessionID == "" {
		t.Fatalf("gauge's turn = %+v, want a priced turn of a session", ran)
	}
	var of atomic.Pointer[langfuseTurn]
	// the CLI opens its span a moment after the hub stamps the turn
	of.Store(&langfuseTurn{loopID: gauge.ID, sessionID: ran.SessionID, start: time.UnixMilli(ran.StartedAt).Add(200 * time.Millisecond)})
	var down atomic.Bool
	lf := fakeLangfuse(t, &of, &down)

	var view figuresJSON
	hub.mustJSON("GET", "/api/loops/gauge/figures", nil, &view)
	if view.Langfuse != "off" || view.Figures != nil || view.Window != "24h" || view.Loop != "gauge" {
		t.Fatalf("figures with no Langfuse = %+v, want off for 24h with no figures", view)
	}
	if resp, body := hub.do("GET", "/api/loops/gauge/figures?window=2d", nil); resp.StatusCode != 400 || !strings.Contains(string(body), "figures_window") {
		t.Errorf("window 2d = %d %s, want 400 figures_window", resp.StatusCode, body)
	}

	hub.mustJSON("PUT", "/api/settings/langfuse", map[string]any{"read_address": lf.URL, "read_key": syntheticReadKey}, nil)
	deadline := time.Now().Add(15 * time.Second)
	for view.Langfuse != "ok" && time.Now().Before(deadline) {
		time.Sleep(200 * time.Millisecond)
		hub.mustJSON("GET", "/api/loops/gauge/figures", nil, &view)
	}
	if view.Langfuse != "ok" || view.Figures == nil || view.Figures.Turns != 1 || view.RefreshSec != 1 || view.FetchedAt == 0 {
		t.Fatalf("figures once read = %+v, want ok: one turn", view)
	}
	got := view.Figures
	if math.Abs(got.CostUSD-spent) > 1e-9 || len(got.PerTurn) != 1 || got.PerTurn[0].CostUSD != ran.CostUSD {
		t.Errorf("spend = %v, the turn's row %+v; want the hub's turns, %v, and the traced turn's own %v on its row", got.CostUSD, got.PerTurn, spent, ran.CostUSD)
	}
	if got.Tokens.Input != 120 || got.Tokens.Output != 30 {
		t.Errorf("tokens = %+v, want 120 in and 30 out, read from the span's attributes", got.Tokens)
	}
	if len(got.Tools) != 1 || got.Tools[0].Name != "Bash" || got.Tools[0].Calls != 1 || got.Tools[0].Failures != 1 ||
		len(got.Failures) != 1 || got.Failures[0].Class != "tool" || got.Failures[0].Count != 1 {
		t.Errorf("tools = %+v, failures = %+v; want one failed Bash call, its execution no call of its own", got.Tools, got.Failures)
	}
	_, body := hub.do("GET", "/api/loops/gauge/figures?window=1h", nil)
	for _, half := range strings.Split(syntheticReadKey, ":") {
		if strings.Contains(string(body), half) {
			t.Fatalf("the figures carry the read key: %s", body)
		}
	}

	down.Store(true)
	fetched := view.FetchedAt
	deadline = time.Now().Add(15 * time.Second)
	for view.Langfuse != "stale" && time.Now().Before(deadline) {
		time.Sleep(200 * time.Millisecond)
		hub.mustJSON("GET", "/api/loops/gauge/figures", nil, &view)
	}
	if view.Langfuse != "stale" || view.Figures == nil || view.Figures.Turns != 1 || view.FetchedAt < fetched {
		t.Fatalf("figures with Langfuse down = %+v, want the last figures, stale", view)
	}
}
