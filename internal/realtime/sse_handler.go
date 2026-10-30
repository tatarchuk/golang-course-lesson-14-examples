package realtime

import (
	"fmt"
	"net/http"
	"time"

	"libratrack/internal/auth"
)

// SSEHandler is slide 30: a long HTTP response in text/event-stream format, fed by a hub
// subscription to topic (the slide's dashboardUpdates channel). It ends when the browser
// leaves, when a write fails or when the hub closes the subscription (slow consumer,
// shutdown), and every write has a deadline (finding H2 of the slide 42 review).
func SSEHandler(h *Hub, topic string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rc := http.NewResponseController(w)
		user, _ := auth.UserFrom(r.Context())
		c := &Client{hub: h, send: make(chan []byte, h.cfg.SendBuffer), topics: map[string]bool{topic: true}, user: user, cfg: h.cfg}
		if !h.registerClient(c) {
			http.Error(w, "the server is shutting down", http.StatusServiceUnavailable)
			return
		}
		defer h.unregisterClient(c)

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		_ = rc.SetWriteDeadline(time.Now().Add(h.cfg.WriteWait))
		w.WriteHeader(http.StatusOK)
		if err := rc.Flush(); err != nil { // the headers go out now, so EventSource reports "open"
			return
		}
		for {
			select {
			case update, ok := <-c.send:
				if !ok {
					return
				}
				_ = rc.SetWriteDeadline(time.Now().Add(h.cfg.WriteWait))
				if _, err := fmt.Fprintf(w, "data: %s\n\n", update); err != nil {
					return
				}
				if err := rc.Flush(); err != nil {
					return
				}
			case <-r.Context().Done():
				return
			}
		}
	}
}
