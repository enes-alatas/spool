package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"testing"

	"github.com/enes-alatas/spool/internal/promptschat"
)

// botFixture answers every fetch with the promptschat package's fixture
// bot, or with err.
type botFixture struct{ err error }

func (fixture botFixture) Fetch(context.Context, string) ([]byte, error) {
	if fixture.err != nil {
		return nil, fixture.err
	}
	return os.ReadFile("../promptschat/testdata/bot.json")
}

func importDraft(t *testing.T, server *Server, botURL string) (int, map[string]any) {
	t.Helper()
	recorder := httptest.NewRecorder()
	server.handleImportPromptsChat(recorder, httptest.NewRequest(http.MethodGet, "/api/import/prompts-chat?url="+url.QueryEscape(botURL), nil))
	var body map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("answer %q: %v", recorder.Body.String(), err)
	}
	return recorder.Code, body
}

// TestImportAnswersABotsDraft: the route answers a bot's draft, the hub's
// default runtime where the bot hints none, and creates nothing.
func TestImportAnswersABotsDraft(t *testing.T) {
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	server := &Server{Log: quiet, PromptsChat: botFixture{}, DefaultRuntime: "docker"}
	code, body := importDraft(t, server, "https://prompts.chat/bots/cfixture0001_release-notes-scribe.md")
	if code != http.StatusOK {
		t.Fatalf("status %d: %v", code, body)
	}
	if body["name"] != "release-notes-scribe" || body["runtime"] != "cloud" ||
		!reflect.DeepEqual(body["egress"], []any{
			map[string]any{"host": "github.com", "connector": "GitHub"},
			map[string]any{"host": "api.github.com", "connector": "GitHub"},
			map[string]any{"host": "slack.com", "connector": "Slack"},
		}) ||
		body["source"] != "https://prompts.chat/bots/cfixture0001_release-notes-scribe" || body["mission"] == "" {
		t.Errorf("draft = %v", body)
	}

	// a bot with no cloud requirement takes the hub's default
	server.PromptsChat = localBot{}
	if _, body := importDraft(t, server, "https://prompts.chat/bots/x_local"); body["runtime"] != "docker" {
		t.Errorf("runtime of a bot with no hint = %v; want the hub's default, docker", body["runtime"])
	}
}

// localBot is a bot with no computer requirement.
type localBot struct{}

func (localBot) Fetch(context.Context, string) ([]byte, error) {
	return []byte(`{"slug":"local","instructions":"Help.","computerRequirements":[]}`), nil
}

// TestImportRefusesWithAReason: another host is refused 400 with the
// reason, a failed fetch is 502, and a hub with no fetcher is 503, each
// with its code.
func TestImportRefusesWithAReason(t *testing.T) {
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	for _, test := range []struct {
		fetcher promptschat.Fetcher
		botURL  string
		status  int
		code    string
	}{
		{botFixture{}, "https://example.com/bots/x_y", http.StatusBadRequest, codeImportRefused},
		{botFixture{err: promptschat.ErrNotFound}, "https://prompts.chat/bots/x_y", http.StatusBadRequest, codeImportRefused},
		{botFixture{err: errors.New("connection reset")}, "https://prompts.chat/bots/x_y", http.StatusBadGateway, codeImportFailed},
		{nil, "https://prompts.chat/bots/x_y", http.StatusServiceUnavailable, codeImportOff},
	} {
		server := &Server{Log: quiet, PromptsChat: test.fetcher}
		status, body := importDraft(t, server, test.botURL)
		if status != test.status || body["code"] != test.code || body["error"] == "" {
			t.Errorf("import of %q = %d %v; want %d with code %q and a reason", test.botURL, status, body, test.status, test.code)
		}
	}
}
