package redact

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func served(t *testing.T, h http.HandlerFunc) *httptest.ResponseRecorder {
	t.Helper()
	r, _ := loaded(t, Secret{Name: "GH_TOKEN", Value: secretValue})
	rec := httptest.NewRecorder()
	HTTP(h, r).ServeHTTP(rec, httptest.NewRequest("GET", "/api/anything", nil))
	return rec
}

// The rows written before the store decorator existed still hold the value;
// this is what stops them being served.
func TestJSONResponsesAreRedacted(t *testing.T) {
	rec := served(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"result_text":%q}`, "leaked "+secretValue)
	})

	if body := rec.Body.String(); strings.Contains(body, secretValue) {
		t.Fatalf("response carried the secret: %s", body)
	} else if !strings.Contains(body, "<redacted:GH_TOKEN>") {
		t.Errorf("no placeholder in %s", body)
	}
}

func TestEventStreamFramesAreRedacted(t *testing.T) {
	rec := served(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "event: agent_event\ndata: {\"payload\":%q}\n\n", secretValue)
	})

	if body := rec.Body.String(); strings.Contains(body, secretValue) {
		t.Fatalf("SSE frame carried the secret: %s", body)
	}
}

// The control room's assets go out with a Content-Length; rewriting one
// would truncate the response against a length already sent.
func TestOtherContentTypesPassThroughUntouched(t *testing.T) {
	body := "// bundled asset mentioning " + secretValue
	rec := served(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/javascript")
		fmt.Fprint(w, body)
	})

	if rec.Body.String() != body {
		t.Errorf("non-JSON body rewritten:\n%s", rec.Body.String())
	}
}

// serveSSE type-asserts its writer to http.Flusher; a wrapper without it
// turns the live timeline into a hang.
func TestWrapperStaysFlushable(t *testing.T) {
	var flushed bool
	served(t, func(w http.ResponseWriter, _ *http.Request) {
		f, ok := w.(http.Flusher)
		if !ok {
			t.Error("wrapped writer is not an http.Flusher")
			return
		}
		f.Flush()
		flushed = true
	})
	if !flushed {
		t.Error("handler never got to flush")
	}
}

// A short write means "wrote less than you asked" to every io.Writer caller,
// and a redacted body is deliberately a different length.
func TestWriteReportsTheCallersLength(t *testing.T) {
	in := []byte(`{"t":"` + secretValue + `"}`)
	served(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		n, err := w.Write(in)
		if err != nil {
			t.Errorf("Write: %v", err)
		}
		if n != len(in) {
			t.Errorf("Write returned %d, want %d", n, len(in))
		}
	})
}
