// Package mutexhub is fix 1 of 2 from slide 24: the connection map of slide 23 guarded
// by a sync.RWMutex. Every access to h.clients, read or write, goes through the mutex;
// broadcasts share the read lock, Register and Unregister take the write lock. The risk
// is discipline: one forgotten lock anywhere brings the slide 23 race back, which is why
// the deck prefers the channel hub (internal/realtime).
//
// Like the slide, Broadcast sends while it holds the read lock, so a client whose buffer
// is full stalls the broadcast and, behind it, every Register and Unregister; the
// channel hub drops such a client instead (slide 28).
package mutexhub

import "sync"

// Client is the part of a connection the hub needs: its outgoing queue. A real client
// would drain send from its own write goroutine.
type Client struct {
	send chan []byte
}

// NewClient returns a client with a send buffer of the given size.
func NewClient(buffer int) *Client {
	return &Client{send: make(chan []byte, buffer)}
}

// Hub is the slide 24 connection set.
type Hub struct {
	mu      sync.RWMutex
	clients map[*Client]bool
}

// NewHub returns an empty hub.
func NewHub() *Hub {
	return &Hub{clients: map[*Client]bool{}}
}

// Register adds a client (slide 24).
func (h *Hub) Register(c *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.clients[c] = true
}

// Unregister removes a client and closes its send channel. The membership check keeps
// the close to exactly one call even when Unregister runs twice for the same client.
func (h *Hub) Unregister(c *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.clients[c] {
		delete(h.clients, c)
		close(c.send)
	}
}

// Broadcast queues msg for every registered client (slide 24).
func (h *Hub) Broadcast(msg []byte) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for c := range h.clients {
		c.send <- msg
	}
}

// Len reports the number of registered clients.
func (h *Hub) Len() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}
