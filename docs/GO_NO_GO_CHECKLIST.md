# Go/No-Go Checklist

All items must be checked before production launch.

## System Health

- API health endpoint returns 200
- Dashboard health endpoint returns 200
- Keycloak health is green
- Metrics are reachable from allowlisted IPs

## Data & Storage

- Postgres backup completed within last 24h
- Restore test completed in staging
- Disk usage alert thresholds configured

## Security

- `METRICS_ALLOWLIST` locked to trusted CIDRs
- Admin RBAC verified
- SSH locked to admin IP ranges
- Secrets stored in vault

## Operations

- Alerting rules deployed and validated
- On-call escalation path confirmed
- Commissioning runbook reviewed

## Pilot Proof

- Live streams verified at pilot site
- ANPR baseline accuracy measured
- Edge devices stable for 2+ weeks
