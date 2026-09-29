package slack

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/coder/websocket"

	"github.com/enes-alatas/spool/internal/surface"
)

// A stand-in Web API that knows one bot token, one user token and one
// app-level token, all synthetic.
const (
	botToken  = "xoxb-synthetic-bot"
	userToken = "xoxp-synthetic-user"
	appToken  = "xapp-synthetic-app"
)

func standIn(t *testing.T, rateLimited bool) *Adapter {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if rateLimited {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		answer := map[string]any{"ok": false, "error": "invalid_auth"}
		switch {
		case r.URL.Path == "/auth.test" && token == botToken:
			answer = map[string]any{"ok": true, "user_id": "U0BOT", "user": "terra", "team_id": "T0TEAM", "team": "Acme", "bot_id": "B0BOT"}
		case r.URL.Path == "/auth.test" && token == userToken:
			answer = map[string]any{"ok": true, "user_id": "U0PERSON", "user": "alice", "team_id": "T0TEAM", "team": "Acme"}
		case r.URL.Path == "/apps.connections.open" && token == appToken:
			answer = map[string]any{"ok": true, "url": "wss://example.invalid/link"}
		case r.URL.Path == "/apps.connections.open" && strings.HasPrefix(token, "xoxb-"):
			answer = map[string]any{"ok": false, "error": "not_allowed_token_type"}
		}
		_ = json.NewEncoder(w).Encode(answer)
	}))
	t.Cleanup(srv.Close)
	return New(nil, slog.New(slog.NewTextHandler(io.Discard, nil)), srv.URL)
}

// Both tokens are judged before either is stored, and a refusal names the
// one to fix (#230).
func TestValidateCredential(t *testing.T) {
	adapter := standIn(t, false)
	ctx := context.Background()

	identity, err := adapter.ValidateCredential(ctx, surface.Credential{Token: botToken, AppToken: appToken})
	if err != nil {
		t.Fatal(err)
	}
	if identity != (surface.Identity{Name: "terra", UserID: "U0BOT", TeamID: "T0TEAM", TeamName: "Acme"}) {
		t.Errorf("identity = %+v", identity)
	}

	for _, testCase := range []struct {
		name       string
		credential surface.Credential
		wantPart   string
	}{
		{"a bot token Slack does not know", surface.Credential{Token: "xoxb-synthetic-unknown", AppToken: appToken}, surface.PartToken},
		{"a person's token in the bot's place", surface.Credential{Token: userToken, AppToken: appToken}, surface.PartToken},
		{"the bot token in the app token's place", surface.Credential{Token: botToken, AppToken: botToken}, surface.PartAppToken},
		{"an app token Slack does not know", surface.Credential{Token: botToken, AppToken: "xapp-synthetic-unknown"}, surface.PartAppToken},
	} {
		_, err := adapter.ValidateCredential(ctx, testCase.credential)
		if got := surface.RejectedPart(err); got != testCase.wantPart {
			t.Errorf("%s: rejected part = %q (err %v), want %q", testCase.name, got, err, testCase.wantPart)
		}
	}
}

// Slack declining to answer is not Slack saying no: the operator's tokens may
// be fine, and the API must not tell them otherwise.
func TestValidateCredentialRateLimitedIsNoVerdict(t *testing.T) {
	_, err := standIn(t, true).ValidateCredential(context.Background(), surface.Credential{Token: botToken, AppToken: appToken})
	if err == nil {
		t.Fatal("a rate-limited validation succeeded")
	}
	if part := surface.RejectedPart(err); part != "" {
		t.Errorf("rate limiting read as a refusal of %q", part)
	}
}

// A Socket Mode URL carries a ticket that opens the app's connection, and a
// failed dial's error is logged and shown in the control room. The error
// says what failed, not where it was going.
func TestDialErrorLeavesOutTheURL(t *testing.T) {
	const ticket = "synthetic-ticket-7f3a"
	_, _, err := websocket.Dial(context.Background(), "ws://127.0.0.1:1/link?ticket="+ticket, nil)
	if err == nil {
		t.Fatal("dialling a closed port succeeded")
	}
	if !strings.Contains(err.Error(), ticket) {
		t.Fatalf("the library's own error no longer carries the URL, so this test proves nothing: %v", err)
	}
	if got := withoutURL(err).Error(); strings.Contains(got, ticket) || strings.Contains(got, "127.0.0.1:1/link") {
		t.Fatalf("withoutURL kept the URL: %s", got)
	}
}
