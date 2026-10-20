//go:build integration

package itest

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// apiMessage is one model request the stand-in took: the Claude proxy's,
// on a contained loop's behalf (ADR-0058).
type apiMessage struct {
	Query   string
	Headers http.Header
	Body    map[string]any
}

// anthropicFake is a stand-in for Anthropic's Messages API, the Claude
// proxy's upstream. It admits the one token it is made with, which tests
// keep synthetic. Lock Mu to read Messages.
type anthropicFake struct {
	*httptest.Server
	Mu       sync.Mutex
	Messages []apiMessage
	token    string
}

// newAnthropicFake starts a stand-in that admits token alone, closed when
// the test ends.
func newAnthropicFake(t *testing.T, token string) *anthropicFake {
	t.Helper()
	fake := &anthropicFake{token: token}
	fake.Server = httptest.NewServer(http.HandlerFunc(fake.serve))
	t.Cleanup(fake.Close)
	return fake
}

func (fake *anthropicFake) serve(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Header.Get("Authorization") != "Bearer "+fake.token {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"type":"error","error":{"type":"authentication_error","message":"invalid token"}}`)
		return
	}
	if r.Method != http.MethodPost || r.URL.Path != "/v1/messages" {
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"type":"error","error":{"type":"not_found_error","message":"no such route"}}`)
		return
	}
	var body map[string]any
	raw, _ := io.ReadAll(r.Body)
	_ = json.Unmarshal(raw, &body)
	fake.Mu.Lock()
	fake.Messages = append(fake.Messages, apiMessage{Query: r.URL.RawQuery, Headers: r.Header.Clone(), Body: body})
	fake.Mu.Unlock()
	_, _ = io.WriteString(w, `{"type":"message","role":"assistant","content":[{"type":"text","text":"fixture model answer"}]}`)
}
