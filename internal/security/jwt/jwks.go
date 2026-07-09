package jwt

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/big"
	"net/http"
)

type jwk struct {
	Kty string `json:"kty"`
	Use string `json:"use"`
	Alg string `json:"alg"`
	Kid string `json:"kid"`
	N   string `json:"n"`
	E   string `json:"e"`
}

type jwks struct {
	Keys []jwk `json:"keys"`
}
type JWKSFetcher struct {
	client *http.Client
	logger *slog.Logger
}

func NewJWKSFetcher(logger *slog.Logger) *JWKSFetcher {
	return &JWKSFetcher{
		client: &http.Client{},
		logger: logger,
	}
}

func (fetcher *JWKSFetcher) Fetch(ctx context.Context, jwksURL string) (*rsa.PublicKey, string, error) {
	fetcher.logger.Info("Starting fetching public key")
	fetcher.logger.Debug("Fetching public key", slog.String("url", jwksURL))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, jwksURL, nil)
	if err != nil {
		return nil, "", fmt.Errorf("failed to create jwks request: %w", err)
	}
	fetcher.logger.Debug("Successfully created new request")

	resp, err := fetcher.client.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("failed to fetch jwks: %w", err)
	}
	fetcher.logger.Debug("Successfully fetched jwks")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("unexpected jwks status: %s", resp.Status)
	}
	fetcher.logger.Debug("Received status code", slog.Int("status_code", resp.StatusCode))

	var set jwks
	if err := json.NewDecoder(resp.Body).Decode(&set); err != nil {
		return nil, "", fmt.Errorf("failed to decode jwks: %w", err)
	}
	fetcher.logger.Debug("JWKS successfully decoded to set")

	if len(set.Keys) == 0 {
		return nil, "", fmt.Errorf("jwks returned no keys")
	}

	key := set.Keys[0]

	if key.Kty != "RSA" {
		return nil, "", fmt.Errorf("unsupported jwk kty: %s", key.Kty)
	}
	if key.Use != "sig" {
		return nil, "", fmt.Errorf("unsupported jwk use: %s", key.Use)
	}
	if key.Alg != "RS256" {
		return nil, "", fmt.Errorf("unsupported jwk alg: %s", key.Alg)
	}
	if key.Kid == "" {
		return nil, "", fmt.Errorf("jwk kid is empty")
	}
	fetcher.logger.Debug("Successfully passed all tests")

	nBytes, err := base64.RawURLEncoding.DecodeString(key.N)
	if err != nil {
		return nil, "", fmt.Errorf("failed to decode jwk n: %w", err)
	}
	eBytes, err := base64.RawURLEncoding.DecodeString(key.E)
	if err != nil {
		return nil, "", fmt.Errorf("failed to decode jwk e: %w", err)
	}
	fetcher.logger.Debug("Successfully decoded jwk N & E")

	n := new(big.Int).SetBytes(nBytes)
	e := int(new(big.Int).SetBytes(eBytes).Int64())

	fetcher.logger.Info("Public key successfully fetched and decoded")
	return &rsa.PublicKey{N: n, E: e}, key.Kid, nil
}
