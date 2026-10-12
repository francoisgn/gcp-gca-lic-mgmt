# Local development

Requirements: Go 1.26+, Docker (PostgreSQL), Node only to regenerate the hero illustration.

## Everything in containers

```bash
docker compose up --build   # http://localhost:8080
```
PostgreSQL + directory simulator (HTTP, 5 % random failures) + portal (fake Google API, demo data, in-process scheduler).

## App on the host

```bash
make db     # PostgreSQL only (+ test database)
make run    # DEMO_SEED=true INPROCESS_SCHEDULER=true go run ./cmd/gcalm
```

## Demo data and identities

`DEMO_SEED=true` fills an **empty** database with 30 days of activity: 11 active licenses (one external) + one console-only license (pool of 12 → exhausted), 26 past licenses, 3 users waiting, escalations (one external), a user rejected twice, a buffered request, a DB-only deviation, the GPS codes allowlist, directory re-check flags (changed / not eligible / error), an e-mail journal, 10 days of technical metrics.

The yellow **DEV** bar switches identity:

| Identity | Shows |
|---|---|
| `super.admin@example.com` | everything + force refresh, jobs, simulation |
| `alex.martin@example.com` | admin pages |
| `olivia.brown@example.com` | active license |
| `noah.wilson@example.com` | waiting list |
| `mia.ko@example.com` | pending admin (case 0.1) |
| `liam.ko@example.com` | rejected twice (case 0.2) |
| `sofia.edslow@example.com` | buffer |
| `jane.doe@example.com`, `paul.new@example.com` | new request (pool exhausted) |
| `john.ko@`, `emma.nogps@`, `lucas.edslow@`, `nina.slow@example.com` | directory KO, no GPS, directory timeout, Google timeout |
| `tom.badgps@example.com` | GPS code not in the allowlist → escalation |
| `marc.dupont.ext@example.com` | external account: never auto-approved |
| `lisa.martin.ext@example.com` | external account waiting for an admin |

*Observability → Simulation* (super admin, fake mode): change the pool size, simulate a subscription renewal, remove a license "in the console" to create a DB_ONLY deviation.

Reset: drop the database and delete `.dev/fake-google.json`.
