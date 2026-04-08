#!/usr/bin/env bash
set -euo pipefail

DOMAIN="${DOMAIN:-}"
if [ -z "$DOMAIN" ]; then
  echo "Usage: DOMAIN=example.com $0"
  exit 1
fi

fail() { echo "[smoke] FAIL: $*"; exit 1; }
pass() { echo "[smoke] OK: $*"; }

curl -fsS "https://api.${DOMAIN}/health" >/dev/null || fail "api health"
pass "api health"

curl -fsS "https://app.${DOMAIN}/api/health" >/dev/null || fail "dashboard health"
pass "dashboard health"

curl -fsS "https://auth.${DOMAIN}/health/ready" >/dev/null || fail "keycloak health"
pass "keycloak health"

echo "[smoke] Completed"
