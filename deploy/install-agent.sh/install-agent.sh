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

  anpr:
    build:
      context: /opt/cam/anpr
    container_name: cam_anpr
    restart: unless-stopped
    network_mode: host
    environment:
      MQTT_HOST: 127.0.0.1
      MQTT_PORT: 1883
      FRIGATE_TOPIC: frigate
      ANPR_TOPIC: anpr
      ANPR_LANG: eng
      ANPR_REGEX: ""
      ANPR_PRESET: et
      ANPR_REGEX_FILE: /app/plate_regex_et.txt
      ANPR_YOLO_MODEL: ""
    volumes:
      - /data/recordings:/media/frigate:ro
EOF

cat > /opt/cam/mosquitto.conf <<'EOF'
listener 1883 127.0.0.1
allow_anonymous true
EOF

mkdir -p /opt/cam
docker compose -f /opt/cam/docker-compose.yml up -d mosquitto
echo "==> Mosquitto MQTT broker started"

# ── 6b. Install ANPR sidecar (open-source OCR) ───────────────────────────────
mkdir -p /opt/cam/anpr
cat > /opt/cam/anpr/requirements.txt <<'EOF'
paho-mqtt==1.6.1
opencv-python-headless==4.9.0.80
pytesseract==0.3.10
numpy==1.26.4
EOF

cat > /opt/cam/anpr/Dockerfile <<'EOF'
FROM python:3.11-slim
RUN apt-get update -qq && apt-get install -y -qq \\
    tesseract-ocr \\
    tesseract-ocr-eng \\
    libglib2.0-0 libsm6 libxrender1 libxext6 \\
  && rm -rf /var/lib/apt/lists/*
WORKDIR /app
COPY requirements.txt /app/requirements.txt
RUN pip install --no-cache-dir -r requirements.txt
COPY anpr.py /app/anpr.py
COPY plate_regex_et.txt /app/plate_regex_et.txt
CMD ["python", "/app/anpr.py"]
EOF

cat > /opt/cam/anpr/plate_regex_et.txt <<'EOF'
# Ethiopia plate regex presets (starter patterns)
# Adjust to match regional formats
# Region / City codes (Latin)
AA|AF|AM|BG|DR|GM|HR|OR|SM|SP|TG|SD

# Common formats (starter)
# Region code + 4-5 digits (e.g., AA1234, OR12345)
(AA|AF|AM|BG|DR|GM|HR|OR|SM|SP|TG|SD)[0-9]{4,5}

# Digits + region code (e.g., 1234AA)
[0-9]{4,5}(AA|AF|AM|BG|DR|GM|HR|OR|SM|SP|TG|SD)

# Generic alnum fallback
[A-Z0-9]{5,7}

# Ge'ez labels (special plates)
ታክሲ
የግል
ንግድ
መንግሥት
ቀይ ?መስቀል
የተመ
አሕ
ተላላፊ
ፖሊስ
EOF

cat > /opt/cam/anpr/anpr.py <<'EOF'
import json
import os
import re
import time
from datetime import datetime

import cv2
import numpy as np
import pytesseract
import paho.mqtt.client as mqtt

MQTT_HOST = os.getenv("MQTT_HOST", "127.0.0.1")
MQTT_PORT = int(os.getenv("MQTT_PORT", "1883"))
FRIGATE_TOPIC = os.getenv("FRIGATE_TOPIC", "frigate")
ANPR_TOPIC = os.getenv("ANPR_TOPIC", "anpr")
ANPR_LANG = os.getenv("ANPR_LANG", "eng")
ANPR_REGEX = os.getenv("ANPR_REGEX", "")
ANPR_PRESET = os.getenv("ANPR_PRESET", "et")
ANPR_REGEX_FILE = os.getenv("ANPR_REGEX_FILE", "")
ANPR_YOLO_MODEL = os.getenv("ANPR_YOLO_MODEL", "")

PRESETS = {
    # Ethiopia (simple starter patterns; adjust with local data)
    "et": [
        r"[A-Z]{1,2}[0-9]{4,5}",
        r"[0-9]{4,5}[A-Z]{1,2}",
        r"[A-Z0-9]{5,7}",
    ],
    "generic": [r"[A-Z0-9]{4,10}"],
}

def compile_plate_regex():
    if ANPR_REGEX:
        return re.compile(ANPR_REGEX)
    if ANPR_REGEX_FILE and os.path.exists(ANPR_REGEX_FILE):
        try:
            patterns = []
            with open(ANPR_REGEX_FILE, "r", encoding="utf-8") as f:
                for line in f:
                    line = line.strip()
                    if not line or line.startswith("#"):
                        continue
                    patterns.append(line)
            if patterns:
                return re.compile("|".join(patterns))
        except Exception:
            pass
    patterns = PRESETS.get(ANPR_PRESET, PRESETS["generic"])
    return re.compile("|".join(patterns))

plate_re = compile_plate_regex()

def log(msg):
    print(f"[anpr] {msg}", flush=True)

def deskew(gray):
    coords = np.column_stack(np.where(gray > 0))
    if coords.size == 0:
        return gray
    angle = cv2.minAreaRect(coords)[-1]
    if angle < -45:
        angle = -(90 + angle)
    else:
        angle = -angle
    (h, w) = gray.shape[:2]
    M = cv2.getRotationMatrix2D((w // 2, h // 2), angle, 1.0)
    return cv2.warpAffine(gray, M, (w, h), flags=cv2.INTER_CUBIC, borderMode=cv2.BORDER_REPLICATE)

def preprocess(img):
    gray = cv2.cvtColor(img, cv2.COLOR_BGR2GRAY)
    gray = cv2.bilateralFilter(gray, 11, 17, 17)
    gray = deskew(gray)
    _, thresh = cv2.threshold(gray, 0, 255, cv2.THRESH_BINARY + cv2.THRESH_OTSU)
    return thresh

def crop_plate(img):
    if not ANPR_YOLO_MODEL:
        return img
    try:
        net = cv2.dnn.readNetFromONNX(ANPR_YOLO_MODEL)
        blob = cv2.dnn.blobFromImage(img, 1/255.0, (640, 640), swapRB=True, crop=False)
        net.setInput(blob)
        outputs = net.forward()
        # Simple max-confidence box selection (expects [x,y,w,h,conf] layout)
        best = None
        for det in outputs.reshape(-1, outputs.shape[-1]):
            conf = float(det[4])
            if conf < 0.4:
                continue
            x, y, w, h = det[0:4]
            if best is None or conf > best[0]:
                best = (conf, x, y, w, h)
        if best is None:
            return img
        _, x, y, w, h = best
        h_img, w_img = img.shape[:2]
        x1 = max(0, int((x - w / 2) * w_img / 640))
        y1 = max(0, int((y - h / 2) * h_img / 640))
        x2 = min(w_img, int((x + w / 2) * w_img / 640))
        y2 = min(h_img, int((y + h / 2) * h_img / 640))
        return img[y1:y2, x1:x2] if x2 > x1 and y2 > y1 else img
    except Exception:
        return img

def extract_text(image_path):
    img = cv2.imread(image_path)
    if img is None:
        return "", 0.0
    img = crop_plate(img)
    processed = preprocess(img)
    text = pytesseract.image_to_string(processed, lang=ANPR_LANG)
    text = re.sub(r"\\s+", "", text).upper()
    m = plate_re.search(text)
    if not m:
        return "", 0.0
    plate = m.group(0)
    conf = min(0.99, 0.5 + (len(plate) / 20.0))
    if plate_re.fullmatch(plate):
        conf = min(0.99, conf + 0.15)
    return plate, conf

def on_message(client, _userdata, msg):
    try:
        payload = json.loads(msg.payload.decode("utf-8"))
    except Exception:
        return

    # Expect Frigate event payloads with snapshot_path or snapshot
    snapshot_path = None
    after = payload.get("after") if isinstance(payload, dict) else None
    if isinstance(after, dict):
        snapshot_path = after.get("snapshot_path") or after.get("snapshot")

    if not snapshot_path:
        return

    # Frigate stores under /media/frigate
    if snapshot_path.startswith("/media/frigate"):
        image_path = snapshot_path
    else:
        image_path = "/media/frigate/" + snapshot_path.lstrip("/")

    plate, conf = extract_text(image_path)
    if not plate:
        return

    parts = msg.topic.split("/")
    if len(parts) < 2:
        return
    cam = parts[1]

    out = {
        "camera": cam,
        "plate_text": plate,
        "confidence": conf,
        "snapshot_path": snapshot_path,
        "timestamp": datetime.utcnow().isoformat() + "Z",
    }
    client.publish(f"{ANPR_TOPIC}/{cam}", json.dumps(out))

def main():
    client = mqtt.Client()
    client.on_message = on_message
    client.connect(MQTT_HOST, MQTT_PORT, 60)
    client.subscribe(f"{FRIGATE_TOPIC}/+/events")
    log(f"listening on {FRIGATE_TOPIC}/+/events")
    while True:
        client.loop()
        time.sleep(0.1)

if __name__ == "__main__":
    main()
EOF

echo "==> Building ANPR sidecar..."
docker compose -f /opt/cam/docker-compose.yml up -d anpr

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
