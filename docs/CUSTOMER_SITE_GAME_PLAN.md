# Customer Site Provisioning & Commissioning Guide (Single Site Game Plan)

This guide covers a single customer site end-to-end: from pre-checks through live verification and handoff. Use it as the default runbook for the first site of each new customer.

## Scope

- One physical site (one edge box + 1–N cameras)
- Control plane already deployed
- VPN/WireGuard online
- Dashboard operational

## Roles

- Operator: performs provisioning and commissioning steps
- Customer IT: provides network access and camera credentials

## Definitions

- Control Plane: cloud stack (API, dashboard, Keycloak)
- Edge Box: on-prem mini-PC running the agent + Frigate
- Site: physical location inside an org

---

## Phase 0: Pre-Flight (1–3 days before install)

### Inputs you must collect

- Site name and address
- Customer VLAN/subnet for cameras
- Camera credentials (admin user/pass for each brand)
- Number of cameras and their IPs (if static)
- Network path to internet for edge box
- Power and mounting location for edge box

### Control plane readiness checklist

- DNS resolves for `app`, `api`, `auth`, `metrics`
- TLS green on `https://app.<domain>`
- Admin account works in dashboard
- `METRICS_ALLOWLIST` tightened
- Backups scheduled and tested at least once

---

## Phase 1: Site Setup (Day of install)

### 1. Physical install

- Mount edge box in secure rack or locked cabinet
- Connect edge box to camera VLAN switch
- Connect edge box to internet uplink
- Confirm stable power and UPS if available

### 2. OS validation on edge box

- Ubuntu 22.04+ installed and updated
- Disk space sufficient for retention (target size based on cameras and days)
- CPU and RAM meet minimum (4 cores, 8 GB RAM)

---

## Phase 2: Provisioning (Control plane + Site)

### 1. Create site in dashboard

- Navigate to `Sites`
- Create site with timezone and address

### 2. Create provisioning token

- Navigate to `Settings → Devices`
- Create a provisioning token for this site
- Record token for install

### 3. Install the edge agent

On the edge box, run:

```bash
curl -fsSL https://api.<domain>/install | sudo bash -s <PROVISION_TOKEN>
```

Expected output:

- WireGuard tunnel up
- Agent running via systemd
- Frigate container started

### 4. Validate edge box status

Run on edge box:

```bash
systemctl status cam-agent
journalctl -u cam-agent -n 50 --no-pager
curl http://localhost:8090/health
```

Expected:

- Health returns `status: ok`
- Cameras discovered count matches expected

---

## Phase 3: Camera Discovery & Validation

### 1. Camera discovery

- Ensure camera credentials are set in `/etc/cam/agent.env`
- Agent should discover cameras within 60 seconds

If cameras are missing:

- Confirm VLAN reachability from edge box
- Verify ONVIF is enabled on cameras
- Try subnet discovery via `CAM_SUBNET=<cidr>` in `/etc/cam/agent.env`

### 2. Verify dashboard camera list

- `Cameras` page should populate
- Each camera should show Online within 1–2 minutes

---

## Phase 4: Live Stream Validation (Critical)

### 1. Live view

For each camera:

- Open camera tile in dashboard
- Verify live stream is visible
- Confirm latency is acceptable

### 2. Snapshot

- Verify snapshot endpoint loads

### 3. Event pipeline

- Trigger motion in front of a camera
- Confirm event appears in `Events` page

---

## Phase 5: Commissioning Acceptance

### Checklist

- Edge agent healthy and running
- All cameras discovered
- Live streams visible
- Events recording and appearing
- Customer admin can log in

### Sign-off

- Record commissioning time, operator, and site details
- Capture a screenshot of camera grid for evidence

---

## Phase 6: Handoff to Customer

### Deliverables

- Admin login credentials
- Site map or camera inventory list
- Support contact and escalation path

### Customer training basics

- How to view live streams
- How to acknowledge events
- How to add another site or device

---

## Troubleshooting Quick Reference

- No cameras discovered: check VLAN routing and ONVIF enabled
- Live stream fails: verify `frigate_name` and WireGuard link
- Device offline: check `wg-quick@wg0` status on edge box

---

## Post-Install Follow-up (48–72 hours)

- Check agent logs for discovery failures
- Validate disk usage and retention targets
- Confirm backups and alerting are live

