#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ENV_FILE="${ENV_FILE:-$ROOT_DIR/.env}"
OUT_DIR="${OUT_DIR:-/var/backups/cam-platform}"
TS="$(date -u +%Y%m%dT%H%M%SZ)"

if [ ! -f "$ENV_FILE" ]; then
  echo "Missing env file: $ENV_FILE"
  exit 1
fi

mkdir -p "$OUT_DIR"

source "$ENV_FILE"

DB_DUMP="$OUT_DIR/postgres_${TS}.sql.gz"
CONFIG_TAR="$OUT_DIR/config_${TS}.tar.gz"

echo "==> Backing up Postgres to $DB_DUMP"
docker exec -e PGPASSWORD="$POSTGRES_PASSWORD" cam_postgres \
  pg_dump -U cam -d camplatform --no-owner --no-privileges \
  | gzip > "$DB_DUMP"

echo "==> Backing up config to $CONFIG_TAR"
tar -czf "$CONFIG_TAR" \
  -C "$ROOT_DIR" \
  .env wireguard caddy Caddyfile mosquitto prometheus keycloak || true

echo "==> Backup complete:"
echo "  $DB_DUMP"
echo "  $CONFIG_TAR"
