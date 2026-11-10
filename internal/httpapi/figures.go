package httpapi

import (
	"net/http"

	"github.com/enes-alatas/spool/internal/figures"
)

// codeFiguresWindow refuses a figures window the route doesn't answer.
const codeFiguresWindow = "figures_window"

// figuresView is GET /api/loops/{name}/figures (#1300): a loop's numbers
// from the operator's Langfuse over a window, numbers and names only.
type figuresView struct {
	Loop     string `json:"loop"`
	Window   string `json:"window"`
	Langfuse string `json:"langfuse"`
	// FetchedAt is when the hub last read Langfuse, Unix milliseconds,
	// 0 before its first read.
	FetchedAt  int64            `json:"fetched_at"`
	RefreshSec int              `json:"refresh_sec"`
	Figures    *figures.Summary `json:"figures"`
}

func (server *Server) handleLoopFigures(w http.ResponseWriter, r *http.Request) {
	loopRecord := server.loopByName(w, r)
	if loopRecord == nil {
		return
	}
	window := r.URL.Query().Get("window")
	if window == "" {
		window = "24h"
	}
	span, ok := figures.Windows[window]
	if !ok {
		server.jsonErrCode(w, 400, codeFiguresWindow, "window is one of 1h, 24h, 7d or 30d")
		return
	}
	view := figuresView{Loop: loopRecord.Name, Window: window, Langfuse: figures.StateOff}
	if server.Figures == nil {
		writeJSON(w, 200, view)
		return
	}
	answer, err := server.Figures.Of(r.Context(), loopRecord, span)
	if err != nil {
		server.jsonErr(w, 500, "%v", err)
		return
	}
	view.Langfuse, view.FetchedAt, view.Figures = answer.State, answer.FetchedAt, answer.Figures
	view.RefreshSec = int(server.Figures.RefreshEvery().Seconds())
	writeJSON(w, 200, view)
}
