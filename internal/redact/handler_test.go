package redact

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
)

func logger(t *testing.T, buf *bytes.Buffer, secrets ...Secret) *slog.Logger {
	t.Helper()
	r, _ := loaded(t, secrets...)
	return slog.New(Handler(slog.NewTextHandler(buf, nil), r))
}

func TestHandlerRedactsMessageAndAttrs(t *testing.T) {
	var buf bytes.Buffer
	log := logger(t, &buf, Secret{Name: "BOT", Value: "1234:AAsecrettoken"})

	log.Info("polling with 1234:AAsecrettoken", "url", "https://api.telegram.org/bot1234:AAsecrettoken/getMe")

	out := buf.String()
	if strings.Contains(out, "1234:AAsecrettoken") {
		t.Fatalf("log line carried the secret: %s", out)
	}
	if n := strings.Count(out, "<redacted:BOT>"); n != 2 {
		t.Errorf("got %d placeholders in %s, want one in the message and one in the attr", n, out)
	}
}

// The leak in #146 was inside an error, not a string attr: nobody wrote the
// token into the log, the transport did.
func TestHandlerRedactsInsideAnError(t *testing.T) {
	var buf bytes.Buffer
	log := logger(t, &buf, Secret{Name: "BOT", Value: "1234:AAsecrettoken"})

	log.Error("send failed", "err", errors.New(`Post "https://api.telegram.org/bot1234:AAsecrettoken/send": timeout`))

	if out := buf.String(); strings.Contains(out, "1234:AAsecrettoken") {
		t.Fatalf("error value carried the secret: %s", out)
	} else if !strings.Contains(out, "<redacted:BOT>") {
		t.Errorf("no placeholder in %s", out)
	}
}

// Values with nothing to hide keep their own formatting: a handler that
// stringified everything would make every log line worse to read, and a
// redactor people turn off protects nothing.
func TestHandlerLeavesCleanValuesAsTheyWere(t *testing.T) {
	var buf bytes.Buffer
	var plain bytes.Buffer
	log := logger(t, &buf, Secret{Name: "BOT", Value: "1234:AAsecrettoken"})
	slog.New(slog.NewTextHandler(&plain, nil)).Info("tick", "loop", "terra", "n", 3, "ok", true)

	log.Info("tick", "loop", "terra", "n", 3, "ok", true)

	got, want := afterTime(buf.String()), afterTime(plain.String())
	if got != want {
		t.Errorf("redacting handler wrote\n%s\nplain handler wrote\n%s", got, want)
	}
}

func TestHandlerRedactsAttrsFromWithAndGroups(t *testing.T) {
	var buf bytes.Buffer
	log := logger(t, &buf, Secret{Name: "GH", Value: "ghp_secretvalue"})

	log.With("token", "ghp_secretvalue").
		Info("cloning", slog.Group("repo", "auth", "ghp_secretvalue"))

	out := buf.String()
	if strings.Contains(out, "ghp_secretvalue") {
		t.Fatalf("log line carried the secret: %s", out)
	}
	if n := strings.Count(out, "<redacted:GH>"); n != 2 {
		t.Errorf("got %d placeholders in %s, want one from With and one from the group", n, out)
	}
}

func TestHandlerRespectsTheWrappedLevel(t *testing.T) {
	var buf bytes.Buffer
	r, _ := loaded(t)
	h := Handler(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}), r)

	if h.Enabled(context.Background(), slog.LevelInfo) {
		t.Error("Enabled(Info) = true through a Warn-level handler")
	}
}

// afterTime drops slog's leading time= field, which differs per line.
func afterTime(line string) string {
	_, rest, _ := strings.Cut(line, " ")
	return rest
}
