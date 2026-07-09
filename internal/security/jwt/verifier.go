package jwt

import (
	"crypto/rsa"
	"fmt"
	"log/slog"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

type Claims struct {
	UserID uuid.UUID `json:"uid"`
	jwt.RegisteredClaims
}
type Verifier struct {
	publicKey *rsa.PublicKey
	kid       string
	logger    *slog.Logger
}

func NewVerifier(publicKey *rsa.PublicKey, kid string, logger *slog.Logger) *Verifier {
	return &Verifier{
		publicKey: publicKey,
		kid:       kid,
		logger:    logger,
	}
}

func (v *Verifier) Verify(tokenString string) (*Claims, error) {
	v.logger.Info("Starting verifying token...")
	token, err := jwt.ParseWithClaims(
		tokenString,
		&Claims{},
		func(token *jwt.Token) (any, error) {
			if token.Method.Alg() != "RS256" {
				return nil, fmt.Errorf("unexpected jwt signing method: %v", token.Header["alg"])
			}
			kid, ok := token.Header["kid"].(string)
			if !ok {
				return nil, fmt.Errorf("missing token kid")
			}
			if kid != v.kid {
				return nil, fmt.Errorf("unknown token kid: %s", kid)
			}
			return v.publicKey, nil
		})
	if err != nil {
		return nil, fmt.Errorf("cant parse token: %w", err)
	}

	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("invalid token")
	}
	if claims.UserID == uuid.Nil {
		return nil, fmt.Errorf("empty user id")
	}

	v.logger.Info("Token verified successfully")
	return claims, nil
}
