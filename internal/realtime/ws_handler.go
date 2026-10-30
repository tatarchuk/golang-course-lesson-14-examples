package realtime

import (
	"log"
	"net/http"
	"time"

	"github.com/gorilla/websocket"

	"libratrack/internal/auth"
)

// ServeWS is slide 13's handler: the upgrader with its CheckOrigin, then the hub
// registration of slide 39 and the pumps of slide 27. topics names what the connection
// follows; onMessage handles what the client sends (nil: ignore it).
func ServeWS(h *Hub, cfg Config, topics func(*http.Request) ([]string, error), onMessage func(c *Client, data []byte)) http.HandlerFunc {
	upgrader := websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
		CheckOrigin: func(r *http.Request) bool {
			return r.Header.Get("Origin") == cfg.AllowedOrigin
		},
	}
	return func(w http.ResponseWriter, r *http.Request) {
		subscribed, err := topics(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		user, _ := auth.UserFrom(r.Context())
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Printf("upgrade failed: %v", err) // Upgrade has already answered with an HTTP error
			return
		}
		c := &Client{hub: h, conn: conn, send: make(chan []byte, cfg.SendBuffer), topics: map[string]bool{}, user: user, cfg: cfg,
			closed: make(chan struct{})}
		for _, t := range subscribed {
			c.topics[t] = true
		}
		if !h.registerClient(c) { // the hub has stopped: the server is shutting down
			_ = conn.WriteControl(websocket.CloseMessage,
				websocket.FormatCloseMessage(websocket.CloseGoingAway, "server shutdown"), time.Now().Add(cfg.WriteWait))
			conn.Close()
			return
		}
		go c.writePump()
		c.readPump(onMessage)
	}
}
