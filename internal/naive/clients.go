// Package naive is the anti-pattern of slide 23, kept only to show that the race
// detector catches it: a package-level map of connections that every handler goroutine
// writes and the broadcaster reads without any synchronisation. Never copy it; the fixes
// are internal/mutexhub (slide 24) and the channel hub in internal/realtime (slide 26).
//
// The code is the slide's, completed only so that it compiles (the upgrader and the read
// loop the slide elides). The race demo is race_test.go, built with the racedemo tag.
package naive

import (
	"net/http"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{ReadBufferSize: 1024, WriteBufferSize: 1024}

// UNSAFE - no synchronisation (slide 23)
var clients = map[*websocket.Conn]bool{}

func handleWS(w http.ResponseWriter, r *http.Request) {
	conn, _ := upgrader.Upgrade(w, r, nil)
	clients[conn] = true        // race!
	defer delete(clients, conn) // race!
	// ... read loop
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
	}
}

func broadcast(msg []byte) {
	for conn := range clients { // race!
		conn.WriteMessage(websocket.TextMessage, msg)
	}
}
