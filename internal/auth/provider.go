package auth

import "context"

// ExternalIdentity is the normalized result of verifying a credential from any
// identity provider. It is the single shape the login pipeline consumes, which
// is what lets new providers (phone, OTP, TOTP) plug in without touching session,
// token, RBAC, or audit code.
type ExternalIdentity struct {
	Provider      string
	Subject       string // stable provider-specific id (Google `sub`, phone number, ...)
	Email         string
	EmailVerified bool
	Name          string
	Raw           map[string]any // non-secret profile data
}

// OAuthProvider is the seam for redirect-based (OAuth2/OIDC) providers. Google
// implements it today; additional OAuth providers implement the same interface.
//
// Non-redirect factors (phone OTP, TOTP) will use a sibling interface that also
// yields an ExternalIdentity, funneling into the same login pipeline.
type OAuthProvider interface {
	Name() string
	// AuthCodeURL builds the provider consent URL for an Authorization Code +
	// PKCE flow.
	AuthCodeURL(state, codeChallenge string) string
	// Exchange swaps an authorization code (with the PKCE verifier) for a
	// verified, normalized identity.
	Exchange(ctx context.Context, code, codeVerifier string) (ExternalIdentity, error)
}
