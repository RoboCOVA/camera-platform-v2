#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

log() { echo "[qa] $*"; }

log "Running Go tests (control-plane)"
( cd "$ROOT_DIR/control-plane" && go test ./... )

log "Running Go tests (agent)"
( cd "$ROOT_DIR/agent" && go test ./... )

if command -v npm >/dev/null 2>&1; then
  log "Running dashboard lint/type-check/tests"
  ( cd "$ROOT_DIR/dashboard" && npm ci )
  ( cd "$ROOT_DIR/dashboard" && npm run lint )
  ( cd "$ROOT_DIR/dashboard" && npm run type-check )
  ( cd "$ROOT_DIR/dashboard" && npm test )
  if [ "${RUN_E2E:-}" = "1" ]; then
    log "Running dashboard E2E (Playwright)"
    ( cd "$ROOT_DIR/dashboard" && npm run e2e )
  fi
else
  log "npm not found; skipping dashboard checks"
fi

log "QA gate complete"
