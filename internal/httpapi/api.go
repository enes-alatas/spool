// Package httpapi exposes Spool's REST + SSE surface and serves the web UI.
package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
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
	"github.com/enes-alatas/spool/internal/githubapp"
	"github.com/enes-alatas/spool/internal/gitws"
	"github.com/enes-alatas/spool/internal/loop"
	"github.com/enes-alatas/spool/internal/roomtext"
	"github.com/enes-alatas/spool/internal/route"
	"github.com/enes-alatas/spool/internal/runtime"
	"github.com/enes-alatas/spool/internal/sched"
	"github.com/enes-alatas/spool/internal/store"
	"github.com/enes-alatas/spool/internal/surface"
	"github.com/enes-alatas/spool/internal/users"
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
	// LoginChecker asks the API whether it accepts the default runtime's
	// login, when a setup-token is saved and when the operator asks
	// (ADR-0044). Nil runs no check.
	LoginChecker *loop.LoginChecker
	// PlanCap judges the fleet's plan cap (ADR-0047): the plan-usage read
	// reports it, and a threshold change or a Resume now re-judges it.
	// Required: a serving hub always has one, and the handlers that read
	// it do not check.
	PlanCap *loop.PlanCap
	// Users signs users in and keeps their sessions (ADR-0048). Required:
	// the guard reads every session cookie through it.
	Users *users.Users
	// Surfaces are the chat platforms loops can be reachable on (ADR-0029),
	// by kind (store.SurfaceTelegram, store.SurfaceSlack). A kind the hub
	// runs without is absent, and an empty map is a hub with none.
	Surfaces  map[string]surface.Surface
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
	// per-loop bare escape hatch ADR-0017 describes. A
	// bare loop runs uncontained under the operator's own account, so asking
	// for one is a decision taken when the hub is started, by the person at
	// the terminal — not one the control room can make later on their behalf
	// (ADR-0017, #240). Loops created before it was set keep running: this
	// gates creation, not execution.
	BareAllowed bool
	// RuntimeAvailable answers whether a runtime kind can host a new loop
	// right now; wired in cmd so this package stays free of runtime imports.
	RuntimeAvailable func(ctx context.Context, kind string) error
	// LoopListenerReachable answers whether a docker workstation can reach
	// the loop listener; an error is the reason, naming the fix (#474). A
	// docker loop that cannot would come up looking healthy and never talk.
	LoopListenerReachable func(ctx context.Context) error
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
	// Egress is the docker runtime's egress wall, which the operator's
	// extra hosts are applied to (#542). Nil on a hub that wires none: the
	// list is still stored and served, and applies to nothing.
	Egress EgressWall
	// CloudListener is the cloud listener's switch (ADR-0056). Nil
	// on a hub that wires none: the page reads it as off and can't open it.
	CloudListener CloudListener
	// CloudHooks takes the posts of cloud loops' hooks on the cloud
	// listener (ADR-0059). Nil on a hub that wires none: the route is 404.
	CloudHooks CloudHooks
	// CloudLoopbackAddress accepts a plain-HTTP loopback public address,
	// which no cloud session can reach. Only tests set it
	// (--cloud-loopback-address).
	CloudLoopbackAddress bool
	// CloudGate is told each check of the public address, which wakes or
	// holds the cloud loops, and asks for one before a cloud loop wakes
	// (ADR-0056). Nil records nowhere.
	CloudGate *loop.CloudGate
	// CloudSignIn runs the CLI's login for the account cloud loops spend,
	// and CloudAccounts checks it (ADR-0056). Nil on a hub that wires
	// none: the page can't sign in.
	CloudSignIn   CloudSignIn
	CloudAccounts CloudAccountChecker
	// ClaudeAPIBase is Anthropic's API, which the Claude proxy forwards a
	// contained loop's model requests to (ADR-0058).
	ClaudeAPIBase string
	// LoginLocks holds a Claude login while it is replaced or removed, so a
	// refresh of its token never writes back over the change (ADR-0058,
	// ADR-0060). Nil holds nothing.
	LoginLocks LoginLocks
	// CloudEnvironments lists the account's cloud environments for
	// Settings → Cloud's pick (ADR-0059). Nil on a hub that wires none:
	// the page falls back to a pasted ID.
	CloudEnvironments CloudEnvironments
	// CloudSessions reads each cloud loop's session link. Nil on a hub
	// that wires none: the view carries no link.
	CloudSessions CloudSessions
	// GitHubApps mints the installation tokens of github-app connections
	// (#887). Nil on a hub that wires none: the token route answers 404,
	// and a new key or a revoke has no cached token to drop.
	GitHubApps *githubapp.Minter
	// EgressFlag is --egress-allow as this hub was started with it, which
	// seeded the stored list on the first start (#542); nil when the hub
	// was started without the flag.
	EgressFlag []string
	Log        *slog.Logger
	WebFS      fs.FS // embedded UI dist; may be nil in dev

	// settingsMu serializes the read-validate-write of paired settings, so
	// two concurrent PUTs cannot interleave into an inverted stored pair.
	settingsMu sync.Mutex
	// rulesMu serializes fleet-rule writes: the section cap is checked
	// against the whole enabled set, so two writes must not interleave
	// between the check and the store.
	rulesMu sync.Mutex
	// egressMu serializes changes to the extra egress hosts: each reads the
	// stored list before writing it, and the proxy must take them in the
	// order they were stored.
	egressMu sync.Mutex
	// attachMu serializes changes to which env-vars and github-apps a loop
	// holds: creating a private env-var and attaching a connection each
	// read the loop's connections before writing, and two interleaving
	// could leave two env-vars setting one variable, or two GitHub
	// identities.
	attachMu sync.Mutex
	// cloudEnvironmentMu serializes changes to the chosen environment: a
	// confirm reads it and the list to paste before it writes.
	cloudEnvironmentMu sync.Mutex
	// cloudChecks are the address-check values issued and not yet seen
	// back on the cloud listener (ADR-0056).
	cloudChecks cloudChecks
	// cloudCheck is the last check of the public address.
	cloudCheck cloudCheckState
	// cloudAccountCheck is the last account check.
	cloudAccountCheck cloudAccountCheckState
}

func (server *Server) Handler() http.Handler {
	// Every /api route is behind the guard: a route that is added later and
	// forgets to authenticate is the failure this shape makes impossible
	// (#239).
	return server.guard(server.routes().ServeMux)
}

// routes registers every route the operator listener serves.
func (server *Server) routes() *routeMux {
	mux := &routeMux{ServeMux: http.NewServeMux(), wrap: server.recordingChange}

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
	mux.HandleFunc("POST /api/loops/{name}/rehome", server.handleRehome)
	mux.HandleFunc("POST /api/loops/{name}/workstation/restart", server.handlePower(loop.PowerRestart))
	mux.HandleFunc("POST /api/loops/{name}/workstation/poweroff", server.handlePower(loop.PowerOff))
	mux.HandleFunc("POST /api/loops/{name}/workstation/poweron", server.handlePower(loop.PowerOn))
	mux.HandleFunc("POST /api/loops/{name}/workstation/recreate", server.handlePower(loop.PowerRecreate))
	mux.HandleFunc("POST /api/loops/{name}/problems/{symptom}/acknowledge", server.handleAcknowledge)
	mux.HandleFunc("POST /api/loops/{name}/message", server.handleLoopMessage)
	mux.HandleFunc("GET /api/loops/{name}/events", server.handleLoopEvents)
	mux.HandleFunc("GET /api/loops/{name}/turns", server.handleLoopTurns)
	mux.HandleFunc("GET /api/loops/{name}/telegram/status", server.handleTelegramStatus)
	mux.HandleFunc("GET /api/loops/{name}/slack/status", server.handleSlackStatus)
	mux.HandleFunc("PUT /api/loops/{name}/owner", server.handlePutOwner)
	mux.HandleFunc("GET /api/loops/{name}/rooms", server.handleListRooms)
	mux.HandleFunc("PUT /api/loops/{name}/rooms", server.handlePutRoom)
	mux.HandleFunc("DELETE /api/loops/{name}/rooms/{surface}/{room}", server.handleDeleteRoom)
	mux.HandleFunc("GET /api/activity", server.handleActivity)
	mux.HandleFunc("GET /api/changes", server.handleChanges)
	mux.HandleFunc("GET /api/attachments/{id}", server.handleAttachment)
	mux.HandleFunc("POST "+uploadPath, server.handleUpload)
	mux.HandleFunc("GET /api/undelivered", server.handleUndelivered)
	mux.HandleFunc("POST /api/messages/{id}/retry", server.handleRetrySend)
	mux.HandleFunc("POST /api/messages/{id}/dismiss", server.handleDismissSend)
	mux.HandleFunc("GET /api/loops/{name}/conversation", server.handleLoopConversation)
	mux.HandleFunc("GET /api/loops/{name}/conversations", server.handleLoopConversations)
	mux.HandleFunc("GET /api/group", server.handleGroupTimeline)
	mux.HandleFunc("POST /api/group", server.handleGroupPost)
	mux.HandleFunc("GET /api/connections", server.handleListConnections)
	mux.HandleFunc("POST /api/connections", server.handleCreateConnection)
	mux.HandleFunc("GET /api/connections/{name}", server.handleGetConnection)
	mux.HandleFunc("DELETE /api/connections/{name}", server.handleDeleteConnection)
	mux.HandleFunc("POST /api/connections/{name}/share", server.handleShareConnection)
	mux.HandleFunc("PUT /api/connections/{name}/secret", server.handleRotateConnection)
	mux.HandleFunc("POST /api/connections/{name}/revoke", server.handleRevokeConnection)
	mux.HandleFunc("GET /api/connections/{name}/events", server.handleConnectionEvents)
	mux.HandleFunc("GET /api/loops/{name}/connection-events", server.handleLoopConnectionEvents)
	mux.HandleFunc("PUT /api/loops/{name}/connections/{connection}", server.handleLoopConnection(true))
	mux.HandleFunc("DELETE /api/loops/{name}/connections/{connection}", server.handleLoopConnection(false))
	mux.HandleFunc("GET /api/channels", server.handleListChannels)
	mux.HandleFunc("POST /api/channels", server.handleCreateChannel)
	mux.HandleFunc("GET /api/channels/{name}", server.handleGetChannel)
	mux.HandleFunc("GET /api/channels/{name}/messages", server.handleChannelMessages)
	mux.HandleFunc("POST /api/channels/{name}/messages", server.handleChannelPost)
	mux.HandleFunc("PATCH /api/channels/{name}", server.handlePatchChannel)
	mux.HandleFunc("DELETE /api/channels/{name}", server.handleDeleteChannel)
	mux.HandleFunc("PUT /api/channels/{name}/loops/{loop}", server.handleChannelLoop(true))
	mux.HandleFunc("DELETE /api/channels/{name}/loops/{loop}", server.handleChannelLoop(false))
	mux.HandleFunc("GET /api/settings", server.handleGetSettings)
	mux.HandleFunc("GET /api/onboarding", server.handleOnboarding)
	mux.HandleFunc("GET /api/plan-usage", server.handlePlanUsage)
	mux.HandleFunc("POST /api/plan-cap/resume", server.handleResumePlanCap)
	mux.HandleFunc("POST /api/onboarding/harness-check", server.handleLoginCheck)
	mux.HandleFunc("POST /api/onboarding/skip-surface", server.handleSkipSurface)
	mux.HandleFunc("PUT /api/settings", server.handlePutSettings)
	mux.HandleFunc("GET /api/settings/egress", server.handleGetEgress)
	mux.HandleFunc("POST /api/settings/egress/hosts", server.handleAddEgressHost)
	mux.HandleFunc("DELETE /api/settings/egress/hosts/{entry}", server.handleRemoveEgressHost)
	mux.HandleFunc("GET /api/settings/cloud", server.handleGetCloudSettings)
	mux.HandleFunc("PUT /api/settings/cloud/address", server.handleSetCloudAddress)
	mux.HandleFunc("DELETE /api/settings/cloud/address", server.handleClearCloudAddress)
	mux.HandleFunc("POST /api/settings/cloud/check", server.handleCheckCloudAddress)
	mux.HandleFunc("PUT /api/settings/cloud/listener", server.handleSwitchCloudListener)
	mux.HandleFunc("POST /api/settings/cloud/account/sign-in", server.handleStartCloudSignIn)
	mux.HandleFunc("POST /api/settings/cloud/account/code", server.handleFinishCloudSignIn)
	mux.HandleFunc("DELETE /api/settings/cloud/account", server.handleRemoveCloudAccount)
	mux.HandleFunc("POST /api/settings/cloud/account/check", server.handleCheckCloudAccount)
	mux.HandleFunc("GET /api/settings/cloud/environments", server.handleListCloudEnvironments)
	mux.HandleFunc("PUT /api/settings/cloud/environment", server.handleChooseCloudEnvironment)
	mux.HandleFunc("DELETE /api/settings/cloud/environment", server.handleClearCloudEnvironment)
	mux.HandleFunc("POST /api/settings/cloud/environment/confirm", server.handleConfirmCloudAllowlist)
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
	mux.HandleFunc("GET /api/slack/senders", server.handleListSlackSenders)
	mux.HandleFunc("POST /api/slack/senders/{id}/allow", server.handleSlackSenderStatus(store.SenderAllowed))
	mux.HandleFunc("POST /api/slack/senders/{id}/block", server.handleSlackSenderStatus(store.SenderBlocked))
	mux.HandleFunc("DELETE /api/slack/senders/{id}", server.handleDeleteSlackSender)
	mux.HandleFunc("POST /api/workspace/inspect", server.handleWorkspaceInspect)
	mux.HandleFunc("GET /api/stream", server.handleGlobalStream)
	mux.HandleFunc("GET /api/loops/{name}/stream", server.handleLoopStream)
	// The two routes that establish and end a session rather than use one;
	// the guard lets them past the credential check and nothing else.
	mux.HandleFunc("POST /api/login", server.handleLogin)
	mux.HandleFunc("POST /api/logout", server.handleLogout)
	mux.HandleFunc("GET "+mePath, server.handleMe)
	mux.HandleFunc("POST "+mePasswordPath, server.handleMePassword)
	mux.HandleFunc("GET "+meLinksPath, server.handleListLinks)
	mux.HandleFunc("POST "+meLinkCodePath, server.handleLinkCode)
	mux.HandleFunc("GET "+meLinksWaitingPath, server.handleListWaitingLinks)
	mux.HandleFunc("DELETE "+meLinksPath+"/{surface}", server.handleRemoveLink)
	mux.HandleFunc("GET "+linkChangesPath, server.handleLinkChanges)
	// The hub's users, which only an owner manages (ADR-0048).
	mux.HandleFunc("GET /api/users", server.handleListUsers)
	mux.HandleFunc("POST /api/users", server.handleAddUser)
	mux.HandleFunc("POST /api/users/{name}/reset", server.handleResetUser)
	mux.HandleFunc("PATCH /api/users/{name}", server.handleSetUserRole)
	mux.HandleFunc("DELETE /api/users/{name}", server.handleRemoveUser)
	mux.HandleFunc("DELETE /api/users/{name}/links/{surface}", server.handleRemoveUserLink)
	mux.HandleFunc("POST /api/users/{name}/block", server.handleUserSenders(store.SenderBlocked))
	mux.HandleFunc("POST /api/users/{name}/unblock", server.handleUserSenders(store.SenderAllowed))
	mux.HandleFunc("GET /api/strangers", server.handleIgnoredStrangers)
	mux.HandleFunc("DELETE /api/strangers/{surface}/pause", server.handleLiftLinkPause)

	// /mcp and the brokered servers under it live on the loop listener
	// alone (#238, #622). Saying so explicitly matters because of what is
	// registered next: the UI's catch-all would otherwise answer these paths
	// with index.html in a build that embeds the control room, so the
	// endpoints would look moved in tests and alive in production.
	mux.HandleFunc("/mcp", http.NotFound)
	mux.HandleFunc("/mcp/", http.NotFound)

	// Registered in every build, not just one that embeds the control
	// room, so tier 2 sees what the shipped binary answers (#245).
	mux.HandleFunc("/api/", server.apiFallback(mux.ServeMux))
	if server.WebFS != nil {
		mux.HandleFunc("/", server.handleUI)
	}
	return mux
}

// MCPHandler is the loop-facing half of the hub, served on its own listener
// (#238). Workstations are allowed to reach this port and no other, so the
// operator's API and control room are unreachable from inside a workstation
// at the network layer, before any question of authentication. Nothing but
// the MCP endpoint is routed here: every other path is 404 and never reaches
// the API mux.
func (server *Server) MCPHandler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/mcp", server.mcpHandler(everyLoop))
	mux.Handle(loop.BrokerPath, server.brokerHandler())
	return mux
}

// --- helpers ---

// jsonErr answers code with msg, the hub's sentence, filled in with args.
func (server *Server) jsonErr(w http.ResponseWriter, code int, msg string, args ...any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": roomSentence(msg, args...)})
}

// roomSentence fills in a sentence the room shows. The format is the hub's
// words, so its backticks mark what the operator types (#871); a string
// argument is a value it interpolates, a name or a host, so its backticks
// are taken out (#959). A bare "%v" or "%s" is the exception: its one
// argument is the whole sentence. An error passes whole too, since it may be
// the hub's own remedy with its marks; outside text in an error is plained
// where it comes in, as the platform and docker errors do.
func roomSentence(format string, args ...any) string {
	if format == "%v" || format == "%s" {
		return fmt.Sprintf(format, args...)
	}
	plain := make([]any, len(args))
	for i, arg := range args {
		if value, ok := arg.(string); ok {
			plain[i] = roomtext.Plain(value)
		} else {
			plain[i] = arg
		}
	}
	return fmt.Sprintf(format, plain...)
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
	json.NewEncoder(w).Encode(map[string]string{"error": roomSentence(msg, args...), "code": reason})
}

// Reasons behind an otherwise ambiguous status, sent as "code".
const (
	codeLoopNotRunning = "loop_not_running"
	codeNoWorkstation  = "no_workstation"
	codeBareNotEnabled = "bare_runtime_not_enabled"
	// A docker loop on a hub whose loop listener its workstation cannot
	// reach (#474).
	codeLoopListenerUnreachable = "loop_listener_unreachable"
	// A cloud loop on a hub whose Settings → Cloud is not green (ADR-0056).
	codeCloudNotReady = "cloud_not_ready"
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
	// CostTodayEstimated is whether CostToday includes a cost the hub
	// estimated, a cloud turn's, and CostTodayUnknownTurns how many of
	// the day's turns it leaves out because their cost is unknown
	// (ADR-0059).
	CostTodayEstimated    bool `json:"cost_today_estimated"`
	CostTodayUnknownTurns int  `json:"cost_today_unknown_turns"`
	// Undelivered counts this loop's messages that never reached their
	// surface and that nobody has dealt with: no successful retry, no
	// dismissal (#269). No age limit — a failure stops counting when
	// someone resolves it, not when a clock decides the operator is done
	// looking. Always present, so a measured zero is distinguishable from a
	// server too old to measure — the lesson of context_fill_pct (#122).
	Undelivered int  `json:"undelivered"`
	HasTGToken  bool `json:"has_tg_token"`
	// HasSlackTokens reports a stored Slack app; the tokens themselves are
	// write-only, like the Telegram token.
	HasSlackTokens bool `json:"has_slack_tokens"`
	// Surface is the platform the loop has an identity on: "telegram",
	// "slack", or "" for none (ADR-0029).
	Surface       string `json:"surface"`
	WorkstationUp bool   `json:"workstation_up"`
	// Rehoming is true from a rehome's 202 until the move lands or fails
	// (#632). It is the actor's, not the row's, so it is false for a loop
	// with no running actor, and after a restart that lost the request.
	Rehoming          bool   `json:"rehoming"`
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
	// DownReason says why the workstation is not up, empty while it is:
	// powered_off, not_provisioned, unauthenticated, unreachable or
	// hub_unreachable (ADR-0021).
	DownReason string `json:"down_reason"`
	// CappedUntil is when a capped loop wakes: the latest reset among the
	// plan windows over their thresholds (ADR-0047), or the next local day
	// for a loop at its own daily cap (#652), whichever is later. 0 for a
	// loop whose state is not capped, even while a cap holds, since a loop
	// in its turn is still busy.
	CappedUntil int64 `json:"capped_until"`
	// DailyCapReached is whether the loop's own daily cap holds, so a capped
	// loop's page can say which cap it is under. It is true for a loop in
	// its turn too, which a cap takes at the turn's end.
	DailyCapReached bool `json:"daily_cap_reached"`
	// Problems are the open problems the health check found with the loop,
	// oldest first, and Health sums them up for the Fleet row: ok with
	// none, failing while the workstation is down, and otherwise warning
	// (ADR-0053).
	Problems []store.Problem `json:"problems"`
	Health   string          `json:"health"`
	// OwnerDMReady reports that the loop can message its owner privately:
	// an owner is configured and, on Telegram, has opened a chat with this
	// loop's own bot. A bot cannot open one, so until the owner writes
	// there is nowhere to send (#73). A Slack app opens the DM itself.
	OwnerDMReady bool `json:"owner_dm_ready"`
	// OwnerUsername is the configured owner's telegram handle, when known —
	// the name the UI shows instead of a numeric id.
	OwnerUsername string `json:"owner_username,omitempty"`
	// OwnerUser is the name of the user linked as the owner, and "" when
	// the owner is a sender no user is linked as: not linked (#731).
	OwnerUser string `json:"owner_user"`
	// InFleetChannel reports that the loop has a group: it receives what
	// addresses it there and may post to it (ADR-0032).
	InFleetChannel bool `json:"in_fleet_channel"`
	// Connections are the connections attached to the loop, name-sorted,
	// by name and kind only: never a value (ADR-0043).
	Connections []loopConnectionView `json:"connections"`
	// MCPSessionID, MCPServers and ToolCount are what the loop's latest
	// session init in this hub run said it can reach through MCP (#489),
	// all three absent before one. ToolCount counts MCP tools only.
	MCPSessionID string           `json:"mcp_session_id,omitempty"`
	MCPServers   *[]mcpServerView `json:"mcp_servers,omitempty"` // [] for an init with none
	ToolCount    *int             `json:"tool_count,omitempty"`
	// CloudSessionURL is a cloud loop's current session on claude.ai,
	// absent before its first session (ADR-0056).
	CloudSessionURL string `json:"cloud_session_url,omitempty"`
	// CloudTurn is a cloud loop's turn that may be running in a session,
	// absent when the hub knows of none (ADR-0059).
	CloudTurn *CloudTurn `json:"cloud_turn,omitempty"`
	// CloudAllowlistUnconfirmed is set on a cloud loop while the operator
	// hasn't confirmed pasting the current allowlist into the environment:
	// the hub's egress allowlist doesn't bind the loop, only what its
	// environment allows (ADR-0059).
	CloudAllowlistUnconfirmed bool `json:"cloud_allowlist_unconfirmed,omitempty"`
}

// loopConnectionView is one connection a loop holds.
type loopConnectionView struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
}

// mcpServerView is one MCP server as the session's init reported it.
type mcpServerView struct {
	Name      string `json:"name"`
	Status    string `json:"status"`
	ToolCount int    `json:"tool_count"`
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
	start, _ := loop.LocalDay(now)
	return start.UnixMilli(), start.Format("2006-01-02")
}

func (server *Server) view(ctx context.Context, loopRecord *store.Loop) *loopView {
	out := &loopView{Loop: loopRecord, State: loop.StateAsleep, HasTGToken: loopRecord.TGBotToken != "",
		HasSlackTokens: loopRecord.SlackBotToken != "", Surface: loopRecord.Surface(), WorkstationUp: true,
		OwnerDMReady: loopRecord.OwnerDMReady(), InFleetChannel: !loopRecord.OutsideFleetChannel,
		Connections: []loopConnectionView{}, Problems: []store.Problem{}, Health: loop.HealthOK}
	if connections, err := server.Store.Connections().ListByLoop(ctx, loopRecord.ID); err == nil {
		for _, connection := range connections {
			out.Connections = append(out.Connections, loopConnectionView{Name: connection.Name, Kind: connection.Kind})
		}
	}
	if loopRecord.OwnerTGUserID != 0 {
		if sender, err := server.Store.TGSenders().Get(ctx, loopRecord.OwnerTGUserID); err == nil {
			out.OwnerUsername = sender.Username
		}
	}
	if loopRecord.Surface() == store.SurfaceSlack && loopRecord.OwnerSlackUserID != "" {
		if sender, err := server.Store.SlackSenders().Get(ctx, loopRecord.OwnerSlackUserID); err == nil {
			out.OwnerUsername = defaultStr(sender.Display, sender.Username)
		}
	}
	if owner := shownOwner(loopRecord); owner != "" {
		if linked, err := server.Store.UserLinks().LinkedUsers(ctx); err == nil {
			out.OwnerUser = linked[owner]
		}
	}
	if actor, ok := server.Manager.Get(loopRecord.ID); ok {
		out.State = actor.State()
		out.Rehoming = actor.Rehoming()
		health := actor.WorkstationHealth()
		out.WorkstationUp = health.Up
		out.WorkstationDetail = health.Detail
		out.DownReason = actor.DownReason()
		if problems := actor.Problems(); len(problems) > 0 {
			out.Problems, out.Health = problems, loop.Health(problems)
		}
		dailyUntil := actor.DailyCapUntil()
		out.DailyCapReached = dailyUntil > 0
		if out.State == loop.StateCapped {
			nowMS := time.Now().UnixMilli()
			out.CappedUntil = dailyUntil
			if server.PlanCap != nil {
				if planCap := server.PlanCap.At(nowMS); planCap.Holds(nowMS) {
					out.CappedUntil = max(out.CappedUntil, planCap.Until)
				}
			}
		}
		if reach, ok := actor.MCPReach(); ok {
			out.MCPSessionID, out.ToolCount = reach.SessionID, &reach.ToolCount
			servers := []mcpServerView{}
			for _, server := range reach.Servers {
				servers = append(servers, mcpServerView{server.Name, server.Status, server.ToolCount})
			}
			out.MCPServers = &servers
		}
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
	if spend, err := server.Store.Turns().CostSince(ctx, loopRecord.ID, dayStart); err == nil {
		out.CostToday, out.CostTodayEstimated, out.CostTodayUnknownTurns = spend.USD, spend.Estimated, spend.UnknownTurns
	}
	if loopRecord.Runtime == store.RuntimeCloud && server.CloudSessions != nil {
		out.CloudSessionURL = server.CloudSessions.SessionURL(loopRecord.ID)
		out.CloudTurn = server.CloudSessions.Turn(loopRecord.ID)
	}
	if loopRecord.Runtime == store.RuntimeCloud {
		out.CloudAllowlistUnconfirmed = server.cloudAllowlistUnconfirmed(ctx)
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

// shownOwner is the owner the loop view names, as TGPerson or SlackPerson
// key them: the Slack owner on a Slack loop, and otherwise the Telegram
// one, which a loop not yet on a surface carries by default.
func shownOwner(loopRecord *store.Loop) string {
	switch {
	case loopRecord.Surface() == store.SurfaceSlack:
		return loopRecord.OwnerPerson()
	case loopRecord.OwnerTGUserID != 0:
		return store.TGPerson(loopRecord.OwnerTGUserID)
	}
	return ""
}

// --- handlers ---

// handleVersion names the build. Unauthenticated like health: everything
// here is already in the binary anyone asking can run, and an operator
// filing a bug should not have to find a token to say which Spool it was.
func (server *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, server.Build)
}

func (server *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	// Liveness and nothing else: this answers anyone who can reach the port.
	// Facts about the host (its default runtime, its claude CLI) are the
	// operator's, and live in settings (#258).
	writeJSON(w, 200, map[string]any{"ok": true})
}

// loopChanged tells every surface a loop's configuration changed. Every one,
// not the loop's own: a detach from one surface and an attach to another are
// both changes, and whether an edit concerns a surface is the surface's call
// (ADR-0029).
func (server *Server) loopChanged(ctx context.Context, loopID string) {
	for _, loopSurface := range server.Surfaces {
		loopSurface.LoopChanged(ctx, loopID)
	}
}

// defaultRuntime is the kind a loop is created as when its request names
// none: the one the hub was started with, bare when it was given none.
func (server *Server) defaultRuntime() string {
	return defaultStr(server.DefaultRuntime, store.RuntimeBare)
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
	Subagents       string  `json:"subagents"`      // ""(=allowed)|allowed|off
	DailyCapUSD     float64 `json:"daily_cap_usd"`  // 0 = no cap
	Runtime         string  `json:"runtime"`        // ""(=server default)|bare|docker|cloud; set at creation; a bare loop moves to docker only through POST .../rehome (ADR-0018)
	Image           string  `json:"image"`          // docker only; "" = the server's default image
	MemMB           int     `json:"mem_mb"`         // docker only; 0 = 4096 (ADR-0017)
	CPUs            float64 `json:"cpus"`           // docker only; 0 = 2
	WorkspacePath   string  `json:"workspace_path"` // bare only; empty = no workspace
	WorkspaceMode   string  `json:"workspace_mode"` // "auto" (default) | "dir" | "none"
	CloudRepo       string  `json:"cloud_repo"`     // refused: the repo field went with the sessions client (ADR-0059)
	TickIntervalSec int     `json:"tick_interval_sec"`
	MinWakeSec      int     `json:"min_wake_sec"`
	MaxWakeSec      int     `json:"max_wake_sec"`
	IdleTimeoutSec  int     `json:"idle_timeout_sec"`
	TGBotToken      string  `json:"tg_bot_token"`
	// SlackAppToken and SlackBotToken give the new loop a Slack app instead
	// of a Telegram bot; both or neither.
	SlackAppToken string `json:"slack_app_token"`
	SlackBotToken string `json:"slack_bot_token"`
	// InFleetChannel places the new loop in the fleet channel or outside
	// it; absent, the server decides (defaultInFleetChannel).
	InFleetChannel *bool `json:"in_fleet_channel"`
}

var validEfforts = map[string]bool{"": true, "low": true, "medium": true, "high": true, "xhigh": true, "max": true}

func validPacing(pacing string) bool {
	return pacing == "" || pacing == store.PacingFixed || pacing == store.PacingSelf
}

// validSubagents reports whether subagents is a loop's setting, or empty
// for the default.
func validSubagents(subagents string) bool {
	return subagents == "" || subagents == store.SubagentsAllowed || subagents == store.SubagentsOff
}

// subagentsRefusal says why a loop on loopRuntime cannot have subagents
// set as asked, or "" when it can. A cloud loop's claude runs on Anthropic's machine,
// started by a command line that carries no tool list, so the hub could
// store "off" and not enforce it (#855).
func subagentsRefusal(subagents, loopRuntime string) string {
	if subagents == store.SubagentsOff && loopRuntime == store.RuntimeCloud {
		return "subagents can't be turned off on a cloud loop: the hub starts its session with no say over its tools"
	}
	return ""
}

// maxDailyCapUSD bounds a loop's daily cap: past it a cap is a typo, not
// a limit anyone means.
const maxDailyCapUSD = 100000

// dailyCapRefusal says why capUSD can't be a loop's daily cap, or "" when
// it can (#652).
func dailyCapRefusal(capUSD float64) string {
	if capUSD < 0 || capUSD > maxDailyCapUSD {
		return fmt.Sprintf("daily_cap_usd must be between 0 (no cap) and %d", maxDailyCapUSD)
	}
	return ""
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
	if !validSubagents(req.Subagents) {
		server.jsonErr(w, 400, "subagents must be 'allowed' or 'off'")
		return
	}

	loopRuntime := defaultStr(req.Runtime, server.defaultRuntime())
	switch loopRuntime {
	case store.RuntimeBare:
		if !server.BareAllowed {
			server.jsonErrCode(w, 400, codeBareNotEnabled,
				"a bare loop runs uncontained on this machine; start spool with `--allow-bare` (or `--runtime bare`) to allow one")
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
		if server.LoopListenerReachable != nil {
			if err := server.LoopListenerReachable(r.Context()); err != nil {
				server.jsonErrCode(w, 400, codeLoopListenerUnreachable, "%v", err)
				return
			}
		}
	case store.RuntimeCloud:
		if req.Image != "" || req.MemMB != 0 || req.CPUs != 0 || req.WorkspacePath != "" {
			server.jsonErr(w, 400, "a cloud loop runs on Anthropic's machine; image, mem_mb, cpus and workspace_path don't apply")
			return
		}
		if err := server.cloudReady(r.Context()); err != nil {
			server.jsonErrCode(w, 409, codeCloudNotReady, "%v", err)
			return
		}
	default:
		server.jsonErr(w, 400, "runtime must be 'bare', 'docker' or 'cloud' (or empty for the server default)")
		return
	}
	if refusal := subagentsRefusal(req.Subagents, loopRuntime); refusal != "" {
		server.jsonErr(w, 400, "%s", refusal)
		return
	}
	if refusal := dailyCapRefusal(req.DailyCapUSD); refusal != "" {
		server.jsonErr(w, 400, "%s", refusal)
		return
	}
	if strings.TrimSpace(req.CloudRepo) != "" {
		server.jsonErr(w, 400, "cloud_repo is gone: attach a GH_TOKEN connection to the loop and name the repo in its mission")
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
		Subagents:       defaultStr(req.Subagents, store.SubagentsAllowed),
		DailyCapUSD:     req.DailyCapUSD,
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

	if telegram := server.Surfaces[store.SurfaceTelegram]; loopRecord.TGBotToken != "" && telegram != nil {
		identity, err := telegram.ValidateCredential(r.Context(), surface.Credential{Token: loopRecord.TGBotToken})
		if err != nil {
			server.jsonErr(w, 400, "telegram token rejected: %v", err)
			return
		}
		loopRecord.TGBotUsername = identity.Name
	}
	if strings.TrimSpace(req.SlackAppToken) != "" || strings.TrimSpace(req.SlackBotToken) != "" {
		if loopRecord.TGBotToken != "" {
			server.refuse(w, oneSurface(""))
			return
		}
		identity, refusal := server.slackIdentity(r.Context(), loopRecord.ID, req.SlackAppToken, req.SlackBotToken)
		if refusal != nil {
			server.refuse(w, refusal)
			return
		}
		loopRecord.SlackAppToken, loopRecord.SlackBotToken = identity.AppToken, identity.BotToken
		loopRecord.SlackBotUserID, loopRecord.SlackBotName = identity.BotUserID, identity.BotName
		loopRecord.SlackTeamID, loopRecord.SlackTeamName = identity.TeamID, identity.TeamName
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
	// the first allowlisted sender no member's or admin's link allowed,
	// reassignable per loop (#73, ADR-0051).
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
	// token or a Slack app's pair: secret values that did not exist a
	// moment ago.
	server.secretsChanged(r.Context())
	server.Manager.Add(loopRecord)
	server.loopChanged(r.Context(), loopRecord.ID)
	// first tick shortly after creation so the mission starts without waiting
	// a full interval
	server.Sched.ScheduleNow(loopRecord.ID)
	server.recordChange(r, store.LoopChangeCreate, loopRecord)
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
	Mission         *string  `json:"mission"`
	Model           *string  `json:"model"`
	Effort          *string  `json:"effort"`
	Pacing          *string  `json:"pacing"`
	Subagents       *string  `json:"subagents"`
	DailyCapUSD     *float64 `json:"daily_cap_usd"`
	TickIntervalSec *int     `json:"tick_interval_sec"`
	MinWakeSec      *int     `json:"min_wake_sec"`
	MaxWakeSec      *int     `json:"max_wake_sec"`
	IdleTimeoutSec  *int     `json:"idle_timeout_sec"`
	TGBotToken      *string  `json:"tg_bot_token"`
	// SlackAppToken and SlackBotToken attach, rotate or (both "") detach the
	// loop's Slack app. Both or neither.
	SlackAppToken  *string `json:"slack_app_token"`
	SlackBotToken  *string `json:"slack_bot_token"`
	InFleetChannel *bool   `json:"in_fleet_channel"`
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
	if req.Subagents != nil {
		if !validSubagents(*req.Subagents) {
			server.jsonErr(w, 400, "subagents must be 'allowed' or 'off'")
			return
		}
		subagents := defaultStr(*req.Subagents, store.SubagentsAllowed)
		if refusal := subagentsRefusal(subagents, loopRecord.Runtime); refusal != "" {
			server.jsonErr(w, 400, "%s", refusal)
			return
		}
		edit.Subagents = &subagents
	}
	if req.DailyCapUSD != nil {
		if refusal := dailyCapRefusal(*req.DailyCapUSD); refusal != "" {
			server.jsonErr(w, 400, "%s", refusal)
			return
		}
		edit.DailyCapUSD = req.DailyCapUSD
	}
	if req.InFleetChannel != nil {
		outside := !*req.InFleetChannel
		edit.OutsideFleetChannel = &outside
	}
	// One surface per loop (ADR-0029), judged on what the loop would
	// hold after this request and before any live call is made.
	slackGiven := req.SlackAppToken != nil || req.SlackBotToken != nil
	if slackGiven && (req.SlackAppToken == nil || req.SlackBotToken == nil) {
		server.jsonErr(w, 400, "slack_app_token and slack_bot_token go together: one Slack app is one pair")
		return
	}
	holdsTelegram := loopRecord.TGBotToken != ""
	if req.TGBotToken != nil {
		holdsTelegram = strings.TrimSpace(*req.TGBotToken) != ""
	}
	holdsSlack := loopRecord.SlackBotToken != ""
	if slackGiven {
		holdsSlack = strings.TrimSpace(*req.SlackBotToken) != "" || strings.TrimSpace(*req.SlackAppToken) != ""
	}
	if holdsTelegram && holdsSlack {
		server.refuse(w, oneSurface(loopRecord.Surface()))
		return
	}
	if slackGiven {
		identity, refusal := server.slackIdentity(r.Context(), loopRecord.ID, *req.SlackAppToken, *req.SlackBotToken)
		if refusal != nil {
			server.refuse(w, refusal)
			return
		}
		edit.Slack = &identity
		// Detaching leaves no app in any of its rooms. Rotating keeps them,
		// as a replaced Telegram token keeps its groups.
		edit.ClearSlackRooms = identity.BotToken == ""
		// Detaching keeps the owner, for a re-attach in the same workspace.
		// An app from another workspace cannot reach them, so they go.
		edit.ClearSlackOwner = !server.ownerInTeam(r.Context(), loopRecord.OwnerSlackUserID, identity.TeamID)
	}
	if req.TGBotToken != nil {
		token := strings.TrimSpace(*req.TGBotToken)
		username := ""
		if telegram := server.Surfaces[store.SurfaceTelegram]; token != "" && telegram != nil {
			// A live call to api.telegram.org, which is why nothing read
			// before this point may be written back afterwards.
			identity, err := telegram.ValidateCredential(r.Context(), surface.Credential{Token: token})
			if err != nil {
				server.jsonErr(w, 400, "telegram token rejected: %v", err)
				return
			}
			username = identity.Name
		}
		edit.TGBotToken, edit.TGBotUsername = &token, &username
		// A cleared token leaves no bot in any room. A replaced one keeps
		// them as they are: the groups are the same groups.
		edit.ClearTelegramRooms = token == ""
	}
	updated, err := server.Store.Loops().Edit(r.Context(), loopRecord.ID, edit)
	if err != nil {
		server.storeErr(w, err, "loop")
		return
	}
	if req.TGBotToken != nil || slackGiven {
		server.secretsChanged(r.Context())
	}
	// The stored row, not the edited copy: everything this request did not
	// name reaches the actor and the surface as it actually stands.
	server.Manager.UpdateLoop(updated)
	server.loopChanged(r.Context(), updated.ID)
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
			if err := actor.Rotate(store.RotationReasonMission); err == nil {
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
	server.Manager.Remove(loopRecord.ID)
	for _, loopSurface := range server.Surfaces {
		loopSurface.LoopRemoved(loopRecord.ID)
	}
	if r.URL.Query().Get("remove_worktree") == "1" && loopRecord.WorkspaceMode == store.WorkspaceWorktree {
		if err := gitws.Remove(loopRecord.RepoPath, loopRecord.WorktreePath); err != nil {
			server.Log.Warn("worktree remove", "err", err)
		}
	}
	_ = server.Store.Sessions().End(r.Context(), loopRecord.CurrentSessionID, store.EndReasonKilled, time.Now().UnixMilli())
	if err := server.Store.Loops().Delete(r.Context(), loopRecord.ID, time.Now().UnixMilli()); err != nil {
		server.jsonErr(w, 500, "%v", err)
		return
	}
	server.secretsChanged(r.Context()) // the connections only it held went with it
	// The workstation goes last: the loop's hub token went with its
	// record, so a cloud loop's archive calls, up to a minute, or a
	// failed one, leave no token that works (ADR-0056).
	server.Manager.DestroyWorkstation(loopRecord.ID, loopRecord.Runtime)
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
	var cutOff error
	if actor, ok := server.Manager.Get(loopRecord.ID); ok {
		cutOff = actor.Pause()
	}
	server.Sched.Suspend(loopRecord.ID)
	if cutOff != nil {
		server.jsonErr(w, http.StatusInternalServerError, "the loop is paused, but its cloud session's token was not revoked, so the session can still reach the hub; pausing or killing it again retries the revocation: %v", cutOff)
		return
	}
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

// handleAcknowledge clears the loop's open problem for a symptom as the
// operator's doing (ADR-0053): the owner hears that it was, and the
// symptom's watch starts over. Acknowledging a symptom with nothing open
// changes nothing.
func (server *Server) handleAcknowledge(w http.ResponseWriter, r *http.Request) {
	loopRecord := server.loopByName(w, r)
	if loopRecord == nil {
		return
	}
	symptom := r.PathValue("symptom")
	if symptom != store.SymptomWorkstationDown && symptom != store.SymptomTurnErrors && symptom != store.SymptomCloudAddress &&
		symptom != store.SymptomCloudConnections {
		server.jsonErr(w, http.StatusBadRequest, "unknown symptom %q: want %s, %s, %s or %s",
			symptom, store.SymptomWorkstationDown, store.SymptomTurnErrors, store.SymptomCloudAddress, store.SymptomCloudConnections)
		return
	}
	actor, ok := server.Manager.Get(loopRecord.ID)
	if !ok {
		server.jsonErrCode(w, http.StatusConflict, codeLoopNotRunning,
			"loop %s is not running", loopRecord.Name)
		return
	}
	actor.Acknowledge(symptom)
	writeJSON(w, 200, server.view(r.Context(), loopRecord))
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
		if err := actor.Kill(); err != nil {
			server.jsonErr(w, http.StatusInternalServerError, "the loop is killed, but its cloud session's token was not revoked, so the session can still reach the hub; killing it again retries the revocation: %v", err)
			return
		}
	}
	writeJSON(w, 200, map[string]bool{"killed": true})
}

// handleRotate queues an operator-asked context rotation (ADR-0022): the
// loop writes its handoff note at the next quiet boundary and continues on
// a fresh session seeded from it. It answers 202 before the rotation lands;
// the loop's current_session_id changing says it has.
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
	if err := actor.Rotate(store.RotationReasonOperator); err != nil {
		server.jsonErr(w, 409, "%v", err)
		return
	}
	writeJSON(w, 202, map[string]bool{"rotating": true})
}

// notCarried is what a rehome leaves on the host, for the operator to give
// the workstation another way if the loop needs it: through the loop's
// secrets and connections, or the workstation's image (#624).
var notCarried = []string{
	"files on the host outside the auto-memory, the workspace among them",
	"SSH keys, git and gh credentials, and every other login on the host",
	"the host user's Claude settings, CLAUDE.md, skills, agents, plugins and MCP servers",
	"tools installed on the host",
}

// handleRehome moves a bare loop into a docker workstation (#624). The move
// lands at the rotation it asks for, after the loop's handoff turn, so the
// answer is the promise; a loop with no session moves before it is given.
func (server *Server) handleRehome(w http.ResponseWriter, r *http.Request) {
	loopRecord := server.loopByName(w, r)
	if loopRecord == nil {
		return
	}
	if loopRecord.Runtime != store.RuntimeBare && loopRecord.Runtime != "" {
		server.jsonErr(w, 409, "%v", loop.ErrNotBare)
		return
	}
	if server.RuntimeAvailable != nil {
		if err := server.RuntimeAvailable(r.Context(), store.RuntimeDocker); err != nil {
			server.jsonErr(w, 400, "docker runtime unavailable: %v", err)
			return
		}
	}
	if server.LoopListenerReachable != nil {
		if err := server.LoopListenerReachable(r.Context()); err != nil {
			server.jsonErrCode(w, 400, codeLoopListenerUnreachable, "%v", err)
			return
		}
	}
	actor, ok := server.Manager.Get(loopRecord.ID)
	if !ok {
		server.jsonErr(w, 409, "loop has no running actor")
		return
	}
	if err := actor.Rehome(); err != nil {
		server.jsonErr(w, 409, "%v", err)
		return
	}
	server.loopChanged(r.Context(), loopRecord.ID)
	writeJSON(w, 202, map[string]any{
		"rehoming":    true,
		"left_behind": loopRecord.WorkspacePath,
		"not_carried": notCarried,
	})
}

type postMessageReq struct {
	Author string `json:"author"`
	Text   string `json:"text"`
	// Destination is the composer's declared destination (ADR-0026):
	// control_room (the default) keeps the message in the loop's web
	// thread; group posts it to the shared group conversation.
	Destination string `json:"destination"`
	// AttachmentID is a file uploaded to POST /api/attachments to send
	// with the message (#460), 0 for none.
	AttachmentID int64 `json:"attachment_id"`
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
		Author:       authorOf(r, req.Author),
		Text:         req.Text,
		ImplicitTo:   loopRecord.ID,
		Conversation: dest,
		UploadID:     req.AttachmentID,
		UploadSender: uploadSenderOf(callerOf(r)),
	})
	if err != nil {
		server.ingestErr(w, err)
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
	if telegram := server.Surfaces[store.SurfaceTelegram]; telegram != nil {
		status["bridge"] = telegram.Status(loopRecord.ID)
	}
	writeJSON(w, 200, status)
}

// --- operator settings (ADR-0017: stored server-side, presence-only on read) ---

// settingsView reports operator settings — secrets as presence only (the
// Claude setup-token is write-only, like a loop's bot token), plain values
// as themselves. The rotation thresholds are always the effective values,
// defaults included.
type settingsView struct {
	// ClaudeTokenSet is whether the hub has a Claude token for contained
	// loops to run on: a Claude login, or a setup-token while none is
	// signed in (ADR-0058).
	ClaudeTokenSet      bool `json:"claude_token_set"`
	ContextArmPercent   int  `json:"context_arm_percent"`
	ContextForcePercent int  `json:"context_force_percent"`
	// The plan cap's thresholds, percentages of each plan window with 0 for
	// off, the defaults included (ADR-0047).
	PlanCapFiveHourPercent int `json:"plan_cap_five_hour_percent"`
	PlanCapSevenDayPercent int `json:"plan_cap_seven_day_percent"`
	// HealthErrorTurns is how many errored turns in a row tell a loop's
	// owner, 0 for never, the default included (ADR-0053).
	HealthErrorTurns int `json:"health_error_turns"`
	// BareAllowed is how the control room knows whether to offer an
	// uncontained loop at all (#255): the choice is the operator's, taken
	// at the terminal when the hub was started, and a form that offered it
	// anyway would be offering a create the API refuses (#254).
	BareAllowed bool `json:"bare_allowed"`
	// DefaultRuntime is the kind a loop gets when a create request names
	// none: the value handleCreateLoop falls back to, so New loop can start
	// its runtime choice where the API would land anyway (#255).
	DefaultRuntime string `json:"default_runtime"`
	// ClaudeVersion is the `claude` CLI this hub found at start, "" when it
	// found none. It describes the host rather than its liveness, so it is
	// served here, behind the operator credential, and not on health (#258).
	ClaudeVersion string `json:"claude_version"`
}

func (server *Server) settingsView(ctx context.Context) (settingsView, error) {
	token, err := server.claudeToken(ctx)
	if err != nil {
		return settingsView{}, err
	}
	signedIn, err := server.cloudAccountSet(ctx)
	if err != nil {
		return settingsView{}, err
	}
	arm, force := loop.RotationThresholds(ctx, server.Store.Settings(), server.Log)
	fiveHour, sevenDay := loop.PlanCapThresholds(ctx, server.Store.Settings(), server.Log)
	return settingsView{
		ClaudeTokenSet:         signedIn || token != "",
		ContextArmPercent:      arm,
		ContextForcePercent:    force,
		PlanCapFiveHourPercent: fiveHour,
		PlanCapSevenDayPercent: sevenDay,
		HealthErrorTurns:       loop.HealthErrorTurns(ctx, server.Store.Settings(), server.Log),
		BareAllowed:            server.BareAllowed,
		DefaultRuntime:         server.defaultRuntime(),
		ClaudeVersion:          server.ClaudeVer,
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
	// nil leaves a plan cap threshold unchanged; each is validated alone
	// (0-100, 0 for off), and a change re-judges the cap (ADR-0047).
	PlanCapFiveHourPercent *int `json:"plan_cap_five_hour_percent"`
	PlanCapSevenDayPercent *int `json:"plan_cap_seven_day_percent"`
	// nil leaves the error-turn threshold unchanged (0-100, 0 for off).
	HealthErrorTurns *int `json:"health_error_turns"`
}

func (server *Server) handlePutSettings(w http.ResponseWriter, r *http.Request) {
	var req putSettingsReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		server.jsonErr(w, 400, "bad json: %v", err)
		return
	}
	// checked before anything is written, so a refused threshold leaves
	// the rest of the request unapplied too
	for _, percent := range []*int{req.PlanCapFiveHourPercent, req.PlanCapSevenDayPercent} {
		if percent != nil && !loop.ValidPlanCapPercent(*percent) {
			server.jsonErr(w, 400, "plan cap thresholds must be percentages 0-100, 0 for off (got %d)", *percent)
			return
		}
	}
	if req.HealthErrorTurns != nil && !loop.ValidHealthErrorTurns(*req.HealthErrorTurns) {
		server.jsonErr(w, 400, "health_error_turns must be a count of turns 0-100, 0 for off (got %d)", *req.HealthErrorTurns)
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
		if code, err := server.setSetupToken(r.Context(), token); err != nil {
			server.jsonErr(w, code, "%v", err)
			return
		}
		server.secretsChanged(r.Context())
		// a setup-token cleared under a Claude login leaves the hub's
		// Claude token as it was
		hubToken, err := loop.HubClaudeToken(r.Context(), server.Store)
		if err == nil && (token != "" || hubToken == "") {
			err = server.loginTokenChanged(r.Context(), hubToken)
		}
		if err != nil {
			server.jsonErr(w, 500, "%v", err)
			return
		}
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
	if req.PlanCapFiveHourPercent != nil || req.PlanCapSevenDayPercent != nil {
		for _, threshold := range []struct {
			key     string
			percent *int
		}{
			{store.SettingPlanCapFiveHourPercent, req.PlanCapFiveHourPercent},
			{store.SettingPlanCapSevenDayPercent, req.PlanCapSevenDayPercent},
		} {
			if threshold.percent == nil {
				continue
			}
			if err := server.Store.Settings().Set(r.Context(), threshold.key, strconv.Itoa(*threshold.percent)); err != nil {
				server.jsonErr(w, 500, "%v", err)
				return
			}
		}
		// a raised or cleared threshold lifts the cap and wakes the loops;
		// a lowered one caps them at their next quiet boundary
		if err := server.PlanCap.Refresh(r.Context()); err != nil {
			server.jsonErr(w, 500, "%v", err)
			return
		}
	}
	if req.HealthErrorTurns != nil {
		if err := server.Store.Settings().Set(r.Context(), store.SettingHealthErrorTurns, strconv.Itoa(*req.HealthErrorTurns)); err != nil {
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

// setSetupToken stores the setup-token, "" to clear it, and answers the
// status a failure gets. The default Claude login supersedes the
// setup-token, so one is refused while it is signed in (ADR-0058). The
// check and the write hold its lock, so a sign-in can't land between
// them.
func (server *Server) setSetupToken(ctx context.Context, token string) (int, error) {
	server.lockLogin(store.DefaultClaudeLogin)
	defer server.unlockLogin(store.DefaultClaudeLogin)
	if token != "" {
		switch signedIn, err := server.cloudAccountSet(ctx); {
		case err != nil:
			return 500, err
		case signedIn:
			return 409, errors.New("a Claude login is signed in, and it replaces the setup-token: sign out first")
		}
	}
	if err := server.Store.Settings().Set(ctx, store.SettingClaudeOAuthToken, token); err != nil {
		return 500, err
	}
	return 0, nil
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
// threads: control_room (the default), owner_dm, or a dm:<name> (see
// handleLoopDM). The shared group has no per-loop thread — it lives on
// /api/activity.
func (server *Server) handleLoopConversation(w http.ResponseWriter, r *http.Request) {
	loopRecord := server.loopByName(w, r)
	if loopRecord == nil {
		return
	}
	kind := defaultStr(r.URL.Query().Get("conversation"), store.ConversationControlRoom)
	if store.IsDM(kind) {
		server.handleLoopDM(w, r, loopRecord, kind)
		return
	}
	if kind != store.ConversationControlRoom && kind != store.ConversationOwnerDM {
		server.jsonErr(w, 400, "conversation must be %s, %s or %s<name>", store.ConversationControlRoom, store.ConversationOwnerDM, store.DMDestinationPrefix)
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
	server.writeMessages(w, r, msgs)
}

// handleGroupTimeline is the fleet channel's own timeline, newest first like
// a loop's conversation. The channel is the hub's rather than any loop's
// (ADR-0032), so it is not reached through one, and it holds no message said
// in another channel (ADR-0038).
func (server *Server) handleGroupTimeline(w http.ResponseWriter, r *http.Request) {
	msgs, err := server.Store.Messages().ListChannel(r.Context(), store.FleetChannel, queryInt(r, "limit", 100))
	if err != nil {
		server.jsonErr(w, 500, "%v", err)
		return
	}
	if msgs == nil {
		msgs = []*store.Message{}
	}
	server.writeMessages(w, r, msgs)
}

type postGroupReq struct {
	Author string `json:"author"`
	Text   string `json:"text"`
	// ReplyToID is the fleet-channel message this post answers, or 0 for
	// a plain post.
	ReplyToID int64 `json:"reply_to_id"`
	// AttachmentID is as on a loop's composer (postMessageReq).
	AttachmentID int64 `json:"attachment_id"`
}

// handleGroupPost is the operator posting to the fleet channel. Unlike a
// group post from a loop's composer, it names no loop implicitly: it wakes
// the loops its text addresses and no others, and a post that addresses
// nobody is kept and wakes nobody — as a human's post in a mirrored room
// is. It never leaves the hub (ADR-0032).
//
// A reply addresses the author of what it answers, as a native reply does
// (ADR-0025), so a loop can be answered without a mention.
func (server *Server) handleGroupPost(w http.ResponseWriter, r *http.Request) {
	var req postGroupReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Text) == "" {
		server.jsonErr(w, 400, "non-empty text required")
		return
	}
	if req.ReplyToID != 0 && !server.channelReplyTarget(r.Context(), w, req.ReplyToID, store.FleetChannel) {
		return
	}
	err := server.Router.Ingest(r.Context(), route.InboundMessage{
		Origin:       store.OriginWeb,
		Author:       authorOf(r, req.Author),
		Text:         req.Text,
		Conversation: store.ConversationGroup,
		ReplyToID:    req.ReplyToID,
		UploadID:     req.AttachmentID,
		UploadSender: uploadSenderOf(callerOf(r)),
	})
	if err != nil {
		server.ingestErr(w, err)
		return
	}
	writeJSON(w, 202, map[string]bool{"queued": true})
}

// channelReplyTarget checks that id names a message of the channel,
// answering the refusal itself when it does not. The operator picked a real
// message, so one that is missing means the page is stale, and one from
// another conversation would add its author to the channel's recipients:
// a private message's, or a loop's that is not in this channel. Both are
// refused with the codes a loop's own send gets for the same mistakes.
// Neither refusal says where the message is: walking ids, a member would
// map the private conversations they may not read (#719).
func (server *Server) channelReplyTarget(ctx context.Context, w http.ResponseWriter, id int64, channel string) bool {
	target, err := server.Store.Messages().Get(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		server.jsonErrCode(w, 400, route.ErrUnknownReplyTo, "no message %d to reply to", id)
		return false
	} else if err != nil {
		server.jsonErr(w, 500, "%v", err)
		return false
	}
	if want := store.ChannelDestination(channel); target.Destination() != want {
		server.jsonErrCode(w, 400, route.ErrCrossConversation,
			"message %d is not in %s; a reply stays in its own conversation", id, want)
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
	server.writeMessages(w, r, msgs)
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
	server.writeMessages(w, r, msgs)
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
	case errors.Is(err, route.ErrNoOwnerDMChat), errors.Is(err, route.ErrNoPrivateChat), errors.Is(err, route.ErrDMNotAllowed):
		// The retry would have nowhere to land, or nobody it may still
		// reach. Said as its own refusal because these are the cases the
		// operator can fix: the owner, or the person a dm:<name> message is
		// for, has to write to the bot once before a loop can DM them (#73,
		// ADR-0049), and a person blocked since has to be allowed again.
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

// tgSenderView is a Telegram sender as Settings → Users and access reads it,
// with the name of the user it is linked to: "" for a sender allowed before
// ADR-0051, who keeps working, not linked (#731).
type tgSenderView struct {
	*store.TGSender
	User string `json:"user"`
}

// slackSenderView is tgSenderView's Slack sender.
type slackSenderView struct {
	*store.SlackSender
	User string `json:"user"`
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
	if !seesPairCodes(callerOf(r)) {
		for _, sender := range senders {
			sender.PairCode = ""
		}
	}
	linked, err := server.Store.UserLinks().LinkedUsers(r.Context())
	if err != nil {
		server.jsonErr(w, 500, "%v", err)
		return
	}
	views := make([]tgSenderView, 0, len(senders))
	for _, sender := range senders {
		views = append(views, tgSenderView{sender, linked[store.TGPerson(sender.TGUserID)]})
	}
	writeJSON(w, 200, views)
}

// --- env-var connections ---

// secretNameRe constrains an env-var connection's variable to a POSIX env
// identifier — what both `docker --env KEY` and the shell require.
var secretNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

const (
	maxSecretNameLen  = 128
	maxSecretValueLen = 16 * 1024
)

// codeNoSetupToken refuses a login check on a docker hub with no Claude
// token to check: neither a Claude login nor a setup-token.
const codeNoSetupToken = "no_setup_token"

// createLoopEnvVar creates a private env-var connection, which the store
// attaches to its owner, named as migration 0042 names a moved loop
// secret: the hub's name for a variable the operator set on one loop.
func (server *Server) createLoopEnvVar(ctx context.Context, connection *store.Connection, owner *store.Loop) error {
	var err error
	for range 3 { // six random hex characters meet another name rarely, and three times never
		connection.Name = loopSecretConnectionName(owner.Name, connection.Config.Env)
		if err = server.Store.Connections().Create(ctx, connection); !errors.Is(err, store.ErrDuplicate) {
			break
		}
	}
	return err
}

// loopSecretConnectionName joins a loop's name and a variable's in the
// connection alphabet: lower case, '_' as '-', cut to 25, then '-' and six
// random hex characters. A loop name may hold '_', so two pairs could
// otherwise meet on one name.
func loopSecretConnectionName(loop, env string) string {
	prefix := strings.ReplaceAll(strings.ToLower(loop+"-"+env), "_", "-")
	if len(prefix) > 25 {
		prefix = prefix[:25]
	}
	suffix := make([]byte, 3)
	_, _ = rand.Read(suffix)
	return prefix + "-" + hex.EncodeToString(suffix)
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

func (server *Server) handleSenderStatus(status string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			server.jsonErr(w, 400, "bad sender id")
			return
		}
		sender, err := server.setTGSenderStatus(r.Context(), id, status)
		if err != nil {
			server.storeErr(w, err, "sender")
			return
		}
		if status == store.SenderAllowed {
			server.adoptDefaultOwner(r.Context(), id)
		}
		writeJSON(w, 200, sender)
	}
}

// setTGSenderStatus sets a Telegram sender's status and tells the control
// room. One no longer allowed owns no loop any more.
func (server *Server) setTGSenderStatus(ctx context.Context, id int64, status string) (*store.TGSender, error) {
	if err := server.Store.TGSenders().SetStatus(ctx, id, status, time.Now().UnixMilli()); err != nil {
		return nil, err
	}
	sender, err := server.Store.TGSenders().Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if status != store.SenderAllowed {
		server.disownLoopsOf(ctx, id)
	}
	server.Bus.Publish(bus.Item{Kind: bus.KindAccess, Payload: sender.Frame()})
	return sender, nil
}

// defaultOwnerID is the first person the operator allowlisted, or 0 when
// nobody is. A person linked to a user who is not an owner is passed
// over: their link allowed them, not the operator (ADR-0051).
func (server *Server) defaultOwnerID(ctx context.Context) int64 {
	senders, err := server.Store.TGSenders().List(ctx)
	if err != nil {
		return 0
	}
	notOwners, err := server.linkedToNonOwners(ctx)
	if err != nil {
		return 0
	}
	var owner *store.TGSender
	for _, sender := range senders {
		if sender.Status != store.SenderAllowed || notOwners[store.TGPerson(sender.TGUserID)] {
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

// linkedToNonOwners is the set of persons linked to a user who is not an
// owner.
func (server *Server) linkedToNonOwners(ctx context.Context) (map[string]bool, error) {
	all, err := server.Store.Users().List(ctx)
	if err != nil {
		return nil, err
	}
	persons := map[string]bool{}
	for _, user := range all {
		if user.Role == store.RoleOwner {
			continue
		}
		links, err := server.Store.UserLinks().List(ctx, user.ID)
		if err != nil {
			return nil, err
		}
		for _, link := range links {
			persons[link.Person] = true
		}
	}
	return persons, nil
}

// defaultInFleetChannel is where a new loop starts when its creator did not
// say: outside the fleet channel when no other loop exists, in it
// otherwise. A fleet of one has no one to talk to in the channel (ADR-0032),
// and the second loop is what makes a fleet — the operator's rule
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
	if err := server.Store.Loops().AdoptOwner(ctx, tgUserID, time.Now().UnixMilli()); err != nil {
		server.Log.Error("default owner", "err", err)
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
	// User names the owner as a hub user, resolved to their link on the
	// loop's surface (#731). It is the only way to name one: the sender
	// ids the route took before are retired (#775).
	User string `json:"user"`
}

// Refusal codes for an owner named as a user.
const (
	codeUnknownUser = "unknown_user"
	codeNoLink      = "no_link"
)

// ownerByUser is the person the user named owns the loop as: their link on
// the loop's surface, Slack for a Slack loop and Telegram for any other, as
// the default owner is. Exactly one of the two ids is set. A user with no
// link there cannot own the loop, since the owner is someone the loop can
// write to.
func (server *Server) ownerByUser(ctx context.Context, loopRecord *store.Loop, name string) (int64, string, *requestError) {
	user, err := server.Store.Users().GetByName(ctx, name)
	if errors.Is(err, store.ErrNotFound) {
		return 0, "", &requestError{status: http.StatusBadRequest, code: codeUnknownUser, msg: "no user is named " + name}
	} else if err != nil {
		return 0, "", &requestError{status: http.StatusInternalServerError, msg: err.Error()}
	}
	surface := store.SurfaceTelegram
	if loopRecord.Surface() == store.SurfaceSlack {
		surface = store.SurfaceSlack
	}
	links, err := server.Store.UserLinks().List(ctx, user.ID)
	if err != nil {
		return 0, "", &requestError{status: http.StatusInternalServerError, msg: err.Error()}
	}
	for _, link := range links {
		if link.Surface != surface {
			continue
		}
		_, id, _ := strings.Cut(link.Person, ":")
		if surface == store.SurfaceSlack {
			return 0, id, nil
		}
		tgUserID, err := strconv.ParseInt(id, 10, 64)
		if err != nil {
			return 0, "", &requestError{status: http.StatusInternalServerError, msg: "bad link " + link.Person}
		}
		return tgUserID, "", nil
	}
	return 0, "", &requestError{status: http.StatusBadRequest, code: codeNoLink,
		msg: user.Name + " has no " + surface + " link: they link it in Settings → Account"}
}

// handlePutOwner sets which person a loop may message privately, named as
// a hub user and stored as the person their link is (#731, #775). The
// link must hold an allowed sender: being able to reach a bot is not the
// same as owning the loop it belongs to (#73). Changing the owner drops the
// captured chat — it belonged to the previous one — and the new owner's
// first DM to this bot captures theirs.
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
	if req.User == "" {
		server.jsonErr(w, 400, `name the owner as a user: {"user": "<name>"}`)
		return
	}
	tgUserID, slackUserID, refusal := server.ownerByUser(r.Context(), loopRecord, req.User)
	if refusal == nil && slackUserID != "" {
		refusal = server.putSlackOwner(r.Context(), loopRecord, slackUserID)
	}
	if refusal != nil {
		server.refuse(w, refusal)
		return
	}
	if slackUserID != "" {
		writeJSON(w, 200, server.view(r.Context(), loopRecord))
		return
	}
	sender, err := server.Store.TGSenders().Get(r.Context(), tgUserID)
	if errors.Is(err, store.ErrNotFound) || (err == nil && sender.Status != store.SenderAllowed) {
		server.jsonErr(w, 400, "owner must be an allowed telegram sender")
		return
	} else if err != nil {
		server.jsonErr(w, 500, "%v", err)
		return
	}
	if loopRecord.OwnerTGUserID != tgUserID {
		loopRecord.OwnerTGUserID, loopRecord.OwnerDMChatID = tgUserID, 0
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
	server.Bus.Publish(bus.Item{Kind: bus.KindAccess, Payload: map[string]any{"deleted": id, "surface": store.SurfaceTelegram}})
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
	access, err := server.openStreamAccess(r.Context(), callerOf(r))
	if err != nil {
		server.jsonErr(w, 500, "%v", err)
		return
	}
	serveSSE(w, r, server.Bus, func(item bus.Item) bool {
		access.notice(item)
		return item.Kind != bus.KindAgentEvent && item.Kind != bus.KindUser
	}, func(item bus.Item) (bool, error) { return access.admits(r.Context(), item) })
}

func (server *Server) handleLoopStream(w http.ResponseWriter, r *http.Request) {
	loopRecord := server.loopByName(w, r)
	if loopRecord == nil {
		return
	}
	access, err := server.openStreamAccess(r.Context(), callerOf(r))
	if err != nil {
		server.jsonErr(w, 500, "%v", err)
		return
	}
	id := loopRecord.ID
	serveSSE(w, r, server.Bus, func(item bus.Item) bool {
		access.notice(item)
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
	}, func(item bus.Item) (bool, error) { return access.admits(r.Context(), item) })
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
