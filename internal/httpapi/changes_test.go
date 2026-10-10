package httpapi

import (
	"strings"
	"testing"
)

// TestEveryLoopChangeIsRecorded: each route that changes a loop or acts on
// it is in loopChangeRoutes, and the table names no route that is gone, so
// a route added later on a loop means deciding what it records (ADR-0048).
// Reading routes change nothing. A message is the loop's conversation,
// which already names its author, and a create records itself.
func TestEveryLoopChangeIsRecorded(t *testing.T) {
	notChanges := map[string]bool{"POST /api/loops": true, "POST /api/loops/{name}/message": true}
	registered := map[string]bool{}
	for _, pattern := range (&Server{}).routes().patterns {
		registered[pattern] = true
		onALoop := strings.Contains(pattern, "/api/loops/{name}") || strings.Contains(pattern, "/loops/{loop}")
		if strings.HasPrefix(pattern, "GET ") || !onALoop || notChanges[pattern] {
			continue
		}
		if _, ok := loopChangeRoutes[pattern]; !ok {
			t.Errorf("route %q changes a loop but is not in loopChangeRoutes", pattern)
		}
	}
	for pattern := range loopChangeRoutes {
		if !registered[pattern] {
			t.Errorf("loopChangeRoutes lists %q, which no route registers", pattern)
		}
	}
}
