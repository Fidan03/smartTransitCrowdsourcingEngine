# Smart Transit & Crowdsourcing Engine

## Project Analysis and Implementation Plan (Go)

**Document type:** Technical implementation plan\
**Project type:** Functional prototype\
**Backend language:** Go (Golang)\
**Initial data mode:** Simulated GPS unless a verified real-time data
source is available

------------------------------------------------------------------------

## 1. Project Overview

The Smart Transit & Crowdsourcing Engine is a prototype for monitoring
public-transit crowding and helping operators respond to sustained
congestion. It has two primary user-facing modules:

1.  **Passenger module:** lets passengers select a route or stop, view
    bus positions and estimated arrival times (ETAs), and report
    crowding as LOW, MEDIUM, or HIGH.
2.  **Operator and analytics module:** combines GPS data and crowd
    reports, displays route crowding, and creates an alert recommending
    that an operator evaluate allocating an additional or spare bus when
    high crowding persists for at least 10 minutes.

The system should be designed so that simulated GPS data can be replaced
by a verified live data feed later, without rewriting the core crowding
and alert logic.

### 1.1 Problem Statement

The project aims to address three operational problems:

-   Passenger crowding may vary substantially between routes, trips,
    stops, and times of day.
-   Passengers may not know where a bus is or when it is expected to
    arrive.
-   Operators may lack timely, consolidated evidence of sustained
    crowding when deciding whether to deploy spare capacity.

These are the problems the prototype is intended to investigate. Their
actual scale and causes must be validated with real operational data.

### 1.2 Main Outcomes

The prototype should demonstrate that it can:

-   Display routes, stops, bus locations, and estimated arrivals.
-   Accept and store passenger crowding reports.
-   Estimate a current crowding state from recent, sufficiently reliable
    reports.
-   Show crowding status and supporting information to an operator.
-   Generate a persistent, non-duplicated alert after HIGH crowding has
    remained active for at least 10 minutes.
-   Clearly distinguish simulated data from real data.

------------------------------------------------------------------------

## 2. Important Assumptions and Risks

### 2.1 GPS Does Not Measure Passenger Count

GPS provides a vehicle's position and possibly its speed and timestamp.
It does not directly reveal how many passengers are on board. Crowding
must initially be estimated from passenger reports, or from a separate
trusted passenger-count data source if one becomes available.

Crowdsourced reports are not ground truth. They can be stale,
duplicated, mistaken, or deliberately misleading. The system must expose
uncertainty instead of treating every report as unquestionably correct.

### 2.2 Live Baku Transit Data Must Be Verified

Do not assume that an official live GPS API is available. Confirm
whether the relevant operator or public authority provides a usable feed
and whether permission to access it has been granted.

For the first implementation, use a GPS simulator or a recorded sample
dataset. Clearly label the interface as **Demo / Simulated Data**
whenever simulated data is active. Keep the GPS ingestion interface
separate from the rest of the application so a real provider can be
added later.

GTFS Realtime is one possible standard for vehicle positions, trip
updates, and service alerts. Availability of a suitable Baku feed must
be verified independently: - https://gtfs.org/documentation/realtime/ -
https://gtfs.org/documentation/realtime/realtime-best-practices/

### 2.3 A Route-Wide Status Can Hide Local Problems

A single report near one stop should not automatically imply that every
bus or every section of a route is crowded. Where possible, associate
reports with a specific vehicle, trip, stop, or route segment. If the
precise target is unknown, retain that uncertainty in the data model and
UI.

### 2.4 Alerts Are Recommendations, Not Dispatch Commands

The prototype should recommend that an operator evaluate assigning an
additional bus. It should **not automatically dispatch a real bus**.
Actual dispatch depends on fleet availability, driver availability,
scheduling, safety, and operational approval.

------------------------------------------------------------------------

## 3. Proposed Technology Stack

  -----------------------------------------------------------------------
  Component               Recommended technology  Purpose
  ----------------------- ----------------------- -----------------------
  Backend                 Go (Golang)             API, business rules,
                                                  analytics, alert engine

  HTTP routing            Go `net/http` or Gin    REST API and middleware

  Frontend                React + TypeScript      Passenger interface and
                                                  operator dashboard

  Map                     Leaflet + OpenStreetMap Routes, stops, and
                                                  vehicle positions

  Database                PostgreSQL              Persistent application
                                                  data

  Geospatial extensions   PostGIS, if needed      Geographic queries and
                                                  route/stop proximity

  Live UI updates         WebSocket; polling      Publish position,
                          initially is acceptable crowding, and alert
                                                  updates

  Backend tests           Go `testing` package    Unit, integration, and
                                                  API tests

  Local environment       Docker Compose          Run the backend and
                                                  database consistently

  Database migrations     A Go-compatible         Versioned database
                          migration tool          schema changes
  -----------------------------------------------------------------------

The stack is a recommendation, not a requirement to use every component
on day one. A modular monolith is appropriate for the prototype: one Go
backend with clearly separated internal packages. Avoid microservices
until there is a demonstrated need.

### 3.1 Why Start With a Web Application?

A responsive web app can serve passengers on mobile browsers and provide
a separate operator dashboard without requiring native Android and iOS
applications. This keeps the first version focused on validating the
data flow and alert rules.

------------------------------------------------------------------------

## 4. High-Level Architecture

``` text
Passenger Web App                 Operator Dashboard
       |                                  |
       +---------------+------------------+
                       |
                  Go Backend
                       |
          +------------+-------------+
          |            |             |
       REST API    Analytics      Alert Engine
          |            |             |
          +------------+-------------+
                       |
                  PostgreSQL
                       |
            GPS Provider / Simulator
```

### Component responsibilities

-   **Passenger frontend:** route and stop selection, map, ETA display,
    crowd reporting.
-   **Operator frontend:** crowding overview, route table, map, alerts,
    and alert status management.
-   **Go API:** validates requests, applies authorization, calls
    application services, and returns consistent responses.
-   **GPS ingestion:** accepts positions from a simulator or an approved
    provider.
-   **Crowd analytics:** evaluates recent reports and determines the
    current crowding state.
-   **Alert engine:** tracks sustained HIGH crowding and creates or
    resolves alerts.
-   **Database:** stores route data, positions, reports, calculated
    states, and alert history.
-   **Real-time delivery:** sends updates to connected clients. Start
    with periodic polling if WebSockets would delay the MVP.

Crowding rules must be implemented on the backend, not only in the
frontend, so the passenger and operator interfaces use the same result.

------------------------------------------------------------------------

## 5. Functional Requirements

### 5.1 Passenger Module

The passenger interface must support:

1.  Selecting a route.
2.  Selecting a stop.
3.  Displaying the selected route and its ordered stops.
4.  Displaying buses associated with the route on a map.
5.  Displaying each bus's latest known position and the time it was
    updated.
6.  Displaying an ETA for a selected stop when sufficient fresh data
    exists.
7.  Displaying crowding status: LOW, MEDIUM, HIGH, or UNKNOWN.
8.  Submitting a crowding report using LOW, MEDIUM, or HIGH.
9.  Showing a clear message when GPS or crowding data is stale or
    unavailable.
10. Showing an explicit demo label while simulated data is being used.

#### Example crowd report payload

``` json
{
  "route_id": "route_18",
  "stop_id": "stop_102",
  "vehicle_id": "bus_101",
  "crowd_level": "HIGH"
}
```

`stop_id` and `vehicle_id` should be optional because a passenger may
not know the exact stop or vehicle. The backend should generate the
report ID and server receipt timestamp. Do not trust a client-provided
timestamp as the authoritative receipt time.

### 5.2 Operator and Analytics Module

The operator dashboard must display:

-   Number of active routes.
-   Number of vehicles with recent GPS data.
-   Routes or route segments currently classified as HIGH.
-   Number of active alerts.
-   A route table containing crowd status, recent valid report count,
    last GPS update, and last analytics update.
-   A map showing routes, stops, and vehicle positions.
-   Alerts containing creation time, route/segment, reason,
    recommendation, and status.
-   Actions to acknowledge an alert and mark it resolved, subject to
    authorization.

A route list and map are sufficient for the first version. More
elaborate heatmaps can be added later.

### 5.3 GPS and Route Data

Each route should have:

-   Route ID and public route number.
-   Route name or description, if available.
-   Ordered stops.
-   Route geometry where available.
-   Direction or trip information where applicable.

Each vehicle-position observation should contain:

-   Vehicle ID.
-   Latitude and longitude.
-   Speed, if available.
-   Route/trip association, if known.
-   `observed_at`: when the position was measured.
-   `received_at`: when the backend received the position.
-   Data source or simulation flag.

Keep `observed_at` and `received_at` separate. This helps identify
delayed or stale data.

### 5.4 GPS Simulator

Until a live data source is verified, implement a development-only
simulator that:

1.  Loads seeded routes and route geometry.
2.  Moves simulated buses along those routes.
3.  Emits positions at a configurable interval.
4.  Can simulate stops, delays, and stale updates.
5.  Sends positions through the same ingestion service used by a future
    live provider.

Simulation endpoints must be disabled or protected in production. The UI
must not present simulated positions as real-world observations.

------------------------------------------------------------------------

## 6. ETA Calculation

ETA means **Estimated Time of Arrival**. For the first prototype, use a
simple baseline rather than an unvalidated machine-learning model.

Potential inputs:

-   Latest valid bus coordinates.
-   Timestamp and age of the GPS observation.
-   Route geometry and direction.
-   Distance remaining to the target stop along the route.
-   Recent speed or historical average speed.
-   Estimated stop dwell time, where available.

A simplified formula is:

``` text
ETA = distance_remaining / estimated_speed + expected_stop_delay
```

Ensure units are consistent. For example, when distance is in kilometres
and speed is in kilometres per hour, convert the result from hours to
minutes.

Recommended processing steps:

1.  Confirm that the bus is associated with the selected route and
    direction.
2.  Match the vehicle position to the route geometry.
3.  Estimate the remaining route distance to the stop.
4.  Estimate speed from recent valid observations or a configured
    baseline.
5.  Add an optional stop-delay allowance.
6.  Validate the result and mark its confidence or freshness.
7.  Avoid displaying a falsely precise ETA if GPS is stale or the
    direction cannot be determined.

This baseline will not reliably predict city traffic by itself. Improve
it only after collecting suitable historical data and measuring ETA
error.

------------------------------------------------------------------------

## 7. Crowdsourcing and Crowding Analytics

### 7.1 Report Validation

When a crowd report arrives, the backend should:

1.  Validate the route and optional stop/vehicle IDs.
2.  Validate `crowd_level` against the allowed values.
3.  Assign a server-generated ID and receipt timestamp.
4.  Apply basic rate limits and duplicate-report controls.
5.  Store the report with its target and source metadata.
6.  Recalculate the relevant crowding state.
7.  Publish the updated state to subscribers or make it available
    through polling.

The initial prototype does not need a complex reputation system. Basic
rate limits, duplicate controls, recency filtering, and minimum sample
sizes are enough to start.

### 7.2 Initial Crowd-State Rule

Calculate crowding using reports from the most recent **10 minutes** for
the relevant route, trip, vehicle, stop, or segment. Do not mix
unrelated segments without an explicit reason.

Suggested initial rule:

-   If there are fewer than **3 valid reports**, return `UNKNOWN`.
-   If at least **60%** of valid reports are `HIGH`, return `HIGH`.
-   If at least **60%** of valid reports are `LOW`, return `LOW`.
-   Otherwise, return `MEDIUM`.

These thresholds are initial prototype defaults, not universal
standards. Tune them after testing with realistic report patterns.
Consider deduplicating repeated reports from the same user or device
within a configurable interval.

Examples with a minimum sample size of three:

  Recent valid reports      Result
  ------------------------- ----------------------------------
  1 HIGH                    UNKNOWN --- insufficient reports
  3 HIGH                    HIGH
  2 HIGH, 1 MEDIUM          HIGH
  1 HIGH, 1 MEDIUM, 1 LOW   MEDIUM

In a production deployment, add stronger abuse detection and assess
whether crowd-report patterns correlate with verified passenger counts.

### 7.3 Report Recency

Old reports should not influence the current status indefinitely. The
initial 10-minute rolling window is sufficient for the MVP. A later
version may use time-decay weighting, but it is not required for the
first implementation.

### 7.4 Confidence and Unknown State

The system must not confuse "no reliable evidence" with "low crowding."
Use `UNKNOWN` when reports are insufficient or stale. Display the report
count and last update time where useful so operators can understand the
evidence behind a status.

------------------------------------------------------------------------

## 8. Alert Engine: Sustained HIGH Crowding for 10 Minutes

This is the main automation requirement.

### 8.1 Business Rule

When a relevant route or segment is classified as HIGH and that state
remains continuously HIGH for at least 10 minutes, create an operator
alert recommending that spare/additional bus capacity be evaluated.

The system should create the alert only when:

-   The crowd status is based on sufficient valid reports.
-   The HIGH state has persisted for at least 10 minutes.
-   The relevant data is sufficiently fresh for the chosen rule.
-   There is no already-active alert for the same ongoing incident.

If the status becomes UNKNOWN, the system must not treat that as proof
that crowding is resolved. Define a deliberate policy for missing data;
the conservative MVP behavior is to pause or invalidate the continuity
check rather than fabricate a reliable HIGH state.

### 8.2 Pseudocode

``` text
for each monitored route or segment:
    status = calculate_crowd_status()

    if status is not reliable:
        do not create an alert from this evaluation
        apply the configured missing-data policy
        continue

    if status == HIGH:
        if high_since is not set:
            persist high_since = current time

        if current time - high_since >= 10 minutes:
            if no active alert exists for this incident:
                create alert:
                    reason = "HIGH crowding persisted for at least 10 minutes"
                    recommendation = "Evaluate allocating a spare/additional bus"
    else:
        clear or reset the sustained-HIGH timer according to the continuity policy
        if an active alert exists and the crowd status has recovered:
            mark the alert resolved or ready for operator review
```

### 8.3 Persistence and Duplicate Prevention

Persist the beginning of the sustained-HIGH period in the database. Do
not store this state only in an in-memory variable, because a backend
restart would reset the timer.

Use a database transaction and/or a uniqueness constraint to prevent
duplicate active alerts for the same incident. The alert should have a
lifecycle, for example:

-   `ACTIVE`
-   `ACKNOWLEDGED`
-   `RESOLVED`

Record when it was created, acknowledged, and resolved, plus the reason
and recommendation. An operator acknowledgement should not automatically
mean that the crowding problem is resolved.

### 8.4 Recovery Policy

Define and test how the timer resets. For the simplest MVP, reset
sustained HIGH when a reliable recalculation returns LOW or MEDIUM. If
the data becomes UNKNOWN, do not falsely claim that the situation has
recovered; use an explicit unknown-data policy and show that state to
the operator.

### 8.5 Alert Example

``` json
{
  "route_id": "route_18",
  "status": "ACTIVE",
  "severity": "HIGH",
  "reason": "HIGH crowding persisted for at least 10 minutes",
  "recommendation": "Evaluate allocating a spare/additional bus",
  "created_at": "2026-10-31T12:10:00Z"
}
```

This is an illustrative payload. The actual timestamp and IDs must be
generated from runtime data.

------------------------------------------------------------------------

## 9. Proposed Data Model

Use PostgreSQL. Add PostGIS if geographic queries become useful for
matching positions to stops or route segments.

Suggested tables:

  -----------------------------------------------------------------------
  Table                   Purpose                 Important fields
  ----------------------- ----------------------- -----------------------
  `routes`                Route metadata          `id`, `route_number`,
                                                  `name`, `geometry`

  `stops`                 Stop metadata           `id`, `name`,
                                                  `latitude`, `longitude`

  `route_stops`           Ordered stops for a     `route_id`, `stop_id`,
                          route                   `stop_order`,
                                                  `direction`

  `vehicles`              Vehicle metadata        `id`, `label`, `active`

  `trips`                 A vehicle's             `id`, `route_id`,
                          scheduled/observed      `vehicle_id`,
                          route run               `started_at`, `status`

  `vehicle_positions`     Position history        `id`, `vehicle_id`,
                                                  `trip_id`, `latitude`,
                                                  `longitude`, `speed`,
                                                  `observed_at`,
                                                  `received_at`, `source`

  `crowd_reports`         Passenger submissions   `id`, `route_id`,
                                                  `stop_id`,
                                                  `vehicle_id`,
                                                  `trip_id`,
                                                  `crowd_level`,
                                                  `created_at`,
                                                  `source_key`

  `route_crowd_status`    Latest calculated state `target_type`,
                          and continuity tracking `target_id`, `status`,
                                                  `valid_report_count`,
                                                  `calculated_at`,
                                                  `high_since`

  `alerts`                Operator alerts and     `id`, `target_type`,
                          lifecycle               `target_id`, `status`,
                                                  `reason`,
                                                  `recommendation`,
                                                  `created_at`,
                                                  `acknowledged_at`,
                                                  `resolved_at`

  `users`                 Operator accounts and   `id`, `email`,
                          roles                   `password_hash`, `role`

  `audit_logs`            Important operator      `id`, `user_id`,
                          actions                 `action`, `target_id`,
                                                  `created_at`,
                                                  `metadata`
  -----------------------------------------------------------------------

Implementation notes:

-   Use database migrations rather than manually editing a production
    schema.
-   Add indexes for recent reports by target and timestamp, recent
    vehicle positions, and active alerts.
-   Use UTC timestamps in storage and API responses.
-   Store current vehicle position separately from historical position
    records if query volume makes this beneficial.
-   Do not store unnecessary passenger personal data.
-   The exact schema may be simplified for the MVP, but the
    relationships between routes, stops, vehicles, reports, and alerts
    must remain clear.

------------------------------------------------------------------------

## 10. Go Backend Structure

A possible repository structure:

``` text
smart-transit/
├── cmd/
│   └── api/
│       └── main.go
├── internal/
│   ├── config/
│   ├── http/
│   │   ├── handlers/
│   │   ├── middleware/
│   │   └── router.go
│   ├── routes/
│   ├── stops/
│   ├── vehicles/
│   ├── gps/
│   │   ├── provider.go
│   │   ├── ingestion.go
│   │   └── simulator/
│   ├── eta/
│   │   └── service.go
│   ├── crowd/
│   │   ├── reports.go
│   │   └── analytics.go
│   ├── alerts/
│   │   ├── service.go
│   │   └── rules.go
│   ├── operator/
│   ├── database/
│   └── realtime/
├── migrations/
├── web/
│   ├── passenger/
│   └── operator/
├── tests/
├── .env.example
├── Dockerfile
├── docker-compose.yml
├── go.mod
├── go.sum
└── README.md
```

This is a suggested structure, not a requirement to create empty
packages before they are needed. Keep modules small and define
interfaces at real integration boundaries, especially for GPS providers
and data storage.

### 10.1 Suggested Go Interfaces

For example, separate the GPS source from its consumers:

``` go
type PositionProvider interface {
    NextPositions(ctx context.Context) (<-chan VehiclePosition, error)
}
```

The real provider and simulator can implement the same interface. The
precise signature can be adjusted to the selected ingestion design; the
key requirement is that crowding and alert logic must not depend
directly on a particular GPS vendor.

Use `context.Context` for request cancellation and long-running service
shutdown. Keep business logic in services so it can be tested without
making HTTP requests.

------------------------------------------------------------------------

## 11. REST API Proposal

Use versioned API routes if the project is expected to grow, for example
`/api/v1`.

### Passenger and route endpoints

  ------------------------------------------------------------------------------------
  Method                  Endpoint                             Purpose
  ----------------------- ------------------------------------ -----------------------
  `GET`                   `/api/v1/routes`                     List available routes

  `GET`                   `/api/v1/routes/{id}`                Get route details

  `GET`                   `/api/v1/routes/{id}/stops`          Get ordered stops

  `GET`                   `/api/v1/routes/{id}/vehicles`       Get current vehicles
                                                               for a route

  `GET`                   `/api/v1/stops/{id}/arrivals`        Get estimated arrivals
                                                               for a stop

  `GET`                   `/api/v1/routes/{id}/crowd-status`   Get current crowd
                                                               status and evidence
                                                               metadata

  `POST`                  `/api/v1/crowd-reports`              Submit a crowd report
  ------------------------------------------------------------------------------------

### Operator endpoints

  --------------------------------------------------------------------------------
  Method                  Endpoint                         Purpose
  ----------------------- -------------------------------- -----------------------
  `GET`                   `/api/v1/operator/dashboard`     Summary statistics

  `GET`                   `/api/v1/operator/routes`        Route crowding table

  `GET`                   `/api/v1/operator/alerts`        List alerts, with
                                                           optional status filters

  `GET`                   `/api/v1/operator/alerts/{id}`   Get alert details

  `PATCH`                 `/api/v1/operator/alerts/{id}`   Acknowledge or resolve
                                                           an alert
  --------------------------------------------------------------------------------

### Development-only endpoints

  -----------------------------------------------------------------------------------
  Method                  Endpoint                            Purpose
  ----------------------- ----------------------------------- -----------------------
  `POST`                  `/api/v1/dev/simulator/positions`   Inject simulated
                                                              positions in
                                                              development/test
                                                              environments

  -----------------------------------------------------------------------------------

Do not expose development simulation endpoints in production. Protect
operator endpoints with authentication and role-based authorization.

### Example crowd-report response

``` json
{
  "id": "report_abc123",
  "accepted": true,
  "received_at": "2026-10-31T12:00:00Z"
}
```

Use consistent error responses and appropriate HTTP status codes.
Validate all IDs and enum values on the backend.

------------------------------------------------------------------------

## 12. Real-Time Updates

The first version can use periodic polling, for example refreshing the
visible data every few seconds. This is easier to implement and debug.

Add WebSockets when the core workflow works and near-real-time updates
are important. Suggested event types:

-   `vehicle.position.updated`
-   `route.crowd.updated`
-   `alert.created`
-   `alert.updated`
-   `alert.resolved`

The frontend should handle reconnects and stale data. Do not rely on a
WebSocket message as the only persistent record of an alert; the
database remains the source of truth.

------------------------------------------------------------------------

## 13. Security, Privacy, and Reliability

Minimum requirements:

-   Authenticate operator users.
-   Apply role-based authorization to operator actions.
-   Validate all incoming data.
-   Rate-limit crowd reports and development endpoints.
-   Use parameterized database queries.
-   Store passwords as secure hashes if local password authentication is
    implemented.
-   Keep secrets in environment variables, not in source control.
-   Avoid collecting precise passenger location unless necessary and
    clearly disclosed.
-   Keep an audit log of operator alert actions.
-   Use timeouts and graceful shutdown in the Go server.
-   Log errors without exposing credentials or sensitive information.
-   Clearly label simulated data.
-   Ensure that stale GPS or insufficient crowd reports do not silently
    become LOW crowding.

------------------------------------------------------------------------

## 14. Implementation Phases

### Phase 1 --- Define Scope and Data Model

Tasks: - Confirm whether the first release is a demo or will connect to
real transit data. - Choose the initial crowding target: vehicle, trip,
stop, or route segment. - Define route, stop, vehicle, report,
crowd-status, and alert models. - Define how missing and stale data are
handled. - Create the repository and `.env.example`.

Deliverable: agreed scope, initial schema, and project skeleton.

### Phase 2 --- Set Up the Go API and Database

Tasks: - Create the Go application entry point. - Configure
environment-based settings. - Connect to PostgreSQL. - Add database
migrations. - Seed a small demo dataset, such as three routes with five
to ten stops each and a few simulated buses. - Implement route and stop
read endpoints.

Deliverable: a running API that returns seeded route and stop data.

### Phase 3 --- Implement GPS Ingestion and Simulation

Tasks: - Define the GPS provider interface. - Implement a development
simulator. - Persist position observations with both observation and
receipt timestamps. - Expose current vehicle positions through the
API. - Handle stale positions and missing vehicle data. - Show the
demo/simulation label.

Deliverable: buses move along seeded routes on the map using simulated
positions.

### Phase 4 --- Implement ETA

Tasks: - Match a vehicle to its route and direction. - Calculate
distance to the next stop along the route. - Estimate speed from recent
valid positions or a configured fallback. - Calculate and validate
ETA. - Return an unavailable or low-confidence state when input data is
stale or inadequate.

Deliverable: passengers can view approximate arrival times, with
uncertainty communicated honestly.

### Phase 5 --- Implement Crowd Reports and Analytics

Tasks: - Add `POST /api/v1/crowd-reports`. - Validate the route,
optional stop/vehicle, and crowd level. - Add duplicate controls and
basic rate limiting. - Calculate status from the rolling 10-minute
window. - Implement minimum report count and `UNKNOWN`. - Expose crowd
status and supporting metadata. - Add unit tests for edge cases.

Deliverable: crowd reports update the calculated status.

### Phase 6 --- Implement the Sustained-HIGH Alert Engine

Tasks: - Persist `high_since` for each monitored target. - Define the
continuity policy for HIGH, LOW, MEDIUM, and UNKNOWN states. - Create an
alert after at least 10 minutes of reliable sustained HIGH. - Prevent
duplicate active alerts for the same incident. - Support acknowledge and
resolve actions. - Ensure state survives backend restarts. - Test timer
boundaries and repeated evaluations.

Deliverable: sustained high crowding produces one persistent operator
alert with a recommendation.

### Phase 7 --- Build the Passenger Interface

Tasks: - Add route and stop selection. - Display the map and bus
markers. - Display ETA, crowd status, freshness, and unknown states. -
Add LOW/MEDIUM/HIGH report controls. - Display submission success or
validation errors. - Make the layout usable on mobile browsers.

Deliverable: a passenger can find a route, view buses, and submit a
crowd report.

### Phase 8 --- Build the Operator Dashboard

Tasks: - Add summary statistics. - Add route crowding table. - Add map
and last-update timestamps. - Add active alert list and details. - Add
acknowledgement and resolution actions. - Protect the dashboard and its
APIs.

Deliverable: an operator can monitor routes and manage alerts.

### Phase 9 --- Integration, Testing, and Demo Preparation

Tasks: - Test the full flow from simulated GPS and passenger reports to
crowd status and alert. - Test stale data, insufficient reports,
duplicated reports, and server restarts. - Verify that an alert is not
created early or duplicated. - Add setup and demo instructions to the
README. - Make it clear which values are simulated. - Prepare a
repeatable demonstration scenario.

Deliverable: a reproducible end-to-end prototype.

------------------------------------------------------------------------

## 15. Acceptance Criteria

The prototype is ready for demonstration when the following criteria
pass.

  -----------------------------------------------------------------------
  ID                                  Acceptance criterion
  ----------------------------------- -----------------------------------
  AC-01                               The passenger can retrieve and
                                      select available routes.

  AC-02                               The passenger can view the selected
                                      route's ordered stops.

  AC-03                               Simulated buses appear on the map
                                      and their positions update.

  AC-04                               The system returns an ETA when the
                                      necessary data is available.

  AC-05                               Stale GPS data is identified and is
                                      not presented as a fresh position
                                      or precise ETA.

  AC-06                               A passenger can submit LOW, MEDIUM,
                                      or HIGH crowding.

  AC-07                               Invalid crowd levels and unknown
                                      route IDs are rejected.

  AC-08                               Crowd analytics uses only the
                                      configured recent time window and
                                      relevant target.

  AC-09                               The system returns UNKNOWN when
                                      valid reports are insufficient.

  AC-10                               Reliable HIGH crowding persisting
                                      for at least 10 minutes creates an
                                      alert.

  AC-11                               The same ongoing incident does not
                                      create duplicate active alerts.

  AC-12                               The sustained-HIGH timer survives a
                                      backend restart.

  AC-13                               An operator can view, acknowledge,
                                      and resolve alerts with
                                      authorization.

  AC-14                               The interface clearly identifies
                                      simulated GPS data.

  AC-15                               The system recommends evaluating
                                      additional bus capacity but does
                                      not dispatch a real bus
                                      automatically.

  AC-16                               Automated tests cover the main
                                      crowding and alert rules.
  -----------------------------------------------------------------------

------------------------------------------------------------------------

## 16. Testing Plan

### Unit tests

Test the logic in isolation:

-   Crowd status with zero, one, two, and three or more reports.
-   LOW/MEDIUM/HIGH threshold boundaries.
-   Reports outside the 10-minute window.
-   Duplicate report handling.
-   ETA calculation and invalid speed/distance cases.
-   Stale GPS detection.
-   Alert creation exactly at and after the 10-minute threshold.
-   Timer reset on reliable recovery.
-   UNKNOWN-state handling.
-   Duplicate active-alert prevention.

### Integration tests

Test API and database behavior:

-   Creating and retrieving crowd reports.
-   Recalculating and retrieving crowd status.
-   Persisting `high_since`.
-   Creating an alert from sustained HIGH.
-   Acknowledging and resolving an alert.
-   Ensuring state survives application restart.

### End-to-end demonstration scenario

1.  Start the application and database.
2.  Load seeded routes, stops, and simulated buses.
3.  Open the passenger interface and select a route.
4.  Confirm that buses and approximate ETAs appear.
5.  Submit enough HIGH reports for the chosen target.
6.  Confirm that the operator dashboard shows HIGH with the correct
    report count.
7.  Keep the state reliably HIGH for at least 10 minutes.
8.  Confirm that one alert appears with the spare/additional-bus
    recommendation.
9.  Confirm that repeated analytics evaluations do not create duplicate
    alerts.
10. Submit reliable LOW/MEDIUM reports and verify the configured
    recovery behavior.

For a faster automated test, inject a clock abstraction or configurable
duration in test environments rather than waiting ten real minutes. The
production rule should remain ten minutes.

------------------------------------------------------------------------

## 17. Main Risks and Mitigations

  -----------------------------------------------------------------------
  Risk                    Why it matters          Mitigation
  ----------------------- ----------------------- -----------------------
  No live GPS feed        Real vehicle positions  Use clearly labelled
                          cannot be shown         simulation; verify
                                                  official access early

  Too few crowd reports   Status may be           Use UNKNOWN and show
                          unreliable              report counts

  False or repeated       Crowding can be         Rate limits,
  reports                 misclassified           deduplication, recency
                                                  filtering

  Stale positions         ETA and alert context   Track observation time
                          may be wrong            and freshness

  Route-wide aggregation  Operators may act on    Associate data with
  hides local congestion  misleading summaries    stops, trips, vehicles,
                                                  or segments

  Duplicate alerts        Operators may lose      Persist alert state and
                          trust in the system     enforce active-incident
                                                  uniqueness

  Server restart resets   Ten-minute rule may     Persist `high_since` in
  the timer               behave incorrectly      the database

  Overengineering         Delivery slows before   Use a modular monolith;
                          the core workflow works defer microservices and
                                                  advanced prediction

  Simulated data mistaken Demo results can        Prominent demo labels
  for real data           mislead users           and metadata indicating
                                                  data source
  -----------------------------------------------------------------------

------------------------------------------------------------------------

## 18. Explicitly Out of Scope for the MVP

Do not build these before the core workflow works:

-   AI-based passenger-count prediction.
-   Camera-based passenger counting.
-   Automatic real-world bus dispatch.
-   Native Android and iOS apps.
-   A microservices architecture.
-   Complex user reputation and fraud-detection models.
-   Advanced traffic prediction.
-   Full fleet scheduling and driver rostering.
-   Large-scale infrastructure for millions of users.

These may be future extensions, but they are not necessary to prove the
prototype's core value.

------------------------------------------------------------------------

## 19. Recommended Development Order

Implement the core backend flow before spending time polishing the
interface:

1.  Create the repository, Go server, database, and migrations.
2.  Seed routes, stops, and demo vehicles.
3.  Implement the GPS simulator and position API.
4.  Implement current vehicle positions and basic ETA.
5.  Implement crowd-report submission.
6.  Implement the 10-minute crowd analytics window and `UNKNOWN` state.
7.  Implement persistent sustained-HIGH tracking and alert creation.
8.  Add alert lifecycle and duplicate prevention.
9.  Build the passenger UI.
10. Build the operator dashboard.
11. Add integration tests and a repeatable demo scenario.

The critical workflow is:

``` text
GPS + passenger reports
          ↓
    Crowd calculation
          ↓
Reliable HIGH status persists for 10 minutes
          ↓
 Persistent operator alert
          ↓
Recommendation to evaluate an additional bus
```

If this workflow is not reliable, a polished map will not make the
project operationally useful.

------------------------------------------------------------------------

## 20. Decisions to Confirm Before Implementation

Resolve these decisions at the start:

1.  **Demo or live integration:** Is simulated GPS acceptable for the
    first presentation, or is a verified live feed a hard requirement?
2.  **Crowding granularity:** Will the initial crowd status be tied to a
    vehicle, trip, stop, route segment, or whole route? Prefer the most
    specific target the available data supports.
3.  **Ten-minute interpretation:** Should HIGH be continuously present
    for ten minutes, or should any HIGH report within a ten-minute
    window trigger an alert? The recommended interpretation is
    **reliable, continuously HIGH status for at least ten minutes**; a
    single report within the window is not enough.
4.  **Alert action:** The prototype should issue a recommendation for an
    operator to evaluate an additional bus, not execute a dispatch.
5.  **Operator access:** Decide whether the demo needs a simple operator
    login and which roles may acknowledge or resolve alerts.

## Final Recommendation

Build a reliable, testable Go backend around four core capabilities: GPS
ingestion, crowd-report processing, crowd-state calculation, and
persistent alerting. Use simulated GPS initially unless access to real
data is confirmed. Keep the frontend responsive and straightforward,
show uncertainty explicitly, and make the ten-minute alert rule
deterministic and testable.

The strongest demonstration is not merely a map with moving bus icons.
It is a repeatable end-to-end scenario in which the system receives
data, calculates crowding, waits for reliable sustained HIGH status,
creates one operator alert, and records how the operator handles it.
