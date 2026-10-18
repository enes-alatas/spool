// Package cloudfake is a fake of the sessions and environments API the
// cloud runtime drives (ADR-0056), for tests at both tiers. Each session's
// "claude" answers a message with what Reply returns, so a test scripts a
// turn as fakeclaude scripts a contained one. It holds no real credential:
// it admits the one token it is made with, which tests keep synthetic.
package cloudfake

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

// Environment is an environment as the fake keeps it.
type Environment struct {
	ID       string
	Body     map[string]any // as last created or updated
	Archived bool
}

// Message is one model request the fake took: the Claude proxy's, on a
// contained loop's behalf (ADR-0058).
type Message struct {
	Query   string
	Headers http.Header
	Body    map[string]any
}

// Session is a session as the fake keeps it.
type Session struct {
	ID            string
	EnvironmentID string
	Context       map[string]any // session_context, as created
	Status        string
	Archived      bool
	Events        []map[string]any
	// Interrupts counts the interrupt requests sent into it.
	Interrupts int
}

// Server is the fake API. Lock Mu to read its sessions and environments.
type Server struct {
	*httptest.Server

	Mu           sync.Mutex
	Environments map[string]*Environment
	Sessions     map[string]*Session
	// Reply is what a session's claude emits for one user message, after
	// the message's echo; the fake gives each event its uuid and session
	// id. Nil is init, one line of text echoing the message, and a result.
	// Called with Mu held.
	Reply func(session *Session, text string) []map[string]any
	// Hold, while set, keeps a session's reply back: the message is echoed
	// and the session runs, but its claude emits nothing until Release.
	Hold bool
	// After is the status a session takes once its claude has replied; empty
	// is idle.
	After string
	// Refusals answers the next that many event reads with RefuseStatus,
	// and Retry-After: 1 when it is 429.
	Refusals     int
	RefuseStatus int
	// Drop, while set, loses every user message sent into a session: no
	// echo, no turn, the session left as it was.
	Drop bool
	// RefuseUpdates, while set, refuses every environment update with a 500
	// and leaves the environment as it was.
	RefuseUpdates bool
	// AccountChecks counts the account checks, the read-only listing of
	// environments.
	AccountChecks int
	// Messages are the model requests it took, POST /v1/messages, each
	// with its query, headers and body.
	Messages []Message
	// ArchiveGate, while set, holds each archive call, of a session or an
	// environment, until it is closed. ArchivesHeld counts the calls it
	// has held.
	ArchiveGate  chan struct{}
	ArchivesHeld int
	held         []heldReply

	ids   atomic.Int64
	token string
}

type heldReply struct {
	session *Session
	text    string
}

// New starts a fake API that admits token alone.
func New(token string) *Server {
	fake := &Server{token: token, Environments: map[string]*Environment{}, Sessions: map[string]*Session{}}
	fake.Server = httptest.NewServer(http.HandlerFunc(fake.serve))
	return fake
}

// Release answers every message Hold kept back.
func (fake *Server) Release() {
	fake.Mu.Lock()
	defer fake.Mu.Unlock()
	fake.Hold = false
	for _, held := range fake.held {
		fake.reply(held.session, held.text)
	}
	fake.held = nil
}

// Newest returns the newest session, or nil.
func (fake *Server) Newest() *Session {
	fake.Mu.Lock()
	defer fake.Mu.Unlock()
	var newest *Session
	for _, session := range fake.Sessions {
		if newest == nil || session.ID > newest.ID {
			newest = session
		}
	}
	return newest
}

func (fake *Server) id(prefix string) string {
	return fmt.Sprintf("%s_%08d", prefix, fake.ids.Add(1))
}

func (fake *Server) uuid() string {
	return fmt.Sprintf("00000000-0000-4000-8000-%012d", fake.ids.Add(1))
}

func (fake *Server) serve(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer "+fake.token {
		refuse(w, http.StatusUnauthorized, "authentication_error", "invalid token")
		return
	}
	var body map[string]any
	if raw, _ := io.ReadAll(r.Body); len(raw) > 0 {
		if err := json.Unmarshal(raw, &body); err != nil {
			refuse(w, http.StatusBadRequest, "invalid_request_error", "body is not JSON")
			return
		}
	}
	path := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if r.Method == http.MethodPost && path[len(path)-1] == "archive" {
		fake.Mu.Lock()
		gate := fake.ArchiveGate
		if gate != nil {
			fake.ArchivesHeld++
		}
		fake.Mu.Unlock()
		if gate != nil {
			<-gate
		}
	}
	fake.Mu.Lock()
	defer fake.Mu.Unlock()
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/v1/messages":
		fake.Messages = append(fake.Messages, Message{Query: r.URL.RawQuery, Headers: r.Header.Clone(), Body: body})
		answer(w, map[string]any{"type": "message", "role": "assistant", "content": []any{map[string]any{"type": "text", "text": "fixture model answer"}}})
	case r.Method == http.MethodGet && r.URL.Path == "/v1/environment_providers":
		fake.AccountChecks++
		answer(w, map[string]any{"data": []any{}, "has_more": false})
	case r.Method == http.MethodPost && r.URL.Path == "/v1/environment_providers/cloud/create":
		environment := &Environment{ID: fake.id("env"), Body: body}
		fake.Environments[environment.ID] = environment
		answer(w, map[string]any{"environment_id": environment.ID})
	case r.Method == http.MethodPost && len(path) == 3 && path[1] == "environment_providers":
		environment := fake.Environments[path[2]]
		if environment == nil {
			refuse(w, http.StatusNotFound, "not_found_error", "no such environment")
			return
		}
		if fake.RefuseUpdates {
			refuse(w, http.StatusInternalServerError, "api_error", "the environment could not be updated")
			return
		}
		environment.Body = body
		answer(w, map[string]any{"environment_id": environment.ID})
	case r.Method == http.MethodPost && len(path) == 4 && path[1] == "environments" && path[3] == "archive":
		environment := fake.Environments[path[2]]
		if environment == nil {
			refuse(w, http.StatusNotFound, "not_found_error", "no such environment")
			return
		}
		environment.Archived = true
		answer(w, map[string]any{})
	case r.Method == http.MethodPost && r.URL.Path == "/v1/sessions":
		fake.create(w, body)
	case len(path) >= 3 && path[1] == "sessions":
		session := fake.Sessions[path[2]]
		if session == nil {
			refuse(w, http.StatusNotFound, "not_found_error", "no such session")
			return
		}
		switch {
		case r.Method == http.MethodGet && len(path) == 3:
			answer(w, session.record())
		case r.Method == http.MethodPost && len(path) == 4 && path[3] == "archive":
			session.Archived = true
			answer(w, map[string]any{})
		case r.Method == http.MethodPost && len(path) == 4 && path[3] == "events":
			if session.Archived {
				refuse(w, http.StatusBadRequest, "invalid_request_error", "session_inactive")
				return
			}
			events, _ := body["events"].([]any)
			for _, event := range events {
				fake.take(session, event.(map[string]any))
			}
			answer(w, map[string]any{})
		case r.Method == http.MethodGet && len(path) == 4 && path[3] == "events":
			if fake.Refusals > 0 {
				fake.Refusals--
				if fake.RefuseStatus == http.StatusTooManyRequests {
					w.Header().Set("Retry-After", "1")
				}
				refuse(w, fake.RefuseStatus, "overloaded_error", "refused by the test")
				return
			}
			answer(w, session.page(r.URL.Query().Get("after_id"), r.URL.Query().Get("limit")))
		default:
			refuse(w, http.StatusNotFound, "not_found_error", "no such route")
		}
	default:
		refuse(w, http.StatusNotFound, "not_found_error", "no such route")
	}
}

func (fake *Server) create(w http.ResponseWriter, body map[string]any) {
	environmentID, _ := body["environment_id"].(string)
	environment := fake.Environments[environmentID]
	if environment == nil || environment.Archived {
		refuse(w, http.StatusBadRequest, "invalid_request_error", "no such environment")
		return
	}
	context, _ := body["session_context"].(map[string]any)
	session := &Session{ID: fake.id("session"), EnvironmentID: environmentID, Context: context, Status: "pending"}
	fake.Sessions[session.ID] = session
	events, _ := body["events"].([]any)
	for _, wrapped := range events {
		data, _ := wrapped.(map[string]any)["data"].(map[string]any)
		fake.take(session, data)
	}
	answer(w, session.record())
}

// take is one event sent into a session: a user message is echoed and
// answered, and a control request is acknowledged.
func (fake *Server) take(session *Session, event map[string]any) {
	switch event["type"] {
	case "user":
		if fake.Drop {
			return
		}
		echo := map[string]any{}
		for key, value := range event {
			echo[key] = value
		}
		session.Events = append(session.Events, echo)
		session.Status = "running"
		text, _ := event["message"].(map[string]any)["content"].(string)
		if fake.Hold {
			fake.held = append(fake.held, heldReply{session, text})
			return
		}
		fake.reply(session, text)
	case "control_request":
		request, _ := event["request"].(map[string]any)
		if request["subtype"] == "interrupt" {
			session.Interrupts++
		}
		session.Events = append(session.Events, map[string]any{"type": "control_response", "uuid": fake.uuid(),
			"response": map[string]any{"request_id": event["request_id"], "subtype": "success"}})
	}
}

func (fake *Server) reply(session *Session, text string) {
	reply := fake.Reply
	if reply == nil {
		reply = Echo
	}
	session.Events = append(session.Events, map[string]any{"type": "env_manager_log", "uuid": fake.uuid(), "data": "booted"})
	for _, event := range reply(session, text) {
		event["uuid"] = fake.uuid()
		if _, set := event["session_id"]; !set {
			event["session_id"] = "cloud-claude-" + session.ID
		}
		session.Events = append(session.Events, event)
	}
	session.Status = "idle"
	if fake.After != "" {
		session.Status = fake.After
	}
}

// Echo is the default reply: init, the message said back, and a result.
func Echo(session *Session, text string) []map[string]any {
	return []map[string]any{
		Init(session),
		Text("echo: " + text),
		Result("echo: " + text),
	}
}

// Init is a session's init event, the hub's MCP server connected.
func Init(session *Session) map[string]any {
	model, _ := session.Context["model"].(string)
	return map[string]any{"type": "system", "subtype": "init", "model": model, "cwd": "/home/user",
		"mcp_servers": []any{map[string]any{"name": "spool", "status": "connected"}},
		"tools":       []any{"Bash", "mcp__spool__send_message"}}
}

// Text is an assistant event carrying one text block.
func Text(text string) map[string]any {
	return map[string]any{"type": "assistant", "message": map[string]any{
		"role": "assistant", "content": []any{map[string]any{"type": "text", "text": text}},
		"usage": map[string]any{"input_tokens": 10, "output_tokens": 5}}}
}

// Result is a turn's successful result.
func Result(text string) map[string]any {
	return map[string]any{"type": "result", "subtype": "success", "is_error": false, "result": text,
		"total_cost_usd": 0.0123, "num_turns": 1, "duration_ms": 1000,
		"usage": map[string]any{"input_tokens": 10, "output_tokens": 5}}
}

func (session *Session) record() map[string]any {
	return map[string]any{"id": session.ID, "session_status": session.Status, "environment_id": session.EnvironmentID,
		"session_url": "https://claude.ai/code/" + session.ID}
}

func (session *Session) page(afterID, limit string) map[string]any {
	start := 0
	if afterID != "" {
		start = len(session.Events)
		for i, event := range session.Events {
			if event["uuid"] == afterID {
				start = i + 1
				break
			}
		}
	}
	size, err := strconv.Atoi(limit)
	if err != nil || size <= 0 {
		size = 1000
	}
	end := min(start+size, len(session.Events))
	data := session.Events[start:end]
	page := map[string]any{"data": data, "has_more": end < len(session.Events)}
	if len(data) > 0 {
		page["first_id"] = data[0]["uuid"]
		page["last_id"] = data[len(data)-1]["uuid"]
	}
	return page
}

func answer(w http.ResponseWriter, body any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(body)
}

func refuse(w http.ResponseWriter, status int, kind, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"type": "error", "error": map[string]any{"type": kind, "message": message}})
}
