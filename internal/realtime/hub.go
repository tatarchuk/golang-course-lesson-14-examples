// Package realtime is the WebSocket side of the examples: the channel hub of slides 20
// and 26, the client pumps of slides 14, 15 and 27, the handler of slide 13, the chat of
// slide 40 and the SSE handler of slide 30.
//
// This is the code as the slides show it, completed only so that it compiles and runs.
// The tests it fails, and why, are in docs/slides/tests-as-on-slides.txt.
package realtime

import (
	"log"
	"runtime"
	"time"
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

// Stats is what GET /debug/stats reports.
type Stats struct {
	Clients    int `json:"clients"`
	Goroutines int `json:"goroutines"`
}

// broadcastBuffer lets Publish drop the event into the queue and move on (slide 20).
const broadcastBuffer = 256

// Hub is slide 26's hub: one goroutine, Run, owns the client map.
type Hub struct {
	cfg        Config
	clients    map[*Client]bool
	register   chan *Client
	unregister chan *Client
	broadcast  chan Event
	stats      chan chan Stats // test and monitoring hook, not on the slides
	quit       chan struct{}   // test hook, not on the slides: they have no shutdown
}

// NewHub returns a hub; start it with go h.Run().
func NewHub(cfg Config) *Hub {
	return &Hub{
		cfg:        cfg,
		clients:    map[*Client]bool{},
		register:   make(chan *Client),
		unregister: make(chan *Client),
		broadcast:  make(chan Event, broadcastBuffer),
		stats:      make(chan chan Stats),
		quit:       make(chan struct{}),
	}
}

// Run is the hub's main loop (slide 26) with the topic filter of slide 20.
func (h *Hub) Run() {
	for {
		select {
		case c := <-h.register:
			h.clients[c] = true
			log.Printf("hub: register %s (%d clients)", c, len(h.clients))
		case c := <-h.unregister:
			delete(h.clients, c)
			close(c.send)
			log.Printf("hub: unregister %s (%d clients)", c, len(h.clients))
		case event := <-h.broadcast:
			for client := range h.clients {
				if client.subscribedTo(event.Topic) {
					client.send <- event.Payload
				}
			}
		case reply := <-h.stats:
			reply <- Stats{Clients: len(h.clients), Goroutines: runtime.NumGoroutine()}
		case <-h.quit:
			return
		}
	}
}

// Publish queues an event for every client subscribed to topic (slide 20).
func (h *Hub) Publish(topic string, payload []byte) {
	h.broadcast <- Event{Topic: topic, Payload: payload}
}

// Stats asks the hub for its client count.
func (h *Hub) Stats() Stats {
	reply := make(chan Stats)
	h.stats <- reply
	return <-reply
}

// Stop ends Run.
func (h *Hub) Stop() {
	close(h.quit)
}

func (h *Hub) registerClient(c *Client) bool {
	h.register <- c
	return true
}

func (h *Hub) unregisterClient(c *Client) {
	h.unregister <- c
}
