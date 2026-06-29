package repository

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// reuseGrace is the window after a rotation during which presenting the
// just-rotated-out token is treated as a benign concurrent refresh rather than
// a breach.
const reuseGrace = 10 * time.Second

// Session is a logged-in device. Refresh token material is never exposed.
type Session struct {
	ID          string     `json:"id"`
	UserID      string     `json:"-"`
	UserAgent   string     `json:"user_agent"`
	IP          string     `json:"ip"`
	DeviceLabel string     `json:"device_label"`
	CreatedAt   time.Time  `json:"created_at"`
	LastUsedAt  time.Time  `json:"last_used_at"`
	ExpiresAt   time.Time  `json:"expires_at"`
	RevokedAt   *time.Time `json:"revoked_at,omitempty"`
	Current     bool       `json:"current"`
}

// CreateSessionParams holds the inputs for a new session.
type CreateSessionParams struct {
	UserID           string
	RefreshTokenHash string
	UserAgent        string
	IP               string // empty string stored as NULL
	DeviceLabel      string
	ExpiresAt        time.Time
}

// RotateStatus is the outcome of attempting a refresh-token rotation.
type RotateStatus int

const (
	RotateInvalid RotateStatus = iota // token matched no session
	RotateOK                          // rotated successfully
	RotateBenign                      // concurrent refresh within grace; do not revoke
	RotateReuse                       // reuse of an old/revoked token; caller must revoke
)

// RotateResult carries the rotation outcome plus the affected session identity.
type RotateResult struct {
	Status    RotateStatus
	SessionID string
	UserID    string
}

// SessionRepository provides access to the sessions table.
type SessionRepository struct {
	pool *pgxpool.Pool
}

func NewSessionRepository(pool *pgxpool.Pool) *SessionRepository {
	return &SessionRepository{pool: pool}
}

// Create inserts a new session and returns its id.
func (r *SessionRepository) Create(ctx context.Context, p CreateSessionParams) (string, error) {
	const q = `
		INSERT INTO sessions (user_id, refresh_token_hash, user_agent, ip, device_label, expires_at)
		VALUES ($1, $2, $3, NULLIF($4,'')::inet, $5, $6)
		RETURNING id`
	var id string
	err := r.pool.QueryRow(ctx, q,
		p.UserID, p.RefreshTokenHash, p.UserAgent, p.IP, p.DeviceLabel, p.ExpiresAt,
	).Scan(&id)
	return id, err
}

// Rotate performs an atomic compare-and-swap of the refresh token hash and
// classifies the outcome (see RotateStatus). It never revokes — on RotateReuse
// the caller decides and calls Revoke, so it can also emit an audit event.
func (r *SessionRepository) Rotate(ctx context.Context, presentedHash, newHash string, newExpiry time.Time) (RotateResult, error) {
	// 1. Atomic CAS: exactly one concurrent caller wins the UPDATE.
	const casQ = `
		UPDATE sessions
		   SET prev_refresh_token_hash = refresh_token_hash,
		       refresh_token_hash      = $2,
		       token_generation        = token_generation + 1,
		       rotated_at  = now(),
		       last_used_at = now(),
		       expires_at  = $3
		 WHERE refresh_token_hash = $1
		   AND revoked_at IS NULL
		   AND expires_at > now()
		RETURNING id, user_id`

	var res RotateResult
	err := r.pool.QueryRow(ctx, casQ, presentedHash, newHash, newExpiry).Scan(&res.SessionID, &res.UserID)
	if err == nil {
		res.Status = RotateOK
		return res, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return RotateResult{}, err
	}

	// 2. CAS missed. Was this the just-rotated-out token (benign) or a reuse?
	const prevQ = `
		SELECT id, user_id, rotated_at, (revoked_at IS NOT NULL) AS revoked
		FROM sessions WHERE prev_refresh_token_hash = $1`

	var (
		rotatedAt *time.Time
		revoked   bool
	)
	err = r.pool.QueryRow(ctx, prevQ, presentedHash).Scan(&res.SessionID, &res.UserID, &rotatedAt, &revoked)
	if errors.Is(err, pgx.ErrNoRows) {
		return RotateResult{Status: RotateInvalid}, nil
	}
	if err != nil {
		return RotateResult{}, err
	}

	if !revoked && rotatedAt != nil && time.Since(*rotatedAt) < reuseGrace {
		res.Status = RotateBenign
		return res, nil
	}
	res.Status = RotateReuse
	return res, nil
}

// ListActive returns a user's non-revoked, unexpired sessions, newest-used first.
// currentSessionID is flagged on the matching row.
func (r *SessionRepository) ListActive(ctx context.Context, userID, currentSessionID string) ([]Session, error) {
	const q = `
		SELECT id, user_id, COALESCE(user_agent,''), COALESCE(host(ip),''), COALESCE(device_label,''),
		       created_at, last_used_at, expires_at, revoked_at
		FROM sessions
		WHERE user_id = $1 AND revoked_at IS NULL AND expires_at > now()
		ORDER BY last_used_at DESC`

	rows, err := r.pool.Query(ctx, q, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	sessions := make([]Session, 0)
	for rows.Next() {
		var s Session
		if err := rows.Scan(&s.ID, &s.UserID, &s.UserAgent, &s.IP, &s.DeviceLabel,
			&s.CreatedAt, &s.LastUsedAt, &s.ExpiresAt, &s.RevokedAt); err != nil {
			return nil, err
		}
		s.Current = s.ID == currentSessionID
		sessions = append(sessions, s)
	}
	return sessions, rows.Err()
}

// Revoke marks a single active session revoked. Returns ErrNotFound if the
// session does not exist or is already revoked. ownerID, when non-empty,
// constrains the revoke to that user's own sessions.
func (r *SessionRepository) Revoke(ctx context.Context, sessionID, ownerID, reason string) error {
	q := `UPDATE sessions SET revoked_at = now(), revoked_reason = $2
	      WHERE id = $1 AND revoked_at IS NULL`
	args := []any{sessionID, reason}
	if ownerID != "" {
		q += ` AND user_id = $3`
		args = append(args, ownerID)
	}
	tag, err := r.pool.Exec(ctx, q, args...)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// RevokeAll revokes all of a user's active sessions and returns the count.
// If exceptID is non-empty, that session is preserved (e.g. "log out everywhere
// except this device").
func (r *SessionRepository) RevokeAll(ctx context.Context, userID, exceptID, reason string) (int64, error) {
	q := `UPDATE sessions SET revoked_at = now(), revoked_reason = $2
	      WHERE user_id = $1 AND revoked_at IS NULL`
	args := []any{userID, reason}
	if exceptID != "" {
		q += ` AND id <> $3`
		args = append(args, exceptID)
	}
	tag, err := r.pool.Exec(ctx, q, args...)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// Status reports the user's account status and whether the session is no longer
// usable (revoked or expired). Used by the status cache resolver. A missing
// session is reported as revoked.
func (r *SessionRepository) Status(ctx context.Context, sessionID string) (userStatus string, revoked bool, err error) {
	const q = `
		SELECT u.status, (s.revoked_at IS NOT NULL OR s.expires_at <= now())
		FROM sessions s JOIN users u ON u.id = s.user_id
		WHERE s.id = $1`
	err = r.pool.QueryRow(ctx, q, sessionID).Scan(&userStatus, &revoked)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", true, nil
	}
	return userStatus, revoked, err
}
