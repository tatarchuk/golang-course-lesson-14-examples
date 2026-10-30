// Package realtime is the WebSocket side of the examples: the channel hub of slides 20
// and 26, the client pumps of slides 14, 15 and 27, the handler of slide 13, the chat of
// slide 40 and the SSE handler of slide 30.
//
// This is the slides' code with the fixes of the slide 42 review (docs/ai/hub-review-raw.md,
// triage in the README): one writer per connection, a hub that never blocks on a slow
// client, an exit path for every goroutine and a send channel closed exactly once. The
// code as the slides show it is in the history (commit 0e85619).
package realtime

import (
	"errors"
	"log"
	"runtime"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// Event is one published message (slide 20).
type Event struct {
	Topic   string
	Payload []byte
}

// Config holds the connection settings of slides 13, 15, 16 and 28.
type Config struct {
	PongWait       time.Duration // read deadline, renewed by every pong (slide 15)
	PingPeriod     time.Duration // how often the server pings; shorter than PongWait
	WriteWait      time.Duration // deadline of one write (slide 16)
	MaxMessageSize int64         // largest message a client may send, in bytes (slide 16)
	SendBuffer     int           // capacity of each client's send channel (slide 28)
	AllowedOrigin  string        // the one Origin the upgrader accepts (slide 13)
}

// Validate checks the settings the hub and the pumps rely on.
func (c Config) Validate() error {
	switch {
	case c.SendBuffer <= 0:
		return errors.New("SendBuffer must be positive: with an unbuffered queue the hub drops every client")
	case c.PingPeriod <= 0 || c.PingPeriod >= c.PongWait:
		return errors.New("PingPeriod must be positive and shorter than PongWait")
	case c.WriteWait <= 0:
		return errors.New("WriteWait must be positive")
	case c.MaxMessageSize <= 0:
		return errors.New("MaxMessageSize must be positive")
	}
	return nil
}

// Stats is what GET /debug/stats reports.
type Stats struct {
	Clients    int `json:"clients"`
	Goroutines int `json:"goroutines"`
}

// broadcastBuffer lets Publish queue an event and move on while the hub is busy (slide 20).
const broadcastBuffer = 256

// Hub is slide 26's hub. Run is the only goroutine that reads or writes h.clients, and
// the only code that closes a client's send channel.
type Hub struct {
	cfg        Config
	clients    map[*Client]bool
	register   chan *Client
	unregister chan *Client
	broadcast  chan Event
	stats      chan chan Stats
	quit       chan struct{} // closed by Stop
	done       chan struct{} // closed when Run has returned
	stopOnce   sync.Once
}

// NewHub returns a hub; start it with go h.Run(). It panics on an invalid config.
func NewHub(cfg Config) *Hub {
	if err := cfg.Validate(); err != nil {
		panic("realtime: " + err.Error())
	}
	return &Hub{
		cfg:        cfg,
		clients:    map[*Client]bool{},
		register:   make(chan *Client),
		unregister: make(chan *Client),
		broadcast:  make(chan Event, broadcastBuffer),
		stats:      make(chan chan Stats),
		quit:       make(chan struct{}),
		done:       make(chan struct{}),
	}
}

// Run is the hub's main loop (slide 26) with the topic filter of slide 20. It never waits
// for a client: one whose queue is full is dropped (slides 28 and 44).
func (h *Hub) Run() {
	defer close(h.done)
	for {
		select {
		case c := <-h.register:
			h.clients[c] = true
			log.Printf("hub: register %s (%d clients)", c, len(h.clients))
		case c := <-h.unregister:
			h.drop(c, 0, "left")
		case event := <-h.broadcast:
			for c := range h.clients {
				if !c.subscribedTo(event.Topic) {
					continue
				}
				select {
				case c.send <- event.Payload:
				default:
					h.drop(c, websocket.ClosePolicyViolation, "slow consumer: send queue full")
				}
			}
		case reply := <-h.stats:
			reply <- Stats{Clients: len(h.clients), Goroutines: runtime.NumGoroutine()}
		case <-h.quit:
			for c := range h.clients {
				h.drop(c, websocket.CloseGoingAway, "server shutdown")
			}
			return
		}
	}
}

// drop removes c and closes its send channel; writePump then sends a close frame with
// code (0: none, the client has gone already). Only a registered client is dropped, so a
// late unregister of a client the hub has already dropped changes nothing: every send
// channel is closed exactly once (slide 27, finding M1 of the slide 42 review).
func (h *Hub) drop(c *Client, code int, reason string) {
	if !h.clients[c] {
		return
	}
	delete(h.clients, c)
	c.closeCode, c.closeText = code, reason
	close(c.send)
	log.Printf("hub: unregister %s, %s (%d clients)", c, reason, len(h.clients))
}

// Publish queues an event for every client subscribed to topic (slide 20). It waits only
// while the queue is full and returns at once when the hub has stopped. Every subscriber
// shares payload, so the caller must not change it afterwards.
func (h *Hub) Publish(topic string, payload []byte) {
	select {
	case h.broadcast <- Event{Topic: topic, Payload: payload}:
	case <-h.done:
	}
}

// Stats reports the number of clients and the process's goroutines.
func (h *Hub) Stats() Stats {
	reply := make(chan Stats, 1)
	select {
	case h.stats <- reply:
		return <-reply
	case <-h.done:
		return Stats{Goroutines: runtime.NumGoroutine()}
	}
}

// Stop sends every client a 1001 close (going away), ends Run and waits for it; a second
// call returns at once. Run must have been started.
func (h *Hub) Stop() {
	h.stopOnce.Do(func() { close(h.quit) })
	<-h.done
}

// registerClient hands c to the hub; false means the hub has stopped.
func (h *Hub) registerClient(c *Client) bool {
	select {
	case h.register <- c:
		return true
	case <-h.done:
		return false
	}
}

// unregisterClient tells the hub that c has gone; it returns at once when the hub has
// stopped.
func (h *Hub) unregisterClient(c *Client) {
	select {
	case h.unregister <- c:
	case <-h.done:
	}
}
