package httpapi

import (
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
}

// planWindowView is one window. A window whose reset time has passed since
// the observation reads Reset with nothing used, and no next reset time,
// which only the next observation can tell.
type planWindowView struct {
	UsedPercent float64 `json:"used_percent"`
	ResetsAt    int64   `json:"resets_at,omitempty"`
	Reset       bool    `json:"reset,omitempty"`
}

// unknownNotObserved is the reason a hub gives before any loop has reported
// the plan's usage.
const unknownNotObserved = "no loop has reported the plan's usage yet"

func (server *Server) handlePlanUsage(w http.ResponseWriter, r *http.Request) {
	record, err := loop.LastPlanUsage(r.Context(), server.Store.Settings())
	if err != nil {
		server.jsonErr(w, 500, "%v", err)
		return
	}
	writeJSON(w, 200, planUsageOf(record, time.Now().UnixMilli()))
}

// planUsageOf renders record as read at the time now.
func planUsageOf(record store.PlanUsageRecord, now int64) planUsageView {
	view := planUsageView{FiveHour: planWindowOf(record.FiveHour, now), SevenDay: planWindowOf(record.SevenDay, now)}
	if view.FiveHour == nil && view.SevenDay == nil {
		view.Unknown = unknownNotObserved
		return view
	}
	view.AsOf, view.Source = record.At, record.Source
	return view
}

func planWindowOf(window *store.PlanUsageWindow, now int64) *planWindowView {
	switch {
	case window == nil:
		return nil
	case window.ResetsAt <= now:
		return &planWindowView{Reset: true}
	}
	return &planWindowView{UsedPercent: window.Utilization * 100, ResetsAt: window.ResetsAt}
}
