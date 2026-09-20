package middleware

import (
	"crypto/rand"
	"crypto/rsa"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	jwtlib "github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	security "post-service/internal/security/jwt"
)

func TestAuthWithSignedTokens(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	user := uuid.New()
	sign := func(id uuid.UUID, expiry time.Time, kid string) string {
		t.Helper()
		token := jwtlib.NewWithClaims(jwtlib.SigningMethodRS256, security.Claims{
			UserID: id, RegisteredClaims: jwtlib.RegisteredClaims{ExpiresAt: jwtlib.NewNumericDate(expiry)},
		})
		token.Header["kid"] = kid
		signed, err := token.SignedString(key)
		if err != nil {
			t.Fatal(err)
		}
		return signed
	}
	valid := sign(user, time.Now().Add(time.Hour), "test-key")
	expired := sign(user, time.Now().Add(-time.Hour), "test-key")
	wrongKid := sign(user, time.Now().Add(time.Hour), "other-key")
	emptyUser := sign(uuid.Nil, time.Now().Add(time.Hour), "test-key")
	verifier := security.NewVerifier(&key.PublicKey, "test-key", slog.New(slog.NewTextHandler(io.Discard, nil)))
	for _, tt := range []struct {
		name    string
		headers []string
		want    int
	}{
		{"valid", []string{"Bearer " + valid}, 204},
		{"case insensitive scheme", []string{"bearer " + valid}, 204},
		{"missing", nil, 401},
		{"empty", []string{"Bearer"}, 401},
		{"wrong scheme", []string{"Basic " + valid}, 401},
		{"malformed", []string{"Bearer garbage"}, 401},
		{"extra field", []string{"Bearer " + valid + " extra"}, 401},
		{"duplicate", []string{"Bearer " + valid, "Bearer " + valid}, 401},
		{"expired", []string{"Bearer " + expired}, 401},
		{"unknown key", []string{"Bearer " + wrongKid}, 401},
		{"missing identity", []string{"Bearer " + emptyUser}, 401},
	} {
		t.Run(tt.name, func(t *testing.T) {
			called := false
			h := Auth(verifier)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
				id, ok := UserID(r.Context())
				if !ok || id != user {
					t.Fatalf("identity=%v ok=%v", id, ok)
				}
				w.WriteHeader(http.StatusNoContent)
			}))
			req := httptest.NewRequest("GET", "/feed", nil)
			for _, value := range tt.headers {
				req.Header.Add("Authorization", value)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			if w.Code != tt.want || called != (tt.want == 204) {
				t.Fatalf("status=%d called=%v", w.Code, called)
			}
			if tt.want == 401 && w.Header().Get("WWW-Authenticate") != "Bearer" {
				t.Fatal("missing auth challenge")
			}
		})
	}
}
