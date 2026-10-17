// Package cloud is the cloud runtime (ADR-0056): a loop whose claude runs
// in a Claude Code cloud session on Anthropic's machines, driven over
// Anthropic's sessions API with Go's own HTTP client.
//
// The API is undocumented: its endpoints are the ones the claude CLI calls,
// and Anthropic may change them without notice. So every call to it lives
// in this file, and each fails loudly, naming the endpoint and what came
// back, rather than guessing (ADR-0056). What comes back is untrusted: it
// carries text the loop wrote, and every response is read under a size
// bound.
package cloud

import (
	"bytes"
	"context"
	crand "crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// DefaultBaseURL is Anthropic's API, where the sessions API lives.
const DefaultBaseURL = "https://api.anthropic.com"

// The beta flags each family of endpoints is served under, as the claude
// CLI sends them.
const (
	sessionsBeta     = "ccr-byoc-2025-07-29"
	environmentsBeta = "environments-2025-11-01"
	apiVersion       = "2023-06-01"
)

// maxResponse bounds every response body read from the API. A page of
// events is the largest: a turn's text runs to tens of KiB, but a page of
// large tool results can run far past that.
const maxResponse = 4 << 20

// An answer over maxResponse is read on, without being kept, as far as
// maxSkipped, for its last tailBytes: a page's last_id is at its end, and
// it is how a poll gets past an event too large to read.
const (
	maxSkipped = 64 << 20
	tailBytes  = 1 << 10
)

// errorSnippet is how much of an unexpected response an error quotes.
const errorSnippet = 300

// Account is the claude.ai account whose plan cloud loops spend: the
// OAuth access token of its sign-in, and the organization it acts in.
// Never logged.
type Account struct {
	Token   string
	OrgUUID string
}

// ErrNoAccount is what a call returns when the hub holds no signed-in
// account, which is every hub until the operator signs in (ADR-0056).
var ErrNoAccount = errors.New("cloud: no Claude account is signed in")

// Client calls the sessions and environments API for one hub.
type Client struct {
	// BaseURL is the API's origin; empty is DefaultBaseURL. Tests point
	// it at a fake.
	BaseURL string
	// HTTP is the client calls go through; nil is one with a 30-second
	// timeout. Whichever it is, calls never follow a redirect: they carry
	// the account's token, and the API has no reason to send one.
	HTTP *http.Client
	// Account returns the signed-in account, or ErrNoAccount. Read on
	// every call, so a sign-in or a removal takes effect at once.
	Account func(ctx context.Context) (Account, error)
}

// APIError is a response the client did not expect: a status, or a body it
// could not read. It names the endpoint and quotes what came back.
type APIError struct {
	Method   string
	Endpoint string // the path, without the host or query
	Status   int    // 0 when the status was right and the body was not
	Body     string // the start of the body, at most errorSnippet bytes
	Reason   string // what was wrong with it, when Status is 0
	// ErrorType and ErrorMessage are the error object a refusal carries,
	// decoded from the whole body rather than the quoted start of it.
	ErrorType    string
	ErrorMessage string
	// Oversized says the answer ran past maxResponse. Tail is then its
	// last tailBytes, read without keeping the rest, or "" when the answer
	// ran past maxSkipped as well.
	Oversized bool
	Tail      string
	// RetryAfter is how long the answer's Retry-After asked a client to
	// wait, 0 when it named no number of seconds.
	RetryAfter time.Duration
}

// lastIDPattern finds a page's last_id, which follows its data.
var lastIDPattern = regexp.MustCompile(`"last_id"\s*:\s*"([^"\\]+)"\s*[,}]`)

// LastID is the last_id an oversized page of events ends with, "" when its
// tail names none. It is how a poll gets past an event too large to read.
func (err *APIError) LastID() string {
	match := lastIDPattern.FindAllStringSubmatch(err.Tail, -1)
	if len(match) == 0 {
		return ""
	}
	return match[len(match)-1][1]
}

func (err *APIError) Error() string {
	if err.Status != 0 {
		return fmt.Sprintf("cloud API %s %s answered %d: %s", err.Method, err.Endpoint, err.Status, err.Body)
	}
	return fmt.Sprintf("cloud API %s %s: %s: %s", err.Method, err.Endpoint, err.Reason, err.Body)
}

// Transient reports whether the API refused for now rather than for good:
// too many requests, or a fault or an overload of its own.
func (err *APIError) Transient() bool {
	return err.Status == http.StatusTooManyRequests || err.Status >= 500
}

// Inactive reports whether the API refused because the session is
// archived, which is how it answers events sent into one. Which field of
// the error object carries the code is not pinned, so either is taken, but
// only whole: a message that merely mentions the code doesn't count.
func (err *APIError) Inactive() bool {
	return err.ErrorType == "session_inactive" || err.ErrorMessage == "session_inactive"
}

// Session is a cloud session as the API reports it.
type Session struct {
	ID            string `json:"id"`
	Status        string `json:"session_status"` // pending|running|idle|requires_action
	URL           string `json:"session_url"`
	EnvironmentID string `json:"environment_id"`
}

// The session statuses the runtime acts on (ADR-0056).
const (
	StatusPending        = "pending"
	StatusRunning        = "running"
	StatusIdle           = "idle"
	StatusRequiresAction = "requires_action"
)

// SessionSpec is what a new session is created with.
type SessionSpec struct {
	EnvironmentID string
	Title         string
	Model         string
	// AppendSystemPrompt is the text a contained loop gets through
	// --append-system-prompt, built in internal/loop/prompt.go.
	AppendSystemPrompt string
	// Env holds the session's own environment variables: the loop's hub
	// MCP token among them (ADR-0056). The session's record never serves
	// them back. Never logged.
	Env map[string]string
	// Repo is the GitHub repo the session starts from, "" for none: an
	// empty home directory.
	Repo string
	// FirstMessage is the turn the session starts with.
	FirstMessage string
	// MessageID is FirstMessage's uuid, which its echo in the event
	// stream carries.
	MessageID string
}

// CreateSession starts a session in an environment, in bypassPermissions
// mode, taking its first turn at once. It is created with an explicit,
// empty mcp_config, which keeps the built-in session tools out of it: they
// could start and message other sessions on the account (ADR-0056). The
// hub's MCP server reaches the session through the environment's setup
// script instead.
func (client *Client) CreateSession(ctx context.Context, spec SessionSpec) (Session, error) {
	env := spec.Env
	if env == nil {
		env = map[string]string{}
	}
	body := map[string]any{
		"title":          spec.Title,
		"environment_id": spec.EnvironmentID,
		"events": []any{
			map[string]any{"type": "event", "data": permissionModeEvent(spec.MessageID)},
			map[string]any{"type": "event", "data": userEvent(spec.FirstMessage, spec.MessageID)},
		},
		"session_context": map[string]any{
			"sources":               sources(spec.Repo),
			"outcomes":              []any{},
			"model":                 spec.Model,
			"append_system_prompt":  spec.AppendSystemPrompt,
			"environment_variables": env,
			"mcp_config":            map[string]any{"mcpServers": map[string]any{}},
		},
	}
	var session Session
	err := client.call(ctx, http.MethodPost, "/v1/sessions", nil, sessionsBeta, body, &session)
	if err == nil && session.ID == "" {
		err = &APIError{Method: http.MethodPost, Endpoint: "/v1/sessions", Reason: "no session id in the answer"}
	}
	return session, err
}

// GetSession reads a session's record, its status among it.
func (client *Client) GetSession(ctx context.Context, id string) (Session, error) {
	var session Session
	err := client.call(ctx, http.MethodGet, "/v1/sessions/"+url.PathEscape(id), nil, sessionsBeta, nil, &session)
	return session, err
}

// SendMessage sends one user message into a session, which starts a turn,
// reviving the session's machine if Anthropic had reclaimed it.
func (client *Client) SendMessage(ctx context.Context, id, text, messageID string) error {
	return client.sendEvents(ctx, id, userEvent(text, messageID))
}

// Interrupt ends the turn in flight, as the control room's kill does. Each
// carries a request_id of its own, as the claude CLI's do: the id pairs a
// control_response with its request, so a second kill reusing the first's
// could be taken as already answered.
func (client *Client) Interrupt(ctx context.Context, id string) error {
	return client.sendEvents(ctx, id, map[string]any{
		"type":       "control_request",
		"request_id": newUUID(),
		"request":    map[string]any{"subtype": "interrupt"},
	})
}

// ArchiveSession ends a session for good: it refuses events from then on.
func (client *Client) ArchiveSession(ctx context.Context, id string) error {
	return client.call(ctx, http.MethodPost, "/v1/sessions/"+url.PathEscape(id)+"/archive", nil, sessionsBeta, map[string]any{}, nil)
}

func (client *Client) sendEvents(ctx context.Context, id string, events ...any) error {
	return client.call(ctx, http.MethodPost, "/v1/sessions/"+url.PathEscape(id)+"/events", nil, sessionsBeta,
		map[string]any{"events": events}, nil)
}

// EventPage is one page of a session's event stream: the stream-json
// events its claude emitted, each still raw, oldest first.
type EventPage struct {
	Events  []json.RawMessage `json:"data"`
	LastID  string            `json:"last_id"`
	HasMore bool              `json:"has_more"`
}

// Events reads at most limit of the session's events after the one afterID
// names, or from the start when afterID is empty. A limit of 0 leaves the
// page size to the API.
func (client *Client) Events(ctx context.Context, id, afterID string, limit int) (EventPage, error) {
	query := url.Values{}
	if afterID != "" {
		query.Set("after_id", afterID)
	}
	if limit > 0 {
		query.Set("limit", strconv.Itoa(limit))
	}
	var page EventPage
	err := client.call(ctx, http.MethodGet, "/v1/sessions/"+url.PathEscape(id)+"/events", query, sessionsBeta, nil, &page)
	return page, err
}

// EnvironmentSpec is a cloud environment as the hub keeps it: a setup
// script, and the network allowlist that Anthropic's proxy holds its
// sessions to. Never Anthropic's Trusted defaults (ADR-0056).
type EnvironmentSpec struct {
	Name         string
	Description  string
	InitScript   string
	AllowedHosts []string
}

func (spec EnvironmentSpec) body() map[string]any {
	hosts := spec.AllowedHosts
	if hosts == nil {
		hosts = []string{}
	}
	return map[string]any{
		"name":        spec.Name,
		"kind":        "anthropic_cloud",
		"description": spec.Description,
		"config": map[string]any{
			"environment_type": "anthropic",
			"cwd":              "/home/user",
			"init_script":      spec.InitScript,
			"environment":      map[string]string{},
			"network_config": map[string]any{
				"allowed_hosts":       hosts,
				"allow_default_hosts": false,
				// The hub's host is in allowed_hosts, so the hub's MCP
				// server needs no exception; this one would let a session
				// reach whatever MCP server its own config names.
				"allow_mcp_servers": false,
			},
		},
	}
}

// CreateEnvironment makes a Custom environment and returns its id.
func (client *Client) CreateEnvironment(ctx context.Context, spec EnvironmentSpec) (string, error) {
	var created struct {
		EnvironmentID string `json:"environment_id"`
		ID            string `json:"id"`
	}
	const endpoint = "/v1/environment_providers/cloud/create"
	if err := client.call(ctx, http.MethodPost, endpoint, nil, sessionsBeta, spec.body(), &created); err != nil {
		return "", err
	}
	// The CLI reads the answer whole; which field carries the id is not
	// pinned, so either is taken.
	switch {
	case created.EnvironmentID != "":
		return created.EnvironmentID, nil
	case created.ID != "":
		return created.ID, nil
	}
	return "", &APIError{Method: http.MethodPost, Endpoint: endpoint, Reason: "no environment id in the answer"}
}

// UpdateEnvironment replaces an environment's setup script and allowlist.
func (client *Client) UpdateEnvironment(ctx context.Context, id string, spec EnvironmentSpec) error {
	body := spec.body()
	delete(body, "kind")
	return client.call(ctx, http.MethodPost, "/v1/environment_providers/"+url.PathEscape(id), nil, sessionsBeta, body, nil)
}

// ArchiveEnvironment stops new sessions starting in an environment.
// Environments can't be deleted; sessions already running carry on.
func (client *Client) ArchiveEnvironment(ctx context.Context, id string) error {
	return client.call(ctx, http.MethodPost, "/v1/environments/"+url.PathEscape(id)+"/archive",
		url.Values{"beta": {"true"}}, environmentsBeta, map[string]any{}, nil)
}

// sources is a session's sources: the repo it starts from, if any. A
// session with none starts in an empty home directory (ADR-0056).
func sources(repo string) []any {
	if repo == "" {
		return []any{}
	}
	return []any{map[string]any{"type": "git_repository", "url": repo}}
}

// permissionModeEvent opens a session's permissions: without it, the first
// tool call parks the session in requires_action (ADR-0056).
func permissionModeEvent(messageID string) map[string]any {
	return map[string]any{
		"type":       "control_request",
		"request_id": "spool-mode-" + messageID,
		"request":    map[string]any{"subtype": "set_permission_mode", "mode": "bypassPermissions"},
	}
}

func userEvent(text, messageID string) map[string]any {
	return map[string]any{
		"uuid":               messageID,
		"session_id":         "",
		"type":               "user",
		"parent_tool_use_id": nil,
		"message":            map[string]any{"role": "user", "content": text},
	}
}

// call makes one request and decodes a 2xx answer into out, when out is
// not nil. Anything else is an *APIError.
func (client *Client) call(ctx context.Context, method, endpoint string, query url.Values, beta string, body, out any) error {
	if client.Account == nil {
		return ErrNoAccount
	}
	account, err := client.Account(ctx)
	if err != nil {
		return err
	}
	base := client.BaseURL
	if base == "" {
		base = DefaultBaseURL
	}
	target := strings.TrimSuffix(base, "/") + endpoint
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+account.Token)
	req.Header.Set("anthropic-version", apiVersion)
	req.Header.Set("anthropic-beta", beta)
	req.Header.Set("x-organization-uuid", account.OrgUUID)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	httpClient := http.Client{Timeout: 30 * time.Second}
	if client.HTTP != nil {
		httpClient = *client.HTTP
	}
	// A 3xx comes back as the answer, so it fails as an *APIError.
	httpClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("cloud API %s %s: %w", method, endpoint, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse+1))
	if err != nil {
		return fmt.Errorf("cloud API %s %s: reading the answer: %w", method, endpoint, err)
	}
	if len(raw) > maxResponse {
		return &APIError{Method: method, Endpoint: endpoint, Reason: fmt.Sprintf("answer over %d bytes", maxResponse), Body: snippet(raw),
			Oversized: true, Tail: readTail(resp.Body, raw)}
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		var refusal struct {
			Error struct {
				Type    string `json:"type"`
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(raw, &refusal) // a body that isn't one leaves both empty
		return &APIError{Method: method, Endpoint: endpoint, Status: resp.StatusCode, Body: snippet(raw),
			ErrorType: refusal.Error.Type, ErrorMessage: refusal.Error.Message, RetryAfter: retryAfter(resp.Header.Get("Retry-After"))}
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return &APIError{Method: method, Endpoint: endpoint, Reason: "unreadable answer: " + err.Error(), Body: snippet(raw)}
	}
	return nil
}

// newUUID returns a random v4 UUID.
func newUUID() string {
	var random [16]byte
	if _, err := crand.Read(random[:]); err != nil {
		panic(err)
	}
	random[6] = (random[6] & 0x0f) | 0x40
	random[8] = (random[8] & 0x3f) | 0x80
	dst := make([]byte, 32)
	hex.Encode(dst, random[:])
	return fmt.Sprintf("%s-%s-%s-%s-%s", dst[0:8], dst[8:12], dst[12:16], dst[16:20], dst[20:32])
}

// readTail reads the rest of an answer that ran past maxResponse, keeping
// only its last tailBytes; read is what was read of it already. An answer
// past maxSkipped is given up on, with no tail.
func readTail(body io.Reader, read []byte) string {
	tail := append([]byte(nil), read[max(0, len(read)-tailBytes):]...)
	total := int64(len(read))
	chunk := make([]byte, 64<<10)
	for {
		n, err := body.Read(chunk)
		total += int64(n)
		if total > maxSkipped {
			return ""
		}
		tail = append(tail, chunk[:n]...)
		if len(tail) > tailBytes {
			tail = append(tail[:0], tail[len(tail)-tailBytes:]...)
		}
		if err != nil {
			return string(tail)
		}
	}
}

func snippet(raw []byte) string {
	if len(raw) > errorSnippet {
		return string(raw[:errorSnippet]) + "…"
	}
	return string(raw)
}

// retryAfter reads a Retry-After header given in seconds. The HTTP-date
// form, and anything else, is 0: the caller's own backoff stands.
func retryAfter(header string) time.Duration {
	seconds, err := strconv.Atoi(strings.TrimSpace(header))
	if err != nil || seconds <= 0 {
		return 0
	}
	return time.Duration(seconds) * time.Second
}

// CheckAccount makes one read-only call, listing the account's
// environments, that spends no turn (ADR-0056): it passes when the API
// takes the account's token and organization.
func (client *Client) CheckAccount(ctx context.Context) error {
	return client.call(ctx, http.MethodGet, "/v1/environment_providers", url.Values{"limit": {"1"}}, sessionsBeta, nil, nil)
}
