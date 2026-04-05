#!/usr/bin/env bash
# local-dev/start-frigate.sh
# Starts Frigate NVR in Docker, using the config written by the agent.
# Run AFTER start-agent.sh has run at least once and written frigate.yml.
set -euo pipefail

cd "$(dirname "$0")/.."

GREEN='\033[0;32m'; YELLOW='\033[1;33m'; RED='\033[0;31m'; NC='\033[0m'
ok()   { echo -e "  ${GREEN}✓${NC} $1"; }
warn() { echo -e "  ${YELLOW}!${NC} $1"; }
err()  { echo -e "  ${RED}✗${NC} $1"; }

echo -e "\n${GREEN}==> Starting Frigate NVR${NC}"

FRIGATE_CONFIG="/tmp/cam-data/frigate.yml"
RECORDINGS_DIR="/tmp/cam-data/recordings"

# Check config exists (agent must have run first)
if [ ! -f "$FRIGATE_CONFIG" ]; then
  err "Frigate config not found: $FRIGATE_CONFIG"
  echo ""
  echo "  The agent generates this file when it discovers cameras."
  echo "  Run start-agent.sh first, wait for 'HLS stream running' message,"
  echo "  then run this script."
  exit 1
fi
ok "Frigate config found: $FRIGATE_CONFIG"

# Show config summary
echo ""
echo "  Cameras configured:"
grep "^  [a-z]" "$FRIGATE_CONFIG" | grep -v "ffmpeg\|detect\|record\|objects\|motion" \
  | awk '{print "    -", $1}' | head -20 || true
echo ""

# Stop existing Frigate
docker rm -f frigate-local 2>/dev/null || true

# Start Frigate
# Note: --add-host ensures Frigate can reach cameras on host network (for sim)
docker run -d \
  --name frigate-local \
  --restart unless-stopped \
  --shm-size=256m \
  --add-host=host.docker.internal:host-gateway \
  -p 5001:5000 \
  -p 8555:8555/tcp \
  -p 8555:8555/udp \
  -v "$FRIGATE_CONFIG:/config/config.yml:ro" \
  -v "$RECORDINGS_DIR:/media/frigate" \
  -v "/tmp/cam-data/frigate-db:/db" \
  -e FRIGATE_RTSP_PASSWORD="" \
  ghcr.io/blakeblackshear/frigate:stable

ok "Frigate started"

# Wait for Frigate API
echo "  Waiting for Frigate to initialize..."
for i in $(seq 1 30); do
  if curl -fs http://localhost:5000/api/version > /dev/null 2>&1; then
    VERSION=$(curl -s http://localhost:5000/api/version | jq -r '.version' 2>/dev/null || echo "unknown")
    ok "Frigate ready — version $VERSION"
    break
  fi
  sleep 2
  [ "$i" = "30" ] && { warn "Frigate taking longer than expected — check: docker logs frigate-local"; }
done

echo ""
echo "  Frigate UI:    http://localhost:5000"
echo "  View logs:     docker logs -f frigate-local"
echo ""
echo "  Check MQTT events from Frigate:"
echo "  mosquitto_sub -h localhost -p 1883 -t 'frigate/#' -v"
echo ""

open http://localhost:5000 2>/dev/null || true
