//go:build integration

package itest

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"
)

// Attachments cross the chat surface inbound (#123): the hub keeps a file
// once and shows the loop a path it can read, under the header and above
// the words.

func pngOf(t *testing.T, width, height int) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, width, height))); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// envelopeWith waits for a message turn of loopName whose input contains
// text, and returns that input.
func envelopeWith(t *testing.T, srv *server, loopName, text string) string {
	t.Helper()
	var found string
	srv.waitTurn(loopName, 20*time.Second, func(tn turn) bool {
		for _, envelope := range srv.turnInputs(loopName)[tn.ID] {
			if strings.Contains(envelope, text) {
				found = envelope
				return true
			}
		}
		return false
	})
	return found
}

var attachmentLine = regexp.MustCompile(`\[(image|file)(?: not kept)?: ([^\]]*)\]`)

// attachmentPath is the path an envelope's one attachment line names.
func attachmentPath(t *testing.T, envelope string) string {
	t.Helper()
	match := attachmentLine.FindStringSubmatch(envelope)
	if match == nil {
		t.Fatalf("no attachment line in the envelope:\n%s", envelope)
	}
	parts := strings.Split(match[2], " · ")
	return parts[len(parts)-1]
}

func TestAPhotoInADMReachesTheLoopAsAFileItCanRead(t *testing.T) {
	t.Parallel()
	operator := user{ID: 5151, First: "Operator", Username: "operator"}
	srv, tg := startTelegramFleet(t, operator)
	shot := pngOf(t, 64, 32)

	tg.withMedia([]string{"alpha"}, operator.ID, "private", "the button is cut off", operator,
		media{photo: true, bytes: shot, width: 64, height: 32})

	envelope := envelopeWith(t, srv, "alpha", "the button is cut off")
	// the header, then the attachment, then the caption as the words
	lines := strings.Split(envelope, "\n")
	if len(lines) < 4 || !strings.HasPrefix(lines[1], "[image: photo-") || lines[3] != "the button is cut off" {
		t.Fatalf("envelope is not header, attachment, blank, caption:\n%s", envelope)
	}
	if !strings.Contains(lines[1], " · 64×32 · ") {
		t.Errorf("the image line does not give its dimensions: %s", lines[1])
	}
	path := attachmentPath(t, envelope)
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the path the loop was shown is not readable: %v", err)
	}
	if !bytes.Equal(got, shot) {
		t.Error("the file at the path is not the photo that was sent")
	}
}

// A document posted to the group reaches every loop it names, and is
// downloaded once however many bots saw it: only the ingesting bot fetches.
func TestAGroupDocumentIsKeptOnceForEveryLoopItNames(t *testing.T) {
	t.Parallel()
	operator := user{ID: 5252, First: "Operator", Username: "operator"}
	srv, tg := startTelegramFleet(t, operator)
	log := []byte("line one\nline two\n")

	tg.withMedia([]string{"alpha", "beta"}, groupChatID, "supergroup", "@alpha @beta the failing run", operator,
		media{name: "run.log", bytes: log})

	alphaSees := envelopeWith(t, srv, "alpha", "the failing run")
	betaSees := envelopeWith(t, srv, "beta", "the failing run")
	for name, envelope := range map[string]string{"alpha": alphaSees, "beta": betaSees} {
		if !strings.Contains(envelope, "[file: run.log · 18 B · ") {
			t.Errorf("%s was not shown the document:\n%s", name, envelope)
			continue
		}
		if got, err := os.ReadFile(attachmentPath(t, envelope)); err != nil || !bytes.Equal(got, log) {
			t.Errorf("%s cannot read the document it was shown: %v", name, err)
		}
	}
	if got := tg.downloadCount(); got != 1 {
		t.Errorf("the document was downloaded %d times, want once", got)
	}
}

// A file over the limit is named, not fetched: the loop is told what was
// sent and why it has nothing to open, and the hub never downloads it.
func TestAFileOverTheLimitIsNamedButNotKept(t *testing.T) {
	t.Parallel()
	operator := user{ID: 5353, First: "Operator", Username: "operator"}
	srv, tg := startTelegramFleet(t, operator)

	tg.withMedia([]string{"alpha"}, operator.ID, "private", "the full dump", operator,
		media{name: "core.dump", bytes: []byte("small here"), size: 30 << 20})

	envelope := envelopeWith(t, srv, "alpha", "the full dump")
	if !strings.Contains(envelope, "[file not kept: core.dump · 30.0 MB · over the 20 MB limit]") {
		t.Errorf("the envelope does not say the file was not kept:\n%s", envelope)
	}
	if got := tg.downloadCount(); got != 0 {
		t.Errorf("an over-limit file was downloaded %d times", got)
	}
}

// A secret in a file's name is caught as it was sent (#123): SafeName
// rewrites the name before the store sees it, so the row's redaction alone
// would miss it. Neither the envelope nor the kept file's name carries it,
// raw or sanitized.
func TestASecretInAFileNameIsRedactedBeforeItIsKept(t *testing.T) {
	t.Parallel()
	operator := user{ID: 5454, First: "Operator", Username: "operator"}
	srv, tg := startTelegramFleet(t, operator)
	const value = "itest:SYNTHETICnameSecret42" // ":" is what SafeName rewrites
	srv.setLoopEnv("alpha", "NAME_TOKEN", value)

	tg.withMedia([]string{"alpha"}, operator.ID, "private", "the leaky name", operator,
		media{name: "dump-" + value + ".txt", bytes: []byte("contents")})

	envelope := envelopeWith(t, srv, "alpha", "the leaky name")
	path := attachmentPath(t, envelope)
	sanitized := strings.ReplaceAll(value, ":", "_")
	for _, leak := range []string{value, sanitized, "SYNTHETICnameSecret42"} {
		if strings.Contains(envelope, leak) {
			t.Errorf("the envelope carries the secret as %q:\n%s", leak, envelope)
		}
	}
	if !strings.Contains(path, "redacted_NAME_TOKEN") {
		t.Errorf("the kept file's name does not stand in for the secret: %s", path)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != "contents" {
		t.Errorf("the redacted name no longer leads to the file: %v", err)
	}
}
