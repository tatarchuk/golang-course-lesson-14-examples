// Command wsclient is the terminal client of the examples. It logs in as a demo user and
// opens the chat or the dashboard WebSocket, or it runs connect/disconnect cycles against
// a running server and compares the server's goroutine count before and after (slide 43).
//
// Usage:
//
//	wsclient [flags] chat      send stdin lines to a chat room, print what arrives
//	wsclient [flags] listen    print the dashboard events
//	wsclient [flags] cycles    connect -clients, chat, disconnect; repeat -cycles times
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

var (
	base    = flag.String("base", "http://127.0.0.1:8080", "server address")
	origin  = flag.String("origin", "http://localhost:8080", "Origin header of the handshake (slide 13)")
	user    = flag.String("user", "anna", "demo user: anna, bohdan or olena")
	room    = flag.String("room", "general", "chat room")
	clients = flag.Int("clients", 20, "cycles: clients per cycle")
	cycles  = flag.Int("cycles", 5, "cycles: number of cycles")
	abrupt  = flag.Bool("abrupt", false, "cycles: drop the TCP connection instead of sending a close frame")
	settle  = flag.Duration("settle", 5*time.Second, "cycles: how long the server may take to clean up")
)

// plain is the HTTP client for login and stats; no keep-alive, so these calls leave no
// idle connection (and no server goroutine) behind.
var plain = &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{DisableKeepAlives: true}}

func main() {
	log.SetFlags(log.Ltime | log.Lmicroseconds)
	flag.Usage = func() {
		fmt.Fprintln(flag.CommandLine.Output(), "usage: wsclient [flags] chat|listen|cycles")
		flag.PrintDefaults()
	}
	flag.Parse()
	var err error
	switch flag.Arg(0) {
	case "chat":
		err = chat()
	case "listen":
		err = listen()
	case "cycles":
		err = runCycles()
	default:
		flag.Usage()
		os.Exit(2)
	}
	if err != nil {
		log.Fatal(err)
	}
}

func login(name string) (string, error) {
	body, _ := json.Marshal(map[string]string{"username": name})
	resp, err := plain.Post(*base+"/api/login", "application/json", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var out struct {
		Token string `json:"token"`
		Error string `json:"error"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("login %s: %s %s", name, resp.Status, out.Error)
	}
	return out.Token, nil
}

func dial(path, token string) (*websocket.Conn, error) {
	u := "ws" + strings.TrimPrefix(*base, "http") + path
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	c, resp, err := websocket.DefaultDialer.Dial(u+sep+"token="+url.QueryEscape(token), http.Header{"Origin": {*origin}})
	if err != nil && resp != nil {
		return nil, fmt.Errorf("handshake refused: %s", resp.Status)
	}
	return c, err
}

type stats struct {
	Clients    int `json:"clients"`
	Goroutines int `json:"goroutines"`
}

func serverStats() (stats, error) {
	var s stats
	resp, err := plain.Get(*base + "/debug/stats")
	if err != nil {
		return s, err
	}
	defer resp.Body.Close()
	return s, json.NewDecoder(resp.Body).Decode(&s)
}

// printFrames shows every frame until the connection ends.
func printFrames(c *websocket.Conn, done chan<- struct{}) {
	defer close(done)
	for {
		_, data, err := c.ReadMessage()
		if err != nil {
			log.Printf("connection closed: %v", err)
			return
		}
		log.Printf("<- %s", data)
	}
}

// waitInterrupt returns on Ctrl+C or when the connection ends, then says goodbye.
func waitInterrupt(c *websocket.Conn, done <-chan struct{}) {
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	select {
	case <-sig:
		_ = c.WriteControl(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, "bye"), time.Now().Add(time.Second))
		select {
		case <-done:
		case <-time.After(time.Second):
		}
	case <-done:
	}
}

func chat() error {
	token, err := login(*user)
	if err != nil {
		return err
	}
	c, err := dial("/ws/chat?room="+url.QueryEscape(*room), token)
	if err != nil {
		return err
	}
	defer c.Close()
	log.Printf("connected to room %s as %s; type a line and press Enter, Ctrl+C to leave", *room, *user)
	done := make(chan struct{})
	go printFrames(c, done)
	go func() {
		sc := bufio.NewScanner(os.Stdin)
		for sc.Scan() {
			msg, _ := json.Marshal(map[string]string{"room": *room, "text": sc.Text()})
			if err := c.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}
		}
	}()
	waitInterrupt(c, done)
	return nil
}

func listen() error {
	token, err := login(*user)
	if err != nil {
		return err
	}
	c, err := dial("/ws/dashboard", token)
	if err != nil {
		return err
	}
	defer c.Close()
	log.Printf("listening to the dashboard as %s, Ctrl+C to leave", *user)
	done := make(chan struct{})
	go printFrames(c, done)
	waitInterrupt(c, done)
	return nil
}

func runCycles() error {
	names := []string{"anna", "bohdan", "olena"}
	tokens := map[string]string{}
	for _, n := range names {
		t, err := login(n)
		if err != nil {
			return err
		}
		tokens[n] = t
	}
	before, err := serverStats()
	if err != nil {
		return err
	}
	how := "with a close frame (1000)"
	if *abrupt {
		how = "abruptly (TCP closed, no close frame)"
	}
	log.Printf("before: clients=%d goroutines=%d; %d cycles of %d clients, leaving %s",
		before.Clients, before.Goroutines, *cycles, *clients, how)

	for cycle := 1; cycle <= *cycles; cycle++ {
		conns := make([]*websocket.Conn, *clients)
		for i := range conns {
			c, err := dial("/ws/chat?room="+url.QueryEscape(*room), tokens[names[i%len(names)]])
			if err != nil {
				return fmt.Errorf("cycle %d client %d: %w", cycle, i+1, err)
			}
			conns[i] = c
		}
		during, _ := serverStats()
		for i, c := range conns {
			msg, _ := json.Marshal(map[string]string{"room": *room, "text": fmt.Sprintf("cycle %d client %d", cycle, i+1)})
			if err := c.WriteMessage(websocket.TextMessage, msg); err != nil {
				return fmt.Errorf("cycle %d client %d write: %w", cycle, i+1, err)
			}
		}
		received := 0
		readBy := time.Now().Add(5 * time.Second) // one deadline for the whole cycle: a lost message costs 5 s once
		for _, c := range conns {
			_ = c.SetReadDeadline(readBy)
			for n := 0; n < len(conns); n++ {
				if _, _, err := c.ReadMessage(); err != nil {
					break
				}
				received++
			}
		}
		for _, c := range conns {
			if *abrupt {
				_ = c.NetConn().Close()
				continue
			}
			_ = c.WriteControl(websocket.CloseMessage,
				websocket.FormatCloseMessage(websocket.CloseNormalClosure, "bye"), time.Now().Add(time.Second))
			_ = c.Close()
		}
		log.Printf("cycle %d/%d: %d connected (server: clients=%d goroutines=%d), %d of %d messages received, all left",
			cycle, *cycles, len(conns), during.Clients, during.Goroutines, received, len(conns)*len(conns))
	}

	deadline := time.Now().Add(*settle)
	after, err := serverStats()
	for err == nil && (after.Clients != 0 || after.Goroutines > before.Goroutines+2) && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
		after, err = serverStats()
	}
	if err != nil {
		return err
	}
	log.Printf("after:  clients=%d goroutines=%d (%+d against the start)", after.Clients, after.Goroutines, after.Goroutines-before.Goroutines)
	if after.Clients != 0 || after.Goroutines > before.Goroutines+2 {
		return fmt.Errorf("the server did not return to its baseline within %v: about %d goroutines leaked",
			*settle, after.Goroutines-before.Goroutines)
	}
	log.Print("OK: no client left registered, the goroutine count is back to the baseline")
	return nil
}
