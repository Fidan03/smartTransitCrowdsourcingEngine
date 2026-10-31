# smartTransitCrowdsourcingEngine

**Smart Transit & Crowdsourcing Engine**: a functional prototype for monitoring
public-transit crowding and helping operators respond to sustained congestion.
See [plan.md](plan.md) for the full design.

**Documents:**
[Technical documentation (Word)](docs/Smart_Transit_Technical_Documentation.docx)
covers the architecture, technologies, algorithms, testing, problems solved and
future improvements. The [AI usage declaration](AI_DECLARATION.md) explains how
AI assistance was used in this project.

> **What is real and what is simulated:** after running the importer, routes,
> stops and ridership are **real open data from [opendata.az](https://opendata.az)**
> (AYNA and BakuBus). **Bus positions are always simulated**, because no live
> GPS feed is published. Without the import, the app uses three fictional demo
> routes (D1–D3). Every API response carries `X-Data-Mode: SIMULATED`, and the
> UI shows a banner plus source credits.

## What it does

```text
Simulated GPS + passenger crowd reports
        ↓
Crowd status per route (last 10 min, ≥ 3 reports, 60% rule; otherwise UNKNOWN)
        ↓
Reliable HIGH persists ≥ 10 minutes (timer stored in PostgreSQL)
        ↓
One persistent operator alert (no duplicates)
        ↓
Recommendation to EVALUATE an additional bus (never an automatic dispatch)
```

- **Passenger page** (`/`): route and stop selection, live map, bus positions
  with freshness, ETAs, crowding status with evidence, and LOW/MEDIUM/HIGH
  reporting.
- **Operator dashboard** (`/operator.html`): login, summary tiles, route
  crowding table (report counts, last report, last GPS, last analytics update),
  network map, and alerts with Acknowledge / Resolve.

## Decisions (plan section 20)

| Decision | Choice |
| --- | --- |
| Data mode | Simulated GPS, clearly labelled |
| Crowding granularity | Per route (reports still record stop, vehicle, and trip when known) |
| 10-minute rule | Reliable, *continuously* HIGH status for ≥ 10 minutes |
| Alert action | Recommendation only; the system never dispatches a bus |
| Operator access | Session login; seeded operator account; OPERATOR/ADMIN roles |
| Frontend | Plain HTML/JS + Leaflet (bundled), served by the Go backend |
| Live updates | Polling (3–5 s); WebSockets deferred, as the plan allows |

## Run locally

### Quick start (already set up)

```powershell
cd "path\to\smartTransitCrowdsourcingEngine"
go build -o bin\smart-transit.exe ./cmd/api
.\bin\smart-transit.exe
```

Then open:

| Page | URL |
| --- | --- |
| Passenger | <http://localhost:8080/> |
| Operator dashboard | <http://localhost:8080/operator.html> (log in with `OPERATOR_EMAIL` / `OPERATOR_PASSWORD` from `.env`) |
| Health check | <http://localhost:8080/healthz> |

Stop the server with **Ctrl + C**.

> Always start the server **from the project folder**: it reads `.env` from
> the current directory.

### First-time setup (Windows)

**Requirements:** Go 1.27+, PostgreSQL 16+ (developed on 18), Git, and
internet access for the opendata.az import and the map background.

**1. Install the tools** (skip any you already have):

```powershell
winget install --id GoLang.Go -e
winget install --id PostgreSQL.PostgreSQL.18 -e   # the installer asks for a password for the "postgres" admin user
winget install --id Git.Git -e
```

Open a **new** terminal afterwards so `go` and `git` are on the PATH, then
check with `go version`.

**2. Get the code:**

```powershell
git clone https://github.com/Fidan03/smartTransitCrowdsourcingEngine.git
cd smartTransitCrowdsourcingEngine
```

**3. Create the database and app user.** The app uses its own user, not the
`postgres` admin. Connect as `postgres`:

```powershell
& "C:\Program Files\PostgreSQL\18\bin\psql.exe" -U postgres -h localhost
```

then run (choose your own password):

```sql
CREATE ROLE smart_transit LOGIN PASSWORD 'choose-a-strong-password';
CREATE DATABASE smart_transit OWNER smart_transit;
CREATE DATABASE smart_transit_test OWNER smart_transit;   -- only for integration tests
\q
```

**4. Create `.env`** from the template:

```powershell
Copy-Item .env.example .env
notepad .env
```

Fill in at least:

| Variable | What to put |
| --- | --- |
| `DATABASE_URL` | `postgres://smart_transit:YOUR_PASSWORD@localhost:5432/smart_transit?sslmode=disable` |
| `TEST_DATABASE_URL` | the same, but ending in `/smart_transit_test?sslmode=disable` |
| `REPORT_KEY_SECRET` | a long random string (see below) |
| `OPERATOR_EMAIL` | the email you will log in with, e.g. `operator@example.com` |
| `OPERATOR_PASSWORD` | 12+ characters |

A quick way to generate a random secret:

```powershell
-join ((48..57) + (65..90) + (97..122) | Get-Random -Count 40 | ForEach-Object { [char]$_ })
```

`.env` is in `.gitignore`. **Never commit it.**

**5. Load the real Baku data** (recommended; takes a few seconds):

```powershell
go run ./cmd/importer -replace
```

This downloads the AYNA and BakuBus datasets from opendata.az, builds the
routes, and loads them. If you skip this step, the server starts with three
fictional demo routes (D1–D3) instead.

**6. Build and run:**

```powershell
go build -o bin\smart-transit.exe ./cmd/api
.\bin\smart-transit.exe
```

On start the server:

- applies database migrations;
- loads the demo dataset if the database has no routes;
- creates the operator account if it is missing;
- starts the GPS simulator and the crowd/alert evaluator.

You should see `server listening addr=:8080` and
`simulator ready (SIMULATED DATA) buses=20`.

**7. Open the pages** (URLs above). Buses start moving after a short layover
at their first stop.

### macOS / Linux

The same steps work. Install with `brew install go postgresql@18` or your
package manager. Run `psql -U postgres` to create the database, then:

```bash
cp .env.example .env
go run ./cmd/importer -replace
go build -o bin/smart-transit ./cmd/api && ./bin/smart-transit
```

### Everyday commands

| Task | Command |
| --- | --- |
| Run without building an .exe | `go run ./cmd/api` |
| Build and run | `go build -o bin\smart-transit.exe ./cmd/api; .\bin\smart-transit.exe` |
| Run on another port | `$env:HTTP_ADDR=":8081"; .\bin\smart-transit.exe` |
| Refresh the real data | `go run ./cmd/importer -replace`, then restart the server |
| Import from local files | `go run ./cmd/importer -replace -kmz buslines.kmz -stops dayanacaqlar.csv -ridership bakubus_passenger-2026.csv` |
| Quick alert demo (2-minute rule) | `$env:ALERT_SUSTAIN="2m"; .\bin\smart-transit.exe`, and in a second terminal `.\scripts\demo.ps1 -Minutes 3 -Recover` |
| Run all tests | `go test ./... -p 1` |
| Run unit tests only (no database) | `go test ./internal/...` |
| Run in the background | `Start-Process .\bin\smart-transit.exe -RedirectStandardOutput bin\server.log -WindowStyle Hidden -PassThru` |
| Stop a background server | `Get-Process smart-transit \| Stop-Process` |

> After editing anything in `web/static/`, **rebuild** the server (the pages
> are embedded into the .exe) and press **Ctrl + F5** in the browser.

### Troubleshooting

| Problem | Fix |
| --- | --- |
| `go` or `psql` is not recognized | Open a new terminal after installing. `psql` lives in `C:\Program Files\PostgreSQL\18\bin`. |
| `DATABASE_URL is required` | `.env` is missing, or you started the server outside the project folder. |
| `password authentication failed` | The password in `DATABASE_URL` doesn't match the one in `CREATE ROLE`. |
| `connection refused` on port 5432 | PostgreSQL isn't running: `Get-Service postgresql*`, then `Start-Service postgresql-x64-18`. |
| `bind: Only one usage of each socket address` | Port 8080 is busy. Stop the other server (`Get-Process smart-transit \| Stop-Process`) or set `HTTP_ADDR=:8081`. |
| `The process cannot access the file` when building | The old server is still running from `bin\`. Stop it first. |
| Map is grey or shows "Access denied" | Internet is needed for OpenStreetMap tiles. Press **Ctrl + F5** to clear cached tiles. |
| Page looks old after an update | Rebuild the server, then press **Ctrl + F5**. |
| Operator login fails | Check `OPERATOR_EMAIL` / `OPERATOR_PASSWORD` in `.env`. The account is created only once, so changing the password in `.env` later does not update it. Use a new `OPERATOR_EMAIL` to create a fresh account. |
| Importer: `database already has routes` | Add `-replace` (it replaces demo routes and related demo data; users are kept). |
| Importer can't reach opendata.az | Download the three files in a browser and use the local-file flags. |
| Integration tests show `skipped` | Set `TEST_DATABASE_URL` in `.env`. The database name must end in `_test`, and it is wiped on each run. |
| No buses on a route | Simulated buses run on the 10 busiest routes (62, 5, 14, 88, 2, 3, 7A, 6, 49, 46). Change this with `-simulate N` when importing. |

## Real open data from opendata.az

```powershell
go run ./cmd/importer -replace     # downloads, matches and loads; then restart the server
```

| Dataset | Publisher | Licence | Used for |
| --- | --- | --- | --- |
| [Avtobusların hərəkət trayektoriyası](https://opendata.az/dataset/avtobuslarin-hereket-trayektoriyasi) (KMZ) | AYNA | CC0 | Real route paths (both directions) |
| [BakuBus stop coordinates](https://opendata.az/dataset/bakubus-mmc-ye-mexsus-avtobus-marsrutlari-uzre-dayanacaqlarin-cografi-koordinatlari) (CSV) | Bakı Metropoliteni | **Not specified** | Stops (proposed "Təklif" stops excluded) |
| [BakuBus daily passenger trips 2026](https://opendata.az/dataset/bakubus-marsrutlari-uzre-gundelik-sernisin-gedislerinin-sayi-2026) (CSV) | Bakı Metropoliteni | CC BY | Expected crowding and operator context |

The importer finds the current download links through the opendata.az CKAN
API (`admin.opendata.az`), then:

1. joins each route's path pieces;
2. splits round trips at the far terminal into direction 0 and direction 1;
3. simplifies the paths (4 m tolerance);
4. assigns stops to each direction by location: within 25 m of the path and
   on the right-hand side (Azerbaijan drives on the right);
5. loads everything in one transaction and adds simulated buses to the 10
   busiest routes.

The last import loaded 56 BakuBus routes, 1,307 stops and 15,686 days of
ridership; 7 routes were skipped because too few stops matched. Local files
can be used instead: `-kmz`, `-stops`, `-ridership`.

> ⚠️ The stops dataset has **no licence stated by the publisher**. Confirm
> permission (e.g. opendata@idda.az) before any public deployment. CC BY data
> is credited in the UI footer and at `/api/v1/data-sources`.

### Expected crowding (when there are not enough reports)

When live reports are insufficient (`UNKNOWN`), passengers and operators see an
**expected** level such as "likely HIGH", based on:

- the **real** average ridership for this route on this weekday (BakuBus data);
- an **assumed** typical hourly pattern (weekday peaks around 08:00 and 18:00,
  flatter at weekends), because the open data has daily totals only;
- network-relative cut-offs: the busiest 25% of route-hours count as HIGH and
  the quietest 35% as LOW.

Rules: the estimate is **never** stored as a report or status, **never**
creates or resolves alerts, and is always shown with a dashed "likely …" badge
and an "estimate" label. It measures typical route demand, not how full a
particular bus is.

## Demo scenario (plan 16)

The production rule is 10 minutes. For a quick demo you may shorten it
**outside production only** (the server refuses to start in production with a
changed value):

```powershell
$env:ALERT_SUSTAIN = "2m"; .\bin\smart-transit.exe    # terminal 1
.\scripts\demo.ps1 -Minutes 3 -Recover                 # terminal 2 (route 62, or pick one with -Route bb_5)
```

The script plays several anonymous passengers who keep reporting HIGH. Watch
the operator dashboard: the route turns HIGH, its timer runs, exactly one alert
appears with the spare-bus recommendation, and with `-Recover` the alert is
flagged "ready for review" after LOW reports (it is never auto-resolved). For
the real rule, omit `ALERT_SUSTAIN` and run `.\scripts\demo.ps1` (11 minutes).

## Rules implemented

**Crowd status** (`internal/crowd`):
- Only reports from the last 10 minutes count; only the latest report per
  submitter counts (one voice per passenger).
- Fewer than 3 valid reports gives `UNKNOWN`. If at least 60% are HIGH the
  status is `HIGH`; if at least 60% are LOW it is `LOW`; otherwise `MEDIUM`.
- `UNKNOWN` is never treated as LOW.

**Alert continuity policy** (`internal/alerts`):
- Reliable HIGH starts the timer (`high_since`, persisted). At ≥ 10 minutes
  with no open alert, one alert is created, guarded by a per-route lock and a
  partial unique index.
- Reliable LOW/MEDIUM resets the timer and marks an open alert "recovered" for
  operator review.
- `UNKNOWN` invalidates the timer (continuity cannot be proven) but does not
  claim recovery.
- If evaluations stop for more than 2 minutes (e.g. a long outage), the timer
  restarts. Normal restarts keep it.
- Resolving an alert while still HIGH restarts the timer, so a new alert can
  only follow after another full 10 minutes.

**ETA** (`internal/eta`): remaining distance along the route divided by recent
measured speed (or a 20 km/h baseline), plus 20 s per intermediate stop, minus
the GPS age. Returns `UNAVAILABLE` for stale GPS, missing GPS, or off-route
buses rather than a falsely precise number.

**Reports and abuse controls:** the server assigns the ID and receipt time (it
never trusts client timestamps). Each submitter may report once per route
every 2 minutes, and each IP is limited to 20 reports per minute. Submitter
keys are HMAC hashes of a random per-browser ID (or the IP); no raw
identifiers are stored.

## API

| Method | Endpoint | Purpose |
| --- | --- | --- |
| GET | `/healthz` | Health and data mode |
| GET | `/api/v1/routes` | List routes |
| GET | `/api/v1/routes/{id}` | Route details and geometry |
| GET | `/api/v1/routes/{id}/stops?direction=0` | Ordered stops |
| GET | `/api/v1/routes/{id}/vehicles` | Current buses with `FRESH`/`STALE`/`NO_DATA` |
| GET | `/api/v1/routes/{id}/crowd-status` | Live status plus evidence, and an `expected` estimate (display only) |
| GET | `/api/v1/data-sources` | Imported dataset provenance and licences |
| GET | `/api/v1/stops/{id}/arrivals?route_id=` | ETAs with confidence |
| POST | `/api/v1/crowd-reports` | Submit `{route_id, stop_id?, vehicle_id?, crowd_level}` |
| POST | `/api/v1/auth/login` · `/logout` | Operator session (HttpOnly, SameSite=Strict cookie) |
| GET | `/api/v1/auth/me` | Current operator |
| GET | `/api/v1/operator/dashboard` | Summary statistics |
| GET | `/api/v1/operator/routes` | Route crowding table |
| GET | `/api/v1/operator/alerts?status=` | Alerts (ACTIVE, ACKNOWLEDGED, RESOLVED) |
| GET/PATCH | `/api/v1/operator/alerts/{id}` | Details; `{"action":"acknowledge"\|"resolve","note"}` |
| POST | `/api/v1/dev/simulator/positions` | Inject a position (non-production only) |

Errors use `{"error": {"code", "message"}}`. Timestamps are UTC.

## Tests

```powershell
go test ./... -p 1
```

- Unit tests cover the crowd rule (plan examples, thresholds, window,
  deduplication), alert timer boundaries and policies, ETA maths, the
  simulator, ingestion validation, rate limiting, configuration, and HTTP
  handlers (validation, auth, roles).
- Integration tests (`tests/`) run against real PostgreSQL using
  `TEST_DATABASE_URL`. The database name must end in `_test`, and it is wiped
  on every run. They cover exactly-at-10-minutes alerting, no duplicates under
  concurrent evaluation, timer survival across restarts, the alert lifecycle
  and audit log, stale GPS withholding ETAs, and sessions. Without
  `TEST_DATABASE_URL` they are skipped.

## Configuration

| Variable | Default | Notes |
| --- | --- | --- |
| `APP_ENV` | `development` | `development`, `test`, or `production` |
| `HTTP_ADDR` | `:8080` | |
| `DATABASE_URL` | (required) | |
| `SIMULATOR_ENABLED` | `false` | Must be false in production |
| `SIMULATOR_INTERVAL` | `2s` | |
| `GPS_STALE_AFTER` / `GPS_MAX_AGE` | `60s` / `15m` | STALE threshold / ignore older |
| `ALERT_SUSTAIN` | `10m` | Locked to 10m in production |
| `CROWD_EVAL_INTERVAL` | `15s` | Periodic re-evaluation |
| `REPORT_DUP_INTERVAL` | `2m` | Per-submitter, per-route |
| `REPORT_KEY_SECRET` | dev fallback | Required (16+ chars) in production |
| `OPERATOR_EMAIL` / `OPERATOR_PASSWORD` | (none) | Account created if missing |

## Layout

```
cmd/api/              entry point: config, DB, migrations, workers, HTTP server
cmd/importer/         opendata.az import (real routes, stops, ridership)
internal/demand/      expected crowding from historical ridership (display only)
internal/opendata/    KMZ/CSV parsing, path stitching, direction split, stop matching, DB load
internal/alerts/      alert continuity policy, evaluation engine, lifecycle
internal/auth/        operator accounts, bcrypt, sessions
internal/clock/       real/fake clock for time-based rules and tests
internal/config/      environment / .env loading and validation
internal/crowd/       report validation, dedup, crowd-status rule
internal/database/    pool (UTC), migrator, demo seeding
internal/eta/         ETA estimation and arrivals service
internal/geo/         distance and polyline projection
internal/gps/         position model, provider interface, ingestion, stores
internal/gps/simulator/  development GPS simulator
internal/httpapi/     REST handlers, middleware, auth, rate limiting hooks
internal/operator/    dashboard and route-table queries
internal/ratelimit/   in-memory token bucket
internal/transit/     routes, stops, trips, route paths
migrations/           versioned SQL schema (embedded)
seeds/                demo dataset (development only)
web/static/           passenger and operator pages (Leaflet bundled)
scripts/demo.ps1      repeatable end-to-end demo
tests/                PostgreSQL integration tests
```

## Known limitations (prototype)

- Rate limits and caches are in-memory (single instance).
- Map tiles load from openstreetmap.org (internet required for the base map).
- The ETA baseline does not model traffic; it should be validated against
  recorded data before any real use (plan 6).
- Crowd reports are not ground truth; thresholds are initial defaults to tune.
- Position history grows (~200k rows/day with the simulator); add retention
  before long-running use.
- Stop-to-route assignment is inferred from locations (the source file has no
  route column). Stops on parallel streets may be misassigned, and the
  direction split assumes paths are drawn in the direction of travel.
- The AYNA route paths are a snapshot (updated every 6 months); ridership runs
  January–September 2026. Re-run the importer to refresh.
- Expected crowding uses an assumed hourly pattern and route-level demand. It
  is a hint, not a measurement.
