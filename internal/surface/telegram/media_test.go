package telegram

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/enes-alatas/spool/internal/store"
)

// calls is a stand-in Bot API that records each call's method, text or
// caption, and uploaded file.
type calls struct {
	mu   sync.Mutex
	seen []string
}

func (recorded *calls) serve(w http.ResponseWriter, r *http.Request) {
	method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
	entry := method
	switch method {
	case "sendPhoto", "sendDocument":
		field := map[string]string{"sendPhoto": "photo", "sendDocument": "document"}[method]
		file, header, err := r.FormFile(field)
		if err != nil {
			http.Error(w, `{"ok":false,"error_code":400,"description":"no file"}`, 400)
			return
		}
		body, _ := io.ReadAll(file)
		entry += " " + header.Filename + "=" + string(body) + " caption=" + r.FormValue("caption") +
			" reply=" + r.FormValue("reply_parameters")
	case "sendMessage":
		body, _ := io.ReadAll(r.Body)
		entry += " " + string(body)
	}
	recorded.mu.Lock()
	recorded.seen = append(recorded.seen, entry)
	recorded.mu.Unlock()
	_, _ = io.WriteString(w, `{"ok":true,"result":{"message_id":7}}`)
}

func sendAll(t *testing.T, req sendReq) []string {
	t.Helper()
	recorded := &calls{}
	api := httptest.NewServer(http.HandlerFunc(recorded.serve))
	defer api.Close()
	client := NewClientAt(api.URL, "synthetic")
	for _, send := range sendParts(client, req) {
		if _, err := send(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	return recorded.seen
}

// A file goes with its words as their caption when they fit in one, and
// after them, bare, when they do not (#123).
func TestAFileIsSentWithItsWords(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shot.png")
	if err := os.WriteFile(path, []byte("PNG"), 0o600); err != nil {
		t.Fatal(err)
	}
	photo := &Media{Path: path, Name: "shot.png", Photo: true}

	got := sendAll(t, sendReq{chatID: 1, text: "the button", replyTo: 42, media: photo})
	if len(got) != 1 || !strings.HasPrefix(got[0], "sendPhoto shot.png=PNG caption=the button reply=") ||
		!strings.Contains(got[0], `"message_id":42`) {
		t.Errorf("a short caption: %q", got)
	}

	long := strings.Repeat("word ", 300) // 1500 characters, over the caption limit
	document := &Media{Path: path, Name: "shot.png"}
	got = sendAll(t, sendReq{chatID: 1, text: long, replyTo: 42, media: document})
	if len(got) != 2 || !strings.HasPrefix(got[0], "sendMessage ") || !strings.Contains(got[0], `"message_id":42`) ||
		got[1] != "sendDocument shot.png=PNG caption= reply=" {
		t.Errorf("a long text: %q", got)
	}

	// a caption is counted as Telegram counts it, in UTF-16 units: not in
	// bytes, and not in runes either
	wide := strings.Repeat("ş", maxCaptionLen)
	if got = sendAll(t, sendReq{chatID: 1, text: wide, media: document}); len(got) != 1 {
		t.Errorf("%d characters of two bytes each went as %d calls, want one", maxCaptionLen, len(got))
	}
	astral := strings.Repeat("🙂", maxCaptionLen/2+1) // 514 runes, 1028 UTF-16 units
	if got = sendAll(t, sendReq{chatID: 1, text: astral, media: document}); len(got) != 2 || got[1] != "sendDocument shot.png=PNG caption= reply=" {
		t.Errorf("emoji over the caption limit in UTF-16 units: %d calls, want the words then the file", len(got))
	}
	if got = sendAll(t, sendReq{chatID: 1, text: strings.Repeat("🙂", maxCaptionLen/2), media: document}); len(got) != 1 {
		t.Errorf("emoji exactly at the caption limit went as %d calls, want one", len(got))
	}
}

// A poll's file goes after the poll, bare: Telegram's poll carries none,
// and its words are the question, not a caption.
func TestAPollsFileIsSentAfterIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plan.txt")
	if err := os.WriteFile(path, []byte("PLAN"), 0o600); err != nil {
		t.Fatal(err)
	}
	got := sendAll(t, sendReq{chatID: 1, text: "ship it?", replyTo: 42,
		poll: &store.Poll{MessageID: 7, Options: []string{"yes", "no"}}, media: &Media{Path: path, Name: "plan.txt"}})
	if len(got) != 2 || got[0] != "sendPoll" || got[1] != "sendDocument plan.txt=PLAN caption= reply=" {
		t.Errorf("a poll with a file: %q", got)
	}
}

func TestOnlyWhatTelegramTakesAsAPhotoIsSentAsOne(t *testing.T) {
	for _, c := range []struct {
		row  store.Attachment
		want bool
	}{
		{store.Attachment{MIME: "image/png", Size: 1 << 20, Width: 1280, Height: 720}, true},
		{store.Attachment{MIME: "image/jpeg", Size: 10 << 20, Width: 4000, Height: 3000}, true},
		{store.Attachment{MIME: "image/gif", Size: 1 << 10, Width: 10, Height: 10}, false},
		{store.Attachment{MIME: "image/webp", Size: 1 << 10}, false},
		{store.Attachment{MIME: "image/png", Size: 10<<20 + 1, Width: 100, Height: 100}, false},
		{store.Attachment{MIME: "image/png", Size: 1 << 10, Width: 6000, Height: 4001}, false},
		{store.Attachment{MIME: "image/png", Size: 1 << 10, Width: 2100, Height: 100}, false},
		{store.Attachment{MIME: "image/png", Size: 1 << 10}, false},
		{store.Attachment{MIME: "text/plain", Size: 10}, false},
	} {
		if got := asPhoto(&c.row); got != c.want {
			t.Errorf("%+v: photo %v, want %v", c.row, got, c.want)
		}
	}
}
