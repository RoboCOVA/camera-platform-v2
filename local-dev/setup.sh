#!/usr/bin/env bash
# local-dev/setup.sh
# One-time setup for running cam-platform on macOS.
# Run from the project root: ./local-dev/setup.sh
set -euo pipefail

cd "$(dirname "$0")/.."
ROOT="$(pwd)"

GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m'

ok()   { echo -e "  ${GREEN}✓${NC} $1"; }
warn() { echo -e "  ${YELLOW}!${NC} $1"; }
err()  { echo -e "  ${RED}✗${NC} $1"; }
step() { echo -e "\n${GREEN}==> $1${NC}"; }

# ─── 1. Check prerequisites ───────────────────────────────────────────────────
step "Checking prerequisites"

check_cmd() {
  if command -v "$1" > /dev/null 2>&1; then
    ok "$1 found: $(command -v $1)"
  else
    err "$1 not found"
    echo "     Install with: $2"
    MISSING=1
  fi
}

MISSING=0
check_cmd docker    "brew install --cask docker"
check_cmd go        "brew install go"
check_cmd node      "brew install node"
check_cmd ffmpeg    "brew install ffmpeg"
check_cmd jq        "brew install jq"
check_cmd npm       "(comes with node)"

if [ "$MISSING" = "1" ]; then
  echo ""
  echo "Please install the missing tools above and re-run this script."
  exit 1
fi

# Check Docker is running
if ! docker info > /dev/null 2>&1; then
  err "Docker Desktop is not running"
  echo "     Open Docker Desktop from Applications and wait for the whale icon."
  exit 1
fi
ok "Docker Desktop is running"

# Check Docker has enough memory (warn if < 4GB)
DOCKER_MEM=$(docker info --format '{{.MemTotal}}' 2>/dev/null || echo 0)
if [ "$DOCKER_MEM" -lt 4000000000 ] 2>/dev/null; then
  warn "Docker has less than 4GB RAM allocated. Recommend 6GB."
  warn "Change in Docker Desktop → Settings → Resources → Memory"
fi

# ─── 2. Create data directories ───────────────────────────────────────────────
step "Creating local data directories"

mkdir -p /tmp/cam-data/recordings
mkdir -p /tmp/cam-data/frigate-db
cp "$ROOT/local-dev/cameras.json" /tmp/cam-data/cameras.json
ok "Created /tmp/cam-data/"

# ─── 3. Copy env files ────────────────────────────────────────────────────────
step "Setting up environment files"

if [ ! -f "$ROOT/deploy/.env" ]; then
  cp "$ROOT/local-dev/.env" "$ROOT/deploy/.env"
  ok "Created deploy/.env"
else
  warn "deploy/.env already exists — skipping"
fi

if ! grep -q '^NEXTAUTH_SECRET=' "$ROOT/deploy/.env"; then
  echo "NEXTAUTH_SECRET=dev-nextauth-secret-absolutely-not-for-production" >> "$ROOT/deploy/.env"
  ok "Added NEXTAUTH_SECRET to deploy/.env"
fi

if ! grep -q '^WG_SERVER_PUBLIC_KEY=' "$ROOT/deploy/.env"; then
  echo "WG_SERVER_PUBLIC_KEY=dev-local-wg-pubkey" >> "$ROOT/deploy/.env"
  ok "Added WG_SERVER_PUBLIC_KEY to deploy/.env"
fi

if [ ! -f "$ROOT/dashboard/.env.local" ]; then
  # already created in the project
  ok "dashboard/.env.local already exists"
else
  ok "dashboard/.env.local found"
fi

if [ ! -f "$ROOT/agent/.env.local" ]; then
  ok "agent/.env.local already exists"
fi

# ─── 4. Install dependencies ──────────────────────────────────────────────────
step "Installing Go dependencies"

echo "  control-plane..."
cd "$ROOT/control-plane" && go mod tidy && ok "control-plane deps installed"

echo "  agent..."
cd "$ROOT/agent" && go mod tidy && ok "agent deps installed"

step "Installing Node.js dependencies"
cd "$ROOT/dashboard" && npm install && ok "dashboard deps installed"

# ─── 5. Create Mosquitto password file ───────────────────────────────────────
step "Creating MQTT password file"
cd "$ROOT"
mkdir -p deploy/mosquitto

# Mosquitto 2.x requires hashed passwords.
# We generate the passwd file using the mosquitto_passwd tool inside the container.
# The docker-compose mounts only the config file; the passwd file is optional.
# For local dev, we use allow_anonymous=true in the default config,
# so this step is optional but ensures the API can authenticate if auth is enabled later.
if command -v mosquitto_passwd > /dev/null 2>&1; then
  mosquitto_passwd -b -c deploy/mosquitto/passwd camapi devmqttpass
  ok "Created deploy/mosquitto/passwd (hashed)"
elif docker info > /dev/null 2>&1; then
  docker run --rm -v "$ROOT/deploy/mosquitto:/mosquitto/config" \
    eclipse-mosquitto:2.0 \
    mosquitto_passwd -b -c /mosquitto/config/passwd camapi devmqttpass 2>/dev/null \
    && ok "Created deploy/mosquitto/passwd (hashed via Docker)" \
    || { warn "Could not generate hashed passwd — using anonymous auth"; }
else
  warn "mosquitto_passwd not available — using anonymous auth (ok for local dev)"
fi

# ─── 6. Start Docker services ────────────────────────────────────────────────
step "Starting Docker services (Postgres, API, Keycloak, MQTT)"

cd "$ROOT/deploy"

docker compose \
  -f docker-compose.yml \
  -f "$ROOT/local-dev/docker-compose.override.yml" \
  --env-file .env \
  up -d postgres mosquitto

echo "  Waiting for Postgres to be healthy..."
for i in $(seq 1 20); do
  if docker exec cam_postgres pg_isready -U cam -d camplatform > /dev/null 2>&1; then
    ok "Postgres is ready"
    break
  fi
  sleep 2
  [ "$i" = "20" ] && { err "Postgres did not start in time"; exit 1; }
done

# ── Ensure database schema exists ────────────────────────────────────────────
# PostgreSQL only runs docker-entrypoint-initdb.d scripts on FIRST init (empty
# data volume). If the volume already existed from a previous run that had
# broken mounts or no init script, the tables won't exist.  Detect and apply.
TABLES_EXIST=$(docker exec cam_postgres psql -U cam -d camplatform -tAq \
  -c "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema='public' AND table_name='orgs'" 2>/dev/null || echo "0")

if [ "${TABLES_EXIST// /}" = "0" ]; then
  warn "Schema not found — applying init.sql to existing database"
  docker cp "$ROOT/deploy/sql/init.sql" cam_postgres:/tmp/init.sql
  docker exec cam_postgres psql -U cam -d camplatform -f /tmp/init.sql > /dev/null 2>&1 \
    && ok "Database schema applied successfully" \
    || { err "Failed to apply schema — try: ./local-dev/reset.sh --hard && ./local-dev/setup.sh"; exit 1; }
else
  ok "Database schema already exists"
fi

# Start API and Keycloak
docker compose \
  -f docker-compose.yml \
  -f "$ROOT/local-dev/docker-compose.override.yml" \
  --env-file .env \
  up -d api keycloak

echo "  Waiting for API..."
for i in $(seq 1 30); do
  if curl -fs http://localhost:3001/health > /dev/null 2>&1; then
    ok "API is ready at http://localhost:3001"
    break
  fi
  sleep 2
  [ "$i" = "30" ] && { err "API did not start"; docker logs cam_api | tail -20; exit 1; }
done

# ─── 7. Setup Keycloak ────────────────────────────────────────────────────────
step "Configuring Keycloak realm"

echo "  Waiting for Keycloak..."
for i in $(seq 1 40); do
  if curl -fs http://localhost:8080/health/ready > /dev/null 2>&1; then
    ok "Keycloak is ready at http://localhost:8080"
    break
  fi
  sleep 3
  [ "$i" = "40" ] && { err "Keycloak did not start — check: docker logs cam_keycloak"; exit 1; }
done

# Get admin token
KC_TOKEN=$(curl -s -X POST \
  "http://localhost:8080/realms/master/protocol/openid-connect/token" \
  -d "client_id=admin-cli&username=admin&password=admin&grant_type=password" \
  | jq -r '.access_token')

if [ -z "$KC_TOKEN" ] || [ "$KC_TOKEN" = "null" ]; then
  err "Could not get Keycloak admin token"
  exit 1
fi

# Check if realm already exists
REALM_EXISTS=$(curl -s -o /dev/null -w "%{http_code}" \
  -H "Authorization: Bearer $KC_TOKEN" \
  "http://localhost:8080/admin/realms/camplatform")

if [ "$REALM_EXISTS" = "200" ]; then
  warn "Keycloak realm 'camplatform' already exists — skipping import"
else
  # Import realm with local substitutions
  sed \
    -e 's|\${DOMAIN}|localhost|g' \
    -e 's|\${CAM_DASHBOARD_SECRET}|devsecret123|g' \
    -e 's|\${CAM_API_SECRET}|devapisecret123|g' \
    -e 's|\${GRAFANA_KC_SECRET}|devgrafanasecret|g' \
    -e 's|\${GOOGLE_CLIENT_ID}||g' \
    -e 's|\${GOOGLE_CLIENT_SECRET}||g' \
    -e 's|\${AZURE_CLIENT_ID}||g' \
    -e 's|\${AZURE_CLIENT_SECRET}||g' \
    -e 's|\${SMTP_HOST}|localhost|g' \
    -e 's|\${SMTP_PORT}|25|g' \
    -e 's|\${SMTP_USER}||g' \
    -e 's|\${SMTP_PASSWORD}||g' \
    "$ROOT/deploy/keycloak/realm.json" > /tmp/realm-local.json

  HTTP=$(curl -s -o /tmp/realm-response.txt -w "%{http_code}" \
    -X POST "http://localhost:8080/admin/realms" \
    -H "Authorization: Bearer $KC_TOKEN" \
    -H "Content-Type: application/json" \
    -d @/tmp/realm-local.json)

  if [ "$HTTP" = "201" ]; then
    ok "Keycloak realm imported"
  else
    err "Realm import returned HTTP $HTTP"
    cat /tmp/realm-response.txt
    exit 1
  fi
fi

# ─── 8. Create org and admin user ─────────────────────────────────────────────
step "Creating dev org and admin user"

# Create org in Postgres
ORG_EXISTS=$(docker exec cam_postgres psql -U cam -d camplatform -tAq \
  -c "SELECT COUNT(*) FROM orgs WHERE slug='dev-org'")

if [ "${ORG_EXISTS// /}" != "0" ]; then
  warn "Dev org already exists"
  ORG_ID=$(docker exec cam_postgres psql -U cam -d camplatform -tAq \
    -c "SELECT id FROM orgs WHERE slug='dev-org'")
else
  ORG_ID=$(docker exec cam_postgres psql -U cam -d camplatform -tAq \
    -c "INSERT INTO orgs (name,slug) VALUES ('Dev Org','dev-org') RETURNING id")
  ok "Created org: $ORG_ID"
fi

ORG_ID="${ORG_ID// /}"

# Create device record
DEVICE_KEY="dev-device-key-local"
DEV_EXISTS=$(docker exec cam_postgres psql -U cam -d camplatform -tAq \
  -c "SELECT COUNT(*) FROM devices WHERE device_key='$DEVICE_KEY'")

if [ "${DEV_EXISTS// /}" != "0" ]; then
  warn "Dev device already exists"
else
  docker exec cam_postgres psql -U cam -d camplatform -c \
    "INSERT INTO devices (org_id,name,device_key,status,frigate_url)
     VALUES ('$ORG_ID','Dev Mac NVR','$DEVICE_KEY','online','http://host.docker.internal:5000')"
  ok "Created device with key: $DEVICE_KEY"
fi

# Create Keycloak user
KC_TOKEN=$(curl -s -X POST \
  "http://localhost:8080/realms/master/protocol/openid-connect/token" \
  -d "client_id=admin-cli&username=admin&password=admin&grant_type=password" \
  | jq -r '.access_token')

USER_EXISTS=$(curl -s \
  "http://localhost:8080/admin/realms/camplatform/users?email=admin%40dev.local" \
  -H "Authorization: Bearer $KC_TOKEN" | jq 'length')

if [ "${USER_EXISTS// /}" != "0" ]; then
  warn "User admin@dev.local already exists"
else
  HTTP=$(curl -s -o /dev/null -w "%{http_code}" \
    -X POST "http://localhost:8080/admin/realms/camplatform/users" \
    -H "Authorization: Bearer $KC_TOKEN" \
    -H "Content-Type: application/json" \
    -d '{
      "username": "admin@dev.local",
      "email": "admin@dev.local",
      "enabled": true,
      "emailVerified": true,
      "credentials": [{"type":"password","value":"devpass123","temporary":false}],
      "attributes": {"org_id": ["'"$ORG_ID"'"]},
      "realmRoles": ["org-owner"]
    }')

  [ "$HTTP" = "201" ] && ok "Created user: admin@dev.local / devpass123" \
                       || err "User creation returned $HTTP"
fi

# ─── 9. Download sample video ─────────────────────────────────────────────────
step "Downloading sample video for RTSP simulation"

if [ ! -f /tmp/test-video.mp4 ]; then
  echo "  Downloading Big Buck Bunny (1MB sample)..."
  curl -L --progress-bar \
    "https://sample-videos.com/video321/mp4/720/big_buck_bunny_720p_1mb.mp4" \
    -o /tmp/test-video.mp4 \
    || curl -L --progress-bar \
      "https://www.learningcontainer.com/wp-content/uploads/2020/05/sample-mp4-file.mp4" \
      -o /tmp/test-video.mp4
  ok "Downloaded to /tmp/test-video.mp4"
else
  ok "Sample video already exists"
fi

# ─── Done ─────────────────────────────────────────────────────────────────────
echo ""
echo -e "${GREEN}════════════════════════════════════════════════════${NC}"
echo -e "${GREEN}  Setup complete!${NC}"
echo -e "${GREEN}════════════════════════════════════════════════════${NC}"
echo ""
echo "  Org ID:      $ORG_ID"
echo "  Device key:  $DEVICE_KEY"
echo ""
echo "  Next steps — run each in a new terminal tab:"
echo ""
echo "  1. Start RTSP simulator:"
echo "     ./local-dev/start-camera-sim.sh"
echo ""
echo "  2. Start edge agent:"
echo "     ./local-dev/start-agent.sh"
echo ""
echo "  3. Start Frigate NVR (after agent runs once):"
echo "     ./local-dev/start-frigate.sh"
echo ""
echo "  4. Start dashboard:"
echo "     cd dashboard && npm run dev"
echo ""
echo "  Then open: http://localhost:3000"
echo "  Login:     admin@dev.local / devpass123"
echo ""
echo "  Or run everything at once:"
echo "     ./local-dev/start-all.sh"
echo ""
