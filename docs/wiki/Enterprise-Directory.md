# Enterprise directory

The directory says whether a user can be approved automatically.

## Contract

Default contract (all of it configurable in `config/settings.yaml`, see below):

```
GET https://directory.example.com/api/v1/accounts?email=jane.doe@example.com
Authorization: Bearer <key from Secret Manager>

200 {"email":"jane.doe@example.com","status":"OK","site":"PRS","businessUnit":"412","gpsCode":"a3f09c1e"}
404 unknown account
```

| Field | Meaning |
|---|---|
| `status` | `OK` / `KO` — account status |
| `site` | attachment site (trigram) |
| `businessUnit` | business unit (3 digits) |
| `gpsCode` | GPS code (8 hexadecimal characters) — drives the auto-approval |

## Plugging the real directory (no code change)

```yaml
# config/settings.yaml
directory:
  mode: http
  base_url: https://directory.corp.example/api
  timeout: 5s
  lookup:
    path: /v2/people/{email}        # {email} in the path, or a query parameter:
    email_param: email              # used when the path has no {email}
  auth:
    type: header                    # bearer (Authorization: Bearer …) | header | none
    header: X-API-Key
    secret_env: DIRECTORY_API_KEY   # value in Secret Manager: gcalm-directory-api-key
  fields:                           # where to read each value in the JSON answer
    status: account.status          # dotted paths for nested objects
    site: org.site
    business_unit: org.bu
    gps_code: org.gps
```

```bash
printf '%s' "$KEY" | gcloud secrets versions add gcalm-directory-api-key --data-file=-
```

Numbers are accepted for any field (a numeric business unit loses its leading zeros: prefer a string in the directory). A contract that does not fit (OAuth, mTLS, POST search…) needs a change in `internal/directory/directory.go` only — the rest of the code sees the `Account` struct.

## Outcomes

| Directory answer | Outcome | User sees |
|---|---|---|
| 200 + eligible (`policy.eligibility`) | ✅ OK | site, BU, masked GPS code, then submission |
| 200 + status KO / no GPS / bad format / GPS code not in the allowlist table | 🔴 functional KO | reason + *Withdraw* / *Escalate* |
| external account (`*.ext@`), whatever the answer | 🔴 functional KO | *External accounts are never approved automatically* + *Escalate* |
| 404 | 🔴 functional KO | "account not found" |
| timeout (5 s), 5xx, 429, network, 401 | 🟠 technical KO → **process stopped** | *"Enterprise directory unavailable — try again in a few minutes"*; nothing buffered |

## Simulator

`DIRECTORY_MODE=simulated` (in-process) or the `cmd/edsim` HTTP server (docker compose). Deterministic per e-mail (site trigram, 3-digit BU, 8-hex GPS code). Markers in the local part (tokens separated by `.`, `+`, `-`, `_`) trigger edge cases:

| Marker | Example | Effect |
|---|---|---|
| `ko` | `john.ko@example.com` | account status KO |
| `nogps` | `emma.nogps@example.com` | empty GPS code |
| `ghost` | `max.ghost@example.com` | 404 |
| `edslow` | `lucas.edslow@example.com` | hangs → timeout → "directory unavailable" |
| `ederr` | `a.ederr@example.com` | HTTP 503 → "directory unavailable" |
| `badgps` | `tom.badgps@example.com` | valid GPS code **not in the allowlist** (`0badc0de`) |

The simulator draws GPS codes from 12 fixed values (`directory.SimulatorGPSCodes`), all present in the allowlist of the demo data, so ordinary demo users are approved automatically.

`DIRECTORY_SIM_FAILURE_RATE` adds random 500 / 503 / timeouts (feeds the technical KPIs). The fake Google API has its own markers: `slow` (first assign hangs), `iamslow` (first credentials grant hangs), `idle` (never uses the license).

## Load and availability

Two sources of calls: **user requests** (step 5 of the form) and the **monthly re-check** (one call per active license per month, nightly, spread over the month, 4 calls in parallel at most). *Observability → Technical KPIs → Enterprise directory — load & availability* shows both per day with the availability (calls the directory answered — OK or unknown account — divided by all calls). Errors by type (timeout, 5xx, 4xx, network, rate limiting) are in the next chart. A drop of availability during the re-check window (02:30) means the directory does not hold the batch: lower `policy.directory_recheck.concurrency`.
