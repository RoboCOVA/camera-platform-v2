#!/usr/bin/env bash
set -euo pipefail

if [ $# -lt 1 ]; then
  echo "Usage: restore.sh <postgres_dump.sql.gz>"
  exit 1
fi

DUMP_FILE="$1"
if [ ! -f "$DUMP_FILE" ]; then
  echo "Dump not found: $DUMP_FILE"
  exit 1
fi

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ENV_FILE="${ENV_FILE:-$ROOT_DIR/.env}"

if [ ! -f "$ENV_FILE" ]; then
  echo "Missing env file: $ENV_FILE"
  exit 1
fi

source "$ENV_FILE"

echo "==> Restoring Postgres from $DUMP_FILE"
gunzip -c "$DUMP_FILE" | docker exec -i -e PGPASSWORD="$POSTGRES_PASSWORD" cam_postgres \
  psql -U cam -d camplatform

echo "==> Restore complete"
