package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/enes-alatas/spool/internal/loop"
	"github.com/enes-alatas/spool/internal/store"
)

// planUsageView is the Claude plan's usage as the hub last observed it: one
// entry per limit window, when it was observed, and where from. Unknown says
// why there is no number instead of failing the read, so the Fleet page can
// show the reason where the bars would be.
type planUsageView struct {
	FiveHour *planWindowView `json:"five_hour"`
	SevenDay *planWindowView `json:"seven_day"`
	AsOf     int64           `json:"as_of,omitempty"`
	Source   string          `json:"source,omitempty"`
	Unknown  string          `json:"unknown,omitempty"`
	// Cap is the plan cap while it holds, and null otherwise: unknown
	// usage, nothing over its threshold, or a Resume now (ADR-0047).
	Cap *planCapView `json:"cap"`
	// ResumedUntil is when the operator's Resume now stops holding the cap
	// off, present only while it does.
	ResumedUntil int64 `json:"resumed_until,omitempty"`
}

// planUsagesView is GET /api/plan-usage: the default login's plan usage at
// the top, and every login's in logins, the default first (ADR-0060).
// Each carries its own cap and Resume now hold.
type planUsagesView struct {
	planUsageView
	Logins []loginPlanUsageView `json:"logins"`
}

// loginPlanUsageView is one Claude login's plan usage, by its name.
type loginPlanUsageView struct {
	Name string `json:"name"`
	planUsageView
}

// planWindowView is one window. A window whose reset time has passed since
// the observation reads Reset with nothing used, and no next reset time,
// which only the next observation can tell. CapPercent is the window's
// plan cap threshold, 0 when its cap is off.
type planWindowView struct {
	UsedPercent float64 `json:"used_percent"`
	ResetsAt    int64   `json:"resets_at,omitempty"`
	Reset       bool    `json:"reset,omitempty"`
	CapPercent  int     `json:"cap_percent"`
}

// planCapView is a holding cap: the windows at or over their thresholds,
// and when the loops wake, the latest of those windows' resets.
type planCapView struct {
	Windows []string `json:"windows"`
	Until   int64    `json:"until"`
}

// unknownNotObserved is the reason a hub gives before any loop has reported
// the plan's usage.
const unknownNotObserved = "no loop has reported the plan's usage yet"

// resumePlanCapReq names the Claude login whose cap Resume now holds off,
// as a loop names it: none, or "default", for the default.
type resumePlanCapReq struct {
	ClaudeLogin string `json:"claude_login"`
}

// handleResumePlanCap holds a login's plan cap off until its windows
// reset, and answers with the plan usage that results. A request with no
// body resumes the default's. With that login not capped it changes
// nothing, so a second press is harmless.
func (server *Server) handleResumePlanCap(w http.ResponseWriter, r *http.Request) {
	var req resumePlanCapReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		server.jsonErr(w, 400, "bad json: %v", err)
		return
	}
	login := loopLoginName(req.ClaudeLogin)
	if login != "" {
		if _, err := server.Store.ClaudeLogins().Get(r.Context(), login); errors.Is(err, store.ErrNotFound) {
			server.jsonErrCode(w, 400, codeClaudeLogin, "the hub holds no Claude login named %s", login)
			return
		} else if err != nil {
			server.jsonErr(w, 500, "%v", err)
			return
		}
	}
	if err := server.PlanCap.Resume(r.Context(), login, time.Now().UnixMilli()); err != nil {
		server.jsonErr(w, 500, "%v", err)
		return
	}
	server.handlePlanUsage(w, r)
}

func (server *Server) handlePlanUsage(w http.ResponseWriter, r *http.Request) {
	usages, err := loop.PlanUsages(r.Context(), server.Store.Settings(), server.Store.ClaudeLogins())
	if err != nil {
		server.jsonErr(w, 500, "%v", err)
		return
	}
	now := time.Now().UnixMilli()
	fiveHour, sevenDay := server.PlanCap.Thresholds()
	// the first is the default's, which the top-level fields stay until
	// the control room reads logins alone (ADR-0060)
	view := planUsagesView{planUsageView: planUsageOf(usages[0].Usage, server.PlanCap.At("", now), fiveHour, sevenDay, now)}
	for _, usage := range usages {
		view.Logins = append(view.Logins, loginPlanUsageView{
			Name:          usage.Login,
			planUsageView: planUsageOf(usage.Usage, server.PlanCap.At(usage.Login, now), fiveHour, sevenDay, now),
		})
	}
	writeJSON(w, 200, view)
}

// planUsageOf renders record as read at the time now, with the plan cap as
// judged then and the thresholds it was judged by.
func planUsageOf(record store.PlanUsageRecord, planCap loop.Cap, fiveHour, sevenDay int, now int64) planUsageView {
	view := planUsageView{
		FiveHour: planWindowOf(record.FiveHour, fiveHour, now),
		SevenDay: planWindowOf(record.SevenDay, sevenDay, now),
	}
	if planCap.Holds(now) {
		view.Cap = &planCapView{Windows: planCap.Windows, Until: planCap.Until}
	}
	if planCap.ResumedUntil > now {
		view.ResumedUntil = planCap.ResumedUntil
	}
	if view.FiveHour == nil && view.SevenDay == nil {
		view.Unknown = unknownNotObserved
		return view
	}
	view.AsOf, view.Source = record.At, record.Source
	return view
}

func planWindowOf(window *store.PlanUsageWindow, capPercent int, now int64) *planWindowView {
	switch {
	case window == nil:
		return nil
	case window.ResetsAt <= now:
		return &planWindowView{Reset: true, CapPercent: capPercent}
	}
	return &planWindowView{UsedPercent: window.Utilization * 100, ResetsAt: window.ResetsAt, CapPercent: capPercent}
}
