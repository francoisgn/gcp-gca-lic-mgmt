# Enterprise directory

The directory says whether a user can be approved automatically.

## Contract

```
GET {DIRECTORY_URL}/v1/accounts?email=jane.doe@example.com
Authorization: Bearer {DIRECTORY_API_KEY}

200 {"email":"jane.doe@example.com","status":"OK","site":"PRS","businessUnit":"412","gpsCode":"a3f09c1e"}
404 unknown account
```

| Field | Meaning |
|---|---|
| `status` | `OK` / `KO` — account status |
| `site` | attachment site (trigram) |
| `businessUnit` | business unit (3 digits) |
| `gpsCode` | GPS code (8 hexadecimal characters) — drives the auto-approval |

If the real directory exposes another contract, adapt `internal/directory/directory.go` (`HTTPClient.Lookup`) — the rest of the code only sees the `Account` struct.

## Outcomes

| Directory answer | Outcome | User sees |
|---|---|---|
| 200 + eligible (`policy.eligibility`) | ✅ OK | site, BU, masked GPS code, then submission |
| 200 + status KO / no GPS / bad format / not in allow-list | 🔴 functional KO | reason + *Withdraw* / *Escalate* |
| 404 | 🔴 functional KO | "account not found" |
| timeout (5 s), 5xx, 429, network, 401 | 🟠 technical KO → buffer | buffer position, retried every 7 min |

## Simulator

`DIRECTORY_MODE=simulated` (in-process) or the `cmd/edsim` HTTP server (docker compose). Deterministic per e-mail (site trigram, 3-digit BU, 8-hex GPS code). Markers in the local part (tokens separated by `.`, `+`, `-`, `_`) trigger edge cases:

| Marker | Example | Effect |
|---|---|---|
| `ko` | `john.ko@example.com` | account status KO |
| `nogps` | `emma.nogps@example.com` | empty GPS code |
| `ghost` | `max.ghost@example.com` | 404 |
| `edslow` | `lucas.edslow@example.com` | hangs → timeout → buffer |
| `ederr` | `a.ederr@example.com` | HTTP 503 → buffer |

`DIRECTORY_SIM_FAILURE_RATE` adds random 500 / 503 / timeouts (feeds the technical KPIs). The fake Google API has its own markers: `slow` (first assign hangs), `iamslow` (first credentials grant hangs), `idle` (never uses the license).
