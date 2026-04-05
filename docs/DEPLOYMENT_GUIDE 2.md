# Deployment Guide

## Purpose

Use this document as the step-by-step runbook for deploying `cam-platform` and bringing the first site online.

Use it when you need to:

- deploy the control plane
- prepare DNS, firewall, and secrets
- validate health and public URLs
- provision an edge device
- verify first camera onboarding

For production release decisions and day-2 operations, also use:

- [Production Deployment](./PRODUCTION_DEPLOYMENT.md)
- [Sizing Guide](./SIZING_GUIDE.md)
- [Operations Guide](./OPERATIONS_GUIDE.md)
- [Commissioning Checklist](./COMMISSIONING_CHECKLIST.md)
- [Incident Runbook](./RUNBOOK_INCIDENTS.md)

## Current Readiness

This repository now supports the full bootstrap and installer artifact flow:

- `deploy/setup.sh` builds Linux agent binaries for `amd64` and `arm64`
- the API serves those binaries from `/releases/...`
- device provisioning now attempts server-side WireGuard peer registration against the running `cam_wireguard` container

Current remaining rollout caution:

1. Live stream proxying still needs final end-to-end validation against a real remote Frigate instance over WireGuard.

That means this guide is valid for deployment and pilot rollout. Full internet-facing production signoff still depends on the live-stream validation gate in [Production Deployment](./PRODUCTION_DEPLOYMENT.md).

## Topology

Public services:

- `https://app.<domain>`
- `https://api.<domain>`
- `https://auth.<domain>`
- `https://metrics.<domain>`
- `udp/51820` for WireGuard

Core services:

- Postgres
- Keycloak
- API
- Dashboard
- Caddy
- Mosquitto
- WireGuard
- Prometheus
- Grafana

## Requirements

### Control plane host

- Ubuntu 24.04 LTS
- public static IP
- recommended minimum:
  - `4 vCPU`
  - `8 GB RAM`
  - `80 GB SSD`
- open inbound ports:
  - `80/tcp`
  - `443/tcp`
  - `443/udp`
  - `51820/udp`

### Edge host

- Ubuntu 22.04+ or Debian-compatible Linux
- Docker support
- WireGuard support
- LAN access to the camera subnet
- outbound internet access to:
  - `https://api.<domain>`
  - `https://auth.<domain>`
  - `udp/51820` on the control plane host

### DNS

Create A records for:

- `app.<domain>`
- `api.<domain>`
- `auth.<domain>`
- `metrics.<domain>`

All should point to the control plane server IP before the public stack starts.

## Phase 1: Prepare the Control Plane Server

### 1. Update the host

```bash
sudo apt-get update
sudo apt-get upgrade -y
sudo timedatectl set-timezone UTC
sudo apt-get install -y git curl
```

Recommended before bootstrap:

- create a non-root admin user
- restrict SSH to key-based auth
- confirm host time sync
- confirm no other process is using `80` or `443`

### 2. Clone the repo

```bash
git clone <your-repo-url> cam-platform
cd cam-platform
chmod +x deploy/setup.sh
```

## Phase 2: Run Bootstrap

### 1. Execute setup

```bash
sudo ./deploy/setup.sh yourdomain.com
```

This script now:

- installs Docker and WireGuard tools
- generates secrets into `deploy/.env`
- creates Mosquitto credentials
- generates WireGuard server keys
- builds `cam-agent` release binaries into `deploy/releases`
- renders the Keycloak realm config
- starts the compose stack with `docker compose up -d --build`

### 2. Save generated values

Record and vault:

- `deploy/.env`
- Keycloak admin password
- Grafana admin password
- `deploy/wireguard/server_private.key`
- `deploy/wireguard/server_public.key`

## Phase 3: Verify the Control Plane

### 1. Check containers

```bash
cd deploy
docker compose ps
```

Expected:

- `cam_postgres` healthy
- `cam_keycloak` healthy
- `cam_mosquitto` healthy
- `cam_api` healthy
- `cam_dashboard` running
- `cam_caddy` running
- `cam_wireguard` running

### 2. Check local health

```bash
curl -fs http://localhost:3001/health
docker logs cam_api --tail 100
docker logs cam_keycloak --tail 100
docker logs cam_caddy --tail 100
docker logs cam_wireguard --tail 100
```

### 3. Check release artifacts

Verify that the API can serve installer binaries:

```bash
curl -I http://localhost:3001/releases/cam-agent-linux-amd64
curl -I http://localhost:3001/releases/cam-agent-linux-arm64
```

Expected:

- both return `200 OK`

### 4. Check public URLs

Open in a browser:

- `https://app.<domain>`
- `https://auth.<domain>`
- `https://api.<domain>/health`

Expected:

- dashboard page loads
- Keycloak login page loads
- API health returns a success response
- certificates are trusted

## Phase 4: Post-Bootstrap Hardening

### 1. Firewall

Allow only:

- `80/tcp`
- `443/tcp`
- `443/udp`
- `51820/udp`
- SSH from admin IP ranges only

### 2. Restrict Grafana

Edit [`Caddyfile`](../deploy/caddy/Caddyfile) and restrict `metrics.<domain>` to trusted IPs, VPN, or internal access.

### 3. Backup plan

At minimum, back up:

- Postgres
- `deploy/.env`
- WireGuard keys
- Caddy data/config
- Keycloak config and identity data

### 4. Secret handling

- move secrets into a vault
- rotate any staging or shared credentials
- do not leave `.env` outside the admin boundary

## Phase 5: Prepare for Edge Deployment

Before installing the first edge device:

1. Log into the dashboard.
2. Create a provisioning token.
3. Confirm the token is associated with the correct org and site.
4. Confirm `cam_wireguard` is healthy and the API can access Docker on the host.

Useful checks:

```bash
docker ps --format '{{.Names}}'
docker logs cam_api --tail 100
```

## Phase 6: Install the Edge Device

Run on the edge host:

```bash
curl -fsSL https://api.<domain>/install | sudo bash -s <PROVISION_TOKEN>
```

Installer tasks:

- install Docker, WireGuard, ffmpeg, and `jq`
- create `/etc/cam/device.id`
- request device credentials from `/api/provision`
- configure `/etc/wireguard/wg0.conf`
- bring up the WireGuard tunnel
- install a local Mosquitto broker
- download `cam-agent` from `/releases/cam-agent-linux-<arch>`
- install and start `cam-agent.service`

## Phase 7: Validate the Edge Device

Run on the edge host:

```bash
systemctl status cam-agent
sudo wg show
docker ps
curl http://localhost:8090/health
```

Expected:

- `cam-agent` active
- WireGuard interface up
- local containers running
- health endpoint returns OK

## Phase 8: Validate Provisioning on the Control Plane

Run on the control plane:

```bash
docker logs cam_api --tail 100
docker exec cam_wireguard wg show
```

Expected:

- the new peer public key appears in `wg show`
- the provisioned device received a unique `10.10.0.x` address
- API logs do not show `wireguard peer registration failed`

## Phase 9: Validate Camera Onboarding

Expected workflow:

1. agent discovers cameras
2. agent posts cameras to `/api/devices/cameras`
3. cameras appear in the dashboard
4. snapshots work
5. playback path resolves through the API proxy
6. live stream is validated against a real remote Frigate instance

Useful checks:

```bash
docker logs cam_api --tail 100
journalctl -u cam-agent -f
```

## Phase 10: First-Site Acceptance

Before declaring the first site deployed:

- login works
- org and site are visible
- device provisions successfully
- WireGuard peer appears on the server
- cameras are listed
- snapshot works
- playback URL resolves through the proxy
- at least one live stream is verified
- one event appears in the dashboard
- retention target is set
- support contact is documented

## Rollback

If deployment fails:

1. stop rollout
2. restore the last known-good images or commit
3. restore Postgres if required
4. verify:
   - dashboard login
   - API health
   - Keycloak
   - site and camera visibility

## Quick Command Reference

### Control plane

```bash
cd deploy
docker compose ps
docker logs cam_api --tail 100
docker logs cam_wireguard --tail 100
curl -fs http://localhost:3001/health
docker exec cam_wireguard wg show
```

### Edge device

```bash
systemctl status cam-agent
journalctl -u cam-agent -f
sudo wg show
docker ps
curl http://localhost:8090/health
```
