// Package library is the slice of LibraTrack the live dashboard watches: book loans and
// the LoanService of slide 41, whose Return method gains exactly one new line, a Publish
// call. The service never learns that WebSockets exist (slide 19).
package library

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
)

var (
	// ErrNotFound means no loan has the given id.
	ErrNotFound = errors.New("loan not found")
	// ErrAlreadyReturned means the loan was returned before.
	ErrAlreadyReturned = errors.New("loan already returned")
)

// Publisher is the one thing the service needs from the hub (slide 20's Publish).
type Publisher interface {
	Publish(topic string, payload []byte)
}

// Loan is one book on loan.
type Loan struct {
	ID       string `json:"id"`
	Book     string `json:"book"`
	Reader   string `json:"reader"`
	Returned bool   `json:"returned"`
}

// MemoryLoans is the loan store of the examples, seeded with five open loans.
type MemoryLoans struct {
	mu    sync.Mutex
	loans []Loan
}

// NewMemoryLoans returns the seeded store: L-1 to L-5, all open.
func NewMemoryLoans() *MemoryLoans {
	return &MemoryLoans{loans: []Loan{
		{ID: "L-1", Book: "The Go Programming Language", Reader: "reader-01"},
		{ID: "L-2", Book: "Concurrency in Go", Reader: "reader-02"},
		{ID: "L-3", Book: "Designing Data-Intensive Applications", Reader: "reader-03"},
		{ID: "L-4", Book: "The Pragmatic Programmer", Reader: "reader-04"},
		{ID: "L-5", Book: "Clean Architecture", Reader: "reader-05"},
	}}
}

// List returns a copy of every loan.
func (m *MemoryLoans) List() []Loan {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Loan(nil), m.loans...)
}

// MarkReturned marks the loan returned.
func (m *MemoryLoans) MarkReturned(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.loans {
		if m.loans[i].ID == id {
			if m.loans[i].Returned {
				return ErrAlreadyReturned
			}
			m.loans[i].Returned = true
			return nil
		}
	}
	return ErrNotFound
}

// LoanService is slide 41's service.
type LoanService struct {
	repo *MemoryLoans
	hub  Publisher
}

// NewLoanService returns a service that publishes to p.
func NewLoanService(repo *MemoryLoans, p Publisher) *LoanService {
	return &LoanService{repo: repo, hub: p}
}

// Return is slide 41: mark the loan returned, then one new line publishes the event.
func (s *LoanService) Return(ctx context.Context, loanID string) error {
	if err := s.repo.MarkReturned(ctx, loanID); err != nil {
		return err
	}
	event, _ := json.Marshal(map[string]any{
		"event":   "loan_returned",
		"loan_id": loanID,
	})
	s.hub.Publish("dashboard", event)
	return nil
}
