package handler

import (
	"net/http"

	"backend/internal/httpx"
	"backend/internal/repository"
)

// ------------------------------------------------------------------ Users ---

var userSort = map[string]string{"created_at": "created_at", "points": "total_points", "email": "email", "name": "name"}

func (a *API) dashListUsers(w http.ResponseWriter, r *http.Request) error {
	q := parseListQuery(r, userSort, "created_at")
	f := repository.UserFilter{Status: r.URL.Query().Get("status"), Search: q.Search}
	items, total, err := a.users.ListPage(r.Context(), f, q.params())
	if err != nil {
		return httpx.Wrap(httpx.ErrInternal, err)
	}
	writeList(w, q, total, items)
	return nil
}

func (a *API) dashGetUser(w http.ResponseWriter, r *http.Request) error {
	id := r.PathValue("id")
	u, err := a.users.GetByID(r.Context(), id)
	if err != nil {
		return writeRepoError(err, "id", id)
	}
	roles, _ := a.roles.RolesForUser(r.Context(), id)
	httpx.JSON(w, http.StatusOK, map[string]any{"user": u, "roles": roles})
	return nil
}

// deleteUser hard-deletes a user (super-admin only).
func (a *API) deleteUser(w http.ResponseWriter, r *http.Request) error {
	id := r.PathValue("id")
	if err := a.users.Delete(r.Context(), id); err != nil {
		return writeRepoError(err, "id", id)
	}
	a.auth.InvalidateUserCache(id)
	a.auditDash(r, "dashboard.user.deleted", map[string]any{"user_id": id})
	httpx.JSON(w, http.StatusOK, map[string]any{"status": "deleted", "id": id})
	return nil
}

// --------------------------------------------------------------- Sessions ---

var sessionSort = map[string]string{"last_used_at": "last_used_at", "created_at": "created_at"}

func (a *API) dashListSessions(w http.ResponseWriter, r *http.Request) error {
	q := parseListQuery(r, sessionSort, "last_used_at")
	items, total, err := a.sessions.ListAll(r.Context(), r.URL.Query().Get("user_id"), q.params())
	if err != nil {
		return httpx.Wrap(httpx.ErrInternal, err)
	}
	writeList(w, q, total, items)
	return nil
}

func (a *API) dashRevokeSession(w http.ResponseWriter, r *http.Request) error {
	id := r.PathValue("id")
	// userID "" => revoke regardless of owner; the service also invalidates cache.
	if err := a.auth.RevokeSession(r.Context(), id, "", metaFromRequest(r)); err != nil {
		return writeRepoError(err, "id", id)
	}
	a.auditDash(r, "dashboard.session.revoked", map[string]any{"session_id": id})
	httpx.JSON(w, http.StatusOK, map[string]any{"status": "revoked", "id": id})
	return nil
}

// --------------------------------------------------------------- Attempts ---

var attemptSort = map[string]string{"started_at": "started_at", "completed_at": "completed_at", "score": "score", "accuracy": "accuracy"}

func (a *API) listAttempts(w http.ResponseWriter, r *http.Request) error {
	q := parseListQuery(r, attemptSort, "started_at")
	f := repository.AttemptFilter{UserID: r.URL.Query().Get("user_id"), TestID: queryInt64(r, "test_id")}
	items, total, err := a.attempts.List(r.Context(), f, q.params())
	if err != nil {
		return httpx.Wrap(httpx.ErrInternal, err)
	}
	writeList(w, q, total, items)
	return nil
}

func (a *API) getAttempt(w http.ResponseWriter, r *http.Request) error {
	id, err := pathInt64(r, "id")
	if err != nil {
		return err
	}
	at, err := a.attempts.Get(r.Context(), id)
	if err != nil {
		return writeRepoError(err, "id", id)
	}
	httpx.JSON(w, http.StatusOK, at)
	return nil
}

func (a *API) deleteAttempt(w http.ResponseWriter, r *http.Request) error {
	id, err := pathInt64(r, "id")
	if err != nil {
		return err
	}
	if err := a.attempts.Delete(r.Context(), id); err != nil {
		return writeRepoError(err, "id", id)
	}
	a.auditDash(r, "dashboard.attempt.deleted", map[string]any{"attempt_id": id})
	httpx.JSON(w, http.StatusOK, map[string]any{"status": "deleted", "id": id})
	return nil
}

// ---------------------------------------------------------------- Battles ---

var battleSort = map[string]string{"created_at": "created_at", "status": "status"}

func (a *API) listBattles(w http.ResponseWriter, r *http.Request) error {
	q := parseListQuery(r, battleSort, "created_at")
	items, total, err := a.battles.List(r.Context(), r.URL.Query().Get("status"), q.params())
	if err != nil {
		return httpx.Wrap(httpx.ErrInternal, err)
	}
	writeList(w, q, total, items)
	return nil
}

func (a *API) getBattle(w http.ResponseWriter, r *http.Request) error {
	id, err := pathInt64(r, "id")
	if err != nil {
		return err
	}
	b, err := a.battles.Get(r.Context(), id)
	if err != nil {
		return writeRepoError(err, "id", id)
	}
	httpx.JSON(w, http.StatusOK, b)
	return nil
}

func (a *API) finishBattle(w http.ResponseWriter, r *http.Request) error {
	id, err := pathInt64(r, "id")
	if err != nil {
		return err
	}
	if err := a.battles.ForceFinish(r.Context(), id); err != nil {
		return writeRepoError(err, "id", id)
	}
	a.auditDash(r, "dashboard.battle.finished", map[string]any{"battle_id": id})
	httpx.JSON(w, http.StatusOK, map[string]any{"status": "finished", "id": id})
	return nil
}

func (a *API) deleteBattle(w http.ResponseWriter, r *http.Request) error {
	id, err := pathInt64(r, "id")
	if err != nil {
		return err
	}
	if err := a.battles.Delete(r.Context(), id); err != nil {
		return writeRepoError(err, "id", id)
	}
	a.auditDash(r, "dashboard.battle.deleted", map[string]any{"battle_id": id})
	httpx.JSON(w, http.StatusOK, map[string]any{"status": "deleted", "id": id})
	return nil
}

// ---------------------------------------------------------------- Follows ---

var followSort = map[string]string{"created_at": "created_at"}

func (a *API) listFollows(w http.ResponseWriter, r *http.Request) error {
	q := parseListQuery(r, followSort, "created_at")
	f := repository.FollowFilter{
		FollowerID: r.URL.Query().Get("follower_id"),
		FolloweeID: r.URL.Query().Get("followee_id"),
	}
	items, total, err := a.follows.List(r.Context(), f, q.params())
	if err != nil {
		return httpx.Wrap(httpx.ErrInternal, err)
	}
	writeList(w, q, total, items)
	return nil
}

func (a *API) deleteFollow(w http.ResponseWriter, r *http.Request) error {
	follower := r.PathValue("follower")
	followee := r.PathValue("followee")
	if err := a.follows.Delete(r.Context(), follower, followee); err != nil {
		return writeRepoError(err, "follow", follower+"->"+followee)
	}
	a.auditDash(r, "dashboard.follow.deleted", map[string]any{"follower_id": follower, "followee_id": followee})
	httpx.JSON(w, http.StatusOK, map[string]any{"status": "deleted"})
	return nil
}

// ----------------------------------------------------------- Roles / RBAC ---

func (a *API) listRoles(w http.ResponseWriter, r *http.Request) error {
	roles, err := a.roles.ListRoles(r.Context())
	if err != nil {
		return httpx.Wrap(httpx.ErrInternal, err)
	}
	httpx.JSON(w, http.StatusOK, roles)
	return nil
}

func (a *API) listPermissions(w http.ResponseWriter, r *http.Request) error {
	perms, err := a.roles.ListPermissions(r.Context())
	if err != nil {
		return httpx.Wrap(httpx.ErrInternal, err)
	}
	httpx.JSON(w, http.StatusOK, perms)
	return nil
}
