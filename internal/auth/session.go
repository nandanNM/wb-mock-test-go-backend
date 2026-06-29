package auth

import (
	"context"
	"time"

	"backend/internal/audit"
	"backend/internal/repository"
)

// ReqMeta carries request metadata for audit records.
type ReqMeta struct {
	IP        string
	UserAgent string
	RequestID string
}

// Refresh rotates a refresh token. It generates the replacement up front and
// hands its hash to the repository's atomic compare-and-swap, then acts on the
// classified outcome:
//   - OK     -> re-check account status (strong-consistency point) + issue new tokens
//   - Benign -> concurrent refresh within grace -> ErrRefreshInProgress (client retries)
//   - Reuse  -> revoke the session + audit + ErrRefreshReuse
//   - Invalid-> ErrRefreshInvalid
func (s *Service) Refresh(ctx context.Context, rawRefresh string, dev DeviceInfo, meta ReqMeta) (Tokens, error) {
	presented := HashToken(rawRefresh)
	newRaw, newHash, err := GenerateRefreshToken()
	if err != nil {
		return Tokens{}, err
	}
	expiry := time.Now().Add(s.refreshTTL)

	res, err := s.sessions.Rotate(ctx, presented, newHash, expiry)
	if err != nil {
		return Tokens{}, err
	}

	switch res.Status {
	case repository.RotateOK:
		// Strong consistency: re-read account status and roles fresh.
		user, err := s.users.GetByID(ctx, res.UserID)
		if err != nil {
			return Tokens{}, err
		}
		if user.Status != "active" {
			_ = s.sessions.Revoke(ctx, res.SessionID, "", "account_not_active")
			s.cache.InvalidateSession(res.SessionID)
			return Tokens{}, ErrAccountNotActive
		}

		roles, err := s.roles.RolesForUser(ctx, res.UserID)
		if err != nil {
			return Tokens{}, err
		}
		access, accessExp, err := s.tokens.IssueAccessToken(res.UserID, res.SessionID, roles)
		if err != nil {
			return Tokens{}, err
		}

		// The session's status may differ from any cached copy; refresh it.
		s.cache.InvalidateSession(res.SessionID)

		s.audit.Record(ctx, repository.AuditEvent{
			EventType: audit.EventTokenRefresh,
			UserID:    &res.UserID, SessionID: &res.SessionID,
			IP: meta.IP, UserAgent: meta.UserAgent, RequestID: meta.RequestID,
		})

		return Tokens{
			AccessToken: access, AccessExpiry: accessExp,
			RefreshToken: newRaw, RefreshExpiry: expiry,
			SessionID: res.SessionID, Roles: roles,
		}, nil

	case repository.RotateBenign:
		return Tokens{}, ErrRefreshInProgress

	case repository.RotateReuse:
		_ = s.sessions.Revoke(ctx, res.SessionID, "", "token_reuse_detected")
		s.cache.InvalidateSession(res.SessionID)
		s.audit.Record(ctx, repository.AuditEvent{
			EventType: audit.EventTokenReuse,
			UserID:    &res.UserID, SessionID: &res.SessionID,
			IP: meta.IP, UserAgent: meta.UserAgent, RequestID: meta.RequestID,
		})
		s.log.Warn("refresh token reuse detected; session revoked",
			"session_id", res.SessionID, "user_id", res.UserID)
		return Tokens{}, ErrRefreshReuse

	default: // RotateInvalid
		return Tokens{}, ErrRefreshInvalid
	}
}

// Logout revokes the caller's current session.
func (s *Service) Logout(ctx context.Context, sessionID, userID string, meta ReqMeta) error {
	err := s.sessions.Revoke(ctx, sessionID, userID, "logout")
	s.cache.InvalidateSession(sessionID)
	if err == nil {
		s.audit.Record(ctx, repository.AuditEvent{
			EventType: audit.EventLogout,
			UserID:    &userID, SessionID: &sessionID,
			IP: meta.IP, UserAgent: meta.UserAgent, RequestID: meta.RequestID,
		})
	}
	return err
}

// LogoutAll revokes all of the user's sessions and returns the count.
func (s *Service) LogoutAll(ctx context.Context, userID string, meta ReqMeta) (int64, error) {
	n, err := s.sessions.RevokeAll(ctx, userID, "", "logout_all")
	s.cache.InvalidateUser(userID)
	if err == nil {
		s.audit.Record(ctx, repository.AuditEvent{
			EventType: audit.EventLogoutAll,
			UserID:    &userID,
			IP:        meta.IP, UserAgent: meta.UserAgent, RequestID: meta.RequestID,
			Detail: map[string]any{"revoked_count": n},
		})
	}
	return n, err
}

// ListSessions returns the user's active devices, flagging the current one.
func (s *Service) ListSessions(ctx context.Context, userID, currentSessionID string) ([]repository.Session, error) {
	return s.sessions.ListActive(ctx, userID, currentSessionID)
}

// RevokeSession revokes one of the user's sessions by id (device sign-out).
func (s *Service) RevokeSession(ctx context.Context, sessionID, userID string, meta ReqMeta) error {
	err := s.sessions.Revoke(ctx, sessionID, userID, "device_signout")
	s.cache.InvalidateSession(sessionID)
	if err == nil {
		s.audit.Record(ctx, repository.AuditEvent{
			EventType: audit.EventSessionRevoked,
			UserID:    &userID, SessionID: &sessionID,
			IP: meta.IP, UserAgent: meta.UserAgent, RequestID: meta.RequestID,
		})
	}
	return err
}
