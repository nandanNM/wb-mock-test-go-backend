package repository

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// AuditEvent is an authentication event to append to the audit log. UserID and
// SessionID are optional (nil for pre-identity events like a failed login).
type AuditEvent struct {
	EventType string
	UserID    *string
	SessionID *string
	IP        string
	UserAgent string
	RequestID string
	Detail    map[string]any
}

// AuditRecord is a stored audit row, returned by List.
type AuditRecord struct {
	ID         int64          `json:"id"`
	OccurredAt time.Time      `json:"occurred_at"`
	EventType  string         `json:"event_type"`
	UserID     *string        `json:"user_id,omitempty"`
	SessionID  *string        `json:"session_id,omitempty"`
	IP         string         `json:"ip,omitempty"`
	UserAgent  string         `json:"user_agent,omitempty"`
	RequestID  string         `json:"request_id,omitempty"`
	Detail     map[string]any `json:"detail,omitempty"`
}

// AuditRepository provides append-only access to auth_audit_log.
type AuditRepository struct {
	pool *pgxpool.Pool
}

func NewAuditRepository(pool *pgxpool.Pool) *AuditRepository {
	return &AuditRepository{pool: pool}
}

// Log appends an audit event. Errors are returned so callers can log them, but
// audit failures must never block the auth flow (callers log-and-continue).
func (r *AuditRepository) Log(ctx context.Context, e AuditEvent) error {
	detail, _ := json.Marshal(e.Detail)
	if len(detail) == 0 {
		detail = []byte("{}")
	}
	const q = `
		INSERT INTO auth_audit_log (event_type, user_id, session_id, ip, user_agent, request_id, detail)
		VALUES ($1, $2::uuid, $3::uuid, NULLIF($4,'')::inet, $5, $6, $7)`
	_, err := r.pool.Exec(ctx, q,
		e.EventType, nilIfEmpty(e.UserID), nilIfEmpty(e.SessionID),
		e.IP, e.UserAgent, e.RequestID, detail)
	return err
}

// List returns recent audit records, optionally filtered by user.
func (r *AuditRepository) List(ctx context.Context, userID string, limit int) ([]AuditRecord, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	q := `SELECT id, occurred_at, event_type, user_id::text, session_id::text,
	             COALESCE(host(ip),''), COALESCE(user_agent,''), COALESCE(request_id,''), detail
	      FROM auth_audit_log`
	args := []any{}
	if userID != "" {
		q += ` WHERE user_id = $1::uuid`
		args = append(args, userID)
	}
	q += ` ORDER BY occurred_at DESC LIMIT $` + strconv.Itoa(len(args)+1)
	args = append(args, limit)

	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]AuditRecord, 0)
	for rows.Next() {
		var rec AuditRecord
		var detail []byte
		if err := rows.Scan(&rec.ID, &rec.OccurredAt, &rec.EventType, &rec.UserID, &rec.SessionID,
			&rec.IP, &rec.UserAgent, &rec.RequestID, &detail); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(detail, &rec.Detail)
		out = append(out, rec)
	}
	return out, rows.Err()
}

func nilIfEmpty(s *string) any {
	if s == nil || *s == "" {
		return nil
	}
	return *s
}
