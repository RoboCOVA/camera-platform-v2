#!/usr/bin/env bash
# local-dev/reset.sh
# Stops all services and optionally wipes all data.
# Usage:
#   ./local-dev/reset.sh         # stop services, keep data
#   ./local-dev/reset.sh --hard  # stop services AND delete all data/volumes
set -euo pipefail

cd "$(dirname "$0")/.."

GREEN='\033[0;32m'; YELLOW='\033[1;33m'; NC='\033[0m'
ok()   { echo -e "  ${GREEN}✓${NC} $1"; }
warn() { echo -e "  ${YELLOW}!${NC} $1"; }

HARD="${1:-}"

echo -e "\n${GREEN}==> Stopping cam-platform services${NC}"

# Stop Docker containers
docker rm -f frigate-local cam-mediamtx 2>/dev/null && ok "Stopped frigate + mediamtx" || true

cd deploy
docker compose \
  -f docker-compose.yml \
  -f ../local-dev/docker-compose.override.yml \
  --env-file .env \
  down 2>/dev/null && ok "Stopped Docker Compose stack" || true

# Kill local processes
pkill -f "cam-agent" 2>/dev/null && ok "Stopped cam-agent" || true
pkill -f "ffmpeg.*rtsp" 2>/dev/null && ok "Stopped ffmpeg RTSP" || true
pkill -f "next dev" 2>/dev/null && ok "Stopped Next.js" || true

if [ "$HARD" = "--hard" ]; then
  echo ""
  warn "Hard reset: deleting all data volumes and temp files"

  # Delete Docker volumes
  cd ../deploy
  docker compose \
    -f docker-compose.yml \
    -f ../local-dev/docker-compose.override.yml \
    --env-file .env \
    down -v 2>/dev/null || true
  ok "Deleted Docker volumes (Postgres data, Keycloak data)"

  # Delete local data
  rm -rf /tmp/cam-data
  ok "Deleted /tmp/cam-data (recordings, frigate config)"

  # Delete env files (keep .env.example)
  rm -f deploy/.env
  ok "Deleted deploy/.env"

  echo ""
  echo "Hard reset complete. Run ./local-dev/setup.sh to start fresh."
else
  echo ""
  echo "Services stopped. Data preserved."
  echo "Restart with: ./local-dev/start-all.sh"
  echo "Full reset:   ./local-dev/reset.sh --hard"
fi
