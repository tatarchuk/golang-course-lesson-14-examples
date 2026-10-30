package realtime

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"regexp"
	"strings"
)

// ChatMessage is slide 40's message.
type ChatMessage struct {
	Room   string `json:"room"`
	Author string `json:"author"`
	Text   string `json:"text"`
}

// ChatOnMessage is slide 40: what readPump does with a chat message. The author comes from
// the JWT claims and the room from the connection, whatever the message body says (on the
// slide the room came from the body, so a client could post into any room).
func ChatOnMessage(h *Hub) func(c *Client, data []byte) {
	return func(c *Client, data []byte) {
		var msg ChatMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			log.Printf("chat: %s sent a message that is not JSON: %v", c, err)
			return
		}
		if strings.TrimSpace(msg.Text) == "" {
			return
		}
		msg.Author = c.user // from the JWT claims
		msg.Room = c.room() // the connection's room
		payload, _ := json.Marshal(msg)
		h.Publish("chat:"+msg.Room, payload)
	}
}

var roomName = regexp.MustCompile(`^[a-z0-9-]{1,32}$`)

// ChatTopics maps GET /ws/chat?room=<room> to the room's topic; the room defaults to
// general.
func ChatTopics(r *http.Request) ([]string, error) {
	room := r.URL.Query().Get("room")
	if room == "" {
		room = "general"
	}
	if !roomName.MatchString(room) {
		return nil, errors.New("room must be 1 to 32 lowercase letters, digits or dashes")
	}
	return []string{"chat:" + room}, nil
}
