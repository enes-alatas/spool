package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/enes-alatas/spool/internal/bus"
)

// serveSSE streams bus items matching filter to one client until it leaves.
func serveSSE(w http.ResponseWriter, r *http.Request, events *bus.Bus, filter func(bus.Item) bool) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	items, cancel := events.Subscribe(filter)
	defer cancel()

	ping := time.NewTicker(25 * time.Second)
	defer ping.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ping.C:
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		case item, ok := <-items:
			if !ok {
				return
			}
			data, err := json.Marshal(item)
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", item.Kind, data)
			flusher.Flush()
		}
	}
}
