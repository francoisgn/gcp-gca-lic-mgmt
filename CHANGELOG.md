# Changelog

All notable changes of the GCA License Portal. Format: [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), versions: [Semantic Versioning](https://semver.org/). Design changes are tracked separately in [DESIGN.md](DESIGN.md).

## [0.1.0-alpha.1] - 2026-10-12

First alpha: complete feature set, validated with the simulators (fake Google API, simulated enterprise directory) — **not yet run against the real Google APIs and directory**.

### Self-service
- Landing page showing the state of the user's license (active, buffer, waiting list, pending admin, rejected, new) with a numbered progress panel.
- Progressive request form: identity, training, duration (7 days → 1 year), usage questions from `policy.yaml`, enterprise directory validation, escalation to an administrator.
- Automatic approval: directory lookup → GPS code → GPS code in the allowlist managed by admins. External accounts (`*.ext@`) are never auto-approved.
- License, cloud roles and confirmation e-mail in under 10 seconds; Google timeouts go to a priority buffer retried every 7 minutes with the seat kept.
- Directory unavailable: the request stops with a clear message, the user tries again.

### Automation
- Waiting list served as soon as a seat is free (renewal > buffer > normal priority).
- Removal after 7 days without usage and at expiry.
- Subscription refresh every 3 hours: the newest subscription wins, licenses migrate (most recent users first when the pool shrinks, the others queued with their position by e-mail), deviations with Google tracked until resolved.
- Cloud roles (IAM bundle group) removed at every end of license and reconciled with the licenses every 3 hours.
- Monthly re-check of every license against the directory (assignment anniversary): data drift and "no longer eligible" flags.
- E-mail at every step change, with a journal.

### Administration and observability
- Approvals, waiting list, buffer, assign and atomic transfer, GPS codes allowlist, e-mail journal, audit.
- Observability: license flows and period waterfall, queue / buffer / deviation depth, external license ratio, directory drift, inventory with filters and mass removal, technical KPIs (directory load and availability, errors, response times, timeouts, Google calls).

### Platform
- Go 1.26, Cloud Run (portal behind IAP + worker), Cloud SQL for PostgreSQL 16, Cloud Scheduler, Secret Manager; Terraform for everything, including the least-privilege authorization of the portal.
- Versioned integration settings (`config/settings.yaml`), secrets only in Secret Manager.
- CI: lint, vulnerability scan, race-detector tests on PostgreSQL, Terraform validation, container image on GHCR, secret scan; GitHub release from this changelog on every version tag.

### Known limitations
- E-mail templates are mock-ups (corporate versions to come).
- Questions of the request form are placeholders (`config/policy.yaml`).
- Open points: see the *Open Questions* page of the wiki.

[0.1.0-alpha.1]: https://github.com/francoisgn/gcp-gca-lic-mgmt/releases/tag/v0.1.0-alpha.1
