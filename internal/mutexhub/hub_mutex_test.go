package mutexhub

import (
	"sync"
	"testing"
)

// TestConcurrentRegisterBroadcastUnregister runs the slide 24 hub from 50 goroutines at
// once. Every client drains its own send channel until Unregister closes it, so a
// broadcast never waits forever. Under -race the run must stay clean, and at the end the
// hub must be empty.
func TestConcurrentRegisterBroadcastUnregister(t *testing.T) {
	h := NewHub()
	var clients, readers sync.WaitGroup
	for i := 0; i < 50; i++ {
		clients.Add(1)
		go func() {
			defer clients.Done()
			c := NewClient(16)
			readers.Add(1)
			go func() {
				defer readers.Done()
				for range c.send {
				}
			}()
			h.Register(c)
			h.Broadcast([]byte("task_updated"))
			h.Unregister(c)
		}()
	}
	clients.Wait()
	readers.Wait()
	if n := h.Len(); n != 0 {
		t.Fatalf("clients left in the hub: %d, want 0", n)
	}
}

// TestUnregisterTwiceIsSafe pins the guard that keeps close(c.send) to exactly one call.
func TestUnregisterTwiceIsSafe(t *testing.T) {
	h := NewHub()
	c := NewClient(1)
	h.Register(c)
	h.Unregister(c)
	h.Unregister(c) // a second close of the same channel would panic
	if _, open := <-c.send; open {
		t.Fatal("send channel still open after Unregister")
	}
}
