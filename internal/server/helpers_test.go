package server_test

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"libratrack/internal/auth"
	"libratrack/internal/realtime"
	"libratrack/internal/server"
)

const secret = "test-secret-for-the-examples-0123456789"

// env is one running server: the hub, the HTTP routes behind an httptest server on
// 127.0.0.1, and the origin the upgrader accepts (the server's own URL, as a browser
// page served by it would send).
type env struct {
	t      *testing.T
	srv    *server.Server
	ts     *httptest.Server
	base   string // http://127.0.0.1:port
	origin string
}

func newEnv(t *testing.T, tune ...func(*realtime.Config)) *env {
	t.Helper()
	ts := httptest.NewUnstartedServer(nil)
	base := "http://" + ts.Listener.Addr().String()
	rt := realtime.Config{PongWait: 60 * time.Second, PingPeriod: 54 * time.Second, WriteWait: 10 * time.Second,
		MaxMessageSize: 4096, SendBuffer: 256, AllowedOrigin: base}
	for _, f := range tune {
		f(&rt)
	}
	s := server.New(server.Config{Secret: secret, Realtime: rt})
	go s.Hub.Run()
	ts.Config.Handler = s.Handler
	ts.Start()
	t.Cleanup(func() {
		func() {
			defer func() { _ = recover() }() // a test may have stopped the hub already
			s.Hub.Stop()
		}()
		ts.CloseClientConnections()
		closeWithin(t, ts, 3*time.Second)
	})
	return &env{t: t, srv: s, ts: ts, base: base, origin: base}
}

// closeWithin closes the test server but turns a handler that never returns into a test
// failure instead of a hung test run.
func closeWithin(t *testing.T, ts *httptest.Server, d time.Duration) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		ts.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(d):
		t.Errorf("a handler was still running %v after the server was asked to close", d)
	}
}

func (e *env) token(user string) string {
	e.t.Helper()
	tok, err := auth.Issue(secret, user, time.Hour)
	if err != nil {
		e.t.Fatalf("issue token: %v", err)
	}
	return tok
}

// dial opens a WebSocket on path as user (no token when user is empty). A nil header
// sends the allowed Origin.
func (e *env) dial(path, user string, hdr http.Header) (*websocket.Conn, *http.Response, error) {
	u := "ws" + strings.TrimPrefix(e.base, "http") + path
	if user != "" {
		sep := "?"
		if strings.Contains(path, "?") {
			sep = "&"
		}
		u += sep + "token=" + url.QueryEscape(e.token(user))
	}
	if hdr == nil {
		hdr = http.Header{"Origin": {e.origin}}
	}
	return websocket.DefaultDialer.Dial(u, hdr)
}

func (e *env) mustDial(path, user string) *websocket.Conn {
	e.t.Helper()
	c, _, err := e.dial(path, user, nil)
	if err != nil {
		e.t.Fatalf("dial %s as %s: %v", path, user, err)
	}
	e.t.Cleanup(func() { _ = c.Close() })
	return c
}

// clients asks the hub how many clients it has, failing instead of blocking when the
// hub does not answer.
func (e *env) clients() int {
	e.t.Helper()
	got := make(chan int, 1)
	go func() { got <- e.srv.Hub.Stats().Clients }()
	select {
	case n := <-got:
		return n
	case <-time.After(time.Second):
		e.t.Fatal("the hub did not answer Stats within 1s")
		return -1
	}
}

// waitClients polls until the hub reports n clients.
func (e *env) waitClients(n int, d time.Duration) {
	e.t.Helper()
	deadline := time.Now().Add(d)
	last := e.clients()
	for last != n && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
		last = e.clients()
	}
	if last != n {
		e.t.Fatalf("hub has %d clients after %v, want %d", last, d, n)
	}
}

// post sends a POST with the user's Bearer token and returns the status code.
func (e *env) post(path, user string) int {
	e.t.Helper()
	req, _ := http.NewRequest(http.MethodPost, e.base+path, nil)
	req.Header.Set("Authorization", "Bearer "+e.token(user))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatalf("POST %s: %v", path, err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

// readJSON reads one frame within d and decodes it into a string map.
func readJSON(t *testing.T, c *websocket.Conn, d time.Duration) map[string]string {
	t.Helper()
	_ = c.SetReadDeadline(time.Now().Add(d))
	_, data, err := c.ReadMessage()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var m map[string]string
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("frame %s: %v", data, err)
	}
	return m
}

// expectSilence fails when a frame arrives within d. It ends the connection's reads:
// gorilla treats a read timeout as final.
func expectSilence(t *testing.T, c *websocket.Conn, d time.Duration) {
	t.Helper()
	_ = c.SetReadDeadline(time.Now().Add(d))
	_, data, err := c.ReadMessage()
	if err == nil {
		t.Errorf("unexpected frame: %s", data)
		return
	}
	var ne net.Error
	if !errors.As(err, &ne) || !ne.Timeout() {
		t.Errorf("read: %v, want a timeout", err)
	}
}

// closeCode reads until the connection ends and returns the close code the server sent.
func closeCode(t *testing.T, c *websocket.Conn, d time.Duration) int {
	t.Helper()
	_ = c.SetReadDeadline(time.Now().Add(d))
	for {
		_, _, err := c.ReadMessage()
		if err == nil {
			continue
		}
		var ce *websocket.CloseError
		if errors.As(err, &ce) {
			return ce.Code
		}
		t.Fatalf("connection ended without a close frame: %v", err)
		return 0
	}
}

// goroutinesBackTo waits until the process runs at most baseline+2 goroutines.
func goroutinesBackTo(t *testing.T, baseline int, d time.Duration) {
	t.Helper()
	deadline := time.Now().Add(d)
	n := runtime.NumGoroutine()
	for n > baseline+2 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
		n = runtime.NumGoroutine()
	}
	if n > baseline+2 {
		t.Fatalf("%d goroutines %v after the clients left, baseline %d: about %d leaked", n, d, baseline, n-baseline)
	}
}
