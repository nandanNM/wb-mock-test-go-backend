package auth

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"backend/internal/config"
)

func testTokenService(t *testing.T, ttl time.Duration) *TokenService {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc, err := NewTokenService(config.AuthConfig{
		JWTKeyID:       "test",
		JWTIssuer:      "backend",
		JWTAudience:    "backend-api",
		AccessTokenTTL: ttl,
	}, log)
	if err != nil {
		t.Fatalf("NewTokenService: %v", err)
	}
	return svc
}

func TestAccessTokenRoundTrip(t *testing.T) {
	svc := testTokenService(t, 10*time.Minute)

	tok, exp, err := svc.IssueAccessToken("user-1", "sess-1", []string{"user", "admin"})
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if !exp.After(time.Now()) {
		t.Fatal("expiry should be in the future")
	}

	claims, err := svc.ParseAccessToken(tok)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if claims.Subject != "user-1" {
		t.Errorf("subject: got %q", claims.Subject)
	}
	if claims.SessionID != "sess-1" {
		t.Errorf("sid: got %q", claims.SessionID)
	}
	if len(claims.Roles) != 2 || claims.Roles[0] != "user" {
		t.Errorf("roles: got %v", claims.Roles)
	}
}

func TestAccessTokenExpired(t *testing.T) {
	svc := testTokenService(t, -time.Hour) // already expired

	tok, _, err := svc.IssueAccessToken("user-1", "sess-1", nil)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	_, err = svc.ParseAccessToken(tok)
	if !errors.Is(err, ErrAccessTokenExpired) {
		t.Fatalf("expected ErrAccessTokenExpired, got %v", err)
	}
}

func TestAccessTokenWrongKeyRejected(t *testing.T) {
	issuer := testTokenService(t, time.Minute)
	other := testTokenService(t, time.Minute) // different ephemeral key

	tok, _, _ := issuer.IssueAccessToken("user-1", "sess-1", nil)
	if _, err := other.ParseAccessToken(tok); !errors.Is(err, ErrAccessTokenInvalid) {
		t.Fatalf("expected ErrAccessTokenInvalid for wrong key, got %v", err)
	}
}

func TestRefreshTokenHashing(t *testing.T) {
	raw, hash, err := GenerateRefreshToken()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if raw == hash {
		t.Fatal("raw token must not equal its hash")
	}
	if HashToken(raw) != hash {
		t.Fatal("HashToken must be deterministic and match GenerateRefreshToken")
	}
	if !ConstantTimeEqual(hash, HashToken(raw)) {
		t.Fatal("ConstantTimeEqual should match identical hashes")
	}
	raw2, _, _ := GenerateRefreshToken()
	if raw == raw2 {
		t.Fatal("refresh tokens must be unique")
	}
}

func TestMemStatusCache(t *testing.T) {
	calls := 0
	resolve := func(_ context.Context, _, _ string) (AccountStatus, error) {
		calls++
		return AccountStatus{UserStatus: "active"}, nil
	}
	c := NewMemStatusCache(time.Minute, resolve)

	for i := 0; i < 3; i++ {
		if _, err := c.Get(context.Background(), "u1", "s1"); err != nil {
			t.Fatalf("get: %v", err)
		}
	}
	if calls != 1 {
		t.Fatalf("resolver should be called once (cached), got %d", calls)
	}

	c.InvalidateSession("s1")
	if _, err := c.Get(context.Background(), "u1", "s1"); err != nil {
		t.Fatalf("get after invalidate: %v", err)
	}
	if calls != 2 {
		t.Fatalf("resolver should re-run after invalidation, got %d", calls)
	}
}
