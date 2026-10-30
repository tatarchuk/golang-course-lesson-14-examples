// Command libratrack serves the Lesson 14 examples: the pages, the demo login, the loans
// API, the chat and dashboard WebSockets and the SSE dashboard. Plain HTTP on the loopback
// interface: local development only.
package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"libratrack/internal/realtime"
	"libratrack/internal/server"
)

// devSecret signs the demo tokens when JWT_SECRET is not set.
const devSecret = "libratrack-examples-development-secret-0123456789"

func main() {
	addr := flag.String("addr", "127.0.0.1:8080", "listen address")
	origin := flag.String("origin", "http://localhost:8080", "the one Origin the WebSocket upgrader accepts (slide 13)")
	pongWait := flag.Duration("pong-wait", 60*time.Second, "read deadline renewed by every pong; pings go out at 9/10 of it (slide 15)")
	flag.Parse()

	secret := os.Getenv("JWT_SECRET")
	if secret == "" {
		secret = devSecret
		log.Print("WARN: JWT_SECRET is not set, the built-in development secret signs the tokens")
	}
	rt := realtime.Config{
		PongWait:       *pongWait,
		PingPeriod:     *pongWait * 9 / 10,
		WriteWait:      10 * time.Second,
		MaxMessageSize: 4096,
		SendBuffer:     16,
		AllowedOrigin:  *origin,
	}
	s := server.New(server.Config{Secret: secret, Realtime: rt})
	go s.Hub.Run()

	srv := &http.Server{Addr: *addr, Handler: s.Handler, ReadHeaderTimeout: 5 * time.Second}
	srv.RegisterOnShutdown(s.Hub.Stop) // Shutdown does not touch hijacked connections
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	log.Printf("listening on %s, open http://localhost%s (origin %s, pong wait %v; TLS off, local development only)",
		*addr, portOf(*addr), *origin, *pongWait)

	select {
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
	case <-ctx.Done():
		log.Print("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			log.Printf("shutdown: %v", err)
		}
	}
}

// portOf returns ":8080" for "127.0.0.1:8080".
func portOf(addr string) string {
	for i := len(addr) - 1; i >= 0; i-- {
		if addr[i] == ':' {
			return addr[i:]
		}
	}
	return ""
}
