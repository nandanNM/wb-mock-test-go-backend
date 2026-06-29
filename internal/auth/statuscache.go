package auth

import (
	"context"
	"sync"
	"time"
)

// AccountStatus is the freshness-sensitive state checked on every authenticated
// request: the user's account status and whether this specific session was
// revoked. It lets ban/suspend/logout take effect within a short cache TTL
// without a database round-trip per request.
type AccountStatus struct {
	UserStatus     string // "active" | "suspended" | "banned"
	SessionRevoked bool
}

// Active reports whether the account is usable and the session is live.
func (s AccountStatus) Active() bool {
	return s.UserStatus == "active" && !s.SessionRevoked
}

// StatusResolver loads authoritative status from the source of truth (the DB).
type StatusResolver func(ctx context.Context, userID, sessionID string) (AccountStatus, error)

// StatusCache caches AccountStatus with a short TTL and supports explicit
// invalidation. Implementations must be safe for concurrent use. The in-memory
// implementation here is the single-instance default; a Redis-backed
// implementation can satisfy the same interface for multi-instance deployments.
type StatusCache interface {
	Get(ctx context.Context, userID, sessionID string) (AccountStatus, error)
	InvalidateUser(userID string)
	InvalidateSession(sessionID string)
}

type cacheEntry struct {
	userID string
	status AccountStatus
	expff  time.Time
}

// MemStatusCache is an in-process TTL cache keyed by session ID.
type MemStatusCache struct {
	ttl     time.Duration
	resolve StatusResolver
	mu      sync.Mutex
	entries map[string]cacheEntry // sessionID -> entry
}

// NewMemStatusCache builds an in-memory status cache. resolve is called on a
// miss or expiry to load authoritative status.
func NewMemStatusCache(ttl time.Duration, resolve StatusResolver) *MemStatusCache {
	return &MemStatusCache{
		ttl:     ttl,
		resolve: resolve,
		entries: make(map[string]cacheEntry),
	}
}

// Get returns cached status if fresh, otherwise resolves and caches it.
func (c *MemStatusCache) Get(ctx context.Context, userID, sessionID string) (AccountStatus, error) {
	now := time.Now()

	c.mu.Lock()
	if e, ok := c.entries[sessionID]; ok && now.Before(e.expff) {
		c.mu.Unlock()
		return e.status, nil
	}
	c.mu.Unlock()

	// Resolve outside the lock to avoid serializing DB calls.
	status, err := c.resolve(ctx, userID, sessionID)
	if err != nil {
		return AccountStatus{}, err
	}

	c.mu.Lock()
	c.entries[sessionID] = cacheEntry{userID: userID, status: status, expff: now.Add(c.ttl)}
	c.mu.Unlock()
	return status, nil
}

// InvalidateUser drops all cached sessions for a user (e.g. on ban/suspend).
func (c *MemStatusCache) InvalidateUser(userID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for sid, e := range c.entries {
		if e.userID == userID {
			delete(c.entries, sid)
		}
	}
}

// InvalidateSession drops a single cached session (e.g. on logout/revoke).
func (c *MemStatusCache) InvalidateSession(sessionID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, sessionID)
}
