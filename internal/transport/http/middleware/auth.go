package middleware

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"post-service/internal/security/jwt"

	"github.com/google/uuid"
)

type TokenVerifier interface {
	Verify(string) (*jwt.Claims, error)
}

type userIDKey struct{}

// UserID returns the authenticated identity, never a value supplied in JSON.
func UserID(ctx context.Context) (uuid.UUID, bool) {
	id, ok := ctx.Value(userIDKey{}).(uuid.UUID)
	return id, ok && id != uuid.Nil
}

// Auth verifies the Bearer token and stores its user ID in the request context, returning 401 for invalid credentials.
func Auth(verifier TokenVerifier) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			headers := r.Header.Values("Authorization")
			if len(headers) != 1 {
				unauthorized(w)
				return
			}
			parts := strings.Fields(headers[0])
			if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
				unauthorized(w)
				return
			}
			claims, err := verifier.Verify(parts[1])
			if err != nil || claims == nil || claims.UserID == uuid.Nil {
				unauthorized(w)
				return
			}
			ctx := context.WithValue(r.Context(), userIDKey{}, claims.UserID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// unauthorized sends a 401 JSON response with a Bearer authentication challenge.
func unauthorized(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("WWW-Authenticate", "Bearer")
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized"})
}
