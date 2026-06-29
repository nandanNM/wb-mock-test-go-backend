// Package auth implements the authentication system: token issuance/verification,
// the OAuth/OIDC login pipeline, session lifecycle, and the account-status cache.
//
// Access tokens are short-lived EdDSA (Ed25519) JWTs. Refresh tokens are opaque
// random strings; only their SHA-256 hash is ever persisted.
package auth

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"backend/internal/config"

	"github.com/golang-jwt/jwt/v5"
)

// Token-related errors surfaced to the transport layer.
var (
	ErrAccessTokenExpired = errors.New("access token expired")
	ErrAccessTokenInvalid = errors.New("access token invalid")
)

// AccessClaims is the payload of an access-token JWT. Roles (not the expanded
// permission set) are embedded; permissions are derived from roles by the RBAC
// layer at request time.
type AccessClaims struct {
	jwt.RegisteredClaims
	SessionID string   `json:"sid"`
	Roles     []string `json:"roles"`
}

// TokenService issues and verifies access tokens and mints refresh tokens.
type TokenService struct {
	priv      ed25519.PrivateKey
	pub       ed25519.PublicKey
	keyID     string
	issuer    string
	audience  string
	accessTTL time.Duration
}

// NewTokenService builds a TokenService from config. If no private key is
// configured it generates an ephemeral one (dev only) and logs a warning —
// tokens won't survive a restart in that mode.
func NewTokenService(cfg config.AuthConfig, log *slog.Logger) (*TokenService, error) {
	var priv ed25519.PrivateKey

	if cfg.JWTPrivateKeyB64 == "" {
		_, generated, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return nil, fmt.Errorf("generate ephemeral signing key: %w", err)
		}
		priv = generated
		log.Warn("AUTH_JWT_PRIVATE_KEY not set — generated an ephemeral signing key; tokens will be invalid after restart. Set AUTH_JWT_PRIVATE_KEY in production.")
	} else {
		raw, err := base64.StdEncoding.DecodeString(cfg.JWTPrivateKeyB64)
		if err != nil {
			return nil, fmt.Errorf("decode AUTH_JWT_PRIVATE_KEY (expected base64): %w", err)
		}
		if len(raw) != ed25519.PrivateKeySize {
			return nil, fmt.Errorf("AUTH_JWT_PRIVATE_KEY must be a %d-byte ed25519 key, got %d bytes", ed25519.PrivateKeySize, len(raw))
		}
		priv = ed25519.PrivateKey(raw)
	}

	return &TokenService{
		priv:      priv,
		pub:       priv.Public().(ed25519.PublicKey),
		keyID:     cfg.JWTKeyID,
		issuer:    cfg.JWTIssuer,
		audience:  cfg.JWTAudience,
		accessTTL: cfg.AccessTokenTTL,
	}, nil
}

// IssueAccessToken signs a short-lived access token for a (user, session) with
// the given roles. Returns the signed token and its expiry.
func (s *TokenService) IssueAccessToken(userID, sessionID string, roles []string) (string, time.Time, error) {
	now := time.Now()
	exp := now.Add(s.accessTTL)

	claims := AccessClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    s.issuer,
			Subject:   userID,
			Audience:  jwt.ClaimStrings{s.audience},
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(exp),
			ID:        randHex(16),
		},
		SessionID: sessionID,
		Roles:     roles,
	}

	tok := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	tok.Header["kid"] = s.keyID

	signed, err := tok.SignedString(s.priv)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("sign access token: %w", err)
	}
	return signed, exp, nil
}

// ParseAccessToken verifies signature, issuer, audience and expiry, returning
// the claims. Expiry is reported distinctly so the client knows to refresh.
func (s *TokenService) ParseAccessToken(raw string) (*AccessClaims, error) {
	claims := &AccessClaims{}
	_, err := jwt.ParseWithClaims(raw, claims, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodEd25519); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return s.pub, nil
	},
		jwt.WithValidMethods([]string{"EdDSA"}),
		jwt.WithIssuer(s.issuer),
		jwt.WithAudience(s.audience),
	)
	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return nil, ErrAccessTokenExpired
		}
		return nil, fmt.Errorf("%w: %v", ErrAccessTokenInvalid, err)
	}
	return claims, nil
}

// AccessTTL exposes the configured access-token lifetime.
func (s *TokenService) AccessTTL() time.Duration { return s.accessTTL }

// GenerateRefreshToken returns a new opaque refresh token and its SHA-256 hash.
// The raw token is sent to the client exactly once; only the hash is stored.
func GenerateRefreshToken() (raw, hash string, err error) {
	b := make([]byte, 32) // 256 bits
	if _, err := rand.Read(b); err != nil {
		return "", "", fmt.Errorf("generate refresh token: %w", err)
	}
	raw = base64.RawURLEncoding.EncodeToString(b)
	return raw, HashToken(raw), nil
}

// HashToken returns the hex SHA-256 of a token. Used to store/compare refresh
// tokens without ever persisting the raw value.
func HashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// ConstantTimeEqual compares two token hashes without leaking timing.
func ConstantTimeEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func randHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return ""
	}
	return hex.EncodeToString(b)
}
