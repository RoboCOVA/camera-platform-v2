#!/usr/bin/env bash
# local-dev/start-all.sh
# Starts the entire cam-platform stack in split terminal panes.
# Uses tmux if available, otherwise prints instructions for manual tabs.
set -euo pipefail

cd "$(dirname "$0")/.."
ROOT="$(pwd)"

GREEN='\033[0;32m'; YELLOW='\033[1;33m'; NC='\033[0m'

echo -e "\n${GREEN}==> Starting cam-platform (all services)${NC}\n"

# Check setup was run
if [ ! -f deploy/.env ]; then
  echo "Run setup first: ./local-dev/setup.sh"
  exit 1
fi

if command -v tmux > /dev/null 2>&1; then
  # ── tmux mode ──────────────────────────────────────────────────────────────
  SESSION="cam-platform"

  # Kill existing session
  tmux kill-session -t "$SESSION" 2>/dev/null || true

  # Create new session with 5 panes
  tmux new-session -d -s "$SESSION" -x 220 -y 50

  # Pane 0: Docker backend
  tmux rename-window -t "$SESSION:0" "backend"
  tmux send-keys -t "$SESSION:0" \
    "cd $ROOT/deploy && docker compose -f docker-compose.yml -f $ROOT/local-dev/docker-compose.override.yml --env-file .env up postgres mosquitto api keycloak 2>&1 | grep -v '^$'" \
    Enter

  # Pane 1: Camera simulator
  tmux new-window -t "$SESSION" -n "camera-sim"
  tmux send-keys -t "$SESSION:1" \
    "sleep 5 && $ROOT/local-dev/start-camera-sim.sh video" \
    Enter

  # Pane 2: Edge agent
  tmux new-window -t "$SESSION" -n "agent"
  tmux send-keys -t "$SESSION:2" \
    "sleep 10 && $ROOT/local-dev/start-agent.sh" \
    Enter

  # Pane 3: Frigate
  tmux new-window -t "$SESSION" -n "frigate"
  tmux send-keys -t "$SESSION:3" \
    "sleep 20 && $ROOT/local-dev/start-frigate.sh" \
    Enter

  # Pane 4: Dashboard
  tmux new-window -t "$SESSION" -n "dashboard"
  tmux send-keys -t "$SESSION:4" \
    "cd $ROOT/dashboard && npm run dev" \
    Enter

  tmux attach-session -t "$SESSION"

else
  # ── Manual mode ────────────────────────────────────────────────────────────
  echo "tmux not found. Install it with: brew install tmux"
  echo "Or open 5 terminal tabs and run each command:"
  echo ""
  echo -e "${GREEN}Tab 1 — Backend${NC}"
  echo "  cd $ROOT/deploy"
  echo "  docker compose -f docker-compose.yml -f $ROOT/local-dev/docker-compose.override.yml --env-file .env up"
  echo ""
  echo -e "${GREEN}Tab 2 — Camera simulator${NC}"
  echo "  $ROOT/local-dev/start-camera-sim.sh"
  echo ""
  echo -e "${GREEN}Tab 3 — Edge agent${NC}"
  echo "  $ROOT/local-dev/start-agent.sh"
  echo ""
  echo -e "${GREEN}Tab 4 — Frigate NVR (after agent runs once)${NC}"
  echo "  $ROOT/local-dev/start-frigate.sh"
  echo ""
  echo -e "${GREEN}Tab 5 — Dashboard${NC}"
  echo "  cd $ROOT/dashboard && npm run dev"
  echo ""
  echo "Then open: http://localhost:3000"
  echo "Login:     admin@dev.local / devpass123"

  # Install tmux suggestion
  echo ""
  echo -e "${YELLOW}Tip: brew install tmux${NC} to run everything in one terminal."
fi
