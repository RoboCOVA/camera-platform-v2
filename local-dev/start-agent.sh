#!/usr/bin/env bash
# local-dev/start-agent.sh
# Builds and runs the edge agent locally on macOS.
# The agent discovers cameras and connects to the local control plane.
set -euo pipefail

cd "$(dirname "$0")/.."

GREEN='\033[0;32m'; YELLOW='\033[1;33m'; NC='\033[0m'
ok()   { echo -e "  ${GREEN}✓${NC} $1"; }
warn() { echo -e "  ${YELLOW}!${NC} $1"; }

echo -e "\n${GREEN}==> Starting cam-agent${NC}"

# Check API is up
if ! curl -fs http://localhost:3001/health > /dev/null 2>&1; then
  echo "  API not running. Start it first:"
  echo "  cd deploy && docker compose -f docker-compose.yml -f ../local-dev/docker-compose.override.yml --env-file .env up -d"
  exit 1
fi
ok "API is reachable"

# Copy simulated cameras config to data dir
if [ ! -f /tmp/cam-data/cameras.json ]; then
  mkdir -p /tmp/cam-data
  cp local-dev/cameras.json /tmp/cam-data/cameras.json
  ok "Copied cameras.json to /tmp/cam-data/"
fi

# Build agent
echo "  Building agent..."
cd agent
go mod tidy 2>/dev/null
go build -o /tmp/cam-agent ./cmd/agent
ok "Agent built: /tmp/cam-agent"
cd ..

# Load local env
if [ -f agent/.env.local ]; then
  set -a
  source agent/.env.local
  set +a
  ok "Loaded agent/.env.local"
fi

# Override: point to local API
export CAM_CONTROL_URL="http://localhost:3001"
export CAM_CONTROL_KEY="dev-device-key-local"
export CAM_DATA_PATH="/tmp/cam-data"
export CAM_FRIGATE_CONFIG="/tmp/cam-data/frigate.yml"
export CAM_MQTT_HOST="localhost"
# Use docker mode: Frigate runs as a separate container (start-frigate.sh)
# instead of being spawned as a subprocess by the agent.
export CAM_FRIGATE_MODE="docker"

# Detect subnet automatically if not set
if [ -z "${CAM_SUBNET:-}" ]; then
  # Get local network subnet from en0 (Wi-Fi)
  LOCAL_IP=$(ipconfig getifaddr en0 2>/dev/null || ipconfig getifaddr en1 2>/dev/null || echo "")
  if [ -n "$LOCAL_IP" ]; then
    # Convert 192.168.1.100 → 192.168.1.0/24
    SUBNET=$(echo "$LOCAL_IP" | awk -F. '{print $1"."$2"."$3".0/24"}')
    export CAM_SUBNET="$SUBNET"
    ok "Auto-detected subnet: $CAM_SUBNET"
  else
    warn "Could not detect subnet — will use cameras.json instead of scanning"
    export CAM_SUBNET=""
  fi
fi

echo ""
echo "  Config:"
echo "    Control plane: $CAM_CONTROL_URL"
echo "    Device key:    $CAM_CONTROL_KEY"
echo "    Data path:     $CAM_DATA_PATH"
echo "    Subnet scan:   ${CAM_SUBNET:-disabled (using cameras.json)}"
echo ""
echo "  Press Ctrl-C to stop."
echo ""

/tmp/cam-agent
