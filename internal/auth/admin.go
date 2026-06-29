package auth

import (
	"context"
	"fmt"

	"backend/internal/audit"
	"backend/internal/repository"
)

// statusEvent maps an account status to its audit event type.
var statusEvent = map[string]string{
	"banned":    audit.EventAccountBanned,
	"suspended": audit.EventAccountSuspended,
	"active":    audit.EventAccountReinstated,
}

// SetUserStatus changes a user's account status (ban/suspend/reinstate). For a
// non-active status it also revokes all of the user's sessions. It invalidates
// the status cache so the change takes effect immediately, and audits the action.
func (s *Service) SetUserStatus(ctx context.Context, actorID, targetID, status, reason string, meta ReqMeta) error {
	event, ok := statusEvent[status]
	if !ok {
		return fmt.Errorf("invalid status %q", status)
	}

	if err := s.users.SetStatus(ctx, targetID, status, reason); err != nil {
		return err
	}
	if status != "active" {
		if _, err := s.sessions.RevokeAll(ctx, targetID, "", "account_"+status); err != nil {
			return err
		}
	}
	s.cache.InvalidateUser(targetID)

	s.audit.Record(ctx, repository.AuditEvent{
		EventType: event,
		UserID:    &targetID,
		IP:        meta.IP, UserAgent: meta.UserAgent, RequestID: meta.RequestID,
		Detail: map[string]any{"actor_id": actorID, "reason": reason},
	})
	return nil
}

// GrantRole assigns a role to a user and audits it. Bumps perm_version is left
// to a future enhancement; for now role changes take effect within the access
// token TTL (or immediately on refresh).
func (s *Service) GrantRole(ctx context.Context, actorID, targetID, role string, meta ReqMeta) error {
	if err := s.roles.AssignRole(ctx, targetID, role, actorID); err != nil {
		return err
	}
	s.audit.Record(ctx, repository.AuditEvent{
		EventType: audit.EventRoleGranted,
		UserID:    &targetID,
		IP:        meta.IP, UserAgent: meta.UserAgent, RequestID: meta.RequestID,
		Detail: map[string]any{"actor_id": actorID, "role": role},
	})
	return nil
}

// RevokeRole removes a role from a user and audits it.
func (s *Service) RevokeRole(ctx context.Context, actorID, targetID, role string, meta ReqMeta) error {
	if err := s.roles.RevokeRole(ctx, targetID, role); err != nil {
		return err
	}
	s.audit.Record(ctx, repository.AuditEvent{
		EventType: audit.EventRoleRevoked,
		UserID:    &targetID,
		IP:        meta.IP, UserAgent: meta.UserAgent, RequestID: meta.RequestID,
		Detail: map[string]any{"actor_id": actorID, "role": role},
	})
	return nil
}

// InvalidateUserCache drops cached status for a user so a change (e.g. hard
// delete) takes effect immediately rather than after the cache TTL.
func (s *Service) InvalidateUserCache(userID string) { s.cache.InvalidateUser(userID) }

// ListAudit returns recent audit records, optionally filtered by user.
func (s *Service) ListAudit(ctx context.Context, userID string, limit int) ([]repository.AuditRecord, error) {
	return s.audit.List(ctx, userID, limit)
}

// PermissionsForRoles expands role names to permission names (used by /v1/me).
func (s *Service) PermissionsForRoles(ctx context.Context, roles []string) ([]string, error) {
	return s.roles.PermissionsForRoles(ctx, roles)
}

// UserByID fetches a user (used by /v1/me).
func (s *Service) UserByID(ctx context.Context, id string) (repository.User, error) {
	return s.users.GetByID(ctx, id)
}
