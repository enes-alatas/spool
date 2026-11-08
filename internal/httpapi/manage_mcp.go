package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/enes-alatas/spool/internal/managementtoken"
	"github.com/enes-alatas/spool/internal/store"
)

// The management MCP (ADR-0067) is how an operator's own agent looks after
// the hub: it lists the loops, pauses one and resumes one, and nothing
// else. The agent holds a management token, and the server takes no other
// credential: not the operator token, a user's session or a loop token.
// The scope is the server: a call it has no tool for can't be made.
//
// It is served at manageMCPPath on the operator listener, for an agent on
// the operator's machine, and on the cloud listener, for one in a cloud
// session; never on the loop listener. Like the loops' /mcp it is
// stateless, so every call shows its token, and a token revoked or past
// its end is refused at the next one.

const manageMCPPath = "/manage/mcp"

type manageLoopIn struct {
	Name string `json:"name" jsonschema:"the loop's name, as list_loops gives it"`
}

type listLoopsIn struct{}

// manageLoopView is one loop as the management MCP shows it: what it is
// doing, and nothing a turn wrote. No problem's text, mission, message,
// secret or token, so no error string from a turn reaches the agent
// (ADR-0067).
type manageLoopView struct {
	Name    string `json:"name"`
	Runtime string `json:"runtime"`
	State   string `json:"state"`
	Paused  bool   `json:"paused"`
	// Resumable is whether resume_loop takes the loop: paused, by a
	// management token.
	Resumable bool `json:"resumable"`
	// Health sums up the loop's open problems: ok, warning or failing
	// (ADR-0053).
	Health string `json:"health"`
	// LastTurnAt is when the loop's latest turn started, in Unix ms, and 0
	// before its first.
	LastTurnAt int64 `json:"last_turn_at"`
}

type listLoopsOut struct {
	Loops []manageLoopView `json:"loops"`
}

type manageLoopOut struct {
	Loop manageLoopView `json:"loop"`
	// Changed is false for a pause of a loop already paused, which keeps
	// the pauser it had.
	Changed bool `json:"changed"`
}

type manageIdentityKey struct{}

// manageMCPHandler serves the management MCP to a request bearing a
// management token, and answers anything else 401, alike whatever it was.
func (server *Server) manageMCPHandler() http.Handler {
	inner := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		identity, ok := r.Context().Value(manageIdentityKey{}).(managementtoken.Identity)
		if !ok {
			return nil
		}
		mcpServer := mcp.NewServer(&mcp.Implementation{Name: "spool-manage", Version: server.ClaudeVer}, nil)
		mcp.AddTool(mcpServer, &mcp.Tool{
			Name: "list_loops",
			Description: "List the hub's loops: each one's name, runtime, state, whether it is paused " +
				"and whether resume_loop can resume it, its health (ok, warning or failing) and when " +
				"its latest turn started (Unix ms, 0 for none).",
		}, server.listLoopsTool())
		mcp.AddTool(mcpServer, &mcp.Tool{
			Name: "pause_loop",
			Description: "Pause a loop, by its name, as the control room's Pause does: it takes no " +
				"turns until it is resumed. Pausing a loop already paused changes nothing.",
		}, server.pauseLoopTool(identity))
		mcp.AddTool(mcpServer, &mcp.Tool{
			Name: "resume_loop",
			Description: "Resume a loop, by its name, as the control room's Resume does. It resumes " +
				"only a loop a management token paused: a person's pause is theirs to lift.",
		}, server.resumeLoopTool(identity))
		return mcpServer
	}, &mcp.StreamableHTTPOptions{
		Stateless: true,
		Logger:    server.Log,
		// Every request has shown a management token before it gets here,
		// which a rebinding page can't know, and the operator listener's
		// host check has run (manageOnOperatorListener). A tunnel delivers
		// the cloud listener's requests on loopback naming the public
		// host, which the SDK's guard would refuse.
		DisableLocalhostProtection: true,
	})

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		// per request, so the routes can be built before the store is set
		checker := managementtoken.Checker{Tokens: server.Store.ManagementTokens()}
		identity, err := checker.Verify(r.Context(), token)
		if errors.Is(err, managementtoken.ErrRefused) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if err != nil {
			server.Log.Error("management token check", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		inner.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), manageIdentityKey{}, identity)))
	})
}

// manageOnOperatorListener is the management MCP on the operator listener,
// under the checks the guard gives every /api route before a credential is
// read (ADR-0030): the Host, a cross-site request, and the body's size.
func (server *Server) manageOnOperatorListener() http.HandlerFunc {
	handler := server.manageMCPHandler()
	return func(w http.ResponseWriter, r *http.Request) {
		if !server.hostAllowed(r.Host) {
			http.Error(w, "unexpected Host", http.StatusForbidden)
			return
		}
		if refusal := server.crossSiteRefusal(r); refusal != "" {
			http.Error(w, refusal, http.StatusForbidden)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxAPIBody)
		handler.ServeHTTP(w, r)
	}
}

func (server *Server) listLoopsTool() func(context.Context, *mcp.CallToolRequest, listLoopsIn) (*mcp.CallToolResult, listLoopsOut, error) {
	return func(ctx context.Context, _ *mcp.CallToolRequest, _ listLoopsIn) (*mcp.CallToolResult, listLoopsOut, error) {
		loops, err := server.Store.Loops().List(ctx)
		if err != nil {
			server.Log.Error("list_loops", "err", err)
			return nil, listLoopsOut{}, errors.New("internal error; try again")
		}
		out := listLoopsOut{Loops: make([]manageLoopView, 0, len(loops))}
		for _, loopRecord := range loops {
			out.Loops = append(out.Loops, server.manageView(ctx, loopRecord))
		}
		return nil, out, nil
	}
}

func (server *Server) pauseLoopTool(identity managementtoken.Identity) func(context.Context, *mcp.CallToolRequest, manageLoopIn) (*mcp.CallToolResult, manageLoopOut, error) {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in manageLoopIn) (*mcp.CallToolResult, manageLoopOut, error) {
		loopRecord, err := server.manageLoop(ctx, in.Name)
		if err != nil {
			return nil, manageLoopOut{}, err
		}
		if loopRecord.Status == store.StatusPaused {
			return nil, manageLoopOut{Loop: server.manageView(ctx, loopRecord)}, nil
		}
		changed, err := server.pauseLoop(ctx, loopRecord, identity.Actor())
		if err != nil {
			server.Log.Error("pause_loop", "loop", loopRecord.Name, "token", identity.Name, "err", err)
			return nil, manageLoopOut{}, errors.New("internal error: the loop is paused, but its pause was not stored; pausing it again retries")
		}
		// a person's pause since the read is theirs, and not this call's change
		if changed {
			server.recordManageChange(ctx, identity, store.LoopChangePause, loopRecord)
			server.Log.Info("loop paused by a management token", "loop", loopRecord.Name, "token", identity.Name)
		}
		return nil, manageLoopOut{Loop: server.manageView(ctx, loopRecord), Changed: changed}, nil
	}
}

func (server *Server) resumeLoopTool(identity managementtoken.Identity) func(context.Context, *mcp.CallToolRequest, manageLoopIn) (*mcp.CallToolResult, manageLoopOut, error) {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in manageLoopIn) (*mcp.CallToolResult, manageLoopOut, error) {
		loopRecord, err := server.manageLoop(ctx, in.Name)
		if err != nil {
			return nil, manageLoopOut{}, err
		}
		if loopRecord.Status != store.StatusPaused {
			return nil, manageLoopOut{}, fmt.Errorf("loop %s is not paused", loopRecord.Name)
		}
		refused := fmt.Errorf("loop %s was paused by a person, and only a person resumes it", loopRecord.Name)
		if !strings.HasPrefix(loopRecord.PausedBy, managementtoken.ActorPrefix) {
			return nil, manageLoopOut{}, refused
		}
		resumed, err := server.resumeLoop(ctx, loopRecord, managementtoken.ActorPrefix)
		if err != nil {
			server.Log.Error("resume_loop", "loop", loopRecord.Name, "token", identity.Name, "err", err)
			return nil, manageLoopOut{}, errors.New("internal error; try again")
		}
		if !resumed {
			// a person resumed it, or resumed and paused it, since the read
			return nil, manageLoopOut{}, refused
		}
		server.recordManageChange(ctx, identity, store.LoopChangeResume, loopRecord)
		server.Log.Info("loop resumed by a management token", "loop", loopRecord.Name, "token", identity.Name)
		return nil, manageLoopOut{Loop: server.manageView(ctx, loopRecord), Changed: true}, nil
	}
}

// manageLoop is the loop named name, or the tool error that says it isn't
// there.
func (server *Server) manageLoop(ctx context.Context, name string) (*store.Loop, error) {
	loopRecord, err := server.Store.Loops().GetByName(ctx, name)
	if errors.Is(err, store.ErrNotFound) {
		return nil, fmt.Errorf("no loop is named %q; list_loops gives their names", name)
	}
	if err != nil {
		server.Log.Error("management mcp: read a loop", "loop", name, "err", err)
		return nil, errors.New("internal error; try again")
	}
	return loopRecord, nil
}

// manageView is loopRecord as list_loops shows it.
func (server *Server) manageView(ctx context.Context, loopRecord *store.Loop) manageLoopView {
	full := server.view(ctx, loopRecord)
	out := manageLoopView{
		Name:      loopRecord.Name,
		Runtime:   loopRecord.Runtime,
		State:     full.State,
		Paused:    loopRecord.Status == store.StatusPaused,
		Resumable: loopRecord.Status == store.StatusPaused && strings.HasPrefix(loopRecord.PausedBy, managementtoken.ActorPrefix),
		Health:    full.Health,
	}
	if turn, err := server.Store.Turns().Latest(ctx, loopRecord.ID); err == nil && turn != nil {
		out.LastTurnAt = turn.StartedAt
	}
	return out
}

// recordManageChange puts a token's pause or resume on the loop's change
// record, naming the token as its actor (ADR-0067). Reads are not
// recorded. A failure is logged: the change itself is done.
func (server *Server) recordManageChange(ctx context.Context, identity managementtoken.Identity, action string, loopRecord *store.Loop) {
	err := server.Store.LoopChanges().Record(ctx, &store.LoopChange{
		At: time.Now().UnixMilli(), By: identity.Actor(), Action: action, LoopID: loopRecord.ID, LoopName: loopRecord.Name})
	if err != nil {
		server.Log.Error("record loop change", "loop", loopRecord.Name, "action", action, "err", err)
		return
	}
	loopRecord.UpdatedBy = identity.Actor()
}
