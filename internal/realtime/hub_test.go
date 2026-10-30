package realtime

import (
	"fmt"
	"sync/atomic"
	"testing"
	"time"
)

// These tests talk to the hub without sockets: a Client is only its send queue and its
// topics, which is all the hub ever touches.

func testConfig() Config {
	return Config{PongWait: time.Minute, PingPeriod: 54 * time.Second, WriteWait: 10 * time.Second,
		MaxMessageSize: 4096, SendBuffer: 256}
}

func newTestClient(buffer int, topics ...string) *Client {
	c := &Client{send: make(chan []byte, buffer), topics: map[string]bool{}}
	for _, topic := range topics {
		c.topics[topic] = true
	}
	return c
}

// within runs f and reports a test error when f has not returned after d.
func within(t *testing.T, d time.Duration, what string, f func()) bool {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		f()
	}()
	select {
	case <-done:
		return true
	case <-time.After(d):
		t.Errorf("%s did not return within %v", what, d)
		return false
	}
}

func TestPublishOnlyToSubscribedTopic(t *testing.T) {
	h := NewHub(testConfig())
	go h.Run()
	defer h.Stop()
	chat := newTestClient(4, "chat:general")
	dash := newTestClient(4, "dashboard")
	h.registerClient(chat)
	h.registerClient(dash)

	h.Publish("dashboard", []byte(`{"event":"loan_returned"}`))
	select {
	case got := <-dash.send:
		if string(got) != `{"event":"loan_returned"}` {
			t.Fatalf("dashboard client got %s", got)
		}
	case <-time.After(time.Second):
		t.Fatal("dashboard client got nothing")
	}
	// The hub finishes one event before it answers the next request, so after a Stats
	// round trip nothing can still be on its way to the chat client.
	h.Stats()
	if n := len(chat.send); n != 0 {
		t.Fatalf("chat client got %d dashboard messages, want 0", n)
	}
}

// TestSlowClientDroppedNotBlocking is slides 28 and 44: a client that stops reading must
// not stall the fan-out; the hub drops it and keeps serving everybody else.
func TestSlowClientDroppedNotBlocking(t *testing.T) {
	h := NewHub(testConfig())
	go h.Run()
	defer h.Stop()
	stuck := newTestClient(2, "dashboard") // nobody drains it
	healthy := newTestClient(16, "dashboard")
	var got atomic.Int32
	go func() {
		for range healthy.send {
			got.Add(1)
		}
	}()
	h.registerClient(stuck)
	h.registerClient(healthy)

	for i := 1; i <= 10; i++ {
		if !within(t, 100*time.Millisecond, fmt.Sprintf("Publish #%d", i), func() { h.Publish("dashboard", []byte("tick")) }) {
			return
		}
	}
	deadline := time.Now().Add(time.Second)
	for got.Load() < 10 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if n := got.Load(); n != 10 {
		t.Fatalf("healthy client got %d messages, want 10 (the hub is stuck on the slow client)", n)
	}
	var clients int
	if !within(t, 100*time.Millisecond, "Stats", func() { clients = h.Stats().Clients }) {
		return
	}
	if clients != 1 {
		t.Fatalf("clients = %d, want 1 (the stuck client dropped)", clients)
	}
	<-stuck.send
	<-stuck.send
	if _, open := <-stuck.send; open {
		t.Fatal("the stuck client's send channel is still open")
	}
}

// TestStopUnblocksLateUnregister is slide 27's exit discipline at shutdown: Stop closes
// every send channel exactly once, a second Stop is harmless, and whatever arrives after
// the hub is gone (a late unregister from a readPump, a publish, a register) returns.
func TestStopUnblocksLateUnregister(t *testing.T) {
	h := NewHub(testConfig())
	go h.Run()
	c := newTestClient(1, "dashboard")
	h.registerClient(c)

	h.Stop()
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("second Stop panicked: %v", r)
			}
		}()
		h.Stop()
	}()
	select {
	case _, open := <-c.send:
		if open {
			t.Error("send delivered a message, want it closed")
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("Stop did not close the client's send channel")
	}
	within(t, 100*time.Millisecond, "unregisterClient after Stop", func() { h.unregisterClient(c) })
	within(t, 100*time.Millisecond, "Publish after Stop", func() { h.Publish("dashboard", []byte("late")) })
	within(t, 100*time.Millisecond, "registerClient after Stop", func() { h.registerClient(newTestClient(1)) })
}

// TestUnregisterAfterDropDoesNotCloseTwice is finding M1 of the slide 42 review: once the
// hub has dropped a slow client (and closed its send channel), the client's own late
// unregister must leave the channel alone. A second close panics inside Run, which ends
// the whole process.
func TestUnregisterAfterDropDoesNotCloseTwice(t *testing.T) {
	h := NewHub(testConfig())
	go h.Run()
	defer h.Stop()
	stuck := newTestClient(1, "dashboard") // nobody drains it
	h.registerClient(stuck)
	for i := 1; i <= 3; i++ {
		if !within(t, 100*time.Millisecond, fmt.Sprintf("Publish #%d", i), func() { h.Publish("dashboard", []byte("tick")) }) {
			return
		}
	}
	clients := -1
	for deadline := time.Now().Add(time.Second); clients != 0 && time.Now().Before(deadline); {
		if !within(t, 100*time.Millisecond, "Stats", func() { clients = h.Stats().Clients }) {
			return
		}
	}
	if clients != 0 {
		t.Fatalf("clients = %d, want 0 (the slow client dropped)", clients)
	}

	within(t, 100*time.Millisecond, "unregister of the dropped client", func() { h.unregisterClient(stuck) })
	if !within(t, 100*time.Millisecond, "Stats after the late unregister", func() { clients = h.Stats().Clients }) {
		return
	}
	if clients != 0 {
		t.Fatalf("clients = %d after the late unregister, want 0", clients)
	}
}

// TestNewHubRejectsBadConfig: the fixes rely on a buffered send queue, a ping period below
// the pong wait, a write deadline and a read limit (review section "Order of the fixes").
func TestNewHubRejectsBadConfig(t *testing.T) {
	for name, mutate := range map[string]func(*Config){
		"unbuffered send":            func(c *Config) { c.SendBuffer = 0 },
		"no ping period":             func(c *Config) { c.PingPeriod = 0 },
		"ping period over pong wait": func(c *Config) { c.PingPeriod = c.PongWait },
		"no write deadline":          func(c *Config) { c.WriteWait = 0 },
		"no read limit":              func(c *Config) { c.MaxMessageSize = 0 },
	} {
		cfg := testConfig()
		mutate(&cfg)
		if err := cfg.Validate(); err == nil {
			t.Errorf("%s: Validate accepted %+v", name, cfg)
		}
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("%s: NewHub accepted %+v", name, cfg)
				}
			}()
			NewHub(cfg)
		}()
	}
	if err := testConfig().Validate(); err != nil {
		t.Errorf("the test config is valid, Validate said %v", err)
	}
}
