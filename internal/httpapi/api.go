// Package httpapi exposes Spool's REST + SSE surface and serves the web UI.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/enes-alatas/spool/internal/bus"
	"github.com/enes-alatas/spool/internal/gitws"
	"github.com/enes-alatas/spool/internal/loop"
	"github.com/enes-alatas/spool/internal/route"
	"github.com/enes-alatas/spool/internal/runtime"
	"github.com/enes-alatas/spool/internal/sched"
	"github.com/enes-alatas/spool/internal/store"
)

var nameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{1,31}$`)

// Telegram is the slice of the bridge the API needs; nil until M5 wires it.
type Telegram interface {
	// ValidateToken calls getMe and returns the bot username.
	ValidateToken(ctx context.Context, token string) (string, error)
	// LoopChanged tells the bridge a loop's telegram config changed.
	LoopChanged(l *store.Loop)
	// LoopRemoved stops a loop's poller.
	LoopRemoved(loopID string)
	// Status describes the bridge state for a loop.
	Status(loopID string) any
}

type Server struct {
	Store     store.Store
	Bus       *bus.Bus
	Manager   *loop.Manager
	Router    *route.Router
	Sched     *sched.Scheduler
	Telegram  Telegram // may be nil
	DataDir   string
	ClaudeVer string
	// DefaultRuntime is the kind loops get when a create request doesn't
	// name one (ADR-0017: docker whenever the daemon is reachable).
	DefaultRuntime string
	// RuntimeAvailable answers whether a runtime kind can host a new loop
	// right now; wired in cmd so this package stays free of runtime imports.
	RuntimeAvailable func(ctx context.Context, kind string) error
	Log              *slog.Logger
	WebFS            fs.FS // embedded UI dist; may be nil in dev

	// settingsMu serializes the read-validate-write of paired settings, so
	// two concurrent PUTs cannot interleave into an inverted stored pair.
	settingsMu sync.Mutex
	// rulesMu serializes fleet-rule writes: the section cap is checked
	// against the whole enabled set, so two writes must not interleave
	// between the check and the store.
	rulesMu sync.Mutex
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/health", s.handleHealth)
	mux.HandleFunc("GET /api/loops", s.handleListLoops)
	mux.HandleFunc("POST /api/loops", s.handleCreateLoop)
	mux.HandleFunc("GET /api/loops/{name}", s.handleGetLoop)
	mux.HandleFunc("PATCH /api/loops/{name}", s.handlePatchLoop)
	mux.HandleFunc("DELETE /api/loops/{name}", s.handleDeleteLoop)
	mux.HandleFunc("POST /api/loops/{name}/pause", s.handlePause)
	mux.HandleFunc("POST /api/loops/{name}/resume", s.handleResume)
	mux.HandleFunc("POST /api/loops/{name}/wake", s.handleWake)
	mux.HandleFunc("POST /api/loops/{name}/kill", s.handleKill)
	mux.HandleFunc("POST /api/loops/{name}/rotate", s.handleRotate)
	mux.HandleFunc("POST /api/loops/{name}/workstation/restart", s.handlePower(loop.PowerRestart))
	mux.HandleFunc("POST /api/loops/{name}/workstation/poweroff", s.handlePower(loop.PowerOff))
	mux.HandleFunc("POST /api/loops/{name}/workstation/poweron", s.handlePower(loop.PowerOn))
	mux.HandleFunc("POST /api/loops/{name}/workstation/recreate", s.handlePower(loop.PowerRecreate))
	mux.HandleFunc("POST /api/loops/{name}/message", s.handleLoopMessage)
	mux.HandleFunc("GET /api/loops/{name}/events", s.handleLoopEvents)
	mux.HandleFunc("GET /api/loops/{name}/turns", s.handleLoopTurns)
	mux.HandleFunc("GET /api/loops/{name}/telegram/status", s.handleTelegramStatus)
	mux.HandleFunc("PUT /api/loops/{name}/owner", s.handlePutOwner)
	mux.HandleFunc("GET /api/loops/{name}/secrets", s.handleListSecrets)
	mux.HandleFunc("PUT /api/loops/{name}/secrets/{key}", s.handlePutSecret)
	mux.HandleFunc("DELETE /api/loops/{name}/secrets/{key}", s.handleDeleteSecret)
	mux.HandleFunc("GET /api/activity", s.handleActivity)
	mux.HandleFunc("GET /api/loops/{name}/conversation", s.handleLoopConversation)
	mux.HandleFunc("GET /api/settings", s.handleGetSettings)
	mux.HandleFunc("PUT /api/settings", s.handlePutSettings)
	mux.HandleFunc("GET /api/rules", s.handleListRules)
	mux.HandleFunc("POST /api/rules", s.handleCreateRule)
	mux.HandleFunc("PATCH /api/rules/{id}", s.handlePatchRule)
	mux.HandleFunc("DELETE /api/rules/{id}", s.handleDeleteRule)
	mux.HandleFunc("GET /api/telegram/senders", s.handleListSenders)
	mux.HandleFunc("POST /api/telegram/senders/{id}/allow", s.handleSenderStatus(store.SenderAllowed))
	mux.HandleFunc("POST /api/telegram/senders/{id}/block", s.handleSenderStatus(store.SenderBlocked))
	mux.HandleFunc("DELETE /api/telegram/senders/{id}", s.handleDeleteSender)
	mux.HandleFunc("POST /api/workspace/inspect", s.handleWorkspaceInspect)
	mux.HandleFunc("GET /api/stream", s.handleGlobalStream)
	mux.HandleFunc("GET /api/loops/{name}/stream", s.handleLoopStream)
	mux.Handle("/mcp", s.mcpHandler())

	if s.WebFS != nil {
		mux.HandleFunc("/", s.handleUI)
	}
	return mux
}

// --- helpers ---

func (s *Server) jsonErr(w http.ResponseWriter, code int, msg string, args ...any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf(msg, args...)})
}

// jsonErrCode is jsonErr plus a stable machine-readable reason, for the
// cases where one status covers outcomes a client must tell apart. The
// prose stays the human's, the code is the client's.
func (s *Server) jsonErrCode(w http.ResponseWriter, status int, reason, msg string, args ...any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf(msg, args...), "code": reason})
}

// Reasons behind an otherwise ambiguous status, sent as "code".
const (
	codeLoopNotRunning = "loop_not_running"
	codeNoWorkstation  = "no_workstation"
)

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func (s *Server) loopByName(w http.ResponseWriter, r *http.Request) *store.Loop {
	l, err := s.Store.Loops().GetByName(r.Context(), r.PathValue("name"))
	if errors.Is(err, store.ErrNotFound) {
		s.jsonErr(w, http.StatusNotFound, "loop %q not found", r.PathValue("name"))
		return nil
	}
	if err != nil {
		s.jsonErr(w, http.StatusInternalServerError, "%v", err)
		return nil
	}
	return l
}

// loopView is a loop plus live runtime info for the UI.
type loopView struct {
	*store.Loop
	State             string  `json:"state"`
	NextTickAt        int64   `json:"next_tick_at"`
	CostToday         float64 `json:"cost_today_usd"`
	HasTGToken        bool    `json:"has_tg_token"`
	WorkstationUp     bool    `json:"workstation_up"`
	WorkstationDetail string  `json:"workstation_detail,omitempty"`
	// ContextTokens is the context occupancy the last finished turn of the
	// loop's current session measured at its final API call — what the next
	// prompt would carry into the window. Measured at that turn, not a live
	// gauge — and zero right after a rotation, when a retired session's
	// turns say nothing about the fresh one, or when the last turn predates
	// the measure entirely: 0 means unmeasured, never an empty context.
	ContextTokens int `json:"context_tokens"`
	// ContextLimitTokens is that model's context window, or 0 when we don't
	// know it — an unrecognized or not-yet-run model. Clients show absolute
	// tokens rather than a ratio against a guess.
	ContextLimitTokens int `json:"context_limit_tokens"`
	// ContextFillPct is that occupancy as a percentage of the window — the
	// number rotation is judged by, computed here so the actor, this view
	// and the UI cannot each arrive at their own. 0 when either side of the
	// ratio is unknown: unmeasured, never empty.
	ContextFillPct int `json:"context_fill_pct"`
	// DownReason distinguishes a workstation the operator switched off from
	// one that died; empty while it is up (ADR-0021).
	DownReason string `json:"down_reason"`
	// OwnerDMReady reports that the loop can message its owner privately:
	// an owner is configured and has opened a chat with this loop's own
	// bot. A bot cannot open one, so until the owner writes there is
	// nowhere to send (#73).
	OwnerDMReady bool `json:"owner_dm_ready"`
	// OwnerUsername is the configured owner's telegram handle, when known —
	// the name the UI shows instead of a numeric id.
	OwnerUsername string `json:"owner_username,omitempty"`
}

func (s *Server) view(ctx context.Context, l *store.Loop) *loopView {
	out := &loopView{Loop: l, State: loop.StateAsleep, HasTGToken: l.TGBotToken != "", WorkstationUp: true,
		OwnerDMReady: l.OwnerTGUserID != 0 && l.OwnerDMChatID != 0}
	if l.OwnerTGUserID != 0 {
		if sender, err := s.Store.TGSenders().Get(ctx, l.OwnerTGUserID); err == nil {
			out.OwnerUsername = sender.Username
		}
	}
	if actor, ok := s.Manager.Get(l.ID); ok {
		out.State = actor.State()
		health := actor.WorkstationHealth()
		out.WorkstationUp = health.Up
		out.WorkstationDetail = health.Detail
		out.DownReason = actor.DownReason()
	}
	if latest, err := s.Store.Turns().Latest(ctx, l.ID); err == nil && latest.SessionID == l.CurrentSessionID {
		out.ContextTokens = latest.ContextTokens
		out.ContextLimitTokens = loop.ContextLimit(latest.Model)
		out.ContextFillPct = loop.FillPercent(out.ContextTokens, out.ContextLimitTokens)
	}
	if entry, err := s.Store.Schedule().Get(ctx, l.ID); err == nil {
		out.NextTickAt = entry.NextTickAt
	}
	dayStart := time.Now().Truncate(24 * time.Hour).UnixMilli()
	if cost, err := s.Store.Turns().CostSince(ctx, l.ID, dayStart); err == nil {
		out.CostToday = cost
	}
	return out
}

// --- handlers ---

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"ok": true, "claude_version": s.ClaudeVer, "runtime": s.DefaultRuntime})
}

func (s *Server) handleListLoops(w http.ResponseWriter, r *http.Request) {
	loops, err := s.Store.Loops().List(r.Context())
	if err != nil {
		s.jsonErr(w, 500, "%v", err)
		return
	}
	views := make([]*loopView, 0, len(loops))
	for _, l := range loops {
		views = append(views, s.view(r.Context(), l))
	}
	writeJSON(w, 200, views)
}

type createLoopReq struct {
	Name            string  `json:"name"`
	Mission         string  `json:"mission"`
	Model           string  `json:"model"`
	Effort          string  `json:"effort"`         // ""|low|medium|high|xhigh|max
	Pacing          string  `json:"pacing"`         // ""(=fixed)|fixed|self
	Runtime         string  `json:"runtime"`        // ""(=server default)|bare|docker; immutable after creation (ADR-0018)
	Image           string  `json:"image"`          // docker only; "" = the server's default image
	MemMB           int     `json:"mem_mb"`         // docker only; 0 = 4096 (ADR-0017)
	CPUs            float64 `json:"cpus"`           // docker only; 0 = 2
	WorkspacePath   string  `json:"workspace_path"` // bare only; empty = no workspace
	WorkspaceMode   string  `json:"workspace_mode"` // "auto" (default) | "dir" | "none"
	TickIntervalSec int     `json:"tick_interval_sec"`
	MinWakeSec      int     `json:"min_wake_sec"`
	MaxWakeSec      int     `json:"max_wake_sec"`
	IdleTimeoutSec  int     `json:"idle_timeout_sec"`
	TGBotToken      string  `json:"tg_bot_token"`
}

var validEfforts = map[string]bool{"": true, "low": true, "medium": true, "high": true, "xhigh": true, "max": true}

func validPacing(p string) bool {
	return p == "" || p == store.PacingFixed || p == store.PacingSelf
}

func (s *Server) handleCreateLoop(w http.ResponseWriter, r *http.Request) {
	var req createLoopReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.jsonErr(w, 400, "bad json: %v", err)
		return
	}
	req.Name = strings.ToLower(strings.TrimSpace(req.Name))
	if !nameRe.MatchString(req.Name) {
		s.jsonErr(w, 400, "name must match %s", nameRe.String())
		return
	}
	if req.Name == route.BroadcastToken {
		// @all addresses the group's eligible loops; a loop of that name
		// could never be mentioned, only broadcast to (#74).
		s.jsonErr(w, 400, "%q is reserved: @%s addresses every eligible loop in the group",
			route.BroadcastToken, route.BroadcastToken)
		return
	}
	if strings.TrimSpace(req.Mission) == "" {
		s.jsonErr(w, 400, "mission is required")
		return
	}
	if !validEfforts[req.Effort] {
		s.jsonErr(w, 400, "effort must be one of: low, medium, high, xhigh, max (or empty for default)")
		return
	}
	if !validPacing(req.Pacing) {
		s.jsonErr(w, 400, "pacing must be 'fixed' or 'self'")
		return
	}

	loopRuntime := defaultStr(req.Runtime, defaultStr(s.DefaultRuntime, store.RuntimeBare))
	switch loopRuntime {
	case store.RuntimeBare:
		if req.Image != "" || req.MemMB != 0 || req.CPUs != 0 {
			s.jsonErr(w, 400, "image, mem_mb and cpus apply to docker loops only")
			return
		}
	case store.RuntimeDocker:
		if req.WorkspacePath != "" {
			s.jsonErr(w, 400, "a docker loop's workstation is its workspace; workspace_path applies to bare loops only")
			return
		}
		if req.MemMB != 0 && (req.MemMB < 256 || req.MemMB > 262144) {
			s.jsonErr(w, 400, "mem_mb must be between 256 and 262144")
			return
		}
		if req.CPUs != 0 && (req.CPUs < 0.1 || req.CPUs > 64) {
			s.jsonErr(w, 400, "cpus must be between 0.1 and 64")
			return
		}
		if s.RuntimeAvailable != nil {
			if err := s.RuntimeAvailable(r.Context(), store.RuntimeDocker); err != nil {
				s.jsonErr(w, 400, "docker runtime unavailable: %v", err)
				return
			}
		}
	default:
		s.jsonErr(w, 400, "runtime must be 'bare' or 'docker' (or empty for the server default)")
		return
	}

	nowMS := time.Now().UnixMilli()
	l := &store.Loop{
		ID:              loopID(),
		Name:            req.Name,
		Mission:         req.Mission,
		Model:           req.Model,
		Effort:          req.Effort,
		Pacing:          defaultStr(req.Pacing, store.PacingFixed),
		TickIntervalSec: defaultInt(req.TickIntervalSec, 1800),
		MinWakeSec:      defaultInt(req.MinWakeSec, 300),
		MaxWakeSec:      defaultInt(req.MaxWakeSec, 14400),
		IdleTimeoutSec:  defaultInt(req.IdleTimeoutSec, 90),
		TGBotToken:      strings.TrimSpace(req.TGBotToken),
		Runtime:         loopRuntime,
		Status:          store.StatusActive,
		CreatedAt:       nowMS,
		UpdatedAt:       nowMS,
	}
	if loopRuntime == store.RuntimeDocker {
		l.Image = strings.TrimSpace(req.Image)
		l.MemMB = defaultInt(req.MemMB, 4096)
		l.CPUs = req.CPUs
		if l.CPUs == 0 {
			l.CPUs = 2
		}
	}

	if l.TGBotToken != "" && s.Telegram != nil {
		username, err := s.Telegram.ValidateToken(r.Context(), l.TGBotToken)
		if err != nil {
			s.jsonErr(w, 400, "telegram token rejected: %v", err)
			return
		}
		l.TGBotUsername = username
	}

	if l.Runtime == store.RuntimeDocker {
		// The workstation is the workspace (ADR-0017): claude's cwd is the
		// volume-backed home inside the container, stable across wakes.
		l.WorkspaceMode = store.WorkspaceNone
		l.WorkspacePath = runtime.WorkstationHome
	} else if err := s.resolveWorkspace(l, req.WorkspacePath, req.WorkspaceMode); err != nil {
		s.jsonErr(w, 400, "%v", err)
		return
	}

	// A new loop starts owned by the same person as the rest of the fleet:
	// the first allowlisted sender, reassignable per loop (#73).
	l.OwnerTGUserID = s.defaultOwnerID(r.Context())

	if err := s.Store.Loops().Create(r.Context(), l); err != nil {
		if errors.Is(err, store.ErrDuplicate) {
			s.jsonErr(w, 409, "a loop named %q already exists", l.Name)
		} else {
			s.jsonErr(w, 500, "%v", err)
		}
		return
	}
	s.Manager.Add(l)
	if s.Telegram != nil && l.TGBotToken != "" {
		s.Telegram.LoopChanged(l)
	}
	// first tick shortly after creation so the mission starts without waiting
	// a full interval
	s.Sched.ScheduleNow(l.ID)
	writeJSON(w, 201, s.view(r.Context(), l))
}

// resolveWorkspace decides the loop's cwd. Every loop needs a stable cwd
// (claude sessions are keyed by it); workspace-less loops get a home dir
// under the data dir.
func (s *Server) resolveWorkspace(l *store.Loop, path, mode string) error {
	path = strings.TrimSpace(path)
	if path == "" || mode == "none" {
		home := filepath.Join(s.DataDir, "homes", l.Name)
		if err := os.MkdirAll(home, 0o755); err != nil {
			return err
		}
		l.WorkspaceMode = store.WorkspaceNone
		l.WorkspacePath = home
		return nil
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	info, err := os.Stat(abs)
	if err != nil || !info.IsDir() {
		return fmt.Errorf("workspace path %s is not a directory", abs)
	}
	if mode != "dir" && gitws.IsGitRepo(abs) {
		wt := filepath.Join(s.DataDir, "worktrees", l.Name)
		branch, err := gitws.Create(abs, wt, l.Name)
		if err != nil {
			return err
		}
		l.WorkspaceMode = store.WorkspaceWorktree
		l.RepoPath = abs
		l.WorktreePath = wt
		l.WorkspacePath = wt
		l.Branch = branch
		return nil
	}
	l.WorkspaceMode = store.WorkspaceDir
	l.WorkspacePath = abs
	return nil
}

func (s *Server) handleGetLoop(w http.ResponseWriter, r *http.Request) {
	l := s.loopByName(w, r)
	if l == nil {
		return
	}
	writeJSON(w, 200, s.view(r.Context(), l))
}

type patchLoopReq struct {
	Mission         *string `json:"mission"`
	Model           *string `json:"model"`
	Effort          *string `json:"effort"`
	Pacing          *string `json:"pacing"`
	TickIntervalSec *int    `json:"tick_interval_sec"`
	MinWakeSec      *int    `json:"min_wake_sec"`
	MaxWakeSec      *int    `json:"max_wake_sec"`
	IdleTimeoutSec  *int    `json:"idle_timeout_sec"`
	TGBotToken      *string `json:"tg_bot_token"`
}

func (s *Server) handlePatchLoop(w http.ResponseWriter, r *http.Request) {
	l := s.loopByName(w, r)
	if l == nil {
		return
	}
	var req patchLoopReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.jsonErr(w, 400, "bad json: %v", err)
		return
	}
	if req.Mission != nil {
		l.Mission = *req.Mission
	}
	if req.Model != nil {
		l.Model = *req.Model
	}
	if req.Effort != nil {
		if !validEfforts[*req.Effort] {
			s.jsonErr(w, 400, "effort must be one of: low, medium, high, xhigh, max (or empty for default)")
			return
		}
		l.Effort = *req.Effort
	}
	if req.Pacing != nil {
		if !validPacing(*req.Pacing) {
			s.jsonErr(w, 400, "pacing must be 'fixed' or 'self'")
			return
		}
		l.Pacing = defaultStr(*req.Pacing, store.PacingFixed)
	}
	if req.TickIntervalSec != nil {
		l.TickIntervalSec = *req.TickIntervalSec
	}
	if req.MinWakeSec != nil {
		l.MinWakeSec = *req.MinWakeSec
	}
	if req.MaxWakeSec != nil {
		l.MaxWakeSec = *req.MaxWakeSec
	}
	if req.IdleTimeoutSec != nil {
		l.IdleTimeoutSec = *req.IdleTimeoutSec
	}
	if req.TGBotToken != nil {
		token := strings.TrimSpace(*req.TGBotToken)
		if token != "" && s.Telegram != nil {
			username, err := s.Telegram.ValidateToken(r.Context(), token)
			if err != nil {
				s.jsonErr(w, 400, "telegram token rejected: %v", err)
				return
			}
			l.TGBotUsername = username
		}
		if token == "" {
			l.TGBotUsername = ""
			l.TGGroupChatID = 0
		}
		l.TGBotToken = token
	}
	l.UpdatedAt = time.Now().UnixMilli()
	if err := s.Store.Loops().Update(r.Context(), l); err != nil {
		s.jsonErr(w, 500, "%v", err)
		return
	}
	s.Manager.UpdateLoop(l)
	if s.Telegram != nil && req.TGBotToken != nil {
		s.Telegram.LoopChanged(l)
	}
	writeJSON(w, 200, s.view(r.Context(), l))
}

func (s *Server) handleDeleteLoop(w http.ResponseWriter, r *http.Request) {
	l := s.loopByName(w, r)
	if l == nil {
		return
	}
	s.Manager.Remove(l.ID, l.Runtime)
	if s.Telegram != nil {
		s.Telegram.LoopRemoved(l.ID)
	}
	if r.URL.Query().Get("remove_worktree") == "1" && l.WorkspaceMode == store.WorkspaceWorktree {
		if err := gitws.Remove(l.RepoPath, l.WorktreePath); err != nil {
			s.Log.Warn("worktree remove", "err", err)
		}
	}
	_ = s.Store.Sessions().End(r.Context(), l.CurrentSessionID, store.EndReasonKilled, time.Now().UnixMilli())
	if err := s.Store.Loops().Delete(r.Context(), l.ID); err != nil {
		s.jsonErr(w, 500, "%v", err)
		return
	}
	writeJSON(w, 200, map[string]bool{"deleted": true})
}

func (s *Server) handlePause(w http.ResponseWriter, r *http.Request) {
	l := s.loopByName(w, r)
	if l == nil {
		return
	}
	l.Status = store.StatusPaused
	l.UpdatedAt = time.Now().UnixMilli()
	_ = s.Store.Loops().Update(r.Context(), l)
	if actor, ok := s.Manager.Get(l.ID); ok {
		actor.Pause()
	}
	s.Sched.Suspend(l.ID)
	writeJSON(w, 200, s.view(r.Context(), l))
}

// handlePower runs one of the operator's power controls on a loop's
// workstation and answers with the loop as it stands afterwards — the call
// is synchronous, so the control room can render the result rather than
// wait for the stream to correct it (ADR-0021).
func (s *Server) handlePower(verb string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		l := s.loopByName(w, r)
		if l == nil {
			return
		}
		actor, ok := s.Manager.Get(l.ID)
		if !ok {
			s.jsonErrCode(w, http.StatusConflict, codeLoopNotRunning,
				"loop %s is not running", l.Name)
			return
		}
		switch err := actor.Power(verb); {
		case errors.Is(err, runtime.ErrUnsupported):
			s.jsonErrCode(w, http.StatusConflict, codeNoWorkstation,
				"loop %s runs on the %s runtime, which has no workstation to %s", l.Name, l.Runtime, verb)
			return
		case err != nil:
			s.jsonErr(w, http.StatusInternalServerError, "%s: %v", verb, err)
			return
		}
		if fresh, err := s.Store.Loops().Get(r.Context(), l.ID); err == nil {
			l = fresh
		}
		writeJSON(w, 200, s.view(r.Context(), l))
	}
}

func (s *Server) handleResume(w http.ResponseWriter, r *http.Request) {
	l := s.loopByName(w, r)
	if l == nil {
		return
	}
	l.Status = store.StatusActive
	l.UpdatedAt = time.Now().UnixMilli()
	_ = s.Store.Loops().Update(r.Context(), l)
	if actor, ok := s.Manager.Get(l.ID); ok {
		actor.Resume()
	}
	s.Sched.Resume(l.ID)
	writeJSON(w, 200, s.view(r.Context(), l))
}

func (s *Server) handleWake(w http.ResponseWriter, r *http.Request) {
	l := s.loopByName(w, r)
	if l == nil {
		return
	}
	s.Manager.Tick(l.ID)
	writeJSON(w, 200, map[string]bool{"woken": true})
}

func (s *Server) handleKill(w http.ResponseWriter, r *http.Request) {
	l := s.loopByName(w, r)
	if l == nil {
		return
	}
	if actor, ok := s.Manager.Get(l.ID); ok {
		actor.Kill()
	}
	writeJSON(w, 200, map[string]bool{"killed": true})
}

// handleRotate queues an operator-asked context rotation (ADR-0022): the
// loop writes its handoff note at the next quiet boundary and continues on
// a fresh session seeded from it.
func (s *Server) handleRotate(w http.ResponseWriter, r *http.Request) {
	l := s.loopByName(w, r)
	if l == nil {
		return
	}
	actor, ok := s.Manager.Get(l.ID)
	if !ok {
		s.jsonErr(w, 409, "loop has no running actor")
		return
	}
	if err := actor.Rotate(); err != nil {
		s.jsonErr(w, 409, "%v", err)
		return
	}
	writeJSON(w, 202, map[string]bool{"rotating": true})
}

type postMessageReq struct {
	Author string `json:"author"`
	Text   string `json:"text"`
	// Destination is the composer's declared destination (ADR-0026):
	// control_room (the default) keeps the message in the loop's private
	// web thread; group posts it to the shared group conversation.
	Destination string `json:"destination"`
}

func (s *Server) handleLoopMessage(w http.ResponseWriter, r *http.Request) {
	l := s.loopByName(w, r)
	if l == nil {
		return
	}
	var req postMessageReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Text) == "" {
		s.jsonErr(w, 400, "author and non-empty text required")
		return
	}
	dest := defaultStr(req.Destination, store.ConversationControlRoom)
	if dest != store.ConversationControlRoom && dest != store.ConversationGroup {
		s.jsonErr(w, 400, "destination must be %s or %s", store.ConversationControlRoom, store.ConversationGroup)
		return
	}
	err := s.Router.Ingest(r.Context(), route.InboundMessage{
		Origin:       store.OriginWeb,
		Author:       defaultStr(req.Author, "operator"),
		Text:         req.Text,
		ImplicitTo:   l.ID,
		Conversation: dest,
	})
	if err != nil {
		s.jsonErr(w, 500, "%v", err)
		return
	}
	writeJSON(w, 202, map[string]bool{"queued": true})
}

func (s *Server) handleLoopEvents(w http.ResponseWriter, r *http.Request) {
	l := s.loopByName(w, r)
	if l == nil {
		return
	}
	limit := queryInt(r, "limit", 200)
	// Which end of the timeline the page is reading from. after_id follows
	// the tail — the default, and what a page polling for new events wants.
	// before_id asks for the newest window instead, so a loop with thousands
	// of events can be opened on its recent history in one request rather
	// than on the first 200 events of its life (#119); 0 means "the newest
	// end", the mirror of after_id 0. Either way the page comes back oldest
	// first.
	query := r.URL.Query()
	var events []*store.Event
	var err error
	if query.Has("before_id") {
		beforeID, _ := strconv.ParseInt(query.Get("before_id"), 10, 64)
		events, err = s.Store.Events().ListByLoopBefore(r.Context(), l.ID, beforeID, limit)
	} else {
		afterID, _ := strconv.ParseInt(query.Get("after_id"), 10, 64)
		events, err = s.Store.Events().ListByLoop(r.Context(), l.ID, afterID, limit)
	}
	if err != nil {
		s.jsonErr(w, 500, "%v", err)
		return
	}
	if events == nil {
		events = []*store.Event{}
	}
	writeJSON(w, 200, events)
}

func (s *Server) handleLoopTurns(w http.ResponseWriter, r *http.Request) {
	l := s.loopByName(w, r)
	if l == nil {
		return
	}
	turns, err := s.Store.Turns().ListByLoop(r.Context(), l.ID, queryInt(r, "limit", 50))
	if err != nil {
		s.jsonErr(w, 500, "%v", err)
		return
	}
	if turns == nil {
		turns = []*store.Turn{}
	}
	writeJSON(w, 200, turns)
}

func (s *Server) handleTelegramStatus(w http.ResponseWriter, r *http.Request) {
	l := s.loopByName(w, r)
	if l == nil {
		return
	}
	status := map[string]any{
		"configured":   l.TGBotToken != "",
		"bot_username": l.TGBotUsername,
		"group_bound":  l.TGGroupChatID != 0,
	}
	if s.Telegram != nil {
		status["bridge"] = s.Telegram.Status(l.ID)
	}
	writeJSON(w, 200, status)
}

// --- operator settings (ADR-0017: stored server-side, presence-only on read) ---

// settingsView reports operator settings — secrets as presence only (the
// Claude setup-token is write-only, like a loop's bot token), plain values
// as themselves. The rotation thresholds are always the effective values,
// defaults included.
type settingsView struct {
	ClaudeTokenSet      bool `json:"claude_token_set"`
	ContextArmPercent   int  `json:"context_arm_percent"`
	ContextForcePercent int  `json:"context_force_percent"`
}

func (s *Server) settingsView(ctx context.Context) (settingsView, error) {
	token, err := s.claudeToken(ctx)
	if err != nil {
		return settingsView{}, err
	}
	arm, force := loop.RotationThresholds(ctx, s.Store.Settings(), s.Log)
	return settingsView{
		ClaudeTokenSet:      token != "",
		ContextArmPercent:   arm,
		ContextForcePercent: force,
	}, nil
}

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	view, err := s.settingsView(r.Context())
	if err != nil {
		s.jsonErr(w, 500, "%v", err)
		return
	}
	writeJSON(w, 200, view)
}

type putSettingsReq struct {
	// nil leaves the token unchanged; "" clears it; otherwise it is validated
	// and stored.
	ClaudeOAuthToken *string `json:"claude_oauth_token"`
	// nil leaves a threshold unchanged; the pair is validated together
	// (percent of the model's window, 1–99, arm below force).
	ContextArmPercent   *int `json:"context_arm_percent"`
	ContextForcePercent *int `json:"context_force_percent"`
}

func (s *Server) handlePutSettings(w http.ResponseWriter, r *http.Request) {
	var req putSettingsReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.jsonErr(w, 400, "bad json: %v", err)
		return
	}
	if req.ClaudeOAuthToken != nil {
		token := ""
		if strings.TrimSpace(*req.ClaudeOAuthToken) != "" {
			validated, err := validateClaudeToken(*req.ClaudeOAuthToken)
			if err != nil {
				s.jsonErr(w, 400, "%v", err)
				return
			}
			token = validated
		}
		if err := s.Store.Settings().Set(r.Context(), store.SettingClaudeOAuthToken, token); err != nil {
			s.jsonErr(w, 500, "%v", err)
			return
		}
	}
	if req.ContextArmPercent != nil || req.ContextForcePercent != nil {
		s.settingsMu.Lock()
		defer s.settingsMu.Unlock()
		arm, force := loop.RotationThresholds(r.Context(), s.Store.Settings(), s.Log)
		if req.ContextArmPercent != nil {
			arm = *req.ContextArmPercent
		}
		if req.ContextForcePercent != nil {
			force = *req.ContextForcePercent
		}
		if !loop.ValidThresholds(arm, force) {
			s.jsonErr(w, 400, "rotation thresholds must be percentages 1-99 with arm below force (got arm %d, force %d)", arm, force)
			return
		}
		if err := s.Store.Settings().Set(r.Context(), store.SettingContextArmPercent, strconv.Itoa(arm)); err != nil {
			s.jsonErr(w, 500, "%v", err)
			return
		}
		if err := s.Store.Settings().Set(r.Context(), store.SettingContextForcePercent, strconv.Itoa(force)); err != nil {
			s.jsonErr(w, 500, "%v", err)
			return
		}
	}
	view, err := s.settingsView(r.Context())
	if err != nil {
		s.jsonErr(w, 500, "%v", err)
		return
	}
	writeJSON(w, 200, view)
}

// claudeToken reads the stored setup-token, mapping an unset key to empty so
// callers can treat "never configured" and "" alike.
func (s *Server) claudeToken(ctx context.Context) (string, error) {
	token, err := s.Store.Settings().Get(ctx, store.SettingClaudeOAuthToken)
	if errors.Is(err, store.ErrNotFound) {
		return "", nil
	}
	return token, err
}

// claudeTokenPrefix is the observed shape of `claude setup-token` output
// (sk-ant-oat01-…); the sk-ant- namespace also covers API-key tokens, so the
// check stays lenient. This is the line most likely to need updating if the
// CLI changes its token format.
const claudeTokenPrefix = "sk-ant-"

// validateClaudeToken rejects obvious paste mistakes without a network call: a
// setup-token is one non-empty run of characters, of plausible length, in the
// sk-ant- namespace. It never proves the token authenticates — a well-formed
// but wrong token surfaces later as the loop's workstation going down.
func validateClaudeToken(raw string) (string, error) {
	token := strings.TrimSpace(raw)
	switch {
	case token == "":
		return "", fmt.Errorf("token is empty")
	case strings.ContainsFunc(token, unicode.IsSpace):
		return "", fmt.Errorf("token contains whitespace — check for a truncated or multi-line paste")
	case len(token) < 20:
		return "", fmt.Errorf("token is too short to be a setup-token")
	case !strings.HasPrefix(token, claudeTokenPrefix):
		return "", fmt.Errorf("token doesn't look like a setup-token (expected an %s… value from `claude setup-token`)", claudeTokenPrefix)
	}
	return token, nil
}

// handleLoopConversation serves one of the loop's private conversation
// threads: control_room (the default) or owner_dm. The shared group has no
// per-loop thread — it lives on /api/activity.
func (s *Server) handleLoopConversation(w http.ResponseWriter, r *http.Request) {
	l := s.loopByName(w, r)
	if l == nil {
		return
	}
	kind := defaultStr(r.URL.Query().Get("conversation"), store.ConversationControlRoom)
	if kind != store.ConversationControlRoom && kind != store.ConversationOwnerDM {
		s.jsonErr(w, 400, "conversation must be %s or %s", store.ConversationControlRoom, store.ConversationOwnerDM)
		return
	}
	msgs, err := s.Store.Messages().ListConversation(r.Context(), kind, l.ID, queryInt(r, "limit", 100))
	if err != nil {
		s.jsonErr(w, 500, "%v", err)
		return
	}
	if msgs == nil {
		msgs = []*store.Message{}
	}
	writeJSON(w, 200, msgs)
}

func (s *Server) handleActivity(w http.ResponseWriter, r *http.Request) {
	msgs, err := s.Store.Messages().List(r.Context(), queryInt(r, "limit", 100))
	if err != nil {
		s.jsonErr(w, 500, "%v", err)
		return
	}
	if msgs == nil {
		msgs = []*store.Message{}
	}
	writeJSON(w, 200, msgs)
}

func (s *Server) handleListSenders(w http.ResponseWriter, r *http.Request) {
	senders, err := s.Store.TGSenders().List(r.Context())
	if err != nil {
		s.jsonErr(w, 500, "%v", err)
		return
	}
	if senders == nil {
		senders = []*store.TGSender{}
	}
	writeJSON(w, 200, senders)
}

// --- per-loop secrets ---

// secretNameRe constrains a secret's name to a POSIX env identifier — what
// both `docker --env KEY` and the shell require.
var secretNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

const (
	maxSecretNameLen  = 128
	maxSecretValueLen = 16 * 1024
	maxSecretsPerLoop = 64
)

// secretView reports a loop secret as name + timestamp only. The value is
// write-only, the same rule the operator's setup-token and a bot token follow.
type secretView struct {
	Name      string `json:"name"`
	UpdatedAt int64  `json:"updated_at"`
}

func (s *Server) secretViews(ctx context.Context, loopID string) ([]secretView, error) {
	secrets, err := s.Store.LoopSecrets().List(ctx, loopID)
	if err != nil {
		return nil, err
	}
	views := make([]secretView, 0, len(secrets))
	for _, sc := range secrets {
		views = append(views, secretView{Name: sc.Name, UpdatedAt: sc.UpdatedAt})
	}
	return views, nil
}

func (s *Server) handleListSecrets(w http.ResponseWriter, r *http.Request) {
	l := s.loopByName(w, r)
	if l == nil {
		return
	}
	views, err := s.secretViews(r.Context(), l.ID)
	if err != nil {
		s.jsonErr(w, 500, "%v", err)
		return
	}
	writeJSON(w, 200, views)
}

type putSecretReq struct {
	Value string `json:"value"`
}

func (s *Server) handlePutSecret(w http.ResponseWriter, r *http.Request) {
	l := s.loopByName(w, r)
	if l == nil {
		return
	}
	name := r.PathValue("key")
	if err := validateSecretName(name); err != nil {
		s.jsonErr(w, 400, "%v", err)
		return
	}
	var req putSecretReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.jsonErr(w, 400, "bad json: %v", err)
		return
	}
	switch {
	case req.Value == "":
		s.jsonErr(w, 400, "secret value is empty (use DELETE to remove a secret)")
		return
	case len(req.Value) > maxSecretValueLen:
		s.jsonErr(w, 400, "secret value too large (max %d bytes)", maxSecretValueLen)
		return
	}
	existing, err := s.Store.LoopSecrets().List(r.Context(), l.ID)
	if err != nil {
		s.jsonErr(w, 500, "%v", err)
		return
	}
	// The cap bounds distinct names; replacing an existing one never grows it.
	if len(existing) >= maxSecretsPerLoop && !hasSecret(existing, name) {
		s.jsonErr(w, 400, "too many secrets on this loop (max %d)", maxSecretsPerLoop)
		return
	}
	if err := s.Store.LoopSecrets().Set(r.Context(), l.ID, name, req.Value, time.Now().UnixMilli()); err != nil {
		s.jsonErr(w, 500, "%v", err)
		return
	}
	views, err := s.secretViews(r.Context(), l.ID)
	if err != nil {
		s.jsonErr(w, 500, "%v", err)
		return
	}
	writeJSON(w, 200, views)
}

func (s *Server) handleDeleteSecret(w http.ResponseWriter, r *http.Request) {
	l := s.loopByName(w, r)
	if l == nil {
		return
	}
	// Delete is idempotent: removing an absent name still reports success.
	if err := s.Store.LoopSecrets().Delete(r.Context(), l.ID, r.PathValue("key")); err != nil {
		s.jsonErr(w, 500, "%v", err)
		return
	}
	writeJSON(w, 200, map[string]bool{"deleted": true})
}

func validateSecretName(name string) error {
	switch {
	case name == "":
		return fmt.Errorf("secret name is empty")
	case len(name) > maxSecretNameLen:
		return fmt.Errorf("secret name too long (max %d chars)", maxSecretNameLen)
	case !secretNameRe.MatchString(name):
		return fmt.Errorf("secret name must be a valid env var identifier ([A-Za-z_][A-Za-z0-9_]*)")
	}
	return nil
}

func hasSecret(secrets []*store.LoopSecret, name string) bool {
	for _, sc := range secrets {
		if sc.Name == name {
			return true
		}
	}
	return false
}

func (s *Server) handleSenderStatus(status string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			s.jsonErr(w, 400, "bad sender id")
			return
		}
		if err := s.Store.TGSenders().SetStatus(r.Context(), id, status, time.Now().UnixMilli()); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				s.jsonErr(w, 404, "sender not found")
			} else {
				s.jsonErr(w, 500, "%v", err)
			}
			return
		}
		sender, _ := s.Store.TGSenders().Get(r.Context(), id)
		if status == store.SenderAllowed {
			s.adoptDefaultOwner(r.Context(), id)
		} else {
			s.disownLoopsOf(r.Context(), id)
		}
		s.Bus.Publish(bus.Item{Kind: bus.KindAccess, Payload: sender})
		writeJSON(w, 200, sender)
	}
}

// defaultOwnerID is the first person the operator allowlisted, or 0 when
// nobody is.
func (s *Server) defaultOwnerID(ctx context.Context) int64 {
	senders, err := s.Store.TGSenders().List(ctx)
	if err != nil {
		return 0
	}
	var owner *store.TGSender
	for _, sender := range senders {
		if sender.Status != store.SenderAllowed {
			continue
		}
		if owner == nil || sender.CreatedAt < owner.CreatedAt {
			owner = sender
		}
	}
	if owner == nil {
		return 0
	}
	return owner.TGUserID
}

// adoptDefaultOwner gives every ownerless loop this sender as its owner.
// The first person the operator allowlists is the owner by default — the
// fleet is built for one operator — and they can reassign per loop.
func (s *Server) adoptDefaultOwner(ctx context.Context, tgUserID int64) {
	loops, err := s.Store.Loops().List(ctx)
	if err != nil {
		return
	}
	for _, l := range loops {
		if l.OwnerTGUserID != 0 || l.Status == store.StatusArchived {
			continue
		}
		l.OwnerTGUserID = tgUserID
		l.UpdatedAt = time.Now().UnixMilli()
		if err := s.Store.Loops().Update(ctx, l); err != nil {
			s.Log.Error("default owner", "loop", l.Name, "err", err)
		}
	}
}

// disownLoopsOf drops the ownership of a sender who may no longer reach the
// fleet. An owner is an allowed sender — checked when one is set — and that
// invariant has to survive the reverse transitions too: otherwise blocking
// or deleting someone leaves the loops they own still messaging them
// privately, with the control room reporting those loops as ready to do it.
func (s *Server) disownLoopsOf(ctx context.Context, tgUserID int64) {
	loops, err := s.Store.Loops().List(ctx)
	if err != nil {
		return
	}
	for _, l := range loops {
		if l.OwnerTGUserID != tgUserID {
			continue
		}
		l.OwnerTGUserID, l.OwnerDMChatID = 0, 0
		l.UpdatedAt = time.Now().UnixMilli()
		if err := s.Store.Loops().Update(ctx, l); err != nil {
			s.Log.Error("disown loop", "loop", l.Name, "err", err)
		}
	}
}

type putOwnerReq struct {
	TGUserID int64 `json:"tg_user_id"`
}

// handlePutOwner sets which person a loop may message privately. Only an
// allowlisted sender qualifies: being able to reach a bot is not the same as
// owning the loop it belongs to (#73). Changing the owner drops the captured
// chat — it belonged to the previous one — and the new owner's first DM to
// this bot captures theirs.
func (s *Server) handlePutOwner(w http.ResponseWriter, r *http.Request) {
	l := s.loopByName(w, r)
	if l == nil {
		return
	}
	var req putOwnerReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.jsonErr(w, 400, "bad json")
		return
	}
	sender, err := s.Store.TGSenders().Get(r.Context(), req.TGUserID)
	if errors.Is(err, store.ErrNotFound) || (err == nil && sender.Status != store.SenderAllowed) {
		s.jsonErr(w, 400, "owner must be an allowed telegram sender")
		return
	} else if err != nil {
		s.jsonErr(w, 500, "%v", err)
		return
	}
	if l.OwnerTGUserID != req.TGUserID {
		l.OwnerTGUserID = req.TGUserID
		l.OwnerDMChatID = 0
		l.UpdatedAt = time.Now().UnixMilli()
		if err := s.Store.Loops().Update(r.Context(), l); err != nil {
			s.jsonErr(w, 500, "%v", err)
			return
		}
	}
	writeJSON(w, 200, s.view(r.Context(), l))
}

func (s *Server) handleDeleteSender(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.jsonErr(w, 400, "bad sender id")
		return
	}
	if err := s.Store.TGSenders().Delete(r.Context(), id); err != nil {
		s.jsonErr(w, 500, "%v", err)
		return
	}
	s.disownLoopsOf(r.Context(), id)
	s.Bus.Publish(bus.Item{Kind: bus.KindAccess, Payload: map[string]any{"deleted": id}})
	writeJSON(w, 200, map[string]bool{"deleted": true})
}

type inspectReq struct {
	Path string `json:"path"`
}

func (s *Server) handleWorkspaceInspect(w http.ResponseWriter, r *http.Request) {
	var req inspectReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.jsonErr(w, 400, "bad json")
		return
	}
	abs, err := filepath.Abs(strings.TrimSpace(req.Path))
	if err != nil {
		s.jsonErr(w, 400, "%v", err)
		return
	}
	info, statErr := os.Stat(abs)
	exists := statErr == nil && info.IsDir()
	writeJSON(w, 200, map[string]any{
		"path":   abs,
		"exists": exists,
		"is_git": exists && gitws.IsGitRepo(abs),
	})
}

func (s *Server) handleGlobalStream(w http.ResponseWriter, r *http.Request) {
	serveSSE(w, r, s.Bus, func(i bus.Item) bool {
		return i.Kind != bus.KindAgentEvent
	})
}

func (s *Server) handleLoopStream(w http.ResponseWriter, r *http.Request) {
	l := s.loopByName(w, r)
	if l == nil {
		return
	}
	id := l.ID
	serveSSE(w, r, s.Bus, func(i bus.Item) bool {
		if i.Kind == bus.KindMessage {
			if mp, ok := i.Payload.(*route.MessagePayload); ok {
				if mp.FromLoopID == id {
					return true
				}
				for _, d := range mp.DeliveredTo {
					if d == id {
						return true
					}
				}
			}
			return false
		}
		return i.LoopID == id
	})
}

// handleUI serves the embedded SPA with an index.html fallback.
func (s *Server) handleUI(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/")
	if path == "" {
		path = "index.html"
	}
	if f, err := s.WebFS.Open(path); err == nil {
		f.Close()
		http.ServeFileFS(w, r, s.WebFS, path)
		return
	}
	http.ServeFileFS(w, r, s.WebFS, "index.html")
}

// --- small utils ---

func defaultInt(v, d int) int {
	if v <= 0 {
		return d
	}
	return v
}

func defaultStr(v, d string) string {
	if strings.TrimSpace(v) == "" {
		return d
	}
	return v
}

func queryInt(r *http.Request, key string, d int) int {
	if v, err := strconv.Atoi(r.URL.Query().Get(key)); err == nil && v > 0 {
		return v
	}
	return d
}

// Ids are creation-ordered: base36 nanoseconds behind a type prefix.
func loopID() string { return mintID("loop_") }
func ruleID() string { return mintID("rule_") }

func mintID(prefix string) string {
	return prefix + strconv.FormatInt(time.Now().UnixNano(), 36)
}
