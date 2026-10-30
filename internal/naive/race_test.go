//go:build racedemo

package naive

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// TestNaiveMapRace drives the slide 23 code the way a server would: every connection
// registers itself from its own handler goroutine while another goroutine broadcasts.
// The test asserts nothing; the race detector is the assertion. With -race it fails
// with "WARNING: DATA RACE"; without -race it passes, which is exactly the trap of
// slide 44. The sleeps keep the map accesses apart in time, so the runtime's own
// concurrent-map check stays quiet, but nothing orders them, so -race still reports.
//
// Run: go test -race -tags racedemo -count=1 -v ./internal/naive
func TestNaiveMapRace(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(handleWS))
	defer srv.Close()
	url := "ws" + strings.TrimPrefix(srv.URL, "http")

	var conns []*websocket.Conn
	for i := 0; i < 5; i++ {
		c, _, err := websocket.DefaultDialer.Dial(url, nil)
		if err != nil {
			t.Fatalf("dial %d: %v", i, err)
		}
		conns = append(conns, c)
		time.Sleep(20 * time.Millisecond) // the handler goroutine writes clients[conn]
		broadcast([]byte("hello"))        // this goroutine ranges over the same map
	}
	for _, c := range conns {
		_ = c.Close()
	}
	time.Sleep(50 * time.Millisecond) // the handlers run their deferred delete
}
