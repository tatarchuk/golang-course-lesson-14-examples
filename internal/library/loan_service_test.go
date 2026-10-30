package library

import (
	"context"
	"errors"
	"sync"
	"testing"
)

// recorder is a Publisher that remembers every call.
type recorder struct {
	mu       sync.Mutex
	topics   []string
	payloads []string
}

func (r *recorder) Publish(topic string, payload []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.topics = append(r.topics, topic)
	r.payloads = append(r.payloads, string(payload))
}

func (r *recorder) calls() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.topics)
}

// TestReturnPublishesLoanReturned is slide 41: the one new line in LoanService.Return.
func TestReturnPublishesLoanReturned(t *testing.T) {
	rec := &recorder{}
	s := NewLoanService(NewMemoryLoans(), rec)
	if err := s.Return(context.Background(), "L-1"); err != nil {
		t.Fatalf("Return: %v", err)
	}
	if rec.calls() != 1 || rec.topics[0] != "dashboard" || rec.payloads[0] != `{"event":"loan_returned","loan_id":"L-1"}` {
		t.Fatalf("published %q %q, want dashboard {\"event\":\"loan_returned\",\"loan_id\":\"L-1\"}", rec.topics, rec.payloads)
	}
}

func TestReturnTwiceConflicts(t *testing.T) {
	rec := &recorder{}
	s := NewLoanService(NewMemoryLoans(), rec)
	_ = s.Return(context.Background(), "L-1")
	if err := s.Return(context.Background(), "L-1"); !errors.Is(err, ErrAlreadyReturned) {
		t.Fatalf("second Return: %v, want ErrAlreadyReturned", err)
	}
	if rec.calls() != 1 {
		t.Fatalf("published %d events, want 1", rec.calls())
	}
}

func TestReturnUnknownLoan(t *testing.T) {
	rec := &recorder{}
	s := NewLoanService(NewMemoryLoans(), rec)
	if err := s.Return(context.Background(), "L-404"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Return L-404: %v, want ErrNotFound", err)
	}
	if rec.calls() != 0 {
		t.Fatalf("published %d events, want 0", rec.calls())
	}
}
