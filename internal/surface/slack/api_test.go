package slack

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A download goes only to Slack's file host, since its URL is the event's.
// An app without files:read is answered with Slack's sign-in page, status
// 200, rather than an error. Kept, it would be a file of HTML the loop is
// told someone sent; so it is a failed download, and says what to fix.
func TestADownloadAnsweredWithASignInPageFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+botToken {
			t.Errorf("downloaded with %q", r.Header.Get("Authorization"))
		}
		if r.URL.Path == "/scoped" {
			w.Header().Set("Content-Type", "image/png")
			_, _ = io.WriteString(w, "png bytes")
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, "<html>sign in</html>")
	}))
	defer srv.Close()
	client := NewClientAt(srv.URL)

	if _, err := client.Download(context.Background(), botToken, srv.URL+"/unscoped"); err == nil ||
		!strings.Contains(err.Error(), "files:read") {
		t.Errorf("a sign-in page downloaded as %v, want an error naming files:read", err)
	}
	// a URL the event names on any other host never sees the token
	foreign := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("a download reached a foreign host with %q", r.Header.Get("Authorization"))
	}))
	defer foreign.Close()
	for _, fileURL := range []string{foreign.URL + "/scoped", "http://" + fileHost + "/files-pri/T0/F0/x.png", "https://files.slack.com.example/x"} {
		if _, err := client.Download(context.Background(), botToken, fileURL); err == nil {
			t.Errorf("downloaded from %s", fileURL)
		}
	}

	body, err := client.Download(context.Background(), botToken, srv.URL+"/scoped")
	if err != nil {
		t.Fatal(err)
	}
	defer body.Close()
	if got, _ := io.ReadAll(body); string(got) != "png bytes" {
		t.Errorf("downloaded %q", got)
	}
}
