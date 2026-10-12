# Testing

```bash
make test       # unit tests (integration tests skipped)
make db         # PostgreSQL in docker compose
make test-int   # all tests against PostgreSQL, race detector on (TEST_DATABASE_URL)
```

| Level | Package | Covers |
|---|---|---|
| Unit | `domain`, `policy`, `roles`, `telemetry` | durations, steps, eligibility rules, YAML files of the repository, error classification |
| Unit | `directory` | simulator determinism and markers, HTTP client ⇄ simulator contract (httptest) |
| Unit | `gcp` | fake pool capacity, idempotent assign, persistence, rollover, slow marker |
| Integration | `service` (real PostgreSQL + fakes) | automatic assignment, server-side validation, queue order and serving, escalation / approval / rejection history, directory timeout buffer, Google timeout buffer with seat reservation, **no global lock during Google calls**, purge (unused / expired), subscription rollover with overflow, deviations detection / resolution, atomic transfer, mass removal guard, telemetry |
| Integration | `web` | every demo identity renders its landing state, authorization matrix, CSRF, rate limiting, security headers |

CI (`.github/workflows/ci.yml`): gofmt, `go vet`, full test suite with a PostgreSQL service and `-race`, container build.

Manual / visual checks were done with Playwright (Chromium) on desktop (1440 px) and mobile (390 px) for every landing state, the admin and observability pages and the full request flows (eligible, functional KO + escalation, Google timeout).
