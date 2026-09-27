package slack

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// A minimal Slack Web API client: the handful of JSON-over-HTTP methods a
// loop's app needs, in the shape of the Telegram surface's own client. The
// Socket Mode connection is a separate protocol and does not live here.

// APIBase is the live Slack Web API. Tests point a client at a stand-in
// server instead; nothing else varies it.
const APIBase = "https://slack.com/api"

type Client struct {
	base string
	http *http.Client
}

// NewClientAt talks to a Web API at base, APIBase in production.
func NewClientAt(base string) *Client {
	if base == "" {
		base = APIBase
	}
	return &Client{base: strings.TrimSuffix(base, "/"), http: &http.Client{Timeout: 30 * time.Second}}
}

// APIError is Slack answering a call with "ok": false. Code is Slack's own
// error string (invalid_auth, not_allowed_token_type, …).
type APIError struct {
	Method string
	Code   string
}

func (apiErr *APIError) Error() string {
	return fmt.Sprintf("slack %s: %s", apiErr.Method, apiErr.Code)
}

// transient are the codes with which Slack declines to answer rather than
// answering no: the credential has not been judged.
var transient = map[string]bool{
	"ratelimited": true, "internal_error": true, "fatal_error": true,
	"request_timeout": true, "service_unavailable": true,
}

// Refused reports whether Slack judged the credential and said no.
func (apiErr *APIError) Refused() bool { return !transient[apiErr.Code] }

// call posts a form-encoded Web API request as token and decodes the answer
// into result, which must carry the envelope's ok and error fields through
// apiEnvelope.
func (client *Client) call(ctx context.Context, token, method string, params url.Values, result interface{ envelope() apiEnvelope }) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, client.base+"/"+method, strings.NewReader(params.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := client.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusTooManyRequests {
		return &APIError{Method: method, Code: "ratelimited"}
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("slack %s: HTTP %d", method, resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(result); err != nil {
		return fmt.Errorf("slack %s: decode: %w", method, err)
	}
	if envelope := result.envelope(); !envelope.OK {
		return &APIError{Method: method, Code: envelope.Error}
	}
	return nil
}

type apiEnvelope struct {
	OK    bool   `json:"ok"`
	Error string `json:"error"`
}

func (envelope apiEnvelope) envelope() apiEnvelope { return envelope }

// AuthTest is what auth.test says a token is. BotID is empty for a token
// that is not a bot's.
type AuthTest struct {
	apiEnvelope
	UserID string `json:"user_id"`
	User   string `json:"user"`
	TeamID string `json:"team_id"`
	Team   string `json:"team"`
	BotID  string `json:"bot_id"`
}

func (client *Client) AuthTest(ctx context.Context, token string) (*AuthTest, error) {
	var result AuthTest
	if err := client.call(ctx, token, "auth.test", url.Values{}, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

type connectionsOpen struct {
	apiEnvelope
	URL string `json:"url"`
}

// OpenConnection asks for a Socket Mode URL with an app-level token. Only an
// app-level token with connections:write gets one.
func (client *Client) OpenConnection(ctx context.Context, appToken string) (string, error) {
	var result connectionsOpen
	if err := client.call(ctx, appToken, "apps.connections.open", url.Values{}, &result); err != nil {
		return "", err
	}
	return result.URL, nil
}
