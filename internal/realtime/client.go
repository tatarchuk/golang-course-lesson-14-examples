package realtime

import (
	"fmt"
	"log"
	"sort"
	"time"

	"github.com/gorilla/websocket"
)

// Client is one connection as the hub sees it: a send queue and the topics it follows.
type Client struct {
	hub    *Hub
	conn   *websocket.Conn
	send   chan []byte
	topics map[string]bool
	user   string
	cfg    Config
}

func (c *Client) subscribedTo(topic string) bool { return c.topics[topic] }

func (c *Client) String() string {
	topics := make([]string, 0, len(c.topics))
	for t := range c.topics {
		topics = append(topics, t)
	}
	sort.Strings(topics)
	return fmt.Sprintf("user=%s topics=%v", c.user, topics)
}

// keepAlive is slide 15.
func (c *Client) keepAlive() {
	conn := c.conn
	pongWait := c.cfg.PongWait
	conn.SetReadDeadline(time.Now().Add(pongWait))
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})
	ticker := time.NewTicker(c.cfg.PingPeriod)
	go func() {
		for range ticker.C {
			conn.WriteMessage(websocket.PingMessage, nil)
		}
	}()
}

// readPump is slide 27 with the read loop of slide 14.
func (c *Client) readPump(onMessage func(*Client, []byte)) {
	defer func() {
		c.hub.unregisterClient(c)
		c.conn.Close()
	}()
	for {
		msgType, data, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway) {
				log.Printf("unexpected close: %v", err)
			}
			break
		}
		if msgType == websocket.TextMessage && onMessage != nil {
			onMessage(c, data) // handleIncoming(msgType, data)
		}
	}
}

// writePump is slide 27.
func (c *Client) writePump() {
	defer c.conn.Close()
	for msg := range c.send {
		c.conn.WriteMessage(websocket.TextMessage, msg)
	}
	// the hub closed the channel -> the loop ends -> the goroutine ends
}
