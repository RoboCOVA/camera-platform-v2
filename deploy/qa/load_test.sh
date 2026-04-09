#!/usr/bin/env bash
set -euo pipefail

API_BASE="${API_BASE:-}"
API_TOKEN="${API_TOKEN:-}"
if [ -z "$API_BASE" ]; then
  echo "Usage: API_BASE=https://api.example.com [API_TOKEN=token] $0"
  exit 1
fi

if ! command -v k6 >/dev/null 2>&1; then
  echo "k6 not installed. Install from https://k6.io/docs/get-started/installation/"
  exit 1
fi

SCRIPT="$(dirname "$0")/load_test.js"
if [ -n "$API_TOKEN" ]; then
  SCRIPT="$(dirname "$0")/load_test_auth.js"
fi

k6 run -e API_BASE="$API_BASE" -e API_TOKEN="$API_TOKEN" "$SCRIPT"
