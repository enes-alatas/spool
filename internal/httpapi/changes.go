package httpapi

import (
	"errors"
	"net/http"
	"time"

	"github.com/enes-alatas/spool/internal/store"
)

// loopChange is what a route that changes a loop, or acts on it, adds to
// the loop's change record (ADR-0048), and which path value names the loop.
type loopChange struct {
	action string
	param  string
}

// loopChangeRoutes are the routes that change a loop or act on it. Each is
// recorded once it succeeds, with who called it. POST /api/loops records
// its own, since the loop it names does not exist until it has run.
var loopChangeRoutes = map[string]loopChange{
	"PATCH /api/loops/{name}":                               {store.LoopChangeEdit, "name"},
	"DELETE /api/loops/{name}":                              {store.LoopChangeDelete, "name"},
	"POST /api/loops/{name}/pause":                          {store.LoopChangePause, "name"},
	"POST /api/loops/{name}/resume":                         {store.LoopChangeResume, "name"},
	"POST /api/loops/{name}/wake":                           {store.LoopChangeWake, "name"},
	"POST /api/loops/{name}/kill":                           {store.LoopChangeKill, "name"},
	"POST /api/loops/{name}/rotate":                         {store.LoopChangeRotate, "name"},
	"POST /api/loops/{name}/rehome":                         {store.LoopChangeRehome, "name"},
	"POST /api/loops/{name}/cloud/token":                    {store.LoopChangeCloudToken, "name"},
	"POST /api/loops/{name}/cloud/allowlist/confirm":        {store.LoopChangeCloudAllow, "name"},
	"POST /api/loops/{name}/cloud/check":                    {store.LoopChangeCloudCheck, "name"},
	"POST /api/loops/{name}/workstation/restart":            {store.LoopChangeRestart, "name"},
	"POST /api/loops/{name}/workstation/poweroff":           {store.LoopChangePowerOff, "name"},
	"POST /api/loops/{name}/workstation/poweron":            {store.LoopChangePowerOn, "name"},
	"POST /api/loops/{name}/workstation/recreate":           {store.LoopChangeRecreate, "name"},
	"POST /api/loops/{name}/problems/{symptom}/acknowledge": {store.LoopChangeAcknowledge, "name"},
	"PUT /api/loops/{name}/owner":                           {store.LoopChangeOwner, "name"},
	"PUT /api/loops/{name}/rooms":                           {store.LoopChangeRoom, "name"},
	"DELETE /api/loops/{name}/rooms/{surface}/{room}":       {store.LoopChangeUnroom, "name"},
	"PUT /api/loops/{name}/connections/{connection}":        {store.LoopChangeAttach, "name"},
	"DELETE /api/loops/{name}/connections/{connection}":     {store.LoopChangeDetach, "name"},
	"PUT /api/channels/{name}/loops/{loop}":                 {store.LoopChangeJoin, "loop"},
	"DELETE /api/channels/{name}/loops/{loop}":              {store.LoopChangeLeave, "loop"},
}

// recordingChange wraps the handler of a route in loopChangeRoutes so that
// a call that succeeds is recorded. The loop is read before the handler
// runs, so a deletion is recorded under the loop it removed. A loop that
// is not there is left to the handler to answer.
func (server *Server) recordingChange(pattern string, next http.HandlerFunc) http.HandlerFunc {
	change, ok := loopChangeRoutes[pattern]
	if !ok {
		return next
	}
	return func(w http.ResponseWriter, r *http.Request) {
		loopRecord, err := server.Store.Loops().GetByName(r.Context(), r.PathValue(change.param))
		if err != nil {
			next(w, r)
			return
		}
		status := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next(status, r)
		if status.status >= 200 && status.status < 300 {
			server.recordChange(r, change.action, loopRecord)
		}
	}
}

// recordChange adds a change to the loop's record under whoever made the
// request, and names them on loopRecord as the record does in the store.
// A failure is logged rather than answered: the change itself is done.
func (server *Server) recordChange(r *http.Request, action string, loopRecord *store.Loop) {
	by := authorOf(r, "")
	err := server.Store.LoopChanges().Record(r.Context(), &store.LoopChange{
		At: time.Now().UnixMilli(), By: by, Action: action, LoopID: loopRecord.ID, LoopName: loopRecord.Name})
	if err != nil {
		server.Log.Error("record loop change", "loop", loopRecord.Name, "action", action, "err", err)
		return
	}
	switch action {
	case store.LoopChangeWake:
	case store.LoopChangeCreate:
		loopRecord.CreatedBy, loopRecord.UpdatedBy = by, by
	default:
		loopRecord.UpdatedBy = by
	}
}

// statusWriter remembers the status a handler answered with.
type statusWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (writer *statusWriter) WriteHeader(status int) {
	if !writer.wroteHeader {
		writer.status, writer.wroteHeader = status, true
	}
	writer.ResponseWriter.WriteHeader(status)
}

func (writer *statusWriter) Unwrap() http.ResponseWriter { return writer.ResponseWriter }

// changeView is one change as GET /api/changes serves it. loop is the
// loop's name as it was when the change was made.
type changeView struct {
	ID     int64  `json:"id"`
	At     int64  `json:"at"`
	By     string `json:"by"`
	Action string `json:"action"`
	Loop   string `json:"loop"`
}

// handleChanges lists the change record, newest first. The loop parameter
// narrows it to one loop: by the loop's id while it exists, so a new loop
// does not inherit the record of a deleted one it shares a name with, and
// by the name once the loop is gone.
func (server *Server) handleChanges(w http.ResponseWriter, r *http.Request) {
	filter := store.LoopChangeFilter{Limit: queryInt(r, "limit", 100)}
	if name := r.URL.Query().Get("loop"); name != "" {
		loopRecord, err := server.Store.Loops().GetByName(r.Context(), name)
		switch {
		case err == nil:
			filter.LoopID = loopRecord.ID
		case errors.Is(err, store.ErrNotFound):
			filter.LoopName = name
		default:
			server.jsonErr(w, http.StatusInternalServerError, "%v", err)
			return
		}
	}
	changes, err := server.Store.LoopChanges().List(r.Context(), filter)
	if err != nil {
		server.jsonErr(w, http.StatusInternalServerError, "%v", err)
		return
	}
	views := make([]changeView, 0, len(changes))
	for _, change := range changes {
		views = append(views, changeView{ID: change.ID, At: change.At, By: change.By, Action: change.Action, Loop: change.LoopName})
	}
	writeJSON(w, http.StatusOK, views)
}
