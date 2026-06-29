package repository

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Identity links a user to an external auth provider (google, phone, ...).
type Identity struct {
	ID              string
	UserID          string
	Provider        string
	ProviderSubject string
	Email           string
	EmailVerified   bool
}

// ProvisionParams describes an authenticated external identity to provision.
type ProvisionParams struct {
	Provider        string
	ProviderSubject string
	Email           string
	Name            string
	EmailVerified   bool
	Data            map[string]any // non-secret provider profile
}

// IdentityRepository provides access to the identities table and the
// find-or-create provisioning flow.
type IdentityRepository struct {
	pool *pgxpool.Pool
}

func NewIdentityRepository(pool *pgxpool.Pool) *IdentityRepository {
	return &IdentityRepository{pool: pool}
}

// FindOrCreateUser resolves an external identity to a user, creating the user
// (and assigning the default "user" role) on first login. Runs in a single
// transaction so provisioning is atomic.
//
// Linking rule: if the identity is new but a user with the same (verified)
// email already exists, the identity is linked to that user — otherwise a new
// user is created. Unverified emails never auto-link (prevents account takeover).
//
// Returns the user and whether it was newly created.
func (r *IdentityRepository) FindOrCreateUser(ctx context.Context, p ProvisionParams) (User, bool, error) {
	var (
		user    User
		created bool
	)

	err := pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
		// 1. Existing identity? -> return its user.
		var userID string
		err := tx.QueryRow(ctx,
			`SELECT user_id FROM identities WHERE provider = $1 AND provider_subject = $2`,
			p.Provider, p.ProviderSubject,
		).Scan(&userID)
		switch {
		case err == nil:
			return scanUser(tx.QueryRow(ctx, `SELECT `+userColumns+` FROM users WHERE id = $1`, userID), &user)
		case !errors.Is(err, pgx.ErrNoRows):
			return err
		}

		// 2. New identity. Link to an existing verified-email user, else create.
		if p.EmailVerified && p.Email != "" {
			e := scanUser(tx.QueryRow(ctx, `SELECT `+userColumns+` FROM users WHERE lower(email) = lower($1)`, p.Email), &user)
			if e == nil {
				userID = user.ID
			} else if !errors.Is(e, pgx.ErrNoRows) {
				return e
			}
		}

		if userID == "" {
			if e := scanUser(tx.QueryRow(ctx,
				`INSERT INTO users (name, email, email_verified) VALUES ($1, $2, $3) RETURNING `+userColumns,
				p.Name, p.Email, p.EmailVerified,
			), &user); e != nil {
				return e
			}
			userID = user.ID
			created = true

			// Assign default role.
			if _, e := tx.Exec(ctx,
				`INSERT INTO user_roles (user_id, role_id)
				 SELECT $1, id FROM roles WHERE name = 'user'
				 ON CONFLICT DO NOTHING`, userID); e != nil {
				return e
			}
		}

		// 3. Insert the identity.
		data, _ := json.Marshal(p.Data)
		if len(data) == 0 {
			data = []byte("{}")
		}
		_, e := tx.Exec(ctx,
			`INSERT INTO identities (user_id, provider, provider_subject, email, email_verified, data)
			 VALUES ($1, $2, $3, $4, $5, $6)`,
			userID, p.Provider, p.ProviderSubject, p.Email, p.EmailVerified, data)
		return e
	})
	if err != nil {
		return User{}, false, err
	}
	return user, created, nil
}
