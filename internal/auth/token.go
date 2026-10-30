// Package auth is the token side of the examples: HS256 JWTs that name a demo user, and
// the middleware of slide 39 that takes the token from the Authorization header (the
// REST API) or from the token query parameter (the browser's WebSocket handshake and
// EventSource cannot set headers). It stands in for the Lesson 9 login.
package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// DemoUsers may log in without a password (POST /api/login): local demo accounts only.
var DemoUsers = []string{"anna", "bohdan", "olena"}

// IsDemoUser reports whether name is one of DemoUsers.
func IsDemoUser(name string) bool { return slices.Contains(DemoUsers, name) }

// Claims is what a verified token says about the caller.
type Claims struct {
	User string
}

// ErrInvalidToken covers every token the server rejects.
var ErrInvalidToken = errors.New("invalid token")

// Issue signs an HS256 token for user that expires after ttl.
func Issue(secret, user string, ttl time.Duration) (string, error) {
	now := time.Now()
	claims := jwt.RegisteredClaims{
		Subject:   user,
		IssuedAt:  jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(secret))
}

// Parse verifies the signature (HS256 only) and the expiry, which must be present.
func Parse(secret, raw string) (Claims, error) {
	var claims jwt.RegisteredClaims
	keyFunc := func(*jwt.Token) (any, error) { return []byte(secret), nil }
	token, err := jwt.ParseWithClaims(raw, &claims, keyFunc,
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}), jwt.WithExpirationRequired())
	if err != nil {
		return Claims{}, fmt.Errorf("%w: %v", ErrInvalidToken, err)
	}
	if !token.Valid || claims.Subject == "" {
		return Claims{}, ErrInvalidToken
	}
	return Claims{User: claims.Subject}, nil
}

type ctxKey struct{}

// UserFrom returns the user the middleware verified, or false.
func UserFrom(ctx context.Context) (string, bool) {
	u, ok := ctx.Value(ctxKey{}).(string)
	return u, ok
}

// Middleware lets a request through only with a valid token (slide 39) and stores its
// user in the context; otherwise it answers 401 with a JSON error.
func Middleware(secret string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw := r.URL.Query().Get("token")
			if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
				raw = strings.TrimPrefix(h, "Bearer ")
			}
			claims, err := Parse(secret, raw)
			if err != nil {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "a valid token is required"})
				return
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, claims.User)))
		})
	}
}
