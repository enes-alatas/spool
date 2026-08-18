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
	"time"

	"github.com/enes-alatas/spool/internal/bus"
	"github.com/enes-alatas/spool/internal/gitws"
	"github.com/enes-alatas/spool/internal/loop"
	"github.com/enes-alatas/spool/internal/route"
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
	Log       *slog.Logger
	WebFS     fs.FS // embedded UI dist; may be nil in dev
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
	mux.HandleFunc("POST /api/loops/{name}/message", s.handleLoopMessage)
	mux.HandleFunc("POST /api/messages", s.handleBroadcastMessage)
	mux.HandleFunc("GET /api/loops/{name}/events", s.handleLoopEvents)
	mux.HandleFunc("GET /api/loops/{name}/turns", s.handleLoopTurns)
	mux.HandleFunc("GET /api/loops/{name}/telegram/status", s.handleTelegramStatus)
	mux.HandleFunc("GET /api/activity", s.handleActivity)
	mux.HandleFunc("GET /api/telegram/senders", s.handleListSenders)
	mux.HandleFunc("POST /api/telegram/senders/{id}/allow", s.handleSenderStatus(store.SenderAllowed))
	mux.HandleFunc("POST /api/telegram/senders/{id}/block", s.handleSenderStatus(store.SenderBlocked))
	mux.HandleFunc("DELETE /api/telegram/senders/{id}", s.handleDeleteSender)
	mux.HandleFunc("POST /api/workspace/inspect", s.handleWorkspaceInspect)
	mux.HandleFunc("GET /api/stream", s.handleGlobalStream)
	mux.HandleFunc("GET /api/loops/{name}/stream", s.handleLoopStream)

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
	State      string  `json:"state"`
	NextTickAt int64   `json:"next_tick_at"`
	CostToday  float64 `json:"cost_today_usd"`
	HasTGToken bool    `json:"has_tg_token"`
}

func (s *Server) view(ctx context.Context, l *store.Loop) *loopView {
	v := &loopView{Loop: l, State: loop.StateAsleep, HasTGToken: l.TGBotToken != ""}
	if actor, ok := s.Manager.Get(l.ID); ok {
		v.State = actor.State()
	}
	if e, err := s.Store.Schedule().Get(ctx, l.ID); err == nil {
		v.NextTickAt = e.NextTickAt
	}
	dayStart := time.Now().Truncate(24 * time.Hour).UnixMilli()
	if c, err := s.Store.Turns().CostSince(ctx, l.ID, dayStart); err == nil {
		v.CostToday = c
	}
	return v
}

// --- handlers ---

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"ok": true, "claude_version": s.ClaudeVer})
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
	Name            string `json:"name"`
	Mission         string `json:"mission"`
	Model           string `json:"model"`
	Effort          string `json:"effort"`         // ""|low|medium|high|xhigh|max
	Pacing          string `json:"pacing"`         // ""(=fixed)|fixed|self
	WorkspacePath   string `json:"workspace_path"` // empty = no workspace
	WorkspaceMode   string `json:"workspace_mode"` // "auto" (default) | "dir" | "none"
	TickIntervalSec int    `json:"tick_interval_sec"`
	MinWakeSec      int    `json:"min_wake_sec"`
	MaxWakeSec      int    `json:"max_wake_sec"`
	IdleTimeoutSec  int    `json:"idle_timeout_sec"`
	TGBotToken      string `json:"tg_bot_token"`
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
		Status:          store.StatusActive,
		CreatedAt:       nowMS,
		UpdatedAt:       nowMS,
	}

	if l.TGBotToken != "" && s.Telegram != nil {
		username, err := s.Telegram.ValidateToken(r.Context(), l.TGBotToken)
		if err != nil {
			s.jsonErr(w, 400, "telegram token rejected: %v", err)
			return
		}
		l.TGBotUsername = username
	}

	if err := s.resolveWorkspace(l, req.WorkspacePath, req.WorkspaceMode); err != nil {
		s.jsonErr(w, 400, "%v", err)
		return
	}

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
	s.Manager.Remove(l.ID)
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

type postMessageReq struct {
	Author string `json:"author"`
	Text   string `json:"text"`
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
	err := s.Router.Ingest(r.Context(), route.InboundMessage{
		Origin:     store.OriginWeb,
		Author:     defaultStr(req.Author, "operator"),
		Text:       req.Text,
		ImplicitTo: l.ID,
	})
	if err != nil {
		s.jsonErr(w, 500, "%v", err)
		return
	}
	writeJSON(w, 202, map[string]bool{"queued": true})
}

func (s *Server) handleBroadcastMessage(w http.ResponseWriter, r *http.Request) {
	var req postMessageReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Text) == "" {
		s.jsonErr(w, 400, "author and non-empty text required")
		return
	}
	err := s.Router.Ingest(r.Context(), route.InboundMessage{
		Origin: store.OriginWeb,
		Author: defaultStr(req.Author, "operator"),
		Text:   req.Text,
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
	afterID, _ := strconv.ParseInt(r.URL.Query().Get("after_id"), 10, 64)
	limit := queryInt(r, "limit", 200)
	events, err := s.Store.Events().ListByLoop(r.Context(), l.ID, afterID, limit)
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
		s.Bus.Publish(bus.Item{Kind: bus.KindAccess, Payload: sender})
		writeJSON(w, 200, sender)
	}
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

func loopID() string {
	return "loop_" + strconv.FormatInt(time.Now().UnixNano(), 36)
}
