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
func HTTP(next http.Handler, r *Redactor) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		next.ServeHTTP(&responseWriter{ResponseWriter: w, r: r}, req)
	})
}

type responseWriter struct {
	http.ResponseWriter
	r *Redactor
}

func (w *responseWriter) Write(b []byte) (int, error) {
	if !redactable(w.Header().Get("Content-Type")) {
		return w.ResponseWriter.Write(b)
	}
	clean := w.r.Text(string(b))
	if clean == string(b) {
		return w.ResponseWriter.Write(b)
	}
	if _, err := w.ResponseWriter.Write([]byte(clean)); err != nil {
		return 0, err
	}
	// Report the caller's own length: a short count means "wrote less than
	// you asked" to every io.Writer user, and the redacted body is
	// deliberately a different size.
	return len(b), nil
}

// Flush keeps SSE streaming: serveSSE type-asserts its writer to
// http.Flusher, and a wrapper that does not implement it turns the live
// timeline into a hang.
func (w *responseWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
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
