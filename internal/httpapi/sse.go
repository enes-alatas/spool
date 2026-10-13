package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/enes-alatas/spool/internal/bus"
)

// serveSSE streams bus items to one client until it leaves: those filter
// matches, which runs in Bus.Publish and so must read nothing but the item,
// and of those the ones admit lets through, which runs in this goroutine
// and may read the store. An error from admit ends the stream.
func serveSSE(w http.ResponseWriter, r *http.Request, events *bus.Bus, filter func(bus.Item) bool, admit func(bus.Item) (bool, error)) {
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
			send, err := admit(item)
			if err != nil {
				return
			}
			if !send {
				continue
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
