package realtime

import (
	"errors"
	"fmt"
	"log"
	"net"
	"sort"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

// Client is one connection as the hub sees it: a send queue and the topics it follows.
// topics and user are set before the client is registered and never change afterwards,
// so the hub and the pumps read them without a lock; only the hub sends on send and
// closes it (finding M3 of the slide 42 review).
type Client struct {
	hub       *Hub
	conn      *websocket.Conn // nil for an SSE subscriber
	send      chan []byte
	topics    map[string]bool
	user      string
	cfg       Config
	closeCode int // set by the hub before it closes send: the close frame writePump sends
	closeText string
	closed    chan struct{} // closed by writePump when it returns: its close frame is sent (nil without a writer: SSE)
}

func (c *Client) subscribedTo(topic string) bool { return c.topics[topic] }

// room is the chat room the connection joined (its "chat:<room>" topic), or "".
func (c *Client) room() string {
	for t := range c.topics {
		if r, ok := strings.CutPrefix(t, "chat:"); ok {
			return r
		}
	}
	return ""
}

func (c *Client) String() string {
	topics := make([]string, 0, len(c.topics))
	for t := range c.topics {
		topics = append(topics, t)
	}
	sort.Strings(topics)
	return fmt.Sprintf("user=%s topics=%v", c.user, topics)
}

// readPump is the connection's only reader (slides 14 and 27). The read deadline is the
// keepalive of slide 15: every pong renews it, so a peer that stops answering pings fails
// the next read. Any read error ends the loop; the deferred unregister lets the hub close
// send, which ends writePump.
func (c *Client) readPump(onMessage func(*Client, []byte)) {
	defer func() {
		c.hub.unregisterClient(c)
		c.conn.Close()
	}()
	c.conn.SetReadLimit(c.cfg.MaxMessageSize) // slide 16: a bigger message closes with 1009
	_ = c.conn.SetReadDeadline(time.Now().Add(c.cfg.PongWait))
	c.conn.SetPongHandler(func(string) error {
		return c.conn.SetReadDeadline(time.Now().Add(c.cfg.PongWait))
	})
	for {
		msgType, data, err := c.conn.ReadMessage()
		if err != nil {
			log.Printf("client: %s disconnected: %s", c, disconnectReason(err))
			return
		}
		if msgType == websocket.TextMessage && onMessage != nil {
			onMessage(c, data)
		}
	}
}

// writePump is the connection's only writer (slides 14 and 44): the hub's messages and
// the pings of slide 15 share this goroutine, every write has a deadline and the first
// failed write ends it. When the hub closes send, writePump sends the close frame the hub
// chose and returns (slide 27).
func (c *Client) writePump() {
	ticker := time.NewTicker(c.cfg.PingPeriod)
	defer func() {
		ticker.Stop()
		c.conn.Close()
		close(c.closed)
	}()
	for {
		select {
		case msg, ok := <-c.send:
			_ = c.conn.SetWriteDeadline(time.Now().Add(c.cfg.WriteWait))
			if !ok {
				if c.closeCode != 0 {
					_ = c.conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(c.closeCode, c.closeText))
				}
				return
			}
			if err := c.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}
		case <-ticker.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(c.cfg.WriteWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// disconnectReason turns a read error into the log line of slide 16.
func disconnectReason(err error) string {
	var ce *websocket.CloseError
	var ne net.Error
	switch {
	case errors.As(err, &ce):
		return strings.TrimSpace(fmt.Sprintf("closed by the client (%d) %s", ce.Code, ce.Text))
	case errors.Is(err, websocket.ErrReadLimit):
		return "message over the read limit, closed with 1009"
	case errors.As(err, &ne) && ne.Timeout():
		return "no pong before the read deadline"
	default:
		return "connection lost: " + err.Error()
	}
}
