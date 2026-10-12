# Testing

```bash
make test       # unit tests (integration tests skipped)
make db         # PostgreSQL in docker compose
make test-int   # all tests against PostgreSQL, race detector on (TEST_DATABASE_URL)
```

| Level | Package | Covers |
|---|---|---|
| Unit | `domain`, `policy`, `roles`, `telemetry` | durations, steps, eligibility rules, external accounts, notification switches, YAML files of the repository, error classification |
| Unit | `config` | strict parsing of `settings*.yaml`, precedence env > YAML, secrets read from the variable named in the file, missing directory key refused |
| Unit | `directory` | simulator determinism and markers, HTTP client ⇄ simulator contract (httptest), configurable path / auth / field mapping |
| Unit | `gcp` | fake pool capacity, idempotent assign, persistence, rollover, slow marker |
| Integration | `service` (real PostgreSQL + fakes) | automatic assignment, server-side validation, queue order and serving, escalation / approval / rejection history, directory unavailable (process stopped, nothing buffered, retry allowed), Google timeout buffer with seat reservation, **no global lock during Google calls**, purge (unused / expired), subscription rollover (newest subscription wins, most recent users kept, requeue e-mail with position), **IAM bundle reconciliation** (revocation at end of license, stray member removed, missing roles restored, GOOGLE_ONLY kept), deviations detection / resolution, atomic transfer, mass removal guard, telemetry, **external accounts** (refused, escalated, approved by an admin), **GPS allowlist** (add / remove / effect on eligibility), **notifications** (one vs two e-mails on approval, journal, disabled events), **monthly directory re-check** (anniversary selection, drift, ineligible, error, acknowledge) |
| Integration | `web` | every demo identity renders its landing state, authorization matrix, CSRF, rate limiting, security headers |

CI: see [[Continuous Integration]] (lint, vulnerabilities, tests with PostgreSQL and `-race`, coverage summary, Terraform, image, secret scan).

Manual / visual checks were done with Playwright (Chromium) on desktop (1440 px) and mobile (390 px) for every landing state, the admin and observability pages and the full request flows (eligible, functional KO + escalation, Google timeout).
