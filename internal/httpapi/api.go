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
	"github.com/enes-alatas/spool/internal/surface"
	"github.com/enes-alatas/spool/internal/version"
)

var nameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{1,31}$`)

type Server struct {
	Store   store.Store
	Bus     *bus.Bus
	Manager *loop.Manager
	Router  *route.Router
	Sched   *sched.Scheduler
	// Models is the model list the dropdowns offer, with what each name
	// runs as on this hub (ADR-0033).
	Models *loop.Models
	// Surface is the chat platform loops are reachable on (ADR-0029); nil
	// when the hub runs without one.
	Surface   surface.Surface
	DataDir   string
	ClaudeVer string
	// Build is which Spool this is, served verbatim by /api/version so a bug
	// report and the control room name the same build.
	Build version.Info
	// DefaultRuntime is the kind loops get when a create request doesn't
	// name one (ADR-0017: docker whenever the daemon is reachable; `auto`
	// refuses to start rather than choosing bare for the operator).
	DefaultRuntime string
	// BareAllowed is whether this hub was started with --runtime bare or
	// --allow-bare — the second is how a docker-default fleet keeps the
	// per-loop bare escape hatch ADR-0017 §6 describes. A
	// bare loop runs uncontained under the operator's own account, so asking
	// for one is a decision taken when the hub is started, by the person at
	// the terminal — not one the control room can make later on their behalf
	// (ADR-0017, #240). Loops created before it was set keep running: this
	// gates creation, not execution.
	BareAllowed bool
	// RuntimeAvailable answers whether a runtime kind can host a new loop
	// right now; wired in cmd so this package stays free of runtime imports.
	RuntimeAvailable func(ctx context.Context, kind string) error
	// SecretsChanged is called after a write that adds, replaces or removes
	// a secret value, so the redactor reloads at once instead of
	// serving its ttl out with a value it has never seen (#150). Wired in
	// cmd; nil in tests that do not care.
	SecretsChanged func(ctx context.Context)
	// OperatorToken is the credential a request must carry to be the
	// operator's (ADR-0030). Empty is not an open API but a closed one:
	// every guarded route answers 401, because a hub with no token must not
	// admit a caller who also presents nothing. cmd always mints one.
	OperatorToken string
	// ListenAddr is the address the API is served on, which is what a
	// legitimate Host header and Origin name. Empty accepts the loopback
	// spellings only.
	ListenAddr string
	// TrustedHosts are additional Host and Origin values this hub answers
	// to, for the operator who put it behind a proxy: that proxy's name is
	// one the hub cannot guess and must not accept blindly. Each entry is a
	// host, optionally with a port.
	TrustedHosts []string
	Log          *slog.Logger
	WebFS        fs.FS // embedded UI dist; may be nil in dev

	// settingsMu serializes the read-validate-write of paired settings, so
	// two concurrent PUTs cannot interleave into an inverted stored pair.
	settingsMu sync.Mutex
	// rulesMu serializes fleet-rule writes: the section cap is checked
	// against the whole enabled set, so two writes must not interleave
	// between the check and the store.
	rulesMu sync.Mutex
}

func (server *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/health", server.handleHealth)
	mux.HandleFunc("GET /api/version", server.handleVersion)
	mux.HandleFunc("GET /api/loops", server.handleListLoops)
	mux.HandleFunc("POST /api/loops", server.handleCreateLoop)
	mux.HandleFunc("GET /api/loops/{name}", server.handleGetLoop)
	mux.HandleFunc("PATCH /api/loops/{name}", server.handlePatchLoop)
	mux.HandleFunc("DELETE /api/loops/{name}", server.handleDeleteLoop)
	mux.HandleFunc("POST /api/loops/{name}/pause", server.handlePause)
	mux.HandleFunc("POST /api/loops/{name}/resume", server.handleResume)
	mux.HandleFunc("POST /api/loops/{name}/wake", server.handleWake)
	mux.HandleFunc("POST /api/loops/{name}/kill", server.handleKill)
	mux.HandleFunc("POST /api/loops/{name}/rotate", server.handleRotate)
	mux.HandleFunc("POST /api/loops/{name}/workstation/restart", server.handlePower(loop.PowerRestart))
	mux.HandleFunc("POST /api/loops/{name}/workstation/poweroff", server.handlePower(loop.PowerOff))
	mux.HandleFunc("POST /api/loops/{name}/workstation/poweron", server.handlePower(loop.PowerOn))
	mux.HandleFunc("POST /api/loops/{name}/workstation/recreate", server.handlePower(loop.PowerRecreate))
	mux.HandleFunc("POST /api/loops/{name}/message", server.handleLoopMessage)
	mux.HandleFunc("GET /api/loops/{name}/events", server.handleLoopEvents)
	mux.HandleFunc("GET /api/loops/{name}/turns", server.handleLoopTurns)
	mux.HandleFunc("GET /api/loops/{name}/telegram/status", server.handleTelegramStatus)
	mux.HandleFunc("PUT /api/loops/{name}/owner", server.handlePutOwner)
	mux.HandleFunc("GET /api/loops/{name}/secrets", server.handleListSecrets)
	mux.HandleFunc("PUT /api/loops/{name}/secrets/{key}", server.handlePutSecret)
	mux.HandleFunc("DELETE /api/loops/{name}/secrets/{key}", server.handleDeleteSecret)
	mux.HandleFunc("GET /api/activity", server.handleActivity)
	mux.HandleFunc("GET /api/undelivered", server.handleUndelivered)
	mux.HandleFunc("POST /api/messages/{id}/retry", server.handleRetrySend)
	mux.HandleFunc("POST /api/messages/{id}/dismiss", server.handleDismissSend)
	mux.HandleFunc("GET /api/loops/{name}/conversation", server.handleLoopConversation)
	mux.HandleFunc("GET /api/group", server.handleGroupTimeline)
	mux.HandleFunc("POST /api/group", server.handleGroupPost)
	mux.HandleFunc("GET /api/settings", server.handleGetSettings)
	mux.HandleFunc("PUT /api/settings", server.handlePutSettings)
	mux.HandleFunc("GET /api/rules", server.handleListRules)
	mux.HandleFunc("POST /api/rules", server.handleCreateRule)
	mux.HandleFunc("PATCH /api/rules/{id}", server.handlePatchRule)
	mux.HandleFunc("DELETE /api/rules/{id}", server.handleDeleteRule)
	mux.HandleFunc("GET /api/models", server.handleListModels)
	mux.HandleFunc("POST /api/models/custom", server.handleAddCustomModel)
	mux.HandleFunc("PATCH /api/models/custom/{id}", server.handlePatchCustomModel)
	mux.HandleFunc("DELETE /api/models/custom/{id}", server.handleDeleteCustomModel)
	mux.HandleFunc("GET /api/telegram/senders", server.handleListSenders)
	mux.HandleFunc("POST /api/telegram/senders/{id}/allow", server.handleSenderStatus(store.SenderAllowed))
	mux.HandleFunc("POST /api/telegram/senders/{id}/block", server.handleSenderStatus(store.SenderBlocked))
	mux.HandleFunc("DELETE /api/telegram/senders/{id}", server.handleDeleteSender)
	mux.HandleFunc("POST /api/workspace/inspect", server.handleWorkspaceInspect)
	mux.HandleFunc("GET /api/stream", server.handleGlobalStream)
	mux.HandleFunc("GET /api/loops/{name}/stream", server.handleLoopStream)
	// The two routes that establish and end a session rather than use one;
	// the guard lets them past the credential check and nothing else.
	mux.HandleFunc("POST /api/login", server.handleLogin)
	mux.HandleFunc("POST /api/logout", server.handleLogout)

	// /mcp lives on the loop listener alone (#238). Saying so explicitly
	// matters because of what is registered next: the UI's catch-all would
	// otherwise answer this path with index.html in a build that embeds the
	// control room, so the endpoint would look moved in tests and alive in
	// production.
	mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})

	// Registered in every build, not just one that embeds the control
	// room, so tier 2 sees what the shipped binary answers (#245).
	mux.HandleFunc("/api/", server.apiFallback(mux))
	if server.WebFS != nil {
		mux.HandleFunc("/", server.handleUI)
	}
	// Every /api route is behind the guard, including the ones registered
	// above: a route that is added later and forgets to authenticate is the
	// failure this shape makes impossible (#239).
	return server.guard(mux)
}

// MCPHandler is the loop-facing half of the hub, served on its own listener
// (#238). Workstations are allowed to reach this port and no other, so the
// operator's API and control room are unreachable from inside a workstation
// at the network layer, before any question of authentication. Nothing but
// the MCP endpoint is routed here: every other path is 404 and never reaches
// the API mux.
func (server *Server) MCPHandler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/mcp", server.mcpHandler())
	return mux
}

// --- helpers ---

func (server *Server) jsonErr(w http.ResponseWriter, code int, msg string, args ...any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf(msg, args...)})
}

// storeErr answers a write the store refused. store.ErrNotFound is 404: the
// subject was there when the handler looked it up and gone by the write, and
// a caller who asked to change something that no longer exists broke
// nothing. Anything else is the store failing, 500 (#180).
func (server *Server) storeErr(w http.ResponseWriter, err error, subject string) {
	if errors.Is(err, store.ErrNotFound) {
		server.jsonErr(w, http.StatusNotFound, "%s not found", subject)
		return
	}
	server.jsonErr(w, http.StatusInternalServerError, "%v", err)
}

// jsonErrCode is jsonErr plus a stable machine-readable reason, for the
// cases where one status covers outcomes a client must tell apart. The
// prose stays the human's, the code is the client's.
func (server *Server) jsonErrCode(w http.ResponseWriter, status int, reason, msg string, args ...any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf(msg, args...), "code": reason})
}

// Reasons behind an otherwise ambiguous status, sent as "code".
const (
	codeLoopNotRunning = "loop_not_running"
	codeNoWorkstation  = "no_workstation"
	codeBareNotEnabled = "bare_runtime_not_enabled"
)

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(body)
}

func (server *Server) loopByName(w http.ResponseWriter, r *http.Request) *store.Loop {
	loopRecord, err := server.Store.Loops().GetByName(r.Context(), r.PathValue("name"))
	if errors.Is(err, store.ErrNotFound) {
		server.jsonErr(w, http.StatusNotFound, "loop %q not found", r.PathValue("name"))
		return nil
	}
	if err != nil {
		server.jsonErr(w, http.StatusInternalServerError, "%v", err)
		return nil
	}
	return loopRecord
}

// loopView is a loop plus live runtime info for the UI.
type loopView struct {
	*store.Loop
	State      string  `json:"state"`
	NextTickAt int64   `json:"next_tick_at"`
	CostToday  float64 `json:"cost_today_usd"`
	// CostDay names the calendar day CostToday sums, as YYYY-MM-DD in the
	// server's own zone. A client that shows the number should not have to
	// work out which day it belongs to, and the boundary is not the one a
	// UTC client would guess.
	CostDay string `json:"cost_day"`
	// Undelivered counts this loop's messages that never reached their
	// surface and that nobody has dealt with: no successful retry, no
	// dismissal (#269). No age limit — a failure stops counting when
	// someone resolves it, not when a clock decides the operator is done
	// looking. Always present, so a measured zero is distinguishable from a
	// server too old to measure — the lesson of context_fill_pct (#122).
	Undelivered       int    `json:"undelivered"`
	HasTGToken        bool   `json:"has_tg_token"`
	WorkstationUp     bool   `json:"workstation_up"`
	WorkstationDetail string `json:"workstation_detail,omitempty"`
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
	// ResolvedModel is the model id the CLI reported for the loop's latest
	// turn — what an alias like "opus" resolved to, or the configured id
	// itself — and "" before any turn has run (#289). The configured model
	// is Loop.Model; a refused one says so in Loop.ModelRefusal.
	ResolvedModel string `json:"resolved_model"`
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
	// InFleetChannel reports that the loop has a group: it receives what
	// addresses it there and may post to it (ADR-0032 item 2).
	InFleetChannel bool `json:"in_fleet_channel"`
}

// localDayStart reports the first instant of now's calendar day, in unix
// millis, and the day it names.
//
// "Today" is the operator's day. The previous `Truncate(24 * time.Hour)`
// rounded the instant, which lands on a UTC boundary whatever the machine's
// zone: in Berlin the fleet's spend reset at 01:00 or 02:00 local, so an
// evening session's cost appeared under tomorrow while the operator was still
// reading today. Truncate cannot express a calendar day at all — it knows
// durations, not dates — so the boundary is built from now's own date in now's
// own location. That is also what makes it survive a DST change, where the
// local day is 23 or 25 hours long and no fixed duration is the day.
func localDayStart(now time.Time) (int64, string) {
	year, month, day := now.Date()
	start := time.Date(year, month, day, 0, 0, 0, 0, now.Location())
	return start.UnixMilli(), start.Format("2006-01-02")
}

func (server *Server) view(ctx context.Context, loopRecord *store.Loop) *loopView {
	out := &loopView{Loop: loopRecord, State: loop.StateAsleep, HasTGToken: loopRecord.TGBotToken != "", WorkstationUp: true,
		OwnerDMReady: loopRecord.OwnerTGUserID != 0 && loopRecord.OwnerDMChatID != 0, InFleetChannel: !loopRecord.OutsideFleetChannel}
	if loopRecord.OwnerTGUserID != 0 {
		if sender, err := server.Store.TGSenders().Get(ctx, loopRecord.OwnerTGUserID); err == nil {
			out.OwnerUsername = sender.Username
		}
	}
	if actor, ok := server.Manager.Get(loopRecord.ID); ok {
		out.State = actor.State()
		health := actor.WorkstationHealth()
		out.WorkstationUp = health.Up
		out.WorkstationDetail = health.Detail
		out.DownReason = actor.DownReason()
	}
	if latest, err := server.Store.Turns().Latest(ctx, loopRecord.ID); err == nil {
		// what the configured model resolved to, whichever session ran it:
		// an alias names a family, and this is the release it meant then
		out.ResolvedModel = latest.Model
		if latest.SessionID == loopRecord.CurrentSessionID {
			out.ContextTokens = latest.ContextTokens
			out.ContextLimitTokens = loop.ContextLimit(latest.Model)
			out.ContextFillPct = loop.FillPercent(out.ContextTokens, out.ContextLimitTokens)
		}
	}
	if entry, err := server.Store.Schedule().Get(ctx, loopRecord.ID); err == nil {
		out.NextTickAt = entry.NextTickAt
	}
	dayStart, day := localDayStart(time.Now())
	out.CostDay = day
	if cost, err := server.Store.Turns().CostSince(ctx, loopRecord.ID, dayStart); err == nil {
		out.CostToday = cost
	}
	if failures, err := server.Store.Messages().UnresolvedSendFailures(ctx, loopRecord.ID); err == nil {
		out.Undelivered = failures
	} else {
		// The zero this leaves behind is the one thing this field must not
		// say quietly, so it is said loudly somewhere.
		server.Log.Error("undelivered count", "loop", loopRecord.Name, "err", err)
	}
	return out
}

// --- handlers ---

// handleVersion names the build. Unauthenticated like health: everything
// here is already in the binary anyone asking can run, and an operator
// filing a bug should not have to find a token to say which Spool it was.
func (server *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, server.Build)
}

func (server *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"ok": true, "claude_version": server.ClaudeVer, "runtime": server.DefaultRuntime})
}

func (server *Server) handleListLoops(w http.ResponseWriter, r *http.Request) {
	loops, err := server.Store.Loops().List(r.Context())
	if err != nil {
		server.jsonErr(w, 500, "%v", err)
		return
	}
	views := make([]*loopView, 0, len(loops))
	for _, loopRecord := range loops {
		views = append(views, server.view(r.Context(), loopRecord))
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
	// InFleetChannel places the new loop in the fleet channel or outside
	// it; absent, the server decides (defaultInFleetChannel).
	InFleetChannel *bool `json:"in_fleet_channel"`
}

var validEfforts = map[string]bool{"": true, "low": true, "medium": true, "high": true, "xhigh": true, "max": true}

func validPacing(pacing string) bool {
	return pacing == "" || pacing == store.PacingFixed || pacing == store.PacingSelf
}

func (server *Server) handleCreateLoop(w http.ResponseWriter, r *http.Request) {
	var req createLoopReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		server.jsonErr(w, 400, "bad json: %v", err)
		return
	}
	req.Name = strings.ToLower(strings.TrimSpace(req.Name))
	if !nameRe.MatchString(req.Name) {
		server.jsonErr(w, 400, "name must match %s", nameRe.String())
		return
	}
	if req.Name == route.BroadcastToken {
		// @all addresses the group's eligible loops; a loop of that name
		// could never be mentioned, only broadcast to (#74).
		server.jsonErr(w, 400, "%q is reserved: @%s addresses every eligible loop in the group",
			route.BroadcastToken, route.BroadcastToken)
		return
	}
	if strings.TrimSpace(req.Mission) == "" {
		server.jsonErr(w, 400, "mission is required")
		return
	}
	if !validEfforts[req.Effort] {
		server.jsonErr(w, 400, "effort must be one of: low, medium, high, xhigh, max (or empty for default)")
		return
	}
	// Trimmed as the model list trims what it is given.
	req.Model = strings.TrimSpace(req.Model)
	if err := loop.ValidLoopModel(req.Model); err != nil {
		server.jsonErr(w, 400, "%v", err)
		return
	}
	if !validPacing(req.Pacing) {
		server.jsonErr(w, 400, "pacing must be 'fixed' or 'self'")
		return
	}

	loopRuntime := defaultStr(req.Runtime, defaultStr(server.DefaultRuntime, store.RuntimeBare))
	switch loopRuntime {
	case store.RuntimeBare:
		if !server.BareAllowed {
			server.jsonErrCode(w, 400, codeBareNotEnabled,
				"a bare loop runs uncontained on this machine; start spool with --allow-bare (or --runtime bare) to allow one")
			return
		}
		if req.Image != "" || req.MemMB != 0 || req.CPUs != 0 {
			server.jsonErr(w, 400, "image, mem_mb and cpus apply to docker loops only")
			return
		}
	case store.RuntimeDocker:
		if req.WorkspacePath != "" {
			server.jsonErr(w, 400, "a docker loop's workstation is its workspace; workspace_path applies to bare loops only")
			return
		}
		if req.MemMB != 0 && (req.MemMB < 256 || req.MemMB > 262144) {
			server.jsonErr(w, 400, "mem_mb must be between 256 and 262144")
			return
		}
		if req.CPUs != 0 && (req.CPUs < 0.1 || req.CPUs > 64) {
			server.jsonErr(w, 400, "cpus must be between 0.1 and 64")
			return
		}
		if server.RuntimeAvailable != nil {
			if err := server.RuntimeAvailable(r.Context(), store.RuntimeDocker); err != nil {
				server.jsonErr(w, 400, "docker runtime unavailable: %v", err)
				return
			}
		}
	default:
		server.jsonErr(w, 400, "runtime must be 'bare' or 'docker' (or empty for the server default)")
		return
	}

	nowMS := time.Now().UnixMilli()
	loopRecord := &store.Loop{
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
		loopRecord.Image = strings.TrimSpace(req.Image)
		loopRecord.MemMB = defaultInt(req.MemMB, 4096)
		loopRecord.CPUs = req.CPUs
		if loopRecord.CPUs == 0 {
			loopRecord.CPUs = 2
		}
	}

	if loopRecord.TGBotToken != "" && server.Surface != nil {
		username, err := server.Surface.ValidateCredential(r.Context(), loopRecord.TGBotToken)
		if err != nil {
			server.jsonErr(w, 400, "telegram token rejected: %v", err)
			return
		}
		loopRecord.TGBotUsername = username
	}

	if loopRecord.Runtime == store.RuntimeDocker {
		// The workstation is the workspace (ADR-0017): claude's cwd is the
		// volume-backed home inside the container, stable across wakes.
		loopRecord.WorkspaceMode = store.WorkspaceNone
		loopRecord.WorkspacePath = runtime.WorkstationHome
	} else if err := server.resolveWorkspace(loopRecord, req.WorkspacePath, req.WorkspaceMode); err != nil {
		server.jsonErr(w, 400, "%v", err)
		return
	}

	// A new loop starts owned by the same person as the rest of the fleet:
	// the first allowlisted sender, reassignable per loop (#73).
	loopRecord.OwnerTGUserID = server.defaultOwnerID(r.Context())

	// Membership is settled before the loop exists, never patched in after:
	// a loop created in the channel and then taken out would be reachable
	// by an @all in between.
	inFleet := req.InFleetChannel
	if inFleet == nil {
		in, err := server.defaultInFleetChannel(r.Context())
		if err != nil {
			server.jsonErr(w, 500, "%v", err)
			return
		}
		inFleet = &in
	}
	loopRecord.OutsideFleetChannel = !*inFleet

	if err := server.Store.Loops().Create(r.Context(), loopRecord); err != nil {
		if errors.Is(err, store.ErrDuplicate) {
			server.jsonErr(w, 409, "a loop named %q already exists", loopRecord.Name)
		} else {
			server.jsonErr(w, 500, "%v", err)
		}
		return
	}
	// A new loop is minted with a hub MCP token, and may arrive with a bot
	// token: two secret values that did not exist a moment ago.
	server.secretsChanged(r.Context())
	server.Manager.Add(loopRecord)
	if server.Surface != nil {
		server.Surface.LoopChanged(r.Context(), loopRecord.ID)
	}
	// first tick shortly after creation so the mission starts without waiting
	// a full interval
	server.Sched.ScheduleNow(loopRecord.ID)
	writeJSON(w, 201, server.view(r.Context(), loopRecord))
}

// resolveWorkspace decides the loop's cwd. Every loop needs a stable cwd
// (claude sessions are keyed by it); workspace-less loops get a home dir
// under the data dir.
func (server *Server) resolveWorkspace(loopRecord *store.Loop, path, mode string) error {
	path = strings.TrimSpace(path)
	if path == "" || mode == "none" {
		home := filepath.Join(server.DataDir, "homes", loopRecord.Name)
		if err := os.MkdirAll(home, 0o755); err != nil {
			return err
		}
		loopRecord.WorkspaceMode = store.WorkspaceNone
		loopRecord.WorkspacePath = home
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
		wt := filepath.Join(server.DataDir, "worktrees", loopRecord.Name)
		branch, err := gitws.Create(abs, wt, loopRecord.Name)
		if err != nil {
			return err
		}
		loopRecord.WorkspaceMode = store.WorkspaceWorktree
		loopRecord.RepoPath = abs
		loopRecord.WorktreePath = wt
		loopRecord.WorkspacePath = wt
		loopRecord.Branch = branch
		return nil
	}
	loopRecord.WorkspaceMode = store.WorkspaceDir
	loopRecord.WorkspacePath = abs
	return nil
}

func (server *Server) handleGetLoop(w http.ResponseWriter, r *http.Request) {
	loopRecord := server.loopByName(w, r)
	if loopRecord == nil {
		return
	}
	writeJSON(w, 200, server.view(r.Context(), loopRecord))
}

// What a PATCH did about the loop's session, reported alongside the loop it
// saved. A mission is part of the loop's system prompt, and a running session
// cannot be given a new one (#162): the change is announced to the session it
// finds — the standing-instructions note carries it at the next wake — but
// the prompt itself is only replaced when the loop starts a fresh session.
// #261 settled that saving asks for that rotation rather than waiting for one
// to come along, which may be hours.
//
// The API asks for it rather than leaving it to a client: only the API knows
// whether the mission it was handed is a change, and a client that rotated
// unconditionally would charge an operator their loop's whole context for
// pressing Save on text nobody touched.
//
// Always present, so a client can tell a server that did not rotate from one
// too old to — the lesson of context_fill_pct (#122).
const (
	// The mission was not named, or is what it already was. Nothing was asked.
	rotationNone = "none"
	// A rotation was asked for. It fires at the loop's next quiet boundary
	// (ADR-0022): the loop finishes the turn it is in, writes its handoff
	// note, and continues on a fresh session under the new mission. Queued,
	// never "in force now".
	rotationQueued = "queued"
	// The mission changed and saved, but there is no session to rotate: the
	// loop has no running actor, or has one that has not started a session.
	// Not an error — there is no prompt in force to be stale, and the new
	// mission is the one the loop's next session is built from.
	rotationNoSession = "no_session"
)

// missionChanged reports whether an edit asks for a different mission.
// Compared trimmed: trailing whitespace is what a textarea collects, not
// something an operator means to spend a session's context on.
func missionChanged(before, after string) bool {
	return strings.TrimSpace(before) != strings.TrimSpace(after)
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
	InFleetChannel  *bool   `json:"in_fleet_channel"`
}

// patchLoopResp is the saved loop with one field the loop itself does not
// have: what the save did about its session. Embedded rather than wrapped, so
// every field a client already reads from a PATCH is where it was.
type patchLoopResp struct {
	*loopView
	Rotation string `json:"rotation"`
}

func (server *Server) handlePatchLoop(w http.ResponseWriter, r *http.Request) {
	loopRecord := server.loopByName(w, r)
	if loopRecord == nil {
		return
	}
	var req patchLoopReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		server.jsonErr(w, 400, "bad json: %v", err)
		return
	}
	if req.Mission != nil && strings.TrimSpace(*req.Mission) == "" {
		// The same refusal the create path gives (:396 before this block).
		// A loop whose instructions are the empty string has no useful
		// reading, and the column would take one: it is NOT NULL DEFAULT ''.
		server.jsonErr(w, 400, "mission is required")
		return
	}
	if req.Model != nil {
		model := strings.TrimSpace(*req.Model)
		req.Model = &model
		if err := loop.ValidLoopModel(model); err != nil {
			server.jsonErr(w, 400, "%v", err)
			return
		}
	}
	// Built as an edit rather than applied to loopRecord: the loop was read before
	// the token round-trip below, and writing that copy back reverts
	// whatever the Telegram poller learned meanwhile (#164).
	edit := store.LoopEdit{
		Mission:         req.Mission,
		Model:           req.Model,
		TickIntervalSec: req.TickIntervalSec,
		MinWakeSec:      req.MinWakeSec,
		MaxWakeSec:      req.MaxWakeSec,
		IdleTimeoutSec:  req.IdleTimeoutSec,
		UpdatedAt:       time.Now().UnixMilli(),
	}
	if req.Effort != nil {
		if !validEfforts[*req.Effort] {
			server.jsonErr(w, 400, "effort must be one of: low, medium, high, xhigh, max (or empty for default)")
			return
		}
		edit.Effort = req.Effort
	}
	if req.Pacing != nil {
		if !validPacing(*req.Pacing) {
			server.jsonErr(w, 400, "pacing must be 'fixed' or 'self'")
			return
		}
		pacing := defaultStr(*req.Pacing, store.PacingFixed)
		edit.Pacing = &pacing
	}
	if req.InFleetChannel != nil {
		outside := !*req.InFleetChannel
		edit.OutsideFleetChannel = &outside
	}
	if req.TGBotToken != nil {
		token := strings.TrimSpace(*req.TGBotToken)
		username := ""
		if token != "" && server.Surface != nil {
			// A live call to api.telegram.org, which is why nothing read
			// before this point may be written back afterwards.
			name, err := server.Surface.ValidateCredential(r.Context(), token)
			if err != nil {
				server.jsonErr(w, 400, "telegram token rejected: %v", err)
				return
			}
			username = name
		}
		edit.TGBotToken, edit.TGBotUsername = &token, &username
		// A cleared token leaves no bot to hold the binding. A replaced one
		// keeps it: the group is the same group, and the poller rebinds.
		edit.ClearGroupBinding = token == ""
	}
	updated, err := server.Store.Loops().Edit(r.Context(), loopRecord.ID, edit)
	if err != nil {
		server.storeErr(w, err, "loop")
		return
	}
	if req.TGBotToken != nil {
		server.secretsChanged(r.Context())
	}
	// The stored row, not the edited copy: everything this request did not
	// name reaches the actor and the surface as it actually stands.
	server.Manager.UpdateLoop(updated)
	if server.Surface != nil {
		server.Surface.LoopChanged(r.Context(), updated.ID)
	}
	// After UpdateLoop, so the session the rotation starts is built from the
	// mission this request saved rather than the one it replaced. `loopRecord`
	// is the row as it was read at the top of the handler, which is what makes
	// this a comparison and not a tautology.
	rotation := rotationNone
	if req.Mission != nil && missionChanged(loopRecord.Mission, *req.Mission) {
		rotation = rotationNoSession
		if actor, ok := server.Manager.Get(updated.ID); ok {
			// An error here means there is no session to rotate, which is
			// not a failed save: handleRotate answers 409 for it because an
			// operator asking for a rotation outright gets nothing, while
			// here the mission is stored and the loop's next session carries
			// it. Reported, not raised.
			if err := actor.Rotate(); err == nil {
				rotation = rotationQueued
			}
		}
	}
	writeJSON(w, 200, patchLoopResp{loopView: server.view(r.Context(), updated), Rotation: rotation})
}

func (server *Server) handleDeleteLoop(w http.ResponseWriter, r *http.Request) {
	loopRecord := server.loopByName(w, r)
	if loopRecord == nil {
		return
	}
	server.Manager.Remove(loopRecord.ID, loopRecord.Runtime)
	if server.Surface != nil {
		server.Surface.LoopRemoved(loopRecord.ID)
	}
	if r.URL.Query().Get("remove_worktree") == "1" && loopRecord.WorkspaceMode == store.WorkspaceWorktree {
		if err := gitws.Remove(loopRecord.RepoPath, loopRecord.WorktreePath); err != nil {
			server.Log.Warn("worktree remove", "err", err)
		}
	}
	_ = server.Store.Sessions().End(r.Context(), loopRecord.CurrentSessionID, store.EndReasonKilled, time.Now().UnixMilli())
	if err := server.Store.Loops().Delete(r.Context(), loopRecord.ID); err != nil {
		server.jsonErr(w, 500, "%v", err)
		return
	}
	writeJSON(w, 200, map[string]bool{"deleted": true})
}

func (server *Server) handlePause(w http.ResponseWriter, r *http.Request) {
	loopRecord := server.loopByName(w, r)
	if loopRecord == nil {
		return
	}
	loopRecord.Status = store.StatusPaused
	loopRecord.UpdatedAt = time.Now().UnixMilli()
	_ = server.Store.Loops().SetStatus(r.Context(), loopRecord.ID, loopRecord.Status, loopRecord.UpdatedAt)
	if actor, ok := server.Manager.Get(loopRecord.ID); ok {
		actor.Pause()
	}
	server.Sched.Suspend(loopRecord.ID)
	writeJSON(w, 200, server.view(r.Context(), loopRecord))
}

// handlePower runs one of the operator's power controls on a loop's
// workstation and answers with the loop as it stands afterwards — the call
// is synchronous, so the control room can render the result rather than
// wait for the stream to correct it (ADR-0021).
func (server *Server) handlePower(verb string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		loopRecord := server.loopByName(w, r)
		if loopRecord == nil {
			return
		}
		actor, ok := server.Manager.Get(loopRecord.ID)
		if !ok {
			server.jsonErrCode(w, http.StatusConflict, codeLoopNotRunning,
				"loop %s is not running", loopRecord.Name)
			return
		}
		switch err := actor.Power(verb); {
		case errors.Is(err, runtime.ErrUnsupported):
			server.jsonErrCode(w, http.StatusConflict, codeNoWorkstation,
				"loop %s runs on the %s runtime, which has no workstation to %s", loopRecord.Name, loopRecord.Runtime, verb)
			return
		case err != nil:
			server.jsonErr(w, http.StatusInternalServerError, "%s: %v", verb, err)
			return
		}
		if fresh, err := server.Store.Loops().Get(r.Context(), loopRecord.ID); err == nil {
			loopRecord = fresh
		}
		writeJSON(w, 200, server.view(r.Context(), loopRecord))
	}
}

func (server *Server) handleResume(w http.ResponseWriter, r *http.Request) {
	loopRecord := server.loopByName(w, r)
	if loopRecord == nil {
		return
	}
	loopRecord.Status = store.StatusActive
	loopRecord.UpdatedAt = time.Now().UnixMilli()
	_ = server.Store.Loops().SetStatus(r.Context(), loopRecord.ID, loopRecord.Status, loopRecord.UpdatedAt)
	if actor, ok := server.Manager.Get(loopRecord.ID); ok {
		actor.Resume()
	}
	server.Sched.Resume(loopRecord.ID)
	writeJSON(w, 200, server.view(r.Context(), loopRecord))
}

func (server *Server) handleWake(w http.ResponseWriter, r *http.Request) {
	loopRecord := server.loopByName(w, r)
	if loopRecord == nil {
		return
	}
	server.Manager.Tick(loopRecord.ID)
	writeJSON(w, 200, map[string]bool{"woken": true})
}

func (server *Server) handleKill(w http.ResponseWriter, r *http.Request) {
	loopRecord := server.loopByName(w, r)
	if loopRecord == nil {
		return
	}
	if actor, ok := server.Manager.Get(loopRecord.ID); ok {
		actor.Kill()
	}
	writeJSON(w, 200, map[string]bool{"killed": true})
}

// handleRotate queues an operator-asked context rotation (ADR-0022): the
// loop writes its handoff note at the next quiet boundary and continues on
// a fresh session seeded from it.
func (server *Server) handleRotate(w http.ResponseWriter, r *http.Request) {
	loopRecord := server.loopByName(w, r)
	if loopRecord == nil {
		return
	}
	actor, ok := server.Manager.Get(loopRecord.ID)
	if !ok {
		server.jsonErr(w, 409, "loop has no running actor")
		return
	}
	if err := actor.Rotate(); err != nil {
		server.jsonErr(w, 409, "%v", err)
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

func (server *Server) handleLoopMessage(w http.ResponseWriter, r *http.Request) {
	loopRecord := server.loopByName(w, r)
	if loopRecord == nil {
		return
	}
	var req postMessageReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Text) == "" {
		server.jsonErr(w, 400, "author and non-empty text required")
		return
	}
	dest := defaultStr(req.Destination, store.ConversationControlRoom)
	if dest != store.ConversationControlRoom && dest != store.ConversationGroup {
		server.jsonErr(w, 400, "destination must be %s or %s", store.ConversationControlRoom, store.ConversationGroup)
		return
	}
	if dest == store.ConversationGroup && loopRecord.OutsideFleetChannel {
		// Refused rather than stored: the post would land in the group and
		// reach nobody, since the loop it was written to has no group.
		server.jsonErr(w, 409, "%s is not in the fleet channel", loopRecord.Name)
		return
	}
	err := server.Router.Ingest(r.Context(), route.InboundMessage{
		Origin:       store.OriginWeb,
		Author:       defaultStr(req.Author, "operator"),
		Text:         req.Text,
		ImplicitTo:   loopRecord.ID,
		Conversation: dest,
	})
	if err != nil {
		server.jsonErr(w, 500, "%v", err)
		return
	}
	writeJSON(w, 202, map[string]bool{"queued": true})
}

func (server *Server) handleLoopEvents(w http.ResponseWriter, r *http.Request) {
	loopRecord := server.loopByName(w, r)
	if loopRecord == nil {
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
		events, err = server.Store.Events().ListByLoopBefore(r.Context(), loopRecord.ID, beforeID, limit)
	} else {
		afterID, _ := strconv.ParseInt(query.Get("after_id"), 10, 64)
		events, err = server.Store.Events().ListByLoop(r.Context(), loopRecord.ID, afterID, limit)
	}
	if err != nil {
		server.jsonErr(w, 500, "%v", err)
		return
	}
	if events == nil {
		events = []*store.Event{}
	}
	writeJSON(w, 200, events)
}

func (server *Server) handleLoopTurns(w http.ResponseWriter, r *http.Request) {
	loopRecord := server.loopByName(w, r)
	if loopRecord == nil {
		return
	}
	turns, err := server.Store.Turns().ListByLoop(r.Context(), loopRecord.ID, queryInt(r, "limit", 50))
	if err != nil {
		server.jsonErr(w, 500, "%v", err)
		return
	}
	if turns == nil {
		turns = []*store.Turn{}
	}
	writeJSON(w, 200, turns)
}

func (server *Server) handleTelegramStatus(w http.ResponseWriter, r *http.Request) {
	loopRecord := server.loopByName(w, r)
	if loopRecord == nil {
		return
	}
	status := map[string]any{
		"configured":   loopRecord.TGBotToken != "",
		"bot_username": loopRecord.TGBotUsername,
		"group_bound":  loopRecord.TGGroupChatID != 0,
	}
	if server.Surface != nil {
		status["bridge"] = server.Surface.Status(loopRecord.ID)
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
	// BareAllowed is how the control room knows whether to offer an
	// uncontained loop at all (#255): the choice is the operator's, taken
	// at the terminal when the hub was started, and a form that offered it
	// anyway would be offering a create the API refuses (#254).
	BareAllowed bool `json:"bare_allowed"`
}

func (server *Server) settingsView(ctx context.Context) (settingsView, error) {
	token, err := server.claudeToken(ctx)
	if err != nil {
		return settingsView{}, err
	}
	arm, force := loop.RotationThresholds(ctx, server.Store.Settings(), server.Log)
	return settingsView{
		ClaudeTokenSet:      token != "",
		ContextArmPercent:   arm,
		ContextForcePercent: force,
		BareAllowed:         server.BareAllowed,
	}, nil
}

func (server *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	view, err := server.settingsView(r.Context())
	if err != nil {
		server.jsonErr(w, 500, "%v", err)
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

func (server *Server) handlePutSettings(w http.ResponseWriter, r *http.Request) {
	var req putSettingsReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		server.jsonErr(w, 400, "bad json: %v", err)
		return
	}
	if req.ClaudeOAuthToken != nil {
		token := ""
		if strings.TrimSpace(*req.ClaudeOAuthToken) != "" {
			validated, err := validateClaudeToken(*req.ClaudeOAuthToken)
			if err != nil {
				server.jsonErr(w, 400, "%v", err)
				return
			}
			token = validated
		}
		if err := server.Store.Settings().Set(r.Context(), store.SettingClaudeOAuthToken, token); err != nil {
			server.jsonErr(w, 500, "%v", err)
			return
		}
		server.secretsChanged(r.Context())
	}
	if req.ContextArmPercent != nil || req.ContextForcePercent != nil {
		server.settingsMu.Lock()
		defer server.settingsMu.Unlock()
		arm, force := loop.RotationThresholds(r.Context(), server.Store.Settings(), server.Log)
		if req.ContextArmPercent != nil {
			arm = *req.ContextArmPercent
		}
		if req.ContextForcePercent != nil {
			force = *req.ContextForcePercent
		}
		if !loop.ValidThresholds(arm, force) {
			server.jsonErr(w, 400, "rotation thresholds must be percentages 1-99 with arm below force (got arm %d, force %d)", arm, force)
			return
		}
		if err := server.Store.Settings().Set(r.Context(), store.SettingContextArmPercent, strconv.Itoa(arm)); err != nil {
			server.jsonErr(w, 500, "%v", err)
			return
		}
		if err := server.Store.Settings().Set(r.Context(), store.SettingContextForcePercent, strconv.Itoa(force)); err != nil {
			server.jsonErr(w, 500, "%v", err)
			return
		}
	}
	view, err := server.settingsView(r.Context())
	if err != nil {
		server.jsonErr(w, 500, "%v", err)
		return
	}
	writeJSON(w, 200, view)
}

// claudeToken reads the stored setup-token, mapping an unset key to empty so
// callers can treat "never configured" and "" alike.
func (server *Server) claudeToken(ctx context.Context) (string, error) {
	token, err := server.Store.Settings().Get(ctx, store.SettingClaudeOAuthToken)
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
func (server *Server) handleLoopConversation(w http.ResponseWriter, r *http.Request) {
	loopRecord := server.loopByName(w, r)
	if loopRecord == nil {
		return
	}
	kind := defaultStr(r.URL.Query().Get("conversation"), store.ConversationControlRoom)
	if kind != store.ConversationControlRoom && kind != store.ConversationOwnerDM {
		server.jsonErr(w, 400, "conversation must be %s or %s", store.ConversationControlRoom, store.ConversationOwnerDM)
		return
	}
	msgs, err := server.Store.Messages().ListConversation(r.Context(), kind, loopRecord.ID, queryInt(r, "limit", 100))
	if err != nil {
		server.jsonErr(w, 500, "%v", err)
		return
	}
	if msgs == nil {
		msgs = []*store.Message{}
	}
	writeJSON(w, 200, msgs)
}

// handleGroupTimeline is the fleet channel's own timeline, newest first like
// a loop's conversation. The channel is the hub's rather than any loop's
// (ADR-0032 item 1), so it is not reached through one.
func (server *Server) handleGroupTimeline(w http.ResponseWriter, r *http.Request) {
	msgs, err := server.Store.Messages().ListConversation(r.Context(), store.ConversationGroup, "", queryInt(r, "limit", 100))
	if err != nil {
		server.jsonErr(w, 500, "%v", err)
		return
	}
	if msgs == nil {
		msgs = []*store.Message{}
	}
	writeJSON(w, 200, msgs)
}

type postGroupReq struct {
	Author string `json:"author"`
	Text   string `json:"text"`
	// ReplyToID is the fleet-channel message this post answers, or 0 for
	// a plain post.
	ReplyToID int64 `json:"reply_to_id"`
}

// handleGroupPost is the operator posting to the fleet channel. Unlike a
// group post from a loop's composer, it names no loop implicitly: it wakes
// the loops its text addresses and no others, and a post that addresses
// nobody is kept and wakes nobody — as a human's post in a mirrored room
// is. It never leaves the hub (ADR-0032 item 4).
//
// A reply addresses the author of what it answers, as a native reply does
// (ADR-0025), so a loop can be answered without a mention.
func (server *Server) handleGroupPost(w http.ResponseWriter, r *http.Request) {
	var req postGroupReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Text) == "" {
		server.jsonErr(w, 400, "non-empty text required")
		return
	}
	if req.ReplyToID != 0 && !server.groupReplyTarget(r.Context(), w, req.ReplyToID) {
		return
	}
	err := server.Router.Ingest(r.Context(), route.InboundMessage{
		Origin:       store.OriginWeb,
		Author:       defaultStr(req.Author, "operator"),
		Text:         req.Text,
		Conversation: store.ConversationGroup,
		ReplyToID:    req.ReplyToID,
	})
	if err != nil {
		server.jsonErr(w, 500, "%v", err)
		return
	}
	writeJSON(w, 202, map[string]bool{"queued": true})
}

// groupReplyTarget checks that id names a message of the fleet channel,
// answering the refusal itself when it does not. The operator picked a real
// message, so one that is missing means the page is stale, and one from
// another conversation would add a private message's author to the
// channel's recipients. Both are refused with the codes a loop's own send
// gets for the same mistakes.
func (server *Server) groupReplyTarget(ctx context.Context, w http.ResponseWriter, id int64) bool {
	target, err := server.Store.Messages().Get(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		server.jsonErrCode(w, 400, route.ErrUnknownReplyTo, "no message %d to reply to", id)
		return false
	} else if err != nil {
		server.jsonErr(w, 500, "%v", err)
		return false
	}
	if target.Conversation != store.ConversationGroup {
		server.jsonErrCode(w, 400, route.ErrCrossConversation,
			"message %d is in %s, not the fleet channel; a reply stays in its own conversation", id, target.Conversation)
		return false
	}
	return true
}

func (server *Server) handleActivity(w http.ResponseWriter, r *http.Request) {
	msgs, err := server.Store.Messages().List(r.Context(), queryInt(r, "limit", 100))
	if err != nil {
		server.jsonErr(w, 500, "%v", err)
		return
	}
	if msgs == nil {
		msgs = []*store.Message{}
	}
	writeJSON(w, 200, msgs)
}

// handleUndelivered lists the sends the Fleet badge counts: messages that
// never reached their surface and that nobody has resolved, newest failure
// first (#263, #269). Activity cannot answer this — it is the newest hundred
// events across everything, so a failure from hours ago has scrolled out of
// it, which is the whole reason this route exists.
//
// The optional loop parameter narrows it to one loop's failures. That is the
// scope the Fleet badge already counts, so the per-loop pane and the badge
// that opens it run one predicate under one scope rather than two queries
// that have to be kept in agreement (#281). Narrowing here rather than in the
// browser also keeps the pane from reading every loop's rows to show one
// loop's, on a route that deliberately has no limit.
//
// It is a loop's name, like every other loop-addressed route, and resolved to
// the id the rows are keyed by. A name nothing matches is a 404 rather than
// an empty list: with a filter, silence is the same answer as a healthy loop,
// so a caller that passed the wrong thing would be told it has no failures.
//
// No limit, deliberately: a cap is what makes Activity unable to answer the
// question, and the set is bounded by the operator dealing with it rather
// than by a clock. A fleet that overflows this has a worse problem than
// paging.
//
// The rows are whole messages, text included, which is what the Activity feed
// already serves and sits behind the same operator credential (ADR-0030).
func (server *Server) handleUndelivered(w http.ResponseWriter, r *http.Request) {
	var loopID string
	if name := r.URL.Query().Get("loop"); name != "" {
		loopRecord, err := server.Store.Loops().GetByName(r.Context(), name)
		if errors.Is(err, store.ErrNotFound) {
			server.jsonErr(w, http.StatusNotFound, "loop %q not found", name)
			return
		}
		if err != nil {
			server.jsonErr(w, 500, "%v", err)
			return
		}
		loopID = loopRecord.ID
	}
	msgs, err := server.Store.Messages().Undelivered(r.Context(), loopID)
	if err != nil {
		server.jsonErr(w, 500, "%v", err)
		return
	}
	if msgs == nil {
		msgs = []*store.Message{}
	}
	writeJSON(w, 200, msgs)
}

// handleRetrySend sends a failed message again, to the destination it was
// sent to the first time (#269). It answers 202 rather than 200: the send is
// the surface's, it is queued behind whatever that surface is already doing,
// and an outcome the operator can trust arrives as the row resolving or its
// error changing — not as this response.
//
// A message with nothing to retry is a 404 rather than a 409: from the
// operator's side the thing they clicked is not there, whether because
// another window already retried it, because it went through, or because the
// id names a message that never failed.
func (server *Server) handleRetrySend(w http.ResponseWriter, r *http.Request) {
	id, ok := server.messageID(w, r)
	if !ok {
		return
	}
	switch err := server.Router.RetrySend(r.Context(), id); {
	case err == nil:
		writeJSON(w, 202, map[string]bool{"retrying": true})
	case errors.Is(err, store.ErrNotFound), errors.Is(err, route.ErrNoUnresolvedFailure):
		server.jsonErr(w, 404, "no unresolved send failure for message %d", id)
	case errors.Is(err, route.ErrNoOwnerDMChat):
		// The retry would have nowhere to land. Said as its own refusal
		// because it is the one case the operator can fix: the owner has to
		// write to the bot once before a loop can DM them (#73).
		server.jsonErr(w, 409, "%v", err)
	default:
		server.jsonErr(w, 500, "%v", err)
	}
}

// handleDismissSend resolves a failure without sending anything: the operator
// has read it and is done with it. The row keeps what failed and why — this
// takes it off their list, it does not rewrite history.
func (server *Server) handleDismissSend(w http.ResponseWriter, r *http.Request) {
	id, ok := server.messageID(w, r)
	if !ok {
		return
	}
	resolved, err := server.Store.Messages().ResolveSend(r.Context(), id,
		time.Now().UnixMilli(), store.SendResolutionDismissed, 0)
	if err != nil {
		server.jsonErr(w, 500, "%v", err)
		return
	}
	if !resolved {
		server.jsonErr(w, 404, "no unresolved send failure for message %d", id)
		return
	}
	writeJSON(w, 200, map[string]bool{"dismissed": true})
}

// messageID reads the {id} path value, answering 400 when it is not a number
// rather than looking up message zero.
func (server *Server) messageID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		server.jsonErr(w, 400, "message id must be a number")
		return 0, false
	}
	return id, true
}

func (server *Server) handleListSenders(w http.ResponseWriter, r *http.Request) {
	senders, err := server.Store.TGSenders().List(r.Context())
	if err != nil {
		server.jsonErr(w, 500, "%v", err)
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

func (server *Server) secretViews(ctx context.Context, loopID string) ([]secretView, error) {
	secrets, err := server.Store.LoopSecrets().List(ctx, loopID)
	if err != nil {
		return nil, err
	}
	views := make([]secretView, 0, len(secrets))
	for _, sc := range secrets {
		views = append(views, secretView{Name: sc.Name, UpdatedAt: sc.UpdatedAt})
	}
	return views, nil
}

func (server *Server) handleListSecrets(w http.ResponseWriter, r *http.Request) {
	loopRecord := server.loopByName(w, r)
	if loopRecord == nil {
		return
	}
	views, err := server.secretViews(r.Context(), loopRecord.ID)
	if err != nil {
		server.jsonErr(w, 500, "%v", err)
		return
	}
	writeJSON(w, 200, views)
}

type putSecretReq struct {
	Value string `json:"value"`
}

func (server *Server) handlePutSecret(w http.ResponseWriter, r *http.Request) {
	loopRecord := server.loopByName(w, r)
	if loopRecord == nil {
		return
	}
	name := r.PathValue("key")
	if err := validateSecretName(name); err != nil {
		server.jsonErr(w, 400, "%v", err)
		return
	}
	var req putSecretReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		server.jsonErr(w, 400, "bad json: %v", err)
		return
	}
	switch {
	case req.Value == "":
		server.jsonErr(w, 400, "secret value is empty (use DELETE to remove a secret)")
		return
	case len(req.Value) > maxSecretValueLen:
		server.jsonErr(w, 400, "secret value too large (max %d bytes)", maxSecretValueLen)
		return
	}
	existing, err := server.Store.LoopSecrets().List(r.Context(), loopRecord.ID)
	if err != nil {
		server.jsonErr(w, 500, "%v", err)
		return
	}
	// The cap bounds distinct names; replacing an existing one never grows it.
	if len(existing) >= maxSecretsPerLoop && !hasSecret(existing, name) {
		server.jsonErr(w, 400, "too many secrets on this loop (max %d)", maxSecretsPerLoop)
		return
	}
	if err := server.Store.LoopSecrets().Set(r.Context(), loopRecord.ID, name, req.Value, time.Now().UnixMilli()); err != nil {
		server.storeErr(w, err, "loop")
		return
	}
	server.secretsChanged(r.Context())
	views, err := server.secretViews(r.Context(), loopRecord.ID)
	if err != nil {
		server.jsonErr(w, 500, "%v", err)
		return
	}
	writeJSON(w, 200, views)
}

func (server *Server) handleDeleteSecret(w http.ResponseWriter, r *http.Request) {
	loopRecord := server.loopByName(w, r)
	if loopRecord == nil {
		return
	}
	// Delete is idempotent: removing an absent name still reports success.
	if err := server.Store.LoopSecrets().Delete(r.Context(), loopRecord.ID, r.PathValue("key")); err != nil {
		server.jsonErr(w, 500, "%v", err)
		return
	}
	server.secretsChanged(r.Context())
	writeJSON(w, 200, map[string]bool{"deleted": true})
}

// secretsChanged tells the redactor to reload. A delete counts: the
// value is gone from the store, so a redactor still holding it would keep
// blanking text that no longer contains a secret.
func (server *Server) secretsChanged(ctx context.Context) {
	if server.SecretsChanged != nil {
		server.SecretsChanged(ctx)
	}
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

func (server *Server) handleSenderStatus(status string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			server.jsonErr(w, 400, "bad sender id")
			return
		}
		if err := server.Store.TGSenders().SetStatus(r.Context(), id, status, time.Now().UnixMilli()); err != nil {
			server.storeErr(w, err, "sender")
			return
		}
		sender, _ := server.Store.TGSenders().Get(r.Context(), id)
		if status == store.SenderAllowed {
			server.adoptDefaultOwner(r.Context(), id)
		} else {
			server.disownLoopsOf(r.Context(), id)
		}
		server.Bus.Publish(bus.Item{Kind: bus.KindAccess, Payload: sender})
		writeJSON(w, 200, sender)
	}
}

// defaultOwnerID is the first person the operator allowlisted, or 0 when
// nobody is.
func (server *Server) defaultOwnerID(ctx context.Context) int64 {
	senders, err := server.Store.TGSenders().List(ctx)
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

// defaultInFleetChannel is where a new loop starts when its creator did not
// say: outside the fleet channel when no other loop exists, in it
// otherwise. A fleet of one has no one to talk to in the channel (ADR-0032
// item 2), and the second loop is what makes a fleet — the operator's rule
// on #287. Any loop counts, archived included, as the New loop form counts
// them: an archived loop is still the fleet's, and could be restored.
func (server *Server) defaultInFleetChannel(ctx context.Context) (bool, error) {
	loops, err := server.Store.Loops().List(ctx)
	if err != nil {
		return false, fmt.Errorf("list loops: %w", err)
	}
	return len(loops) > 0, nil
}

// adoptDefaultOwner gives every ownerless loop this sender as its owner.
// The first person the operator allowlists is the owner by default — the
// fleet is built for one operator — and they can reassign per loop.
func (server *Server) adoptDefaultOwner(ctx context.Context, tgUserID int64) {
	loops, err := server.Store.Loops().List(ctx)
	if err != nil {
		return
	}
	for _, loopRecord := range loops {
		if loopRecord.OwnerTGUserID != 0 || loopRecord.Status == store.StatusArchived {
			continue
		}
		if err := server.Store.Loops().SetOwner(ctx, loopRecord.ID, tgUserID, 0, time.Now().UnixMilli()); err != nil {
			server.Log.Error("default owner", "loop", loopRecord.Name, "err", err)
		}
	}
}

// disownLoopsOf drops the ownership of a sender who may no longer reach the
// fleet. An owner is an allowed sender — checked when one is set — and that
// invariant has to survive the reverse transitions too: otherwise blocking
// or deleting someone leaves the loops they own still messaging them
// privately, with the control room reporting those loops as ready to do it.
func (server *Server) disownLoopsOf(ctx context.Context, tgUserID int64) {
	loops, err := server.Store.Loops().List(ctx)
	if err != nil {
		return
	}
	for _, loopRecord := range loops {
		if loopRecord.OwnerTGUserID != tgUserID {
			continue
		}
		if err := server.Store.Loops().SetOwner(ctx, loopRecord.ID, 0, 0, time.Now().UnixMilli()); err != nil {
			server.Log.Error("disown loop", "loop", loopRecord.Name, "err", err)
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
func (server *Server) handlePutOwner(w http.ResponseWriter, r *http.Request) {
	loopRecord := server.loopByName(w, r)
	if loopRecord == nil {
		return
	}
	var req putOwnerReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		server.jsonErr(w, 400, "bad json")
		return
	}
	sender, err := server.Store.TGSenders().Get(r.Context(), req.TGUserID)
	if errors.Is(err, store.ErrNotFound) || (err == nil && sender.Status != store.SenderAllowed) {
		server.jsonErr(w, 400, "owner must be an allowed telegram sender")
		return
	} else if err != nil {
		server.jsonErr(w, 500, "%v", err)
		return
	}
	if loopRecord.OwnerTGUserID != req.TGUserID {
		loopRecord.OwnerTGUserID, loopRecord.OwnerDMChatID = req.TGUserID, 0
		loopRecord.UpdatedAt = time.Now().UnixMilli()
		if err := server.Store.Loops().SetOwner(r.Context(), loopRecord.ID, loopRecord.OwnerTGUserID, 0, loopRecord.UpdatedAt); err != nil {
			server.storeErr(w, err, "loop")
			return
		}
	}
	writeJSON(w, 200, server.view(r.Context(), loopRecord))
}

func (server *Server) handleDeleteSender(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		server.jsonErr(w, 400, "bad sender id")
		return
	}
	if err := server.Store.TGSenders().Delete(r.Context(), id); err != nil {
		server.jsonErr(w, 500, "%v", err)
		return
	}
	server.disownLoopsOf(r.Context(), id)
	server.Bus.Publish(bus.Item{Kind: bus.KindAccess, Payload: map[string]any{"deleted": id}})
	writeJSON(w, 200, map[string]bool{"deleted": true})
}

type inspectReq struct {
	Path string `json:"path"`
}

func (server *Server) handleWorkspaceInspect(w http.ResponseWriter, r *http.Request) {
	var req inspectReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		server.jsonErr(w, 400, "bad json")
		return
	}
	abs, err := filepath.Abs(strings.TrimSpace(req.Path))
	if err != nil {
		server.jsonErr(w, 400, "%v", err)
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

func (server *Server) handleGlobalStream(w http.ResponseWriter, r *http.Request) {
	serveSSE(w, r, server.Bus, func(item bus.Item) bool {
		return item.Kind != bus.KindAgentEvent
	})
}

func (server *Server) handleLoopStream(w http.ResponseWriter, r *http.Request) {
	loopRecord := server.loopByName(w, r)
	if loopRecord == nil {
		return
	}
	id := loopRecord.ID
	serveSSE(w, r, server.Bus, func(item bus.Item) bool {
		if item.Kind == bus.KindMessage {
			if mp, ok := item.Payload.(*route.MessagePayload); ok {
				if mp.FromLoopID == id {
					return true
				}
				for _, deliveredTo := range mp.DeliveredTo {
					if deliveredTo == id {
						return true
					}
				}
			}
			return false
		}
		return item.LoopID == id
	})
}

// routeMethods are the methods apiFallback asks the mux about.
var routeMethods = []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete}

// apiFallback answers an /api/ request no route took. A ServeMux synthesises
// 405 only when no pattern matched at all, and this one, like the control
// room's catch-all behind it, matches every method at every path; so it asks
// the mux itself which methods the path would have taken. Some: 405 with
// Allow. None: 404. Either way JSON, and never index.html at an API URL
// (#245).
func (server *Server) apiFallback(mux *http.ServeMux) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var allowed []string
		for _, method := range routeMethods {
			probe := r.Clone(r.Context())
			probe.Method = method
			if _, pattern := mux.Handler(probe); pattern != "/api/" && pattern != "" {
				allowed = append(allowed, method)
			}
		}
		if len(allowed) == 0 {
			server.jsonErr(w, http.StatusNotFound, "no such route: %s", r.URL.Path)
			return
		}
		w.Header().Set("Allow", strings.Join(allowed, ", "))
		server.jsonErr(w, http.StatusMethodNotAllowed, "%s takes %s", r.URL.Path, strings.Join(allowed, ", "))
	}
}

// handleUI serves the embedded SPA with an index.html fallback.
func (server *Server) handleUI(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/")
	if path == "" {
		path = "index.html"
	}
	if file, err := server.WebFS.Open(path); err == nil {
		file.Close()
		http.ServeFileFS(w, r, server.WebFS, path)
		return
	}
	http.ServeFileFS(w, r, server.WebFS, "index.html")
}

// --- small utils ---

func defaultInt(value, fallback int) int {
	if value <= 0 {
		return fallback
	}
	return value
}

func defaultStr(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func queryInt(r *http.Request, key string, fallback int) int {
	if value, err := strconv.Atoi(r.URL.Query().Get(key)); err == nil && value > 0 {
		return value
	}
	return fallback
}

// Ids are creation-ordered: base36 nanoseconds behind a type prefix.
func loopID() string { return mintID("loop_") }
func ruleID() string { return mintID("rule_") }

func mintID(prefix string) string {
	return prefix + strconv.FormatInt(time.Now().UnixNano(), 36)
}
