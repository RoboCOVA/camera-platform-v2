#!/usr/bin/env bash
set -euo pipefail

API_BASE="${API_BASE:-}"
DEVICE_KEY="${DEVICE_KEY:-}"
DEVICE_ID="${DEVICE_ID:-sim-$(date +%s)}"

if [ -z "$API_BASE" ] || [ -z "$DEVICE_KEY" ]; then
  echo "Usage: API_BASE=https://api.example.com DEVICE_KEY=devkey_xxx $0"
  exit 1
fi

payload=$(cat <<JSON
{
  "device_id": "$DEVICE_ID",
  "agent_version": "sim-0.1",
  "cameras": [
    {"id":"cam-sim-1","online":true},
    {"id":"cam-sim-2","online":true}
  ]
}
JSON
)

curl -fsS -X POST "$API_BASE/api/devices/heartbeat" \
  -H "Content-Type: application/json" \
  -H "X-Device-Key: $DEVICE_KEY" \
  -d "$payload" >/dev/null

cameras=$(cat <<JSON
{
  "cameras": [
    {
      "id":"cam-sim-1",
      "name":"Sim Cam 1",
      "manufacturer":"Sim",
      "model":"S1",
      "serial":"SIM-001",
      "ip":"10.0.0.10",
      "main_stream_url":"rtsp://user:pass@10.0.0.10/stream",
      "sub_stream_url":"rtsp://user:pass@10.0.0.10/sub",
      "width":1920,
      "height":1080
    },
    {
      "id":"cam-sim-2",
      "name":"Sim Cam 2",
      "manufacturer":"Sim",
      "model":"S2",
      "serial":"SIM-002",
      "ip":"10.0.0.11",
      "main_stream_url":"rtsp://user:pass@10.0.0.11/stream",
      "sub_stream_url":"rtsp://user:pass@10.0.0.11/sub",
      "width":1280,
      "height":720
    }
  ]
}
JSON
)

curl -fsS -X POST "$API_BASE/api/devices/cameras" \
  -H "Content-Type: application/json" \
  -H "X-Device-Key: $DEVICE_KEY" \
  -d "$cameras" >/dev/null

echo "[edge-sim] sent heartbeat + cameras"
