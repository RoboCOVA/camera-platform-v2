#!/usr/bin/env bash
# local-dev/start-camera-sim.sh
# Starts a fake RTSP camera server using MediaMTX + ffmpeg.
# Streams a test video (or your webcam) as if it were an IP camera.
set -euo pipefail

cd "$(dirname "$0")/.."

GREEN='\033[0;32m'; YELLOW='\033[1;33m'; NC='\033[0m'
ok()   { echo -e "  ${GREEN}✓${NC} $1"; }
warn() { echo -e "  ${YELLOW}!${NC} $1"; }

MODE="${1:-video}"   # video | webcam | public

echo -e "\n${GREEN}==> Starting RTSP camera simulator (mode: $MODE)${NC}"

# Stop any previous MediaMTX instance
docker rm -f cam-mediamtx 2>/dev/null || true

# Start MediaMTX RTSP server
docker run -d \
  --name cam-mediamtx \
  --restart unless-stopped \
  -p 8554:8554 \
  -p 1935:1935 \
  -p 8888:8888 \
  bluenviron/mediamtx:latest

ok "MediaMTX RTSP server started on port 8554"
sleep 2

case "$MODE" in

  video)
    # Stream a looping test video
    if [ ! -f /tmp/test-video.mp4 ]; then
      echo "  Downloading sample video..."
      curl -sL \
        "https://www.learningcontainer.com/wp-content/uploads/2020/05/sample-mp4-file.mp4" \
        -o /tmp/test-video.mp4
    fi

    echo "  Streaming test video to rtsp://localhost:8554/cam1 and /cam2..."
    echo "  Press Ctrl-C to stop."

    # Stream two "cameras" from the same video (different stream paths)
    ffmpeg -re -stream_loop -1 \
      -i /tmp/test-video.mp4 \
      -map 0:v -c:v libx264 -preset ultrafast -tune zerolatency \
        -b:v 1M -g 30 -rtsp_transport tcp -f rtsp rtsp://localhost:8554/cam1 \
      -map 0:v -c:v libx264 -preset ultrafast -tune zerolatency \
        -b:v 500k -vf scale=640:360 -g 30 -rtsp_transport tcp -f rtsp rtsp://localhost:8554/cam2 \
      -nostdin -loglevel warning
    ;;

  webcam)
    # Stream your MacBook's FaceTime camera
    echo ""
    echo "  Available cameras:"
    ffmpeg -f avfoundation -list_devices true -i "" 2>&1 \
      | grep -E "AVFoundation.*\[" | head -10 || true
    echo ""

    DEVICE="${WEBCAM_INDEX:-0}"
    echo "  Streaming FaceTime camera [${DEVICE}] to rtsp://localhost:8554/webcam"
    echo "  Set WEBCAM_INDEX=1 if wrong camera. Press Ctrl-C to stop."

    ffmpeg \
      -f avfoundation -framerate 15 -video_size 1280x720 -i "${DEVICE}" \
      -c:v libx264 -preset ultrafast -tune zerolatency \
      -b:v 1M -g 30 \
      -rtsp_transport tcp -f rtsp rtsp://localhost:8554/webcam \
      -nostdin -loglevel warning
    ;;

  public)
    # Use a public RTSP stream (no local ffmpeg needed)
    echo ""
    echo "  Using public RTSP test stream — no local video needed."
    echo ""
    echo "  Add this URL to your cameras.json:"
    echo "  rtsp://wowzaec2demo.streamlock.net/vod/mp4:BigBuckBunny_115k.mp4"
    echo ""
    echo "  Update local-dev/cameras.json with:"
    cat << 'JEOF'
[{
  "id": "public-cam-001",
  "name": "Public Test Stream",
  "manufacturer": "Test",
  "model": "Public",
  "serial": "PUB-001",
  "ip": "wowzaec2demo.streamlock.net",
  "main_stream_url": "rtsp://wowzaec2demo.streamlock.net/vod/mp4:BigBuckBunny_115k.mp4",
  "sub_stream_url": "rtsp://wowzaec2demo.streamlock.net/vod/mp4:BigBuckBunny_115k.mp4",
  "width": 320,
  "height": 240
}]
JEOF
    ;;

  *)
    echo "Usage: $0 [video|webcam|public]"
    exit 1
    ;;
esac
