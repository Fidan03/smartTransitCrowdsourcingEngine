# smartTransitCrowdsourcingEngine

**Smart Transit & Crowdsourcing Engine**: a functional prototype for monitoring
public-transit crowding and helping operators respond to sustained congestion.
See [plan.md](plan.md) for the full design.

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

## Requirements

- Go 1.27+
- PostgreSQL 16+ (developed on 18), running locally on port 5432

## Setup

1. Create a database and user, e.g. `smart_transit` (and `smart_transit_test`
   for integration tests).
2. Copy `.env.example` to `.env` and fill in `DATABASE_URL`,
   `REPORT_KEY_SECRET`, `OPERATOR_EMAIL`, `OPERATOR_PASSWORD`, and
   `TEST_DATABASE_URL`.
3. Build and run:

   ```powershell
   go build -o bin\smart-transit.exe ./cmd/api
   .\bin\smart-transit.exe
   ```

   On start the server applies migrations, loads the demo dataset into an
   empty database (non-production only), creates the operator account if it
   is missing, and starts the GPS simulator and the crowd/alert evaluator.

4. Open <http://localhost:8080/> (passenger) and
   <http://localhost:8080/operator.html> (log in with `OPERATOR_EMAIL` /
   `OPERATOR_PASSWORD` from `.env`).

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
$env:ALERT_SUSTAIN = "2m"; .\bin\smart-transit.exe       # terminal 1
.\scripts\demo.ps1 -Route route_d1 -Minutes 3 -Recover    # terminal 2
```

The script plays several anonymous passengers who keep reporting HIGH. Watch
the operator dashboard: D1 turns HIGH, its timer runs, exactly one alert
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
