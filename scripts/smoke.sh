#!/usr/bin/env bash
# Smoke test for the auth backend. Exercises the endpoints that don't require
# live Google credentials (health + auth negative paths). For the full Google
# login flow, see the Authentication section of the README.
#
# Usage: BASE=http://localhost:8080 ./scripts/smoke.sh
set -euo pipefail
BASE="${BASE:-http://localhost:8080}"

pass=0; fail=0
check() { # check <description> <expected-status> <actual-status>
  if [ "$2" = "$3" ]; then echo "  ok   $1 ($3)"; pass=$((pass+1));
  else echo "  FAIL $1 (expected $2, got $3)"; fail=$((fail+1)); fi
}

code() { curl -s -o /dev/null -w '%{http_code}' "$@"; }

echo "Smoke testing $BASE"
check "GET /healthz"                          200 "$(code "$BASE/healthz")"
check "GET /readyz"                           200 "$(code "$BASE/readyz")"
check "GET /v1/me without token -> 401"       401 "$(code "$BASE/v1/me")"
check "GET /v1/me bad token -> 401"           401 "$(code -H 'Authorization: Bearer bad' "$BASE/v1/me")"
check "GET /v1/users no auth -> 401"          401 "$(code "$BASE/v1/users")"
check "refresh (native) no token -> 401"      401 "$(code -X POST -H 'X-Client-Type: native' -H 'Content-Type: application/json' -d '{}' "$BASE/v1/auth/refresh")"
check "refresh cookie no CSRF -> 403"         403 "$(code -X POST --cookie 'refresh_token=x; csrf_token=y' "$BASE/v1/auth/refresh")"

echo "----"
echo "passed: $pass  failed: $fail"
[ "$fail" -eq 0 ]
