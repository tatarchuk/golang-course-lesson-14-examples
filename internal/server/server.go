// Package server wires the examples into one HTTP handler: the pages, the demo login,
// the loans API of slide 41, the WebSocket endpoints of slides 13 and 39, the SSE
// endpoint of slide 30 and a stats endpoint for the connect/disconnect cycles of slide 43.
package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"libratrack/internal/auth"
	"libratrack/internal/library"
	"libratrack/internal/realtime"
	"libratrack/web"
)

// Config is what New needs.
type Config struct {
	Secret   string          // signs and verifies the JWTs
	Realtime realtime.Config // WebSocket settings
}

// Server is the wired application. Start the hub with go s.Hub.Run() before serving.
type Server struct {
	Hub     *realtime.Hub
	Loans   *library.MemoryLoans
	Handler http.Handler
}

// tokenTTL is how long a demo login lasts.
const tokenTTL = 8 * time.Hour

// New builds the hub, the loan service and every route.
func New(cfg Config) *Server {
	hub := realtime.NewHub(cfg.Realtime)
	loans := library.NewMemoryLoans()
	service := library.NewLoanService(loans, hub)
	authn := auth.Middleware(cfg.Secret)
	dashboard := func(*http.Request) ([]string, error) { return []string{"dashboard"}, nil }

	mux := http.NewServeMux()
	mux.Handle("GET /", http.FileServerFS(web.FS))
	mux.HandleFunc("POST /api/login", login(cfg.Secret))
	mux.HandleFunc("GET /api/loans", func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, http.StatusOK, loans.List()) })
	mux.Handle("POST /api/loans/{id}/return", authn(returnLoan(service)))
	mux.Handle("GET /ws/chat", authn(realtime.ServeWS(hub, cfg.Realtime, realtime.ChatTopics, realtime.ChatOnMessage(hub))))
	mux.Handle("GET /ws/dashboard", authn(realtime.ServeWS(hub, cfg.Realtime, dashboard, nil)))
	mux.Handle("GET /sse/dashboard", authn(realtime.SSEHandler(hub, "dashboard")))
	mux.HandleFunc("GET /debug/stats", func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, http.StatusOK, hub.Stats()) })
	return &Server{Hub: hub, Loans: loans, Handler: mux}
}

// login is the demo stand-in for the Lesson 9 login: a known demo user gets a token.
func login(secret string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Username string `json:"username"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&in); err != nil || !auth.IsDemoUser(in.Username) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unknown demo user (anna, bohdan or olena)"})
			return
		}
		token, err := auth.Issue(secret, in.Username, tokenTTL)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not issue a token"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"token": token, "user": in.Username})
	}
}

// returnLoan is the REST call of slide 41: POST /api/loans/{id}/return.
func returnLoan(s *library.LoanService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch err := s.Return(r.Context(), r.PathValue("id")); {
		case err == nil:
			w.WriteHeader(http.StatusNoContent)
		case errors.Is(err, library.ErrNotFound):
			writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		case errors.Is(err, library.ErrAlreadyReturned):
			writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		default:
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		}
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
