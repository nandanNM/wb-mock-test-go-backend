# wb-mock-test — Dashboard API Reference

Base URL
- Production: `https://api.wb.codernandan.in`
- Local: `http://localhost:8080`

All paths are versioned under `/v1` (health checks are unversioned).

---

## Conventions

### Response envelopes
```jsonc
// success (single object)
{ "data": { ... } }

// paginated list
{ "data": { "items": [ ... ], "page": 1, "limit": 20, "total": 57, "total_pages": 3 } }

// bare collection (a few read endpoints — noted per endpoint)
{ "data": [ ... ] }

// error
{ "error": { "code": "string", "message": "string", "details": { ... }, "request_id": "string" } }
```

### Auth
Send the access token on every protected call:
```
Authorization: Bearer <access_token>
```
Browser (web) clients also send cookies — use `credentials: "include"`.

### List query parameters (dashboard list endpoints)
| Param | Meaning | Default |
|---|---|---|
| `page` | 1-based page | 1 |
| `limit` | page size (max 100) | 20 |
| `sort` | sort field (per-endpoint whitelist) | resource default |
| `order` | `asc` \| `desc` | `desc` |
| `search` | free-text (per-endpoint columns) | — |
Plus resource-specific filters (documented per endpoint).

### Common error codes
| HTTP | code | Meaning |
|---|---|---|
| 400 | `bad_request` | Malformed input / path |
| 401 | `unauthorized` | Missing/invalid token |
| 401 | `token_expired` | Refresh and retry |
| 401 | `session_revoked` | Re-login |
| 403 | `forbidden` | Lacks permission |
| 403 | `account_banned` / `account_suspended` | Account not active |
| 403 | `csrf_failed` | Missing/invalid CSRF token |
| 404 | `not_found` | Resource doesn't exist |
| 409 | `conflict` | Duplicate / referenced-in-use / refresh in progress |
| 422 | `validation_failed` | See `details` for per-field errors |

---

## Authentication

### `GET /v1/auth/google/start` — begin Google login
Public. Browser navigation → **302 redirect** to Google. Native apps pass `?client=native` to receive `{ "data": { "authorization_url": "..." } }` instead.
```js
window.location.href = "https://api.wb.codernandan.in/v1/auth/google/start";
```

### `GET /v1/auth/google/callback` — OAuth return
Public. Handled by the backend; the browser is redirected here by Google, then redirected to `OAUTH_SUCCESS_REDIRECT` (web) with cookies set. **Your frontend never calls this directly.**

### `POST /v1/auth/refresh` — rotate refresh token, get access token
- **Web:** send cookies + `X-CSRF-Token` header (value of the `csrf_token` cookie). No body.
- **Native:** `{ "refresh_token": "..." }` in the body.

Response:
```json
{ "data": { "access_token": "eyJ…", "token_type": "Bearer", "expires_in": 600,
            "session_id": "uuid", "csrf_token": "…(web)", "refresh_token": "…(native)" } }
```
Errors: `401 no_refresh_token | invalid_refresh_token | token_reuse`, `403 csrf_failed | account_inactive`, `409 refresh_in_progress` (retry).

### `POST /v1/auth/logout` — revoke current session
Bearer. → `{ "data": { "status": "logged_out" } }` (clears cookies).

### `POST /v1/auth/logout-all` — revoke all of the user's sessions
Bearer. → `{ "data": { "status": "logged_out_all", "revoked": 3 } }`

### `GET /v1/me` — current user, roles, permissions
Bearer.
```json
{ "data": {
    "user": { "id": "uuid", "name": "…", "email": "…", "status": "active", "email_verified": true,
              "created_at": "…", "updated_at": "…" },
    "roles": ["admin"],
    "permissions": ["subjects:read", "subjects:manage", "…"],
    "session_id": "uuid" } }
```

### `GET /v1/auth/sessions` — the caller's own devices
Bearer. Bare array. Each: `{ id, user_id, user_agent, ip, device_label, created_at, last_used_at, expires_at, current }`.

### `DELETE /v1/auth/sessions/{id}` — sign out one of your devices
Bearer. → `{ "data": { "status": "revoked" } }`

---

## Roles & permissions (who can call what)

- **super_admin** — everything (bypasses all checks).
- **admin** — `subjects/chapters/notes/questions/tests` `:read`+`:manage`, `tests:publish`, `users:read`, `users:ban`, `attempts:read`, `battles:read`+`battles:manage`, `follows:read`, `sessions:read`+`sessions:revoke`, `audit:read`.
- **super_admin only** — `users:delete`, `attempts:delete`, `roles:manage` (role assignment).

Gate UI with `permissions` from `/v1/me`; the backend enforces it regardless.

---

## Content resources

All under `/v1/admin`. Reads need `<res>:read`, writes need `<res>:manage`.
Standard shape: `GET /` (list), `GET /{id}`, `POST /` (201), `PATCH /{id}`, `DELETE /{id}` → `{ "data": { "status": "deleted", "id": N } }`.

### Subjects — `/v1/admin/subjects`
Object: `{ id, name_en, name_bn, position, created_at, updated_at }`
- List: filters `search`; sort `position|name|created_at`.
- Create: `{ "name_en": "…", "name_bn": "…", "position": 1 }` (names required)
- Update (partial): `{ "name_en"?, "name_bn"?, "position"? }`

### Chapters — `/v1/admin/chapters`
Object: `{ id, subject_id, name_en, name_bn, position, created_at, updated_at }`
- List: filters `subject_id`, `search`; sort `position|name|created_at`.
- Create: `{ "subject_id": 1, "name_en": "…", "name_bn": "…", "position": 0 }`
- Update: `{ "name_en"?, "name_bn"?, "position"? }`

### Chapter notes — `/v1/admin/notes`
Object: `{ id, chapter_id, language_code, title, pdf_url, page_count?, created_by?, created_at, updated_at }`
- List: filter `chapter_id`; sort `created_at|title`.
- Create: `{ "chapter_id": 1, "language_code": "en", "title": "…", "pdf_url": "https://…", "page_count": 12 }` (`language_code` ∈ `en|bn`)
- Update: `{ "title"?, "pdf_url"?, "page_count"? }`

### Questions — `/v1/admin/questions`
Object: `{ id, chapter_id, prompt_en, prompt_bn, explanation_en?, explanation_bn?, position, created_by?, created_at, updated_at, options: [Option] }`
Option: `{ id, question_id, position, body_en, body_bn, is_correct }`
- **List returns questions without options**; **GET /{id} includes options.**
- List: filter `chapter_id`, `search` (prompt); sort `position|created_at`.
- Create (≥2 options, exactly 1 `is_correct`):
```json
{ "chapter_id": 1, "prompt_en": "2+2?", "prompt_bn": "২+২?", "position": 0,
  "options": [
    { "position": 0, "body_en": "3", "body_bn": "৩", "is_correct": false },
    { "position": 1, "body_en": "4", "body_bn": "৪", "is_correct": true }
  ] }
```
- Update: `{ "prompt_en"?, …, "position"?, "options"? }` — if `options` is present it **fully replaces** them (same ≥2 / exactly-1-correct rule).

### Tests — `/v1/admin/tests`
Object: `{ id, subject_id, scope_type, title_en, title_bn, test_code, difficulty?, position, is_published, created_by?, created_at, updated_at, chapter_ids?, question_ids? }`
- **GET /{id} includes `chapter_ids` and `question_ids`** (ordered).
- List: filters `subject_id`, `published=true|false`, `search` (title/code); sort `position|created_at|code`.
- Create:
```json
{ "subject_id": 1, "scope_type": "chapter", "title_en": "…", "title_bn": "…",
  "test_code": "GK-06", "difficulty": "medium", "position": 0, "is_published": false,
  "chapter_ids": [1,2], "question_ids": [10,11,12] }
```
`scope_type` ∈ `chapter|multi_chapter|subject`; `difficulty` ∈ `easy|medium|hard`.
- Update: any field above; `chapter_ids`/`question_ids` (if present) **replace** the set. Publish/unpublish = `{ "is_published": true }`.

---

## Users — `/v1/admin/users`

- `GET /v1/admin/users` (`users:read`) — list. Filters: `status` (`active|suspended|banned`), `search` (name/email). Sort `created_at|points|email|name`.
- `GET /v1/admin/users/{id}` (`users:read`) → `{ "data": { "user": {…}, "roles": ["…"] } }`
- `DELETE /v1/admin/users/{id}` (`users:delete`, **super_admin**) → `{ "data": { "status": "deleted", "id": "uuid" } }`
- `POST /v1/admin/users/{id}/ban` (`users:ban`) — body `{ "reason": "…" }` → `{ "data": { "status": "banned", "user_id": "uuid" } }`
- `POST /v1/admin/users/{id}/suspend` (`users:ban`) — body `{ "reason": "…" }`
- `POST /v1/admin/users/{id}/reinstate` (`users:ban`)
- `POST /v1/admin/users/{id}/roles` (`roles:manage`, **super_admin**) — body `{ "role": "admin" }`
- `DELETE /v1/admin/users/{id}/roles/{role}` (`roles:manage`, **super_admin**)

> `ban`/`suspend` also revoke all of the user's sessions and take effect within seconds.

---

## Sessions — `/v1/admin/sessions`
- `GET` (`sessions:read`) — list (paginated). Filter `user_id`; sort `last_used_at|created_at`. Each session includes `revoked_at`.
- `DELETE /{id}` (`sessions:revoke`) → `{ "data": { "status": "revoked", "id": "uuid" } }`

## Attempts — `/v1/admin/attempts`
Object: `{ id, user_id, test_id, battle_id?, language_code, score, total_questions, accuracy, points_earned, started_at, completed_at? }`
- `GET` (`attempts:read`) — filters `user_id`, `test_id`; sort `started_at|completed_at|score|accuracy`.
- `GET /{id}` (`attempts:read`) — includes `answers: [{ id, question_id, selected_option_id?, is_correct, answered_at }]`.
- `DELETE /{id}` (`attempts:delete`, **super_admin**).

## Battles — `/v1/admin/battles`
Object: `{ id, room_code, host_id, test_id, status, max_players, started_at?, finished_at?, created_at, updated_at }`
- `GET` (`battles:read`) — filter `status` (`lobby|active|finished|abandoned`); sort `created_at|status`.
- `GET /{id}` (`battles:read`) — includes `participants: [{ user_id, role, score?, placement?, joined_at, finished_at? }]`.
- `POST /{id}/finish` (`battles:manage`) → `{ "data": { "status": "finished", "id": N } }`
- `DELETE /{id}` (`battles:manage`).

## Follows — `/v1/admin/follows`
Object: `{ follower_id, followee_id, created_at }`
- `GET` (`follows:read`) — filters `follower_id`, `followee_id`; sort `created_at`.
- `DELETE /v1/admin/follows/{follower}/{followee}` (`follows:read`) → `{ "data": { "status": "deleted" } }`

## RBAC catalog (read-only)
- `GET /v1/admin/roles` (`users:read`) → bare array `[{ id, name, description? }]`
- `GET /v1/admin/permissions` (`users:read`) → bare array `[{ id, name, description? }]`

## Audit log — `/v1/admin/audit`
- `GET` (`audit:read`) — query `user_id?`, `limit?` (default 100, max 500). **Bare array** under `data`.
- Record: `{ id, occurred_at, event_type, user_id?, session_id?, ip, user_agent, request_id, detail }`
- Event types include: `login_success`, `login_failure`, `token_refresh`, `token_reuse_detected`, `logout`, `logout_all`, `session_revoked`, `account_banned/suspended/reinstated`, `role_granted/revoked`, and `dashboard.<resource>.<action>`.

---

## Health (public, unversioned)
- `GET /healthz` → `{ "data": { "status": "ok", "version": "…", "uptime": "…" } }`
- `GET /readyz` → `{ "data": { "status": "ready" } }` (200) or `503` if the DB is unreachable.

---

## cURL quick reference
```bash
BASE=https://api.wb.codernandan.in
AT="<access_token>"   # from POST /v1/auth/refresh

# list subjects
curl -s "$BASE/v1/admin/subjects?page=1&limit=20&sort=position&order=asc" -H "Authorization: Bearer $AT"

# create a subject
curl -s -X POST "$BASE/v1/admin/subjects" -H "Authorization: Bearer $AT" \
  -H 'Content-Type: application/json' -d '{"name_en":"GK","name_bn":"সাধারণ","position":1}'

# ban a user
curl -s -X POST "$BASE/v1/admin/users/<uuid>/ban" -H "Authorization: Bearer $AT" \
  -H 'Content-Type: application/json' -d '{"reason":"spam"}'
```
