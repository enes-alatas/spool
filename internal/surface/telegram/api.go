package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"strconv"
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
	// http carries every call but the long poll, and longPoll that alone:
	// Telegram holds a getUpdates open for up to pollTimeoutSec before it
	// answers, and answers everything else in seconds, so the two wait on
	// different clocks.
	http     *http.Client
	longPoll *http.Client
}

const (
	// DefaultAnswerTimeout is how long a call other than the long poll waits
	// for Telegram's answer once its request is written. Telegram answers
	// in about a second; a call left waiting longer is on a connection that
	// died under it, and a retry gets through on a fresh one (#559). Before
	// this the wait was the long poll's 70s, so one dead connection held a
	// bot's sends for minutes and gave up messages a new connection would
	// have carried.
	DefaultAnswerTimeout = 20 * time.Second
	// callTimeout bounds a call whole, upload included: the 20 MB file
	// Telegram takes outlasts any answer timeout on a slow link.
	callTimeout = 70 * time.Second
)

func NewClient(token string) *Client { return NewClientAt(APIBase, token) }

// NewClientAt talks to a Bot API at base — APIBase in production.
func NewClientAt(base, token string) *Client {
	return newClient(base, token, DefaultAnswerTimeout)
}

// newClient is NewClientAt with the answer timeout a test may shorten.
//
// Each client dials its own connections, over HTTP/1.1. Over HTTP/2 every
// bot multiplexed onto one shared connection to api.telegram.org, and a
// call that timed out left that connection pooled: when it died, every
// bot's every retry was written into it and waited out the full timeout.
// Over HTTP/1.1 a call that times out closes its connection, so a retry
// dials fresh, and one bot's connections are no other bot's.
func newClient(base, token string, answerTimeout time.Duration) *Client {
	if base == "" {
		base = APIBase
	}
	return &Client{token: token, base: strings.TrimSuffix(base, "/"),
		http:     &http.Client{Timeout: callTimeout, Transport: http1Transport(answerTimeout)},
		longPoll: &http.Client{Timeout: callTimeout, Transport: http1Transport(0)}}
}

// http1Transport is a connection pool of its own that speaks HTTP/1.1 only
// and waits answerTimeout for an answer's headers once the request is
// written (0 = no wait of its own beyond the call's).
func http1Transport(answerTimeout time.Duration) *http.Transport {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	var protocols http.Protocols
	protocols.SetHTTP1(true)
	transport.Protocols = &protocols
	transport.ResponseHeaderTimeout = answerTimeout
	return transport
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
	return client.post(ctx, method, "application/json", bytes.NewReader(body), result)
}

// post sends one Bot API request whose body is already encoded, and decodes
// the answer into result.
func (client *Client) post(ctx context.Context, method, contentType string, body io.Reader, result any) error {
	url := client.base + "/bot" + client.token + "/" + method
	req, err := http.NewRequestWithContext(ctx, "POST", url, body)
	if err != nil {
		return redactToken(err, client.token)
	}
	req.Header.Set("Content-Type", contentType)
	caller := client.http
	if method == "getUpdates" {
		caller = client.longPoll
	}
	resp, err := caller.Do(req)
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
	// A group upgraded to a supergroup gets a new chat id: Telegram says so
	// with a service message in each, MigrateToChatID in the old chat and
	// MigrateFromChatID in the new one.
	MigrateToChatID   int64 `json:"migrate_to_chat_id,omitempty"`
	MigrateFromChatID int64 `json:"migrate_from_chat_id,omitempty"`
	// Poll is the poll a message carries; sendPoll answers with one.
	Poll *Poll `json:"poll,omitempty"`
}

// Poll is a native poll. Its ID, not its message, is what each vote names.
type Poll struct {
	ID string `json:"id"`
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
	// MessageReaction is a person's reactions on a message changing. In a
	// group a bot hears it only as an administrator (ADR-0040).
	MessageReaction *MessageReactionUpdated `json:"message_reaction"`
	// PollAnswer is a person's choice in a poll changing. A bot hears it
	// only in a non-anonymous poll it sent itself (ADR-0041).
	PollAnswer *PollAnswer `json:"poll_answer"`
}

// PollAnswer is one voter's whole choice in one poll: OptionIDs are the
// options picked, empty once retracted. User is nil when a chat voted.
type PollAnswer struct {
	PollID    string `json:"poll_id"`
	User      *User  `json:"user"`
	OptionIDs []int  `json:"option_ids"`
}

// MessageReactionUpdated is one reactor's reactions on one message, before
// and after a change. MessageID is in the receiving bot's own numbering
// (ADR-0020). User is nil when an anonymous admin or a chat reacted.
type MessageReactionUpdated struct {
	Chat        Chat           `json:"chat"`
	MessageID   int64          `json:"message_id"`
	User        *User          `json:"user"`
	Date        int64          `json:"date"`
	OldReaction []ReactionType `json:"old_reaction"`
	NewReaction []ReactionType `json:"new_reaction"`
}

// ReactionType is one reaction. Only Type "emoji" names a Unicode emoji;
// "custom_emoji" and "paid" carry none.
type ReactionType struct {
	Type  string `json:"type"`
	Emoji string `json:"emoji,omitempty"`
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
		"allowed_updates": []string{"message", "message_reaction", "poll_answer"},
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

// SendPoll sends a native poll, non-anonymous so each vote names its voter
// (ADR-0041). replyTo is as SendMessage's.
func (client *Client) SendPoll(ctx context.Context, chatID int64, question string, options []string, multiple bool, replyTo int64) (*Message, error) {
	inputs := make([]map[string]string, len(options))
	for i, option := range options {
		inputs[i] = map[string]string{"text": option}
	}
	params := map[string]any{"chat_id": chatID, "question": question, "options": inputs,
		"is_anonymous": false, "allows_multiple_answers": multiple}
	if replyTo != 0 {
		params["reply_parameters"] = map[string]any{
			"message_id": replyTo, "allow_sending_without_reply": true,
		}
	}
	var sent Message
	if err := client.call(ctx, "sendPoll", params, &sent); err != nil {
		return nil, err
	}
	return &sent, nil
}

// StopPoll closes a poll this bot sent, by its own id for the message.
func (client *Client) StopPoll(ctx context.Context, chatID, messageID int64) error {
	var stopped Poll
	return client.call(ctx, "stopPoll", map[string]any{"chat_id": chatID, "message_id": messageID}, &stopped)
}

// SetMessageReaction sets this bot's reaction on a message, by this bot's
// own id for it, or clears it when emoji is "". A bot sets one reaction per
// message, so it replaces whatever the bot had there.
func (client *Client) SetMessageReaction(ctx context.Context, chatID, messageID int64, emoji string) error {
	reaction := []ReactionType{}
	if emoji != "" {
		reaction = append(reaction, ReactionType{Type: "emoji", Emoji: emoji})
	}
	var ok bool
	return client.call(ctx, "setMessageReaction",
		map[string]any{"chat_id": chatID, "message_id": messageID, "reaction": reaction}, &ok)
}

// Media is a file to send with a message, as SendMedia uploads it.
type Media struct {
	Path string // the file on the host
	Name string // what the chat calls it
	// Photo sends it as a photo, which Telegram recompresses and shows
	// inline; otherwise it goes as a document, byte for byte.
	Photo bool
}

// SendMedia uploads a file to a chat as a photo or a document, with caption
// as its words ("" for none), and returns the message Telegram created.
// replyTo is as SendMessage's. The file is read afresh on every call, so a
// retry sends it whole.
func (client *Client) SendMedia(ctx context.Context, chatID int64, media Media, caption string, replyTo int64) (*Message, error) {
	file, err := os.Open(media.Path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	method, field := "sendDocument", "document"
	if media.Photo {
		method, field = "sendPhoto", "photo"
	}
	// Streamed through a pipe rather than built in memory: a 20 MB file
	// need not sit in the hub twice.
	reader, writer := io.Pipe()
	form := multipart.NewWriter(writer)
	go func() {
		writer.CloseWithError(writeMediaForm(form, file, field, media.Name, chatID, caption, replyTo))
	}()
	defer func() { _ = reader.Close() }()
	var sent Message
	if err := client.post(ctx, method, form.FormDataContentType(), reader, &sent); err != nil {
		return nil, err
	}
	return &sent, nil
}

// writeMediaForm writes SendMedia's multipart body.
func writeMediaForm(form *multipart.Writer, file io.Reader, field, name string, chatID int64, caption string, replyTo int64) error {
	fields := map[string]string{"chat_id": strconv.FormatInt(chatID, 10)}
	if caption != "" {
		fields["caption"] = caption
	}
	if replyTo != 0 {
		// as SendMessage: a vanished target still gets the file, unthreaded
		fields["reply_parameters"] = fmt.Sprintf(`{"message_id":%d,"allow_sending_without_reply":true}`, replyTo)
	}
	for _, key := range []string{"chat_id", "caption", "reply_parameters"} {
		if value, ok := fields[key]; ok {
			if err := form.WriteField(key, value); err != nil {
				return err
			}
		}
	}
	part, err := form.CreateFormFile(field, name)
	if err != nil {
		return err
	}
	if _, err := io.Copy(part, file); err != nil {
		return err
	}
	return form.Close()
}
