package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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

func (e *APIError) Error() string { return fmt.Sprintf("telegram %d: %s", e.Code, e.Desc) }

func (c *Client) call(ctx context.Context, method string, params any, result any) error {
	body, err := json.Marshal(params)
	if err != nil {
		return err
	}
	url := c.base + "/bot" + c.token + "/" + method
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var ar apiResponse
	if err := json.NewDecoder(resp.Body).Decode(&ar); err != nil {
		return fmt.Errorf("telegram %s: decode: %w", method, err)
	}
	if !ar.OK {
		e := &APIError{Code: ar.ErrorCode, Desc: ar.Description}
		if ar.Parameters != nil {
			e.RetryAfter = ar.Parameters.RetryAfter
		}
		return e
	}
	if result != nil {
		return json.Unmarshal(ar.Result, result)
	}
	return nil
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
}

type Update struct {
	UpdateID int64    `json:"update_id"`
	Message  *Message `json:"message"`
}

func (c *Client) GetMe(ctx context.Context) (*User, error) {
	var u User
	if err := c.call(ctx, "getMe", struct{}{}, &u); err != nil {
		return nil, err
	}
	return &u, nil
}

func (c *Client) GetUpdates(ctx context.Context, offset int64, timeoutSec int) ([]Update, error) {
	params := map[string]any{
		"offset":          offset,
		"timeout":         timeoutSec,
		"allowed_updates": []string{"message"},
	}
	var updates []Update
	if err := c.call(ctx, "getUpdates", params, &updates); err != nil {
		return nil, err
	}
	return updates, nil
}

func (c *Client) SendMessage(ctx context.Context, chatID int64, text string) error {
	// plain text (no parse_mode) avoids entity-escaping pitfalls
	return c.call(ctx, "sendMessage", map[string]any{"chat_id": chatID, "text": text}, nil)
}
