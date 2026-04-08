# QA Gate

This QA gate is the minimum quality bar before production rollout.

## Required Steps

1. Automated tests pass
2. Staging smoke checks pass
3. Load test baseline passed
4. Go/No-Go checklist signed

## How to Run

### Local QA gate

```bash
./deploy/qa/qa_gate.sh
```

### Staging smoke check

```bash
DOMAIN=example.com ./deploy/qa/staging_smoke.sh
```

### Load test baseline

```bash
API_BASE=https://api.example.com ./deploy/qa/load_test.sh
```

## CI

GitHub Actions workflow:

- `.github/workflows/qa.yml`
