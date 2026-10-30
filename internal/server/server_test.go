package server_test

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"net/http"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"libratrack/internal/realtime"
)

// TestHandshakeAcceptMatchesSlide8 sends the slide 8 request byte for byte (its sample
// Sec-WebSocket-Key) and expects the slide's 101 answer with its Sec-WebSocket-Accept.
func TestHandshakeAcceptMatchesSlide8(t *testing.T) {
	e := newEnv(t)
	addr := e.ts.Listener.Addr().String()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	req := "GET /ws/dashboard?token=" + e.token("anna") + " HTTP/1.1\r\n" +
		"Host: " + addr + "\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n" +
		"Sec-WebSocket-Version: 13\r\n" +
		"Origin: " + e.origin + "\r\n\r\n"
	if _, err := conn.Write([]byte(req)); err != nil {
		t.Fatal(err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("status %d, want 101", resp.StatusCode)
	}
	if got := resp.Header.Get("Sec-WebSocket-Accept"); got != "s3pPLMBiTxaQ9kYGzzhZRbK+xOo=" {
		t.Fatalf("Sec-WebSocket-Accept %q, want the slide 8 value s3pPLMBiTxaQ9kYGzzhZRbK+xOo=", got)
	}
	if !strings.EqualFold(resp.Header.Get("Upgrade"), "websocket") {
		t.Fatalf("Upgrade header %q", resp.Header.Get("Upgrade"))
	}
}

// TestCheckOriginRejectsForeignOrigin is slides 13 and 45: the upgrader accepts exactly
// one origin.
func TestCheckOriginRejectsForeignOrigin(t *testing.T) {
	e := newEnv(t)
	for name, hdr := range map[string]http.Header{
		"foreign origin": {"Origin": {"http://evil.example"}},
		"no origin":      {},
	} {
		c, resp, err := e.dial("/ws/dashboard", "anna", hdr)
		if err == nil {
			c.Close()
			t.Errorf("%s: upgrade accepted, want 403", name)
			continue
		}
		if resp == nil || resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s: %v, want a 403 response", name, err)
		}
	}
	e.mustDial("/ws/dashboard", "anna")
}

func TestHandshakeRequiresToken(t *testing.T) {
	e := newEnv(t)
	if _, resp, err := e.dial("/ws/dashboard", "", nil); err == nil || resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("no token: %v, want 401", err)
	}
	u := "ws" + strings.TrimPrefix(e.base, "http") + "/ws/dashboard?token=not.a.token"
	if _, resp, err := websocket.DefaultDialer.Dial(u, http.Header{"Origin": {e.origin}}); err == nil || resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("bad token: %v, want 401", err)
	}
}

// TestChatThreeClients is slides 40 and 43: three clients in one room, a message from one
// reaches the other two, and its sender sees it exactly once (no duplicate).
func TestChatThreeClients(t *testing.T) {
	e := newEnv(t)
	anna := e.mustDial("/ws/chat?room=general", "anna")
	bohdan := e.mustDial("/ws/chat?room=general", "bohdan")
	olena := e.mustDial("/ws/chat?room=general", "olena")
	e.waitClients(3, 2*time.Second)

	if err := anna.WriteMessage(websocket.TextMessage, []byte(`{"room":"general","text":"hi"}`)); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"room": "general", "author": "anna", "text": "hi"}
	for name, c := range map[string]*websocket.Conn{"anna": anna, "bohdan": bohdan, "olena": olena} {
		if got := readJSON(t, c, time.Second); fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("%s got %v, want %v", name, got, want)
		}
		expectSilence(t, c, 200*time.Millisecond)
	}
}

// TestChatAuthorAndRoomFromConnection: the author comes from the token (slide 40) and the
// room from the connection, whatever the message body claims.
func TestChatAuthorAndRoomFromConnection(t *testing.T) {
	e := newEnv(t)
	anna := e.mustDial("/ws/chat?room=general", "anna")
	bohdan := e.mustDial("/ws/chat?room=general", "bohdan")
	olena := e.mustDial("/ws/chat?room=events", "olena")
	e.waitClients(3, 2*time.Second)

	if err := anna.WriteMessage(websocket.TextMessage, []byte(`{"room":"events","author":"mallory","text":"x"}`)); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"room": "general", "author": "anna", "text": "x"}
	if got := readJSON(t, bohdan, time.Second); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("bohdan got %v, want %v", got, want)
	}
	expectSilence(t, olena, 200*time.Millisecond)
}

// TestDashboardEventWithinOneSecond is slides 41 and 43: a REST call reaches the
// connected dashboard clients in about a second.
func TestDashboardEventWithinOneSecond(t *testing.T) {
	e := newEnv(t)
	dash := e.mustDial("/ws/dashboard", "anna")
	e.waitClients(1, 2*time.Second)

	if code := e.post("/api/loans/L-2/return", "bohdan"); code != http.StatusNoContent {
		t.Fatalf("return L-2: %d, want 204", code)
	}
	want := map[string]string{"event": "loan_returned", "loan_id": "L-2"}
	if got := readJSON(t, dash, time.Second); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("dashboard got %v, want %v", got, want)
	}
	if code := e.post("/api/loans/L-2/return", "bohdan"); code != http.StatusConflict {
		t.Errorf("return L-2 again: %d, want 409", code)
	}
	if code := e.post("/api/loans/L-404/return", "bohdan"); code != http.StatusNotFound {
		t.Errorf("return L-404: %d, want 404", code)
	}
}

// TestSSEDashboard is slide 30: the same dashboard events as a text/event-stream, and the
// subscription ends when the browser goes away.
func TestSSEDashboard(t *testing.T) {
	e := newEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, e.base+"/sse/dashboard?token="+e.token("anna"), nil)
	type result struct {
		resp *http.Response
		err  error
	}
	got := make(chan result, 1)
	go func() {
		resp, err := http.DefaultClient.Do(req)
		got <- result{resp, err}
	}()
	e.waitClients(1, 2*time.Second)
	if code := e.post("/api/loans/L-3/return", "bohdan"); code != http.StatusNoContent {
		t.Fatalf("return L-3: %d, want 204", code)
	}

	var r result
	select {
	case r = <-got:
	case <-time.After(time.Second):
		t.Fatal("no SSE response within 1s")
	}
	if r.err != nil {
		t.Fatal(r.err)
	}
	defer r.resp.Body.Close()
	if ct := r.resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("Content-Type %q, want text/event-stream", ct)
	}
	lines := make(chan string, 8)
	go func() {
		sc := bufio.NewScanner(r.resp.Body)
		for sc.Scan() {
			lines <- sc.Text()
		}
		close(lines)
	}()
	want := `data: {"event":"loan_returned","loan_id":"L-3"}`
	deadline := time.After(time.Second)
	for found := false; !found; {
		select {
		case line, ok := <-lines:
			if !ok {
				t.Fatal("stream ended before the event")
			}
			found = line == want
		case <-deadline:
			t.Fatalf("no %q within 1s", want)
		}
	}

	cancel() // the browser closes the page
	e.waitClients(0, time.Second)
}

// TestOversizedMessageCloses1009 is slide 16: SetReadLimit bounds a message.
func TestOversizedMessageCloses1009(t *testing.T) {
	e := newEnv(t)
	c := e.mustDial("/ws/chat?room=general", "anna")
	if err := c.WriteMessage(websocket.TextMessage, []byte(strings.Repeat("x", 5000))); err != nil {
		t.Fatal(err)
	}
	if code := closeCode(t, c, time.Second); code != websocket.CloseMessageTooBig {
		t.Fatalf("close code %d, want 1009", code)
	}
}

// TestAbruptDisconnectCleanedUp is slide 43: a client that vanishes without a close frame
// is unregistered.
func TestAbruptDisconnectCleanedUp(t *testing.T) {
	e := newEnv(t)
	gone := e.mustDial("/ws/dashboard", "anna")
	e.mustDial("/ws/dashboard", "bohdan")
	e.mustDial("/ws/dashboard", "olena")
	e.waitClients(3, 2*time.Second)

	_ = gone.NetConn().Close() // no close frame: a crashed tab or a lost network
	e.waitClients(2, time.Second)
}

// TestDeadPeerTimesOut is slides 15 and 43: a client that stops answering pings is closed
// by the read deadline.
func TestDeadPeerTimesOut(t *testing.T) {
	e := newEnv(t, func(c *realtime.Config) {
		c.PongWait = 300 * time.Millisecond
		c.PingPeriod = 100 * time.Millisecond
	})
	e.mustDial("/ws/dashboard", "anna") // never reads, so it never answers a ping
	e.waitClients(1, 2*time.Second)
	e.waitClients(0, time.Second)
}

// TestPingsAndBroadcastsShareOneWriter is slides 14 and 44: a connection has exactly one
// writer. With a ping every millisecond and a burst of 300 broadcasts, a second writer on
// the same connection is caught by the race detector or by gorilla's own check.
func TestPingsAndBroadcastsShareOneWriter(t *testing.T) {
	e := newEnv(t, func(c *realtime.Config) {
		c.PingPeriod = time.Millisecond
		c.PongWait = 5 * time.Second
		c.SendBuffer = 512 // the whole burst fits: this test is about writers, not backpressure
	})
	dash := e.mustDial("/ws/dashboard", "anna")
	e.waitClients(1, 2*time.Second)

	const total = 300
	got := make(chan int, 1)
	go func() { // reading also answers the pings
		n := 0
		_ = dash.SetReadDeadline(time.Now().Add(5 * time.Second))
		for n < total {
			if _, _, err := dash.ReadMessage(); err != nil {
				break
			}
			n++
		}
		got <- n
	}()
	for i := 0; i < total; i++ {
		e.srv.Hub.Publish("dashboard", []byte(`{"event":"tick"}`))
	}
	if n := <-got; n != total {
		t.Fatalf("dashboard got %d of %d messages", n, total)
	}
}

// TestConnectDisconnectCyclesNoLeak is slide 43 and homework item 2: 100 clients connect
// and leave in five rounds, half with a close frame and half abruptly, and the goroutine
// count returns to where it started.
func TestConnectDisconnectCyclesNoLeak(t *testing.T) {
	e := newEnv(t)
	users := []string{"anna", "bohdan", "olena"}
	baseline := runtime.NumGoroutine()

	for cycle := 1; cycle <= 5; cycle++ {
		conns := make([]*websocket.Conn, 20)
		for i := range conns {
			c, _, err := e.dial("/ws/chat?room=general", users[i%len(users)], nil)
			if err != nil {
				t.Fatalf("cycle %d dial %d: %v", cycle, i, err)
			}
			conns[i] = c
		}
		e.waitClients(20, 2*time.Second)
		for i, c := range conns {
			msg := fmt.Sprintf(`{"room":"general","text":"ping %d.%d"}`, cycle, i)
			if err := c.WriteMessage(websocket.TextMessage, []byte(msg)); err != nil {
				t.Fatalf("cycle %d write %d: %v", cycle, i, err)
			}
		}
		for i, c := range conns {
			_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
			for n := 0; n < len(conns); n++ {
				if _, _, err := c.ReadMessage(); err != nil {
					t.Fatalf("cycle %d client %d read %d of %d: %v", cycle, i, n+1, len(conns), err)
				}
			}
		}
		for i, c := range conns {
			if i%2 == 0 {
				_ = c.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "bye"), time.Now().Add(time.Second))
				_ = c.Close()
			} else {
				_ = c.NetConn().Close()
			}
		}
		e.waitClients(0, 2*time.Second)
	}
	goroutinesBackTo(t, baseline, 3*time.Second)
}

// TestShutdownClosesClients is slide 27 at server shutdown: every client gets a 1001
// close frame and every per-client goroutine ends.
func TestShutdownClosesClients(t *testing.T) {
	e := newEnv(t)
	baseline := runtime.NumGoroutine()
	conns := make([]*websocket.Conn, 10)
	for i := range conns {
		conns[i] = e.mustDial("/ws/dashboard", "anna")
	}
	e.waitClients(10, 2*time.Second)

	e.srv.Hub.Stop()
	for i, c := range conns {
		if code := closeCode(t, c, time.Second); code != websocket.CloseGoingAway {
			t.Fatalf("client %d: close code %d, want 1001", i, code)
		}
	}
	goroutinesBackTo(t, baseline, 3*time.Second)
}
