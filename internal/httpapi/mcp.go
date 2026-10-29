package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

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
	Destination string  `json:"destination" jsonschema:"where this message goes: owner_dm (your owner's private chat on your attached surface — always the same person, so a tick can open a private conversation), dm:<name> (a private chat with anyone else allowed who has messaged you privately, named as your system prompt lists them), group (the fleet channel; @mention recipients in the text), channel:<name> (another channel you are in; @mention recipients in it), or control_room (your thread in the Spool web UI, which every user of this hub reads). Your system prompt says which of these you have"`
	ReplyTo     string  `json:"reply_to,omitempty" jsonschema:"reference of the message this replies to (\"ref:42\"), exactly as its envelope header gave it; the reply addresses that message's author and, in the group, renders as a native reply. Must belong to this destination's conversation. Omit for a new message."`
	Text        string  `json:"text,omitempty" jsonschema:"the message text, or a poll's question; in the group or a channel, @mentions name the recipients. Omit only with react, vote, close_poll or dismiss."`
	Resends     string  `json:"resends,omitempty" jsonschema:"reference of your own message whose send failed (\"ref:42\"), exactly as the undelivered note gave it, when these words are you saying that message again. The destination must be the one it was lost going to. When this send gets through, that failure stops being the operator's to deal with. Omit unless you are repeating a message you were told never arrived."`
	Attach      string  `json:"attach,omitempty" jsonschema:"path of one file in your workspace to send with this message, at most 20 MB; an image is shown as a photo where the surface can. The text goes with it. Omit to send words alone."`
	React       string  `json:"react,omitempty" jsonschema:"one emoji to react with to the message reply_to names, instead of answering it in words; send no text with it. It wakes nobody, and counts as a send. Omit to send a message."`
	Poll        *pollIn `json:"poll,omitempty" jsonschema:"makes this message a poll: its text is the question, at most 300 characters. Votes in it reach you ahead of your next turn, and so does the result once it closes. Omit for a plain message."`
	Vote        *[]int  `json:"vote,omitempty" jsonschema:"your vote in the poll reply_to names: the numbers of the options you pick, as the poll listed them, or [] to take your vote back; send no text with it. It wakes nobody, and counts as a send. Omit unless voting."`
	ClosePoll   string  `json:"close_poll,omitempty" jsonschema:"reference of your own poll to close (\"ref:42\"); send no text with it. It stops taking votes, and its result reaches you ahead of your next turn. Omit unless closing a poll."`
	Dismiss     string  `json:"dismiss,omitempty" jsonschema:"reference of your own message whose send failed (\"ref:42\"), exactly as the undelivered note gave it, when it is no longer worth saying; send no text with it, and the destination it was lost going to. That failure stops being the operator's to deal with, and you are not reminded of it again. Omit unless dismissing a message you were told never arrived."`
}

type pollIn struct {
	Options  []string `json:"options" jsonschema:"the answers to choose from, 2 to 10, each at most 100 characters"`
	Multiple bool     `json:"multiple,omitempty" jsonschema:"true lets a voter pick more than one option; omit for one each"`
	ClosesIn string   `json:"closes_in,omitempty" jsonschema:"how long the poll stays open, up to 7 days, such as 90m, 4h or 2d; omit to keep it open until you close it"`
}

type sendMessageOut struct {
	MessageID int64 `json:"message_id,omitempty"`
	// Ref is the sent message's reply reference, in the same form every
	// envelope header uses — so a loop can reply to its own message with
	// what it was handed, not a form it has to infer.
	Ref string `json:"ref,omitempty"`
	// ReactedTo is the reference of the message a reaction was put on, in
	// place of the two above: a reaction is no message of its own.
	ReactedTo string `json:"reacted_to,omitempty"`
	// VotedIn and Closed are the reference of the poll a vote was cast in,
	// or that was closed, in their place too.
	VotedIn string `json:"voted_in,omitempty"`
	Closed  string `json:"closed,omitempty"`
	// Dismissed is the reference of the lost message a dismissal resolved,
	// in their place too.
	Dismissed string `json:"dismissed,omitempty"`
}

// mcpHandler serves /mcp to a caller whose loop admits lets onto this
// listener. One it refuses is answered as an unknown token is, so a
// refusal says nothing about whose token it was. One it admits with a
// held reason gets the handshake and the tool list, and each tool call
// is refused as a tool error naming it.
func (server *Server) mcpHandler(admits func(*http.Request, *store.Loop) (admitted bool, held error)) http.Handler {
	inner := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		caller, ok := r.Context().Value(mcpLoopKey{}).(*store.Loop)
		if !ok {
			return nil
		}
		held, _ := r.Context().Value(mcpHeldKey{}).(error)
		mcpServer := mcp.NewServer(&mcp.Implementation{Name: "spool", Version: server.ClaudeVer}, nil)
		mcp.AddTool(mcpServer, &mcp.Tool{
			Name: "send_message",
			Description: "Send one explicitly addressed message. Each call is one message to one " +
				"destination; call again for another destination or recipient set. Errors are " +
				"correctable: fix what the message names and retry.",
		}, server.sendMessageTool(caller, held))
		return mcpServer
	}, &mcp.StreamableHTTPOptions{
		Stateless: true,
		Logger:    server.Log,
		// The SDK refuses a request that comes in on loopback naming
		// another host, against DNS rebinding. Docker Desktop delivers
		// every workstation's request that way: on the host's loopback,
		// still naming host.docker.internal (#508). The guard is for an
		// endpoint that takes no credential; every request here has
		// already shown its loop's bearer token, which a rebinding page
		// cannot know (ADR-0028).
		DisableLocalhostProtection: true,
	})

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		caller, ok := server.mcpCaller(w, r)
		if !ok {
			return
		}
		admitted, held := admits(r, caller)
		if !admitted {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		ctx := context.WithValue(r.Context(), mcpLoopKey{}, caller)
		if held != nil {
			ctx = context.WithValue(ctx, mcpHeldKey{}, held)
		}
		inner.ServeHTTP(w, r.WithContext(ctx))
	})
}

// mcpCaller is the loop a request on the loop listener comes from, by its
// hub MCP bearer token. A request without one, or with one no loop holds,
// is answered 401 here and reports false.
func (server *Server) mcpCaller(w http.ResponseWriter, r *http.Request) (*store.Loop, bool) {
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	// Refused before the lookup, so no row with an empty token can ever
	// answer for it: this check is all that keeps a rebinding page out.
	if token == "" {
		server.cloudRefused(r, "no token")
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return nil, false
	}
	caller, err := server.Store.Loops().GetByHubMCPToken(r.Context(), token)
	if err != nil {
		server.cloudRefused(r, "a token no loop holds")
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return nil, false
	}
	return caller, true
}

// cloudRefused logs a request the cloud listener refused for its token,
// at most once a minute: a session whose network secret is missing or
// wrong is refused on every request, and the log is where the operator
// finds why. It names the path and what came, never a token or its hash.
// A request on the loop listener logs nothing.
func (server *Server) cloudRefused(r *http.Request, came string) {
	if r.Context().Value(cloudListenerKey{}) == nil {
		return
	}
	now := time.Now().UnixMilli()
	last := server.cloudRefusedLogged.Load()
	if now-last < time.Minute.Milliseconds() || !server.cloudRefusedLogged.CompareAndSwap(last, now) {
		return
	}
	server.Log.Warn("cloud listener refused a request for its token, logged at most once a minute: check the environment's network secret",
		"path", r.URL.Path, "came", came)
}

// cloudListenerKey marks a request that came in on the cloud listener.
type cloudListenerKey struct{}

type mcpLoopKey struct{}

// mcpHeldKey carries why an admitted request's tool calls are refused.
type mcpHeldKey struct{}

// sendMessageTool is the send_message tool for caller. A held one refuses
// every call, as a tool error the model reads, and logs it with the loop.
func (server *Server) sendMessageTool(caller *store.Loop, held error) func(context.Context, *mcp.CallToolRequest, sendMessageIn) (*mcp.CallToolResult, sendMessageOut, error) {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in sendMessageIn) (*mcp.CallToolResult, sendMessageOut, error) {
		if held != nil {
			server.Log.Warn("cloud mcp call refused", "loop", caller.Name, "tool", "send_message", "reason", held.Error())
			return nil, sendMessageOut{}, fmt.Errorf("refused: %v; this loop's sends reach the hub only during its turns", held)
		}
		req := route.SendRequest{
			From:        caller,
			Destination: in.Destination,
			ReplyTo:     in.ReplyTo,
			Text:        in.Text,
			Resends:     in.Resends,
			Attach:      in.Attach,
			React:       in.React,
			Voting:      in.Vote != nil,
			ClosePoll:   in.ClosePoll,
			Dismiss:     in.Dismiss,
		}
		if in.Poll != nil {
			req.Poll = &route.PollRequest{Options: in.Poll.Options, Multiple: in.Poll.Multiple, ClosesIn: in.Poll.ClosesIn}
		}
		if in.Vote != nil {
			req.Vote = *in.Vote
		}
		kind, serr := req.Kind()
		if serr != nil {
			return nil, sendMessageOut{}, serr
		}
		send := server.Router.Send
		switch kind {
		case "react":
			send = server.Router.SendReaction
		case "vote":
			send = server.Router.SendVote
		case "close_poll":
			send = server.Router.SendClosePoll
		case "dismiss":
			send = server.Router.SendDismiss
		}
		msg, serr, err := send(ctx, req)
		if serr != nil {
			// A typed refusal: the SDK renders a returned error as an
			// isError tool result, which is what lets the model correct.
			return nil, sendMessageOut{}, serr
		}
		if err != nil {
			server.Log.Error("send_message", "loop", caller.Name, "err", err)
			return nil, sendMessageOut{}, fmt.Errorf("internal error; try again")
		}
		switch kind {
		case "react":
			return nil, sendMessageOut{ReactedTo: loop.MessageRef(msg.ID)}, nil
		case "vote":
			return nil, sendMessageOut{VotedIn: loop.MessageRef(msg.ID)}, nil
		case "close_poll":
			return nil, sendMessageOut{Closed: loop.MessageRef(msg.ID)}, nil
		case "dismiss":
			return nil, sendMessageOut{Dismissed: loop.MessageRef(msg.ID)}, nil
		}
		return nil, sendMessageOut{MessageID: msg.ID, Ref: loop.MessageRef(msg.ID)}, nil
	}
}

// everyLoop admits any loop's token: the loop listener's rule.
func everyLoop(*http.Request, *store.Loop) (bool, error) { return true, nil }
