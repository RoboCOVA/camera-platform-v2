#!/usr/bin/env bash
# cam-platform setup script
# Run this on a fresh Ubuntu 24.04 VPS to get the full stack running.
set -euo pipefail

DOMAIN="${1:-}"
if [ -z "$DOMAIN" ]; then
  echo "Usage: $0 yourdomain.com"
  exit 1
fi

echo "==> Setting up cam-platform on $DOMAIN"

# ── 1. System deps ────────────────────────────────────────────────────────────
echo "==> Installing dependencies..."
apt-get update -qq
apt-get install -y -qq docker.io docker-compose-plugin wireguard-tools openssl curl

systemctl enable --now docker

# ── 2. Generate secrets ───────────────────────────────────────────────────────
echo "==> Generating secrets..."
POSTGRES_PASSWORD=$(openssl rand -base64 32)
KC_ADMIN_PASSWORD=$(openssl rand -base64 32)
MQTT_API_PASSWORD=$(openssl rand -base64 24)
DEVICE_SECRET=$(openssl rand -hex 32)
GRAFANA_PASSWORD=$(openssl rand -base64 20)
NEXTAUTH_SECRET=$(openssl rand -hex 32)
CAM_DASHBOARD_SECRET=$(openssl rand -hex 32)
CAM_API_SECRET=$(openssl rand -hex 32)
GRAFANA_KC_SECRET=$(openssl rand -hex 32)
METRICS_ALLOWLIST="0.0.0.0/0"

cat > deploy/.env <<EOF
DOMAIN=$DOMAIN
POSTGRES_PASSWORD=$POSTGRES_PASSWORD
KC_ADMIN_USER=admin
KC_ADMIN_PASSWORD=$KC_ADMIN_PASSWORD
MQTT_API_USER=camapi
MQTT_API_PASSWORD=$MQTT_API_PASSWORD
DEVICE_SECRET=$DEVICE_SECRET
GRAFANA_PASSWORD=$GRAFANA_PASSWORD
NEXTAUTH_SECRET=$NEXTAUTH_SECRET
CAM_DASHBOARD_SECRET=$CAM_DASHBOARD_SECRET
CAM_API_SECRET=$CAM_API_SECRET
GRAFANA_KC_SECRET=$GRAFANA_KC_SECRET
METRICS_ALLOWLIST=$METRICS_ALLOWLIST
S3_ENDPOINT=
S3_BUCKET=
S3_ACCESS_KEY=
S3_SECRET_KEY=
EOF

echo "==> Secrets written to deploy/.env (keep this safe!)"

# ── 3. Mosquitto password file ────────────────────────────────────────────────
echo "==> Creating MQTT credentials..."
docker run --rm eclipse-mosquitto:2.0 \
  mosquitto_passwd -b -c /dev/stdout camapi "$MQTT_API_PASSWORD" \
  > deploy/mosquitto/passwd 2>/dev/null || \
  echo "camapi:$(openssl passwd -6 "$MQTT_API_PASSWORD")" > deploy/mosquitto/passwd

# ── 4. WireGuard server keys ──────────────────────────────────────────────────
echo "==> Generating WireGuard server keys..."
mkdir -p deploy/wireguard
wg genkey | tee deploy/wireguard/server_private.key | wg pubkey > deploy/wireguard/server_public.key
chmod 600 deploy/wireguard/server_private.key

SERVER_PUBKEY=$(cat deploy/wireguard/server_public.key)
echo "WG_SERVER_PUBLIC_KEY=$SERVER_PUBKEY" >> deploy/.env
echo "WireGuard server public key: $SERVER_PUBKEY"

# ── 5. Build agent release artifacts ──────────────────────────────────────────
echo "==> Building cam-agent release artifacts..."
mkdir -p deploy/releases
docker run --rm \
  -v "$PWD/agent:/src" \
  -v "$PWD/deploy/releases:/out" \
  -w /src \
  golang:1.22-alpine \
  sh -lc '
    set -e
    go mod download
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/cam-agent-linux-amd64 ./cmd/agent
    CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o /out/cam-agent-linux-arm64 ./cmd/agent
  '

sed \
  -e "s|\${DOMAIN}|$DOMAIN|g" \
  -e "s|\${CAM_DASHBOARD_SECRET}|$CAM_DASHBOARD_SECRET|g" \
  -e "s|\${CAM_API_SECRET}|$CAM_API_SECRET|g" \
  -e "s|\${GRAFANA_KC_SECRET}|$GRAFANA_KC_SECRET|g" \
  -e "s|\${GOOGLE_CLIENT_ID}||g" \
  -e "s|\${GOOGLE_CLIENT_SECRET}||g" \
  -e "s|\${AZURE_CLIENT_ID}||g" \
  -e "s|\${AZURE_CLIENT_SECRET}||g" \
  -e "s|\${SMTP_HOST}|smtp.example.com|g" \
  -e "s|\${SMTP_PORT}|587|g" \
  -e "s|\${SMTP_USER}||g" \
  -e "s|\${SMTP_PASSWORD}||g" \
  deploy/keycloak/realm.json > deploy/keycloak/realm.resolved.json

# ── 6. Start the stack ────────────────────────────────────────────────────────
echo "==> Starting services..."
cd deploy
docker compose up -d --build

echo ""
echo "======================================================"
echo "  cam-platform is starting up!"
echo "======================================================"
echo ""
echo "  Dashboard:  https://app.$DOMAIN"
echo "  API:        https://api.$DOMAIN"
echo "  Auth:       https://auth.$DOMAIN"
echo "  Metrics:    https://metrics.$DOMAIN"
echo ""
echo "  Keycloak admin: admin / $KC_ADMIN_PASSWORD"
echo "  Grafana admin:  admin / $GRAFANA_PASSWORD"
echo ""
echo "  WireGuard pubkey: $SERVER_PUBKEY"
echo "  WireGuard endpoint: $DOMAIN:51820"
echo ""
echo "  DNS records needed (all → this server's IP):"
echo "    A  app.$DOMAIN"
echo "    A  api.$DOMAIN"
echo "    A  auth.$DOMAIN"
echo "    A  metrics.$DOMAIN"
echo ""
echo "  TLS certificates will auto-provision via Let's Encrypt."
echo "======================================================"

# ── 7. Wait for health ────────────────────────────────────────────────────────
echo ""
echo "==> Waiting for API to be healthy..."
for i in $(seq 1 30); do
  if curl -fs "http://localhost:3001/health" > /dev/null 2>&1; then
    echo "==> API is healthy!"
    break
  fi
  echo "   attempt $i/30..."
  sleep 5
done
