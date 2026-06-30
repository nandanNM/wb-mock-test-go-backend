package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"

	"backend/internal/config"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// googleIssuer is Google's OpenID Connect issuer URL.
const googleIssuer = "https://accounts.google.com"

// GoogleProvider implements OAuthProvider using Google's OIDC endpoints. It
// verifies the returned id_token's signature against Google's JWKS.
type GoogleProvider struct {
	oauth2Config *oauth2.Config
	verifier     *oidc.IDTokenVerifier
}

// NewGoogleProvider discovers Google's OIDC config and builds the provider.
// Returns nil, nil when Google is not configured (so the server can run without
// it in dev).
func NewGoogleProvider(ctx context.Context, cfg config.AuthConfig) (*GoogleProvider, error) {
	if !cfg.GoogleConfigured() {
		return nil, nil
	}
	provider, err := oidc.NewProvider(ctx, googleIssuer)
	if err != nil {
		return nil, fmt.Errorf("discover Google OIDC: %w", err)
	}
	return &GoogleProvider{
		oauth2Config: &oauth2.Config{
			ClientID:     cfg.GoogleClientID,
			ClientSecret: cfg.GoogleClientSecret,
			RedirectURL:  cfg.GoogleRedirectURL,
			Endpoint:     provider.Endpoint(),
			Scopes:       []string{oidc.ScopeOpenID, "email", "profile"},
		},
		verifier: provider.Verifier(&oidc.Config{ClientID: cfg.GoogleClientID}),
	}, nil
}

// Name implements OAuthProvider.
func (g *GoogleProvider) Name() string { return "google" }

// AuthCodeURL builds the Google consent URL with PKCE (S256).
func (g *GoogleProvider) AuthCodeURL(state, codeChallenge string) string {
	return g.oauth2Config.AuthCodeURL(state,
		oauth2.SetAuthURLParam("code_challenge", codeChallenge),
		oauth2.SetAuthURLParam("code_challenge_method", "S256"),
	)
}

// Exchange swaps the code for tokens, verifies the id_token, and extracts a
// normalized identity.
func (g *GoogleProvider) Exchange(ctx context.Context, code, codeVerifier string) (ExternalIdentity, error) {
	tok, err := g.oauth2Config.Exchange(ctx, code,
		oauth2.SetAuthURLParam("code_verifier", codeVerifier))
	if err != nil {
		return ExternalIdentity{}, fmt.Errorf("exchange code: %w", err)
	}

	rawID, ok := tok.Extra("id_token").(string)
	if !ok || rawID == "" {
		return ExternalIdentity{}, fmt.Errorf("no id_token in token response")
	}

	return g.VerifyIDToken(ctx, rawID)
}

// VerifyIDToken verifies a Google-issued ID token (signature, issuer, audience,
// expiry) and maps it to a normalized identity. Used by the native mobile flow,
// where the app obtains the ID token from the Google Sign-In SDK directly.
//
// The mobile SDK must be configured with this app's WEB client ID as the
// audience (e.g. `webClientId` in @react-native-google-signin), so the token's
// `aud` matches the verifier's configured client ID.
func (g *GoogleProvider) VerifyIDToken(ctx context.Context, rawID string) (ExternalIdentity, error) {
	idToken, err := g.verifier.Verify(ctx, rawID)
	if err != nil {
		return ExternalIdentity{}, fmt.Errorf("verify id_token: %w", err)
	}

	var claims struct {
		Sub           string `json:"sub"`
		Email         string `json:"email"`
		EmailVerified bool   `json:"email_verified"`
		Name          string `json:"name"`
		Picture       string `json:"picture"`
	}
	if err := idToken.Claims(&claims); err != nil {
		return ExternalIdentity{}, fmt.Errorf("parse id_token claims: %w", err)
	}
	if claims.Sub == "" {
		return ExternalIdentity{}, fmt.Errorf("id_token missing subject")
	}

	return ExternalIdentity{
		Provider:      "google",
		Subject:       claims.Sub,
		Email:         claims.Email,
		EmailVerified: claims.EmailVerified,
		Name:          claims.Name,
		Raw:           map[string]any{"picture": claims.Picture},
	}, nil
}

// GeneratePKCE returns a high-entropy code_verifier and its S256 code_challenge.
func GeneratePKCE() (verifier, challenge string) {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	verifier = base64.RawURLEncoding.EncodeToString(b)
	sum := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(sum[:])
	return verifier, challenge
}
