# Government Traffic Monitoring Deployment Guide

This document provides a full production-ready blueprint for government traffic monitoring deployments using the cam-platform. It includes architecture, regional hub design, compliance considerations, redundancy, and large-scale Frigate guidance.

## 1. Executive Summary

Recommended architecture:

- **Per-site edge deployment** (Agent + Frigate + local storage)
- **Optional regional hubs** for aggregation at scale
- **Central control plane** for governance, dashboards, RBAC, and analytics

Frigate should remain **site-local**. Only metadata, events, and clips are aggregated centrally.

## 2. Reference Architecture (Gov Scale)

```
[Traffic Cameras VLAN] → [Edge Node + Frigate]
                               |
                          WireGuard VPN
                               |
                     [Regional Hub (optional)]
                               |
                     [Central Control Plane]
                               |
                    [Gov SOC / Ops Dashboard]
```

### Core Components

- **Cameras (ONVIF/RTSP)**: isolated on Traffic VLAN
- **Edge Node**: on-prem compute with Frigate + Agent
- **Regional Hub** (optional): aggregation + cache
- **Control Plane**: API, Dashboard, IAM, Metrics
- **Operations Center**: SOC access to live view and events

## 3. Network Segmentation

Recommended segmentation:

- **Traffic VLAN**: cameras + edge only
- **Edge Management VLAN**: OS management, updates
- **Gov Core / SOC VLAN**: dashboards and analytics

Security rules:

- No inbound access to cameras from public internet
- All edge traffic flows outbound only through WireGuard
- RBAC enforced by org, role, and site

## 4. Regional Hub Design (Optional)

Use regional hubs when:

- Sites > ~100
- WAN latency is high
- You need localized operations

Regional hub responsibilities:

- Aggregate events/metadata
- Cache clips/snapshots
- VPN concentrator
- Local observability stack

Typical hub services:

- WireGuard
- Event cache (Redis or lightweight MQ)
- Optional local dashboard
- Prometheus/Grafana for regional metrics

## 5. Compliance Checklist (Gov)

Minimum compliance baseline:

- TLS everywhere
- RBAC with least privilege
- Audit logging enabled
- Retention policy enforced
- Encrypted backups

Alignments:

- **NIST 800-53**: access control, logging, incident response
- **CJIS** (if law enforcement): audit, encryption, retention
- **ISO 27001**: policy-based access, risk management

## 6. Redundancy & Failover Plan

Edge:

- Continues local recording if central is down
- Buffers events and resyncs when online

Regional Hub:

- Active/standby VPN concentrator
- Event buffering to prevent loss

Central:

- Postgres replication + tested restores
- Blue/green deployments
- Multi-zone redundancy for control plane

## 7. Large-Scale Frigate Guidance

### What happens at large scale?

Frigate is **not designed** to run centrally for thousands of cameras. Centralizing video streams causes:

- WAN bandwidth overload
- High latency for inference
- Massive GPU/CPU requirements
- Single point of failure

### Correct scaling approach

- **Deploy Frigate per site**
- **Centralize only metadata + snapshots**
- Use **regional hubs** for large scale

### Workarounds for very large deployments

- **Event-only uplink** (no continuous video)
- **Clip-on-demand** retrieval for investigations
- **Adaptive streaming** (low bitrate for central dashboards)

## 8. Multi-Site Deployment Model

Recommended:

- Each site: edge node + cameras
- Central: dashboard + governance
- Optional regional hub per district

Multi-site effects:

- More WireGuard peers
- Higher event volume
- Need for alerting and monitoring at scale

Workarounds:

- Group sites by region
- Automate provisioning
- Use regional hubs to reduce WAN traffic

## 9. Deployment Game Plan (Single Site)

Use `docs/CUSTOMER_SITE_GAME_PLAN.md` for step-by-step commissioning of one site.

## 10. Operational Readiness Checklist

Before production rollout:

- Backups scheduled and restore tested
- Alerting rules deployed and validated
- Metrics allowlisted
- Incident runbook ready
- Pilot site validated end-to-end

