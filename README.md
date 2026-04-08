# cam-platform

Open, hybrid-cloud video surveillance platform. Works with any ONVIF IP camera.
No hardware lock-in. Footage stays on-prem.

## Architecture

```
[IP Cameras] → RTSP → [Edge Agent + Frigate NVR]
                              ↕ WireGuard VPN
                       [Cloud Control Plane]
                              ↕
                       [Dashboard / Mobile]
```

## Quick start

Detailed deployment and commissioning guide:

- [Deployment Guide](./docs/DEPLOYMENT_GUIDE.md)
- [Production Deployment](./docs/PRODUCTION_DEPLOYMENT.md)
- [Operations Guide](./docs/OPERATIONS_GUIDE.md)
- [Sizing Guide](./docs/SIZING_GUIDE.md)
- [Commissioning Checklist](./docs/COMMISSIONING_CHECKLIST.md)
- [Customer Site Game Plan](./docs/CUSTOMER_SITE_GAME_PLAN.md)
- [Government Traffic Monitoring Guide](./docs/GOV_TRAFFIC_MONITORING_GUIDE.md)
- [Ethiopia ANPR Training Recipe](./docs/ANPR_TRAINING_ETHIOPIA.md)
- [Ethiopia Execution Roadmap](./docs/EXECUTION_ROADMAP_ETHIOPIA.md)
- [QA Gate](./docs/QA_GATE.md)
- [Go/No-Go Checklist](./docs/GO_NO_GO_CHECKLIST.md)
- [Incident Runbook](./docs/RUNBOOK_INCIDENTS.md)

### 1. Deploy control plane (VPS — run once)

```bash
# Requires: Ubuntu 24.04, 2+ vCPU, 4GB RAM, ports 80/443/51820 open
# DNS: point app. api. auth. metrics. all to this server's IP

git clone https://github.com/yourorg/cam-platform
cd cam-platform
chmod +x deploy/setup.sh
sudo ./deploy/setup.sh yourdomain.com
```

### 2. Install edge agent (on-prem mini-PC — per site)

```bash
# Requires: Ubuntu 22.04+, 4+ core CPU, 8GB RAM, connected to camera network
# Get a provision token from: https://app.yourdomain.com → Settings → Devices

curl -fsSL https://api.yourdomain.com/install | sudo bash -s YOUR_PROVISION_TOKEN
```

That's it. Cameras appear in the dashboard within 60 seconds.

## Repository structure

```
cam-platform/
├── agent/                    # Edge agent (Go) — runs on customer mini-PC
│   ├── cmd/agent/main.go     # Entrypoint: discovery → frigate → heartbeat
│   ├── internal/
│   │   ├── discovery/        # ONVIF WS-Discovery + camera probing
│   │   └── frigate/          # Frigate config generator + process manager
│   └── go.mod
│
├── control-plane/            # Cloud API (Go) — runs on VPS
│   ├── cmd/api/main.go       # All routes, JWT middleware, WS hub, MQTT bridge
│   └── Dockerfile
│
├── dashboard/                # Next.js frontend
│   └── app/
│       └── cameras/page.tsx  # Camera grid with HLS live view
│
└── deploy/                   # Infrastructure
    ├── docker-compose.yml    # Full stack: postgres, keycloak, api, dashboard, caddy, wg
    ├── sql/init.sql          # Multi-tenant database schema
    ├── caddy/Caddyfile       # Auto-TLS reverse proxy config
    ├── mosquitto/            # MQTT broker config
    ├── setup.sh              # One-command VPS setup
    ├── install-agent.sh      # One-command agent install
    └── .env.example          # Environment template
```

## Key design decisions

**ONVIF + RTSP** — works with any IP camera brand. No proprietary protocols.

**Frigate NVR** — open-source recording engine with built-in AI detection.
Your value-add is the multi-tenant management layer and dashboard on top.

**WireGuard** — each on-prem site connects back to the cloud via a WireGuard
tunnel. The cloud API proxies HLS streams through itself — the browser never
gets a direct connection to the customer's network.

**Multi-tenant from day one** — every DB table has `org_id`. Every API call
validates org scope. Keycloak handles SSO per org.

**Hybrid by default** — video stays on-prem. Only events/metadata go to cloud.
Customers who want cloud archiving opt in via S3 config.

## Camera compatibility

Tested with:
- Hikvision (DS-2CD series)
- Dahua (IPC-HDW series)
- Axis (P and Q series)
- Hanwha (QNV series)
- Reolink (RLC series — partial ONVIF)
- Amcrest (IP series)
- Uniview (IPC series)
- Generic ONVIF cameras

## Adding AI detection hardware

Edit `deploy/docker-compose.yml` → frigate service, and set:

```yaml
# For Google Coral USB:
environment:
  - DETECTOR_TYPE=coral
  - DETECTOR_DEVICE=usb

# For Intel integrated GPU (most mini-PCs):
# Add to frigate.yml: hwaccel_args: preset-vaapi
```

## License

Apache 2.0 — use freely, build commercially, keep the attribution.
