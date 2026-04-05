#!/usr/bin/env bash
# local-dev/run-tests.sh
# Runs the full test suite on macOS.
# No external services needed for unit tests.
# Integration tests auto-start/stop a Docker Postgres.
set -euo pipefail

cd "$(dirname "$0")/.."

GREEN='\033[0;32m'; RED='\033[0;31m'; YELLOW='\033[1;33m'; NC='\033[0m'
PASS=0; FAIL=0

suite() { echo -e "\n${GREEN}── $1 ──────────────────────────────────────────────${NC}"; }
ok()    { PASS=$((PASS+1)); echo -e "  ${GREEN}PASS${NC} $1"; }
fail()  { FAIL=$((FAIL+1)); echo -e "  ${RED}FAIL${NC} $1"; }

MODE="${1:-unit}"   # unit | integration | all | bench | watch

echo -e "\n${GREEN}==> cam-platform tests (mode: $MODE)${NC}"

# ─── Unit tests ───────────────────────────────────────────────────────────────

run_auth_tests() {
  suite "Auth package (JWT verification)"
  if cd control-plane && go test ./internal/auth/... -v -race -count=1 2>&1; then
    ok "auth tests"
  else
    fail "auth tests"
  fi
  cd ..
}

run_agent_tests() {
  suite "Edge agent (discovery + frigate config)"
  if cd agent && go test ./... -v -race -count=1 2>&1; then
    ok "agent tests"
  else
    fail "agent tests"
  fi
  cd ..
}

run_dashboard_tests() {
  suite "Dashboard (React components + API client)"
  if ! command -v npx > /dev/null 2>&1; then
    echo "  SKIP (Node.js not installed)"
    return
  fi
  # Install vitest if not present
  if [ ! -f "dashboard/node_modules/.bin/vitest" ]; then
    echo "  Installing test deps..."
    cd dashboard && npm install --save-dev \
      vitest @vitejs/plugin-react \
      @testing-library/react @testing-library/user-event \
      @testing-library/jest-dom jsdom \
      --silent 2>/dev/null || true
    cd ..
  fi
  if cd dashboard && npm test -- --run 2>&1; then
    ok "dashboard tests"
  else
    fail "dashboard tests"
  fi
  cd ..
}

# ─── Integration tests (need Postgres) ───────────────────────────────────────

run_api_tests() {
  suite "API integration (starts Docker Postgres)"

  # Start test Postgres
  docker rm -f cam-test-pg 2>/dev/null || true
  docker run -d --name cam-test-pg \
    -e POSTGRES_DB=camplatform_test \
    -e POSTGRES_USER=cam \
    -e POSTGRES_PASSWORD=cam \
    -p 5433:5432 \
    postgres:16-alpine > /dev/null 2>&1

  # Wait for it
  for i in $(seq 1 15); do
    if docker exec cam-test-pg pg_isready -U cam -q 2>/dev/null; then break; fi
    sleep 1
  done

  # Run tests
  if TEST_DATABASE_URL="postgres://cam:cam@localhost:5433/camplatform_test?sslmode=disable" \
     cd control-plane && go test ./internal/api/... -v -race -count=1 -tags integration 2>&1; then
    ok "API integration tests"
  else
    fail "API integration tests"
  fi
  cd ..

  # Cleanup
  docker rm -f cam-test-pg > /dev/null 2>&1 || true
}

# ─── Benchmarks ───────────────────────────────────────────────────────────────

run_benchmarks() {
  suite "Benchmarks"
  cd control-plane
  go test ./internal/auth/... \
    -bench=BenchmarkVerify \
    -benchmem \
    -benchtime=3s \
    -count=3 2>&1 | tee /tmp/bench-results.txt
  echo ""
  echo "  Summary:"
  grep "Benchmark" /tmp/bench-results.txt | column -t
  cd ..
}

# ─── Dispatch ─────────────────────────────────────────────────────────────────

case "$MODE" in
  unit)
    run_auth_tests
    run_agent_tests
    run_dashboard_tests
    ;;
  integration)
    run_api_tests
    ;;
  all)
    run_auth_tests
    run_agent_tests
    run_dashboard_tests
    run_api_tests
    ;;
  bench)
    run_benchmarks
    ;;
  watch)
    # Watch mode for Go (re-runs on file change)
    if ! command -v entr > /dev/null 2>&1; then
      echo "Install entr for watch mode: brew install entr"
      exit 1
    fi
    echo "Watching for changes in control-plane/internal/... (Ctrl-C to stop)"
    find control-plane/internal -name "*.go" | \
      entr -c sh -c "cd control-plane && go test ./internal/... -v -race -count=1"
    ;;
  *)
    echo "Usage: $0 [unit|integration|all|bench|watch]"
    exit 1
    ;;
esac

# ─── Summary ─────────────────────────────────────────────────────────────────

if [ "$MODE" != "bench" ] && [ "$MODE" != "watch" ]; then
  echo ""
  echo -e "${GREEN}════════════════════════════════════════${NC}"
  if [ "$FAIL" -eq 0 ]; then
    echo -e "${GREEN}  All $PASS test suites passed!${NC}"
  else
    echo -e "${RED}  $FAIL failed, $PASS passed${NC}"
  fi
  echo -e "${GREEN}════════════════════════════════════════${NC}"
  [ "$FAIL" -eq 0 ] && exit 0 || exit 1
fi
