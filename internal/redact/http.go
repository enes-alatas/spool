package redact

import (
	"net/http"
	"strings"
)

// HTTP wraps next so no JSON response or SSE frame leaves the API carrying a
// known secret value.
//
// This is the belt to the store decorator's braces, and it exists for the
// rows written before the decorator did: those are on disk with the value in
// them, and the transcript endpoints serve them. It also covers whatever a
// future handler computes on the way out without going near the store.
//
// It rewrites only JSON and event-stream bodies. Everything else — the
// embedded control room's assets, above all — goes through untouched: those
// are served with a Content-Length that a substitution could falsify, and a
// secret value is not something a checked-in asset contains.
//
// One limit worth knowing: redaction is per Write. A body split across
// writes with a secret straddling the boundary would slip through. Both
// writers here (the JSON encoder, the SSE frame printer) emit a whole
// document per call, and the store decorator means a stored secret should
// not reach this layer at all.
func HTTP(next http.Handler, redactor *Redactor) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		next.ServeHTTP(&responseWriter{ResponseWriter: w, redactor: redactor}, req)
	})
}

type responseWriter struct {
	http.ResponseWriter
	redactor *Redactor
}

func (writer *responseWriter) Write(data []byte) (int, error) {
	if !redactable(writer.Header().Get("Content-Type")) {
		return writer.ResponseWriter.Write(data)
	}
	clean := writer.redactor.Text(string(data))
	if clean == string(data) {
		return writer.ResponseWriter.Write(data)
	}
	if _, err := writer.ResponseWriter.Write([]byte(clean)); err != nil {
		return 0, err
	}
	// Report the caller's own length: a short count means "wrote less than
	// you asked" to every io.Writer user, and the redacted body is
	// deliberately a different size.
	return len(data), nil
}

// Flush keeps SSE streaming: serveSSE type-asserts its writer to
// http.Flusher, and a wrapper that does not implement it turns the live
// timeline into a hang.
func (writer *responseWriter) Flush() {
	if flusher, ok := writer.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func redactable(contentType string) bool {
	mediaType, _, _ := strings.Cut(contentType, ";")
	switch strings.TrimSpace(mediaType) {
	case "application/json", "text/event-stream":
		return true
	}
	return false
}
