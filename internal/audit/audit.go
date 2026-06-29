// Package audit records authentication events to an append-only log. Writes are
// best-effort: a failure to record is logged but never blocks the auth flow.
package audit

import (
	"context"
	"log/slog"

	"backend/internal/repository"
)

// Event type constants for the audit log.
const (
	EventLoginSuccess      = "login_success"
	EventLoginFailure      = "login_failure"
	EventTokenRefresh      = "token_refresh"
	EventTokenReuse        = "token_reuse_detected"
	EventLogout            = "logout"
	EventLogoutAll         = "logout_all"
	EventSessionRevoked    = "session_revoked"
	EventAccountBanned     = "account_banned"
	EventAccountSuspended  = "account_suspended"
	EventAccountReinstated = "account_reinstated"
	EventRoleGranted       = "role_granted"
	EventRoleRevoked       = "role_revoked"
)

// Recorder writes audit events.
type Recorder struct {
	repo *repository.AuditRepository
	log  *slog.Logger
}

// New builds a Recorder.
func New(repo *repository.AuditRepository, log *slog.Logger) *Recorder {
	return &Recorder{repo: repo, log: log}
}

// List returns recent audit records, optionally filtered by user.
func (r *Recorder) List(ctx context.Context, userID string, limit int) ([]repository.AuditRecord, error) {
	return r.repo.List(ctx, userID, limit)
}

// Record appends an event, best-effort. A storage error is logged (so it is
// observable) but swallowed so authentication is never blocked by audit I/O.
func (r *Recorder) Record(ctx context.Context, e repository.AuditEvent) {
	if r == nil || r.repo == nil {
		return
	}
	if err := r.repo.Log(ctx, e); err != nil {
		r.log.Error("audit write failed", "event_type", e.EventType, "error", err)
	}
}
