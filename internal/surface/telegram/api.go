package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Minimal Telegram Bot API client: getMe, getUpdates long-poll, sendMessage.

// APIBase is the live Telegram Bot API. Tests point a client at a stand-in
// server instead; nothing else varies it.
const APIBase = "https://api.telegram.org"

type Client struct {
	token string
	base  string
	http  *http.Client
}

func NewClient(token string) *Client { return NewClientAt(APIBase, token) }

// NewClientAt talks to a Bot API at base — APIBase in production.
func NewClientAt(base, token string) *Client {
	if base == "" {
		base = APIBase
	}
	return &Client{token: token, base: strings.TrimSuffix(base, "/"),
		http: &http.Client{Timeout: 70 * time.Second}}
}

type apiResponse struct {
	OK          bool            `json:"ok"`
	Result      json.RawMessage `json:"result"`
	Description string          `json:"description"`
	ErrorCode   int             `json:"error_code"`
	Parameters  *struct {
		RetryAfter int `json:"retry_after"`
	} `json:"parameters"`
}

// APIError carries the Telegram error code (401 bad token, 409 duplicate
// poller, 429 rate limit with RetryAfter).
type APIError struct {
	Code       int
	Desc       string
	RetryAfter int
}

func (apiErr *APIError) Error() string {
	return fmt.Sprintf("telegram %d: %s", apiErr.Code, apiErr.Desc)
}

func (client *Client) call(ctx context.Context, method string, params any, result any) error {
	body, err := json.Marshal(params)
	if err != nil {
		return err
	}
	url := client.base + "/bot" + client.token + "/" + method
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return redactToken(err, client.token)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.http.Do(req)
	if err != nil {
		return redactToken(err, client.token)
	}
	defer resp.Body.Close()
	var ar apiResponse
	if err := json.NewDecoder(resp.Body).Decode(&ar); err != nil {
		return fmt.Errorf("telegram %s: decode: %w", method, err)
	}
	if !ar.OK {
		apiErr := &APIError{Code: ar.ErrorCode, Desc: ar.Description}
		if ar.Parameters != nil {
			apiErr.RetryAfter = ar.Parameters.RetryAfter
		}
		return apiErr
	}
	if result != nil {
		return json.Unmarshal(ar.Result, result)
	}
	return nil
}

// redactToken keeps a bot token out of an error's text. net/http puts the
// request URL in every transport error, and the token is in the URL — so a
// timeout against api.telegram.org carries the bot's credential into
// whatever reads the error. That used to be the server log; since #147 it is
// also the message row and the loop's timeline, which the API serves.
func redactToken(err error, token string) error {
	if err == nil || token == "" {
		return err
	}
	msg := strings.ReplaceAll(err.Error(), token, "<bot-token>")
	if msg == err.Error() {
		return err
	}
	// deliberately not wrapped: the original's text is the leak
	return errors.New(msg)
}

type User struct {
	ID        int64  `json:"id"`
	IsBot     bool   `json:"is_bot"`
	FirstName string `json:"first_name"`
	Username  string `json:"username"`
}

type Chat struct {
	ID    int64  `json:"id"`
	Type  string `json:"type"` // private|group|supergroup|channel
	Title string `json:"title"`
}

type Message struct {
	MessageID int64  `json:"message_id"`
	From      *User  `json:"from"`
	Chat      Chat   `json:"chat"`
	Text      string `json:"text"`
	Date      int64  `json:"date"`
	// ReplyToMessage is the message this one natively replies to, as
	// Telegram embeds it. Its message_id is in the receiving bot's own
	// numbering, but the embedded sender, date and text are what every bot
	// sees alike — which is what makes the target identifiable at all.
	ReplyToMessage *Message `json:"reply_to_message,omitempty"`
	// A photo or a document message carries its words as Caption, not
	// Text (#123). Photo lists the sizes Telegram made of one image,
	// smallest first.
	Caption  string      `json:"caption,omitempty"`
	Photo    []PhotoSize `json:"photo,omitempty"`
	Document *Document   `json:"document,omitempty"`
}

type PhotoSize struct {
	FileID   string `json:"file_id"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
	FileSize int64  `json:"file_size"`
}

type Document struct {
	FileID   string `json:"file_id"`
	FileName string `json:"file_name"`
	MimeType string `json:"mime_type"`
	FileSize int64  `json:"file_size"`
}

// File is what getFile answers: where the bytes of a file_id can be
// downloaded from, for about an hour.
type File struct {
	FileID   string `json:"file_id"`
	FileSize int64  `json:"file_size"`
	FilePath string `json:"file_path"`
}

type Update struct {
	UpdateID int64    `json:"update_id"`
	Message  *Message `json:"message"`
}

// Download fetches a file's bytes by its file_id: getFile, then a GET on the
// file URL. The URL carries the bot token, so no error it produces is let
// out with the token in it. Telegram serves bots files up to 20 MB, the same
// limit the hub keeps.
func (client *Client) Download(ctx context.Context, fileID string) (io.ReadCloser, error) {
	var file File
	if err := client.call(ctx, "getFile", map[string]any{"file_id": fileID}, &file); err != nil {
		return nil, err
	}
	if file.FilePath == "" {
		return nil, errors.New("telegram getFile: no file_path")
	}
	url := client.base + "/file/bot" + client.token + "/" + file.FilePath
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, redactToken(err, client.token)
	}
	resp, err := client.http.Do(req)
	if err != nil {
		return nil, redactToken(err, client.token)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("telegram file download: %s", resp.Status)
	}
	return resp.Body, nil
}

func (client *Client) GetMe(ctx context.Context) (*User, error) {
	var user User
	if err := client.call(ctx, "getMe", struct{}{}, &user); err != nil {
		return nil, err
	}
	return &user, nil
}

func (client *Client) GetUpdates(ctx context.Context, offset int64, timeoutSec int) ([]Update, error) {
	params := map[string]any{
		"offset":          offset,
		"timeout":         timeoutSec,
		"allowed_updates": []string{"message"},
	}
	var updates []Update
	if err := client.call(ctx, "getUpdates", params, &updates); err != nil {
		return nil, err
	}
	return updates, nil
}

// SendMessage posts text to a chat and returns the message Telegram created,
// whose message_id is this bot's own reference to it — the only id this bot
// may later use as a reply target (ADR-0020). replyTo is such an id from
// this bot's numbering, or 0 for a plain post.
func (client *Client) SendMessage(ctx context.Context, chatID int64, text string, replyTo int64) (*Message, error) {
	// plain text (no parse_mode) avoids entity-escaping pitfalls
	params := map[string]any{"chat_id": chatID, "text": text}
	if replyTo != 0 {
		// allow_sending_without_reply: a target that vanished (deleted, or
		// too old for Telegram) must still deliver the message, unthreaded,
		// rather than fail the send.
		params["reply_parameters"] = map[string]any{
			"message_id": replyTo, "allow_sending_without_reply": true,
		}
	}
	var sent Message
	if err := client.call(ctx, "sendMessage", params, &sent); err != nil {
		return nil, err
	}
	return &sent, nil
}
