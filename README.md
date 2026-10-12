# GCA License Portal

[![CI](https://github.com/francoisgn/gcp-gca-lic-mgmt/actions/workflows/ci.yml/badge.svg)](https://github.com/francoisgn/gcp-gca-lic-mgmt/actions/workflows/ci.yml)
[![License: Apache-2.0](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](LICENSE)
[![Go](https://img.shields.io/badge/Go-1.26-00ADD8?logo=go&logoColor=white)](go.mod)
[![Buy Me a Coffee](https://img.shields.io/badge/Buy%20me%20a%20coffee-%E2%98%95-FFDD00?logo=buymeacoffee&logoColor=black)](https://buymeacoffee.com/francoisgn)

**Self-service portal to govern Gemini Code Assist Standard / Enterprise licenses in a Google Cloud organization**: eligible employees get a license in seconds, the pool is reclaimed automatically, administrators keep control and Google stays the source of truth.

> **Status: alpha** — `0.1.0-alpha.1` ([changelog](CHANGELOG.md)). Complete feature set validated with the simulators; not yet run against the real Google APIs and directory.

![Landing page](docs/wiki/images/landing-active.png)

## Features

- **Self-service** — progressive request form (training, duration, usage details), automatic approval when the enterprise directory GPS code is in the allowlist, license + IAM credentials + confirmation e-mail in under 10 seconds.
- **Fair pool** — waiting list when the pool is exhausted, priority buffer when Google or the directory time out (retried every 7 min), removal after 7 days without usage or at expiry.
- **Admin control** — escalations (external `*.ext@` accounts always go through an admin), approve / reject with history, manual assignment, atomic transfer, criteria-based mass removal, GPS code allowlist managed in the UI.
- **Continuous compliance** — monthly re-check of every license against the enterprise directory (assignment anniversary), drift and "no longer eligible" flags in the inventory.
- **Subscription lifecycle** — the newest subscription wins (checked every 3 h), licenses migrate with the most recent users first; deviations with Google tracked until resolved; cloud roles reconciled with the licenses so they never accumulate.
- **Observability** — license flow waterfall, queue / buffer / deviation depth, external license ratio, directory errors and response times, e-mail journal.

**Stack**: Go 1.26 · Cloud Run · Identity-Aware Proxy · Cloud SQL for PostgreSQL 16 · Cloud Scheduler · Secret Manager · Cloud Commerce Consumer Procurement API · Cloud Identity Groups API · Terraform.

## Try it in 1 minute (everything simulated)

```bash
git clone https://github.com/francoisgn/gcp-gca-lic-mgmt && cd gcp-gca-lic-mgmt
docker compose up --build        # → http://localhost:8080
```

Fake Google API, simulated enterprise directory and 30 days of demo data. The yellow **DEV** bar switches identity (super admin, admin, users in every state).

## Develop

```bash
make db        # PostgreSQL (docker compose)
make run       # portal on :8080 with demo data
make test-int  # unit + integration tests (race detector)
make lint      # gofmt + go vet
```

## Documentation

The documentation lives in the **[wiki](../../wiki)** — architecture and decisions, functional specification, notifications, Google APIs and permissions, enterprise directory, KPIs, security, configuration, deployment, runbook, CI. Sources: [`docs/wiki/`](docs/wiki). Design language: [`DESIGN.md`](DESIGN.md).

## Contributing & security

Issues and pull requests are welcome. Please report vulnerabilities privately (see [SECURITY.md](SECURITY.md)).

## Support

If this project saves you time, you can [buy me a coffee ☕](https://buymeacoffee.com/francoisgn).

## License

[Apache License 2.0](LICENSE). Google, Google Cloud, Gemini and Gemini Code Assist are trademarks of Google LLC; this project is not affiliated with or endorsed by Google (see [NOTICE](NOTICE)).
