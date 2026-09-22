package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/enes-alatas/spool/internal/loop"
	"github.com/enes-alatas/spool/internal/route"
	"github.com/enes-alatas/spool/internal/store"
)

// The hub serves its own MCP endpoint (ADR-0026): send_message is the one
// way a loop's claude process emits an explicitly addressed message. Each
// request authenticates with the loop's hub MCP bearer token; the endpoint
// is stateless, so every POST stands alone and no session state accrues.

type sendMessageIn struct {
	Destination string `json:"destination" jsonschema:"where this message goes: owner_dm (your owner's private Telegram chat — always the same person, so a tick can open a private conversation), group (the shared group; @mention recipients in the text), or control_room (your private web thread with the operator)"`
	ReplyTo     string `json:"reply_to,omitempty" jsonschema:"reference of the message this replies to (\"ref:42\"), exactly as its envelope header gave it; the reply addresses that message's author and, in the group, renders as a native reply. Must belong to this destination's conversation. Omit for a new message."`
	Text        string `json:"text" jsonschema:"the message text; in the group, @mentions name the recipients"`
	Resends     string `json:"resends,omitempty" jsonschema:"reference of your own message whose send failed (\"ref:42\"), exactly as the undelivered note gave it, when these words are you saying that message again. The destination must be the one it was lost going to. When this send gets through, that failure stops being the operator's to deal with. Omit unless you are repeating a message you were told never arrived."`
}

type sendMessageOut struct {
	MessageID int64 `json:"message_id"`
	// Ref is the sent message's reply reference, in the same form every
	// envelope header uses — so a loop can reply to its own message with
	// what it was handed, not a form it has to infer.
	Ref string `json:"ref"`
}

func (s *Server) mcpHandler() http.Handler {
	inner := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		l, ok := r.Context().Value(mcpLoopKey{}).(*store.Loop)
		if !ok {
			return nil
		}
		srv := mcp.NewServer(&mcp.Implementation{Name: "spool", Version: s.ClaudeVer}, nil)
		mcp.AddTool(srv, &mcp.Tool{
			Name: "send_message",
			Description: "Send one explicitly addressed message. Each call is one message to one " +
				"destination; call again for another destination or recipient set. Errors are " +
				"correctable: fix what the message names and retry.",
		}, s.sendMessageTool(l))
		return srv
	}, &mcp.StreamableHTTPOptions{Stateless: true, Logger: s.Log})

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		l, err := s.Store.Loops().GetByHubMCPToken(r.Context(), token)
		if err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		inner.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), mcpLoopKey{}, l)))
	})
}

type mcpLoopKey struct{}

func (s *Server) sendMessageTool(l *store.Loop) func(context.Context, *mcp.CallToolRequest, sendMessageIn) (*mcp.CallToolResult, sendMessageOut, error) {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in sendMessageIn) (*mcp.CallToolResult, sendMessageOut, error) {
		msg, serr, err := s.Router.Send(ctx, route.SendRequest{
			From:        l,
			Destination: in.Destination,
			ReplyTo:     in.ReplyTo,
			Text:        in.Text,
			Resends:     in.Resends,
		})
		if serr != nil {
			// A typed refusal: the SDK renders a returned error as an
			// isError tool result, which is what lets the model correct.
			return nil, sendMessageOut{}, serr
		}
		if err != nil {
			s.Log.Error("send_message", "loop", l.Name, "err", err)
			return nil, sendMessageOut{}, fmt.Errorf("internal error; try again")
		}
		return nil, sendMessageOut{MessageID: msg.ID, Ref: loop.MessageRef(msg.ID)}, nil
	}
}
