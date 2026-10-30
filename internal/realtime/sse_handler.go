package realtime

import (
	"fmt"
	"net/http"

	"libratrack/internal/auth"
)

// SSEHandler is slide 30: a long HTTP response in text/event-stream format. The slide's
// dashboardUpdates channel becomes a hub subscription to topic.
func SSEHandler(h *Hub, topic string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		user, _ := auth.UserFrom(r.Context())
		c := &Client{hub: h, send: make(chan []byte, h.cfg.SendBuffer), topics: map[string]bool{topic: true}, user: user, cfg: h.cfg}
		h.registerClient(c)
		for update := range c.send {
			fmt.Fprintf(w, "data: %s\n\n", update)
			flusher.Flush()
		}
	}
}
