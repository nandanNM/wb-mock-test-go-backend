package repository

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// OAuthFlow is the short-lived state of an in-progress OAuth login.
type OAuthFlow struct {
	State        string
	Provider     string
	CodeVerifier string
	RedirectURI  string
	ClientType   string
}

// OAuthFlowRepository stores OAuth state + PKCE verifier between /start and
// /callback.
type OAuthFlowRepository struct {
	pool *pgxpool.Pool
}

func NewOAuthFlowRepository(pool *pgxpool.Pool) *OAuthFlowRepository {
	return &OAuthFlowRepository{pool: pool}
}

// Create persists a login flow with an expiry.
func (r *OAuthFlowRepository) Create(ctx context.Context, f OAuthFlow, expiresAt time.Time) error {
	const q = `
		INSERT INTO oauth_login_flow (state, provider, code_verifier, redirect_uri, client_type, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6)`
	_, err := r.pool.Exec(ctx, q, f.State, f.Provider, f.CodeVerifier, f.RedirectURI, f.ClientType, expiresAt)
	return err
}

// Consume atomically fetches and deletes a non-expired flow by state. Returns
// ErrNotFound if the state is unknown or expired (single-use, anti-replay).
func (r *OAuthFlowRepository) Consume(ctx context.Context, state string) (OAuthFlow, error) {
	const q = `
		DELETE FROM oauth_login_flow
		WHERE state = $1 AND expires_at > now()
		RETURNING state, provider, code_verifier, COALESCE(redirect_uri,''), COALESCE(client_type,'')`
	var f OAuthFlow
	err := r.pool.QueryRow(ctx, q, state).
		Scan(&f.State, &f.Provider, &f.CodeVerifier, &f.RedirectURI, &f.ClientType)
	if errors.Is(err, pgx.ErrNoRows) {
		return OAuthFlow{}, ErrNotFound
	}
	return f, err
}

// DeleteExpired sweeps expired flows. Safe to call periodically.
func (r *OAuthFlowRepository) DeleteExpired(ctx context.Context) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM oauth_login_flow WHERE expires_at <= now()`)
	return err
}
