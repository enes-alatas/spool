//go:build integration

package itest

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/enes-alatas/spool/internal/store"
)

type planWindowJSON struct {
	UsedPercent float64 `json:"used_percent"`
	ResetsAt    int64   `json:"resets_at"`
	Reset       bool    `json:"reset"`
	CapPercent  int     `json:"cap_percent"`
}

// planUsageJSON is one Claude login's plan usage.
type planUsageJSON struct {
	FiveHour *planWindowJSON `json:"five_hour"`
	SevenDay *planWindowJSON `json:"seven_day"`
	AsOf     int64           `json:"as_of"`
	Source   string          `json:"source"`
	Unknown  string          `json:"unknown"`
	Cap      *struct {
		Windows []string `json:"windows"`
		Until   int64    `json:"until"`
	} `json:"cap"`
	ResumedUntil int64 `json:"resumed_until"`
}

// planUsagesJSON is GET /api/plan-usage: every Claude login's plan usage,
// the default first.
type planUsagesJSON struct {
	Logins []loginPlanUsageJSON `json:"logins"`
}

type loginPlanUsageJSON struct {
	Name string `json:"name"`
	planUsageJSON
}

// login is the entry for the Claude login called name, nil without one.
func (view planUsagesJSON) login(name string) *loginPlanUsageJSON {
	for i := range view.Logins {
		if view.Logins[i].Name == name {
			return &view.Logins[i]
		}
	}
	return nil
}

// planUsages reads plan usage, which carries logins and nothing else: the
// default login's fields at the top went with the single-login routes
// (#1077).
func (hub *server) planUsages() planUsagesJSON {
	hub.t.Helper()
	var fields map[string]json.RawMessage
	hub.mustJSON("GET", "/api/plan-usage", nil, &fields)
	for field := range fields {
		if field != "logins" {
			hub.t.Fatalf("plan usage carries %q, want logins alone", field)
		}
	}
	var view planUsagesJSON
	if err := json.Unmarshal(fields["logins"], &view.Logins); err != nil {
		hub.t.Fatalf("plan usage logins: %v", err)
	}
	return view
}

// planUsage is the default Claude login's plan usage.
func (hub *server) planUsage() planUsageJSON {
	hub.t.Helper()
	usage := hub.planUsages().login("default")
	if usage == nil {
		hub.t.Fatal("plan usage has no entry for the default login")
	}
	return usage.planUsageJSON
}

// The hub reads the Claude plan's usage from the rate-limit event a loop's
// own claude process emits (#647): unknown, with the reason, until a turn
// carries one; then both windows, as percent with their reset times; kept
// through a turn that carries none, and through a restart of the hub.
func TestThePlanUsageIsReadFromTheLoopsStream(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	srv := startServer(t, dataDir)
	// line 1 answers the creation tick with no rate-limit event; line 2
	// reports the windows; line 3 is a turn with no event again
	ws := workspaceWithScript(t, "first\n!usage 0.42 0.17 measured\nquiet\n")
	srv.createLoop("meter", map[string]any{"workspace_path": ws})
	srv.waitTurn("meter", firstTurnWait, func(tr turn) bool { return strings.Contains(tr.ResultText, "first") })

	if view := srv.planUsage(); view.Unknown == "" || view.FiveHour != nil || view.SevenDay != nil || view.AsOf != 0 {
		t.Fatalf("before any rate-limit event the read must be unknown with a reason, got %+v", view)
	}

	before := time.Now().UnixMilli()
	srv.message("meter", "how much is used?")
	srv.waitTurn("meter", 30*time.Second, func(tr turn) bool { return strings.Contains(tr.ResultText, "measured") })
	measured := srv.planUsage()
	assertPlanUsage(t, measured, before)

	srv.message("meter", "and now?")
	srv.waitTurn("meter", 30*time.Second, func(tr turn) bool { return strings.Contains(tr.ResultText, "quiet") })
	if view := srv.planUsage(); view.AsOf != measured.AsOf || *view.FiveHour != *measured.FiveHour {
		t.Fatalf("a turn with no rate-limit event changed the read: %+v, was %+v", view, measured)
	}

	srv.stop()
	srv = startServer(t, dataDir)
	if view := srv.planUsage(); view.AsOf != measured.AsOf || view.FiveHour == nil || *view.SevenDay != *measured.SevenDay {
		t.Fatalf("the read did not survive a restart: %+v, was %+v", view, measured)
	}
}

// assertPlanUsage checks a read of fakeclaude's "!usage 0.42 0.17", which
// reports the five-hour window resetting in five hours and the seven-day one
// in seven days, observed no earlier than since.
func assertPlanUsage(t *testing.T, view planUsageJSON, since int64) {
	t.Helper()
	if view.Unknown != "" || view.Source != "stream" || view.AsOf < since {
		t.Fatalf("read = %+v; want a stream observation from after %d", view, since)
	}
	near := func(window *planWindowJSON, percent float64, resetIn time.Duration) bool {
		if window == nil || window.Reset {
			return false
		}
		want := since + resetIn.Milliseconds()
		return window.UsedPercent > percent-0.01 && window.UsedPercent < percent+0.01 &&
			window.ResetsAt > want-time.Minute.Milliseconds() && window.ResetsAt < want+time.Minute.Milliseconds()
	}
	if !near(view.FiveHour, 42, 5*time.Hour) {
		t.Fatalf("five_hour = %+v; want 42%% resetting in five hours", view.FiveHour)
	}
	if !near(view.SevenDay, 17, 7*24*time.Hour) {
		t.Fatalf("seven_day = %+v; want 17%% resetting in seven days", view.SevenDay)
	}
}

// Within a window that has not reset, the hub's plan usage only rises: a
// reading lower than the last one leaves it, and a window whose reset is
// further off than its span is refused, so neither can lift or stretch the
// plan cap (ADR-0047).
func TestThePlanUsageOnlyRisesWithinAWindow(t *testing.T) {
	t.Parallel()
	srv := startServer(t, t.TempDir())
	ws := workspaceWithScript(t, "first\n!usage 0.42 0.17 measured\n!usage 0.05 0.02 lower\n!usage 0.99 0.17 resets=86400 far\n")
	srv.createLoop("meter", map[string]any{"workspace_path": ws})
	srv.waitTurn("meter", firstTurnWait, func(tr turn) bool { return strings.Contains(tr.ResultText, "first") })

	before := time.Now().UnixMilli()
	srv.message("meter", "how much is used?")
	srv.waitTurn("meter", 30*time.Second, func(tr turn) bool { return strings.Contains(tr.ResultText, "measured") })
	measured := srv.planUsage()
	assertPlanUsage(t, measured, before)

	srv.message("meter", "and now?")
	srv.waitTurn("meter", 30*time.Second, func(tr turn) bool { return strings.Contains(tr.ResultText, "lower") })
	if view := srv.planUsage(); *view.FiveHour != *measured.FiveHour || *view.SevenDay != *measured.SevenDay {
		t.Fatalf("a lower reading lowered the plan usage: %+v %+v, was %+v %+v", view.FiveHour, view.SevenDay, measured.FiveHour, measured.SevenDay)
	}

	srv.message("meter", "and now?")
	srv.waitTurn("meter", 30*time.Second, func(tr turn) bool { return strings.Contains(tr.ResultText, "far") })
	if view := srv.planUsage(); view.Cap != nil || *view.FiveHour != *measured.FiveHour {
		t.Fatalf("a five-hour window resetting in a day was kept: cap %+v, five_hour %+v", view.Cap, view.FiveHour)
	}
}

// Each Claude login keeps its own plan usage (ADR-0060): a loop's report
// lands on the login it runs on, and neither raises nor lowers another
// login's. The top-level fields stay the default's, and a login's usage
// survives a restart. The loops are bare, which the API refuses to put on
// a login, so the test names one in the store: what the hub does with a
// loop's report is the same on any runtime.
func TestPlanUsageIsKeptPerClaudeLogin(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dataDir := t.TempDir()
	hub := startServer(t, dataDir)
	hub.createLoop("aster", map[string]any{"workspace_path": workspaceWithScript(t, "first\n!usage 0.42 0.17 measured\n!usage 0.05 0.02 lower\n")})
	hub.createLoop("bloom", map[string]any{"workspace_path": workspaceWithScript(t, "first\n!usage 0.60 0.30 bloomed\n")})
	for _, name := range []string{"aster", "bloom"} {
		hub.waitTurn(name, firstTurnWait, func(tr turn) bool { return strings.Contains(tr.ResultText, "first") })
	}
	hub.stop()
	db := openStoreAt(t, dataDir)
	if err := db.ClaudeLogins().Create(ctx, &store.ClaudeLogin{Name: "ada", CreatedAt: 1}); err != nil {
		t.Fatal(err)
	}
	db.Close()
	raw, err := sql.Open("sqlite", "file:"+filepath.Join(dataDir, "spool.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`UPDATE loops SET claude_login = 'ada' WHERE name = 'bloom'`); err != nil {
		t.Fatal(err)
	}
	raw.Close()
	hub = startServer(t, dataDir)

	view := hub.planUsages()
	if len(view.Logins) != 2 || view.Logins[0].Name != "default" || view.Logins[1].Name != "ada" {
		t.Fatalf("logins = %+v; want default, then ada", view.Logins)
	}
	if ada := view.login("ada"); ada.Unknown == "" || ada.FiveHour != nil {
		t.Fatalf("ada before any report = %+v; want unknown with a reason", ada.planUsageJSON)
	}

	before := time.Now().UnixMilli()
	hub.message("aster", "how much is used?")
	hub.waitTurn("aster", 30*time.Second, func(tr turn) bool { return strings.Contains(tr.ResultText, "measured") })
	hub.message("bloom", "how much is used?")
	hub.waitTurn("bloom", 30*time.Second, func(tr turn) bool { return strings.Contains(tr.ResultText, "bloomed") })
	view = hub.planUsages()
	assertPlanUsage(t, view.login("default").planUsageJSON, before)
	ada := view.login("ada")
	if ada.FiveHour == nil || ada.FiveHour.UsedPercent < 59.99 || ada.FiveHour.UsedPercent > 60.01 || ada.SevenDay == nil {
		t.Fatalf("ada = %+v %+v; want bloom's 60%%", ada.FiveHour, ada.SevenDay)
	}

	// a lower report on the default leaves it, and leaves ada's too
	hub.message("aster", "and now?")
	hub.waitTurn("aster", 30*time.Second, func(tr turn) bool { return strings.Contains(tr.ResultText, "lower") })
	hub.stop()
	hub = startServer(t, dataDir)
	after := hub.planUsages()
	if *after.login("default").FiveHour != *view.login("default").FiveHour || *after.login("ada").FiveHour != *ada.FiveHour {
		t.Fatalf("after a lower report and a restart: default %+v, ada %+v; want %+v and %+v kept",
			after.login("default").FiveHour, after.login("ada").FiveHour, view.login("default").FiveHour, ada.FiveHour)
	}
}
