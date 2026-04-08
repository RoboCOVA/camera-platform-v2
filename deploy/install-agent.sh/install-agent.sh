#!/usr/bin/env bash
# cam-agent installer — run on the on-prem mini-PC (Ubuntu 22.04+)
# Usage: curl -fsSL https://api.yourdomain.com/install | bash -s <provision-token>
set -euo pipefail

PROVISION_TOKEN="${1:-}"
CONTROL_URL="${CAM_CONTROL_URL:-https://api.yourdomain.com}"
AGENT_VERSION="${CAM_AGENT_VERSION:-latest}"

if [ -z "$PROVISION_TOKEN" ]; then
  echo "Usage: install.sh <provision-token>"
  echo "Get a token from your dashboard: Settings → Devices → New Device"
  exit 1
fi

echo "==> Installing cam-agent..."

# ── 1. System deps ────────────────────────────────────────────────────────────
apt-get update -qq
apt-get install -y -qq \
  docker.io docker-compose-v2 \
  wireguard wireguard-tools \
  ffmpeg \
  curl jq

systemctl enable --now docker

# ── 2. Create device identity ─────────────────────────────────────────────────
mkdir -p /etc/cam
if [ ! -f /etc/cam/device.id ]; then
  # Generate stable device ID from machine ID
  DEVICE_ID="dev-$(cat /etc/machine-id | head -c 16)"
  echo "$DEVICE_ID" > /etc/cam/device.id
  chmod 640 /etc/cam/device.id
fi

DEVICE_ID=$(cat /etc/cam/device.id)
echo "==> Device ID: $DEVICE_ID"

# ── 3. Exchange provision token for device credentials ────────────────────────
echo "==> Registering device with control plane..."
RESPONSE=$(curl -fsSL -X POST "$CONTROL_URL/api/provision" \
  -H "Content-Type: application/json" \
  -d "{\"token\":\"$PROVISION_TOKEN\",\"device_id\":\"$DEVICE_ID\"}")

DEVICE_KEY=$(echo "$RESPONSE" | jq -r '.device_key')
WG_PRIVATE_KEY=$(echo "$RESPONSE" | jq -r '.wg_private_key')
WG_IP=$(echo "$RESPONSE" | jq -r '.wg_ip')
SERVER_PUBKEY=$(echo "$RESPONSE" | jq -r '.server_pubkey')
SERVER_ENDPOINT=$(echo "$RESPONSE" | jq -r '.server_endpoint')

if [ -z "$DEVICE_KEY" ] || [ "$DEVICE_KEY" = "null" ]; then
  echo "ERROR: Registration failed. Check your provision token."
  exit 1
fi

echo "==> Registration successful. WireGuard IP: $WG_IP"

# ── 4. Configure WireGuard tunnel ─────────────────────────────────────────────
echo "==> Configuring WireGuard..."
cat > /etc/wireguard/wg0.conf <<EOF
[Interface]
Address = $WG_IP/24
PrivateKey = $WG_PRIVATE_KEY
DNS = 1.1.1.1

[Peer]
PublicKey = $SERVER_PUBKEY
Endpoint = $SERVER_ENDPOINT
AllowedIPs = 10.10.0.0/24
PersistentKeepalive = 25
EOF

chmod 600 /etc/wireguard/wg0.conf
systemctl enable --now wg-quick@wg0

echo "==> WireGuard tunnel up. Testing connectivity..."
sleep 3
if ping -c 1 10.10.0.1 > /dev/null 2>&1; then
  echo "==> Tunnel working!"
else
  echo "WARNING: Cannot reach control plane over WireGuard. Check firewall."
fi

# ── 5. Write agent config ─────────────────────────────────────────────────────
mkdir -p /etc/cam /data/recordings /data/frigate

cat > /etc/cam/agent.env <<EOF
CAM_CONTROL_URL=$CONTROL_URL
CAM_CONTROL_KEY=$DEVICE_KEY
CAM_DATA_PATH=/data
CAM_FRIGATE_CONFIG=/etc/frigate/frigate.yml
CAM_MQTT_HOST=localhost
CAM_FRIGATE_MODE=docker
# Optional: specify subnet to scan instead of multicast
# CAM_SUBNET=192.168.1.0/24
# Camera credentials to try (add more as needed)
CAM_CRED_USER_1=admin
CAM_CRED_PASS_1=admin
EOF

chmod 600 /etc/cam/agent.env

# ── 6. Install and start Frigate via Docker ───────────────────────────────────
echo "==> Starting Frigate..."
mkdir -p /etc/frigate

cat > /opt/cam/docker-compose.yml <<'EOF'
version: "3.9"
services:
  frigate:
    image: ghcr.io/blakeblackshear/frigate:stable
    container_name: frigate
    restart: unless-stopped
    privileged: true
    shm_size: "256mb"
    network_mode: host   # use host networking so it can reach cameras on LAN
    volumes:
      - /etc/frigate:/config
      - /data/recordings:/media/frigate
      - type: tmpfs
        target: /tmp/cache
        tmpfs:
          size: 512m
    environment:
      FRIGATE_RTSP_PASSWORD: ""

  mqtt:
    image: eclipse-mosquitto:2.0
    container_name: mosquitto
    restart: unless-stopped
    network_mode: host
    volumes:
      - /opt/cam/mosquitto.conf:/mosquitto/config/mosquitto.conf
EOF

cat > /opt/cam/mosquitto.conf <<'EOF'
listener 1883 127.0.0.1
allow_anonymous true
EOF

mkdir -p /opt/cam
docker compose -f /opt/cam/docker-compose.yml up -d mosquitto
echo "==> Mosquitto MQTT broker started"

# ── 7. Download and install cam-agent binary ──────────────────────────────────
echo "==> Downloading cam-agent..."
ARCH=$(uname -m)
case $ARCH in
  x86_64)  ARCH_TAG="amd64" ;;
  aarch64) ARCH_TAG="arm64" ;;
  *)       echo "Unsupported arch: $ARCH"; exit 1 ;;
esac

curl -fsSL "$CONTROL_URL/releases/cam-agent-linux-$ARCH_TAG" -o /usr/local/bin/cam-agent
chmod +x /usr/local/bin/cam-agent

# ── 8. Install systemd service ────────────────────────────────────────────────
cat > /etc/systemd/system/cam-agent.service <<EOF
[Unit]
Description=Camera Edge Agent
After=network-online.target docker.service wg-quick@wg0.service
Wants=network-online.target
Requires=docker.service

[Service]
Type=simple
User=root
EnvironmentFile=/etc/cam/agent.env
ExecStart=/usr/local/bin/cam-agent
Restart=always
RestartSec=10
StandardOutput=journal
StandardError=journal
SyslogIdentifier=cam-agent

# Watchdog: restart if no heartbeat for 2 minutes
WatchdogSec=120

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable --now cam-agent

echo ""
echo "======================================================"
echo "  cam-agent installed successfully!"
echo "======================================================"
echo "  Device ID:  $DEVICE_ID"
echo "  WireGuard:  $WG_IP"
echo ""
echo "  Check status: systemctl status cam-agent"
echo "  View logs:    journalctl -u cam-agent -f"
echo "  Health:       curl http://localhost:8090/health"
echo ""
echo "  Your cameras will appear in the dashboard within 60s."
echo "======================================================"
