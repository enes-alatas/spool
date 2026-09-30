package slack

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
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
	// transfer moves a file's bytes. It has no timeout of its own: 20 MB
	// on a slow link outlasts the Web API's, and the caller's context
	// bounds it instead.
	transfer *http.Client
}

// NewClientAt talks to a Web API at base, APIBase in production.
func NewClientAt(base string) *Client {
	if base == "" {
		base = APIBase
	}
	return &Client{base: strings.TrimSuffix(base, "/"), http: &http.Client{Timeout: 30 * time.Second}, transfer: &http.Client{}}
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

// User is who users.info says a Slack user is. Name is their handle, which
// Slack no longer shows but still keeps unique; DisplayName is what it
// shows, and may be "".
type User struct {
	ID          string
	TeamID      string
	Name        string
	DisplayName string
}

type usersInfo struct {
	apiEnvelope
	User struct {
		ID      string `json:"id"`
		TeamID  string `json:"team_id"`
		Name    string `json:"name"`
		Profile struct {
			DisplayName string `json:"display_name"`
			RealName    string `json:"real_name"`
		} `json:"profile"`
	} `json:"user"`
}

// UserInfo asks who userID is, as the bot token's app. It needs users:read.
func (client *Client) UserInfo(ctx context.Context, botToken, userID string) (*User, error) {
	var result usersInfo
	if err := client.call(ctx, botToken, "users.info", url.Values{"user": {userID}}, &result); err != nil {
		return nil, err
	}
	display := result.User.Profile.DisplayName
	if display == "" {
		display = result.User.Profile.RealName
	}
	return &User{ID: result.User.ID, TeamID: result.User.TeamID, Name: result.User.Name, DisplayName: display}, nil
}

type posted struct {
	apiEnvelope
	Channel string `json:"channel"`
	TS      string `json:"ts"`
}

// PostMessage posts text to channel as the bot token's app, in the thread
// threadTS starts ("" = a top-level post), and returns the ts Slack gave
// the post. Text is Slack mrkdwn: the caller escapes it.
func (client *Client) PostMessage(ctx context.Context, botToken, channel, text, threadTS string) (string, error) {
	params := url.Values{"channel": {channel}, "text": {text}}
	if threadTS != "" {
		params.Set("thread_ts", threadTS)
	}
	var result posted
	if err := client.call(ctx, botToken, "chat.postMessage", params, &result); err != nil {
		return "", err
	}
	return result.TS, nil
}

type opened struct {
	apiEnvelope
	Channel struct {
		ID string `json:"id"`
	} `json:"channel"`
}

// OpenDM returns the DM channel between the bot token's app and userID,
// opening it if it is not open yet. It needs im:write. Unlike a Telegram
// bot, a Slack app can write to someone first.
func (client *Client) OpenDM(ctx context.Context, botToken, userID string) (string, error) {
	var result opened
	if err := client.call(ctx, botToken, "conversations.open", url.Values{"users": {userID}}, &result); err != nil {
		return "", err
	}
	return result.Channel.ID, nil
}

type conversationInfo struct {
	apiEnvelope
	Channel struct {
		Name string `json:"name"`
	} `json:"channel"`
}

// ChannelName is channel's name as its members see it, without the #. It
// needs channels:read, or groups:read for a private channel.
func (client *Client) ChannelName(ctx context.Context, botToken, channel string) (string, error) {
	var result conversationInfo
	if err := client.call(ctx, botToken, "conversations.info", url.Values{"channel": {channel}}, &result); err != nil {
		return "", err
	}
	return result.Channel.Name, nil
}

// File is a file as Slack describes it on a message event and in
// files.info (#123). A file shared from another organization can arrive
// with its id alone and FileAccess "check_file_info", its details left to
// files.info.
type File struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Mimetype    string `json:"mimetype"`
	Size        int64  `json:"size"`
	DownloadURL string `json:"url_private_download"`
	FileAccess  string `json:"file_access"`
	// Mode "tombstone" is a deleted file, "hidden_by_limit" one a free
	// workspace no longer shows; neither can be downloaded.
	Mode string `json:"mode"`
}

type fileInfo struct {
	apiEnvelope
	File File `json:"file"`
}

// FileInfo asks Slack for a file's details. It needs files:read.
func (client *Client) FileInfo(ctx context.Context, botToken, fileID string) (*File, error) {
	var result fileInfo
	if err := client.call(ctx, botToken, "files.info", url.Values{"file": {fileID}}, &result); err != nil {
		return nil, err
	}
	return &result.File, nil
}

// fileHost is where Slack serves the files shared in a workspace.
const fileHost = "files.slack.com"

// Download fetches a file's bytes from its url_private_download, as the
// bot token's app. The URL comes from the event, so the token goes only to
// Slack's file host, or to the Web API's own host, which is where a test's
// stand-in serves files; anywhere else is refused before a request. Without
// files:read Slack answers with its sign-in page rather than an error, so an
// HTML answer is one.
func (client *Client) Download(ctx context.Context, botToken, fileURL string) (io.ReadCloser, error) {
	if err := client.fileURLAllowed(fileURL); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fileURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+botToken)
	resp, err := client.transfer.Do(req)
	if err != nil {
		return nil, err
	}
	switch {
	case resp.StatusCode != http.StatusOK:
		resp.Body.Close()
		return nil, fmt.Errorf("slack file download: HTTP %d", resp.StatusCode)
	case strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html"):
		resp.Body.Close()
		return nil, errors.New("slack file download: answered with a sign-in page; the app needs files:read, so reinstall it")
	}
	return resp.Body, nil
}

// fileURLAllowed refuses a download URL the bot token must not be sent to:
// one not on https at Slack's file host, and not on the Web API's own host.
func (client *Client) fileURLAllowed(fileURL string) error {
	target, err := url.Parse(fileURL)
	if err != nil {
		return fmt.Errorf("slack file download: unreadable URL: %w", err)
	}
	if target.Scheme == "https" && target.Host == fileHost {
		return nil
	}
	if base, err := url.Parse(client.base); err == nil && target.Scheme == base.Scheme && target.Host == base.Host {
		return nil
	}
	return fmt.Errorf("slack file download: refused %s://%s, which is not Slack's file host", target.Scheme, target.Host)
}

type uploadURL struct {
	apiEnvelope
	UploadURL string `json:"upload_url"`
	FileID    string `json:"file_id"`
}

// Upload shares the file at hostPath in channel as the bot token's app, in
// the thread threadTS starts ("" = top level), named name. It is Slack's
// three steps: ask for an upload URL, send the bytes there, and complete
// the upload into the channel. It needs files:write.
func (client *Client) Upload(ctx context.Context, botToken, channel, threadTS, hostPath, name string) error {
	file, err := os.Open(hostPath)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	var target uploadURL
	if err := client.call(ctx, botToken, "files.getUploadURLExternal", url.Values{
		"filename": {name}, "length": {strconv.FormatInt(info.Size(), 10)},
	}, &target); err != nil {
		return err
	}
	// The upload URL is presigned: it takes the bytes without the token.
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target.UploadURL, file)
	if err != nil {
		return err
	}
	req.ContentLength = info.Size()
	req.Header.Set("Content-Type", "application/octet-stream")
	resp, err := client.transfer.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("slack file upload: HTTP %d", resp.StatusCode)
	}
	files, err := json.Marshal([]map[string]string{{"id": target.FileID, "title": name}})
	if err != nil {
		return err
	}
	params := url.Values{"files": {string(files)}, "channel_id": {channel}}
	if threadTS != "" {
		params.Set("thread_ts", threadTS)
	}
	var done apiEnvelope
	return client.call(ctx, botToken, "files.completeUploadExternal", params, &done)
}
