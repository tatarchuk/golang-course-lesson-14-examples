package auth

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

const secret = "test-secret-for-the-examples-0123456789"

func TestIssueParseRoundTrip(t *testing.T) {
	tok, err := Issue(secret, "anna", time.Hour)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	c, err := Parse(secret, tok)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if c.User != "anna" {
		t.Fatalf("user = %q, want anna", c.User)
	}
}

func TestParseRejectsOtherSecretAndExpiredToken(t *testing.T) {
	other, _ := Issue("another-secret-that-is-long-enough-0123", "anna", time.Hour)
	if _, err := Parse(secret, other); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("other secret: err = %v, want ErrInvalidToken", err)
	}
	expired, _ := Issue(secret, "anna", -time.Minute)
	if _, err := Parse(secret, expired); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("expired: err = %v, want ErrInvalidToken", err)
	}
}

// TestMiddlewareReadsHeaderOrQuery: the API sends the Bearer header, the browser's
// WebSocket handshake cannot, so it sends ?token= (slide 39).
func TestMiddlewareReadsHeaderOrQuery(t *testing.T) {
	h := Middleware(secret)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, _ := UserFrom(r.Context())
		_, _ = io.WriteString(w, u)
	}))
	tok, _ := Issue(secret, "bohdan", time.Hour)

	byHeader := httptest.NewRequest(http.MethodGet, "/", nil)
	byHeader.Header.Set("Authorization", "Bearer "+tok)
	byQuery := httptest.NewRequest(http.MethodGet, "/?token="+tok, nil)
	for name, req := range map[string]*http.Request{"header": byHeader, "query": byQuery} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK || rec.Body.String() != "bohdan" {
			t.Errorf("%s: %d %q, want 200 bohdan", name, rec.Code, rec.Body.String())
		}
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("no token: %d, want 401", rec.Code)
	}
}
