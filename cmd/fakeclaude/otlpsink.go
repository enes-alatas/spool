package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
)

// otlpSink is `claude otlp-sink <addr>`, a stand-in for Langfuse's OTLP
// endpoint and nothing of the real CLI: tier-2 tests run it in a container
// as the trace relay's upstream (ADR-0064, #1296). Each request it takes
// is printed to stdout as one JSON line, its path, Authorization, content
// type and body, and answered 200, so a test reads what reached
// "Langfuse" from docker logs.
func otlpSink(addr string) int {
	out := json.NewEncoder(os.Stdout)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(io.LimitReader(r.Body, 8<<20))
		_ = out.Encode(map[string]string{
			"method":        r.Method,
			"path":          r.URL.Path,
			"authorization": r.Header.Get("Authorization"),
			"content_type":  r.Header.Get("Content-Type"),
			"body":          string(body),
		})
		w.WriteHeader(http.StatusOK)
	})
	if err := http.ListenAndServe(addr, handler); err != nil { //nolint:gosec // a test stand-in, alive for one test
		fmt.Fprintln(os.Stderr, "otlp-sink:", err)
		return 1
	}
	return 0
}
