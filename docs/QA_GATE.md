# QA Gate

This QA gate is the minimum quality bar before production rollout.

## Required Steps

1. Automated tests pass
2. Staging smoke checks pass
3. Load test baseline passed (unauth + auth)
4. Go/No-Go checklist signed

## How to Run

### Local QA gate

```bash
./deploy/qa/qa_gate.sh
```

To include Playwright E2E in the gate:

```bash
RUN_E2E=1 ./deploy/qa/qa_gate.sh
```

### Staging smoke check

```bash
DOMAIN=example.com ./deploy/qa/staging_smoke.sh
```

### Load test baseline

```bash
API_BASE=https://api.example.com ./deploy/qa/load_test.sh
```

Authenticated load test:

```bash
API_BASE=https://api.example.com API_TOKEN=token ./deploy/qa/load_test.sh
```

### Synthetic edge device simulation (staging)

```bash
API_BASE=https://api.example.com DEVICE_KEY=devkey_xxx ./deploy/qa/edge_sim.sh
```

### Dashboard E2E (Playwright)

```bash
cd dashboard
npm run e2e
```

Set `E2E_BASE_URL` to run against a non-local environment (staging).
Install browsers once with `npx playwright install`.

## CI

GitHub Actions workflow:

- `.github/workflows/qa.yml`
