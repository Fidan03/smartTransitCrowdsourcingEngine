-- Initial schema for the Smart Transit & Crowdsourcing Engine (plan section 9).
-- All timestamps are TIMESTAMPTZ and are written in UTC by the application.

CREATE TABLE routes (
    id            TEXT PRIMARY KEY,
    route_number  TEXT NOT NULL,
    name          TEXT NOT NULL DEFAULT '',
    -- Ordered [[lat, lon], ...] polyline. PostGIS can replace this later if needed.
    geometry      JSONB NOT NULL DEFAULT '[]'::jsonb,
    is_simulated  BOOLEAN NOT NULL DEFAULT TRUE,
    active        BOOLEAN NOT NULL DEFAULT TRUE,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE stops (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL,
    latitude    DOUBLE PRECISION NOT NULL CHECK (latitude BETWEEN -90 AND 90),
    longitude   DOUBLE PRECISION NOT NULL CHECK (longitude BETWEEN -180 AND 180),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE route_stops (
    route_id    TEXT NOT NULL REFERENCES routes(id) ON DELETE CASCADE,
    stop_id     TEXT NOT NULL REFERENCES stops(id) ON DELETE RESTRICT,
    direction   SMALLINT NOT NULL DEFAULT 0 CHECK (direction IN (0, 1)),
    stop_order  INTEGER NOT NULL CHECK (stop_order >= 0),
    PRIMARY KEY (route_id, direction, stop_order),
    UNIQUE (route_id, direction, stop_id)
);

CREATE TABLE vehicles (
    id          TEXT PRIMARY KEY,
    label       TEXT NOT NULL,
    active      BOOLEAN NOT NULL DEFAULT TRUE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE trips (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    route_id    TEXT NOT NULL REFERENCES routes(id),
    vehicle_id  TEXT NOT NULL REFERENCES vehicles(id),
    direction   SMALLINT NOT NULL DEFAULT 0 CHECK (direction IN (0, 1)),
    started_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    ended_at    TIMESTAMPTZ,
    status      TEXT NOT NULL DEFAULT 'IN_PROGRESS'
                CHECK (status IN ('SCHEDULED', 'IN_PROGRESS', 'COMPLETED', 'CANCELLED'))
);
CREATE INDEX trips_vehicle_active_idx ON trips (vehicle_id) WHERE status = 'IN_PROGRESS';

CREATE TABLE vehicle_positions (
    id           BIGSERIAL PRIMARY KEY,
    vehicle_id   TEXT NOT NULL REFERENCES vehicles(id),
    route_id     TEXT REFERENCES routes(id),
    trip_id      UUID REFERENCES trips(id),
    latitude     DOUBLE PRECISION NOT NULL CHECK (latitude BETWEEN -90 AND 90),
    longitude    DOUBLE PRECISION NOT NULL CHECK (longitude BETWEEN -180 AND 180),
    speed_kmh    DOUBLE PRECISION CHECK (speed_kmh IS NULL OR speed_kmh >= 0),
    observed_at  TIMESTAMPTZ NOT NULL,           -- when the position was measured
    received_at  TIMESTAMPTZ NOT NULL DEFAULT now(), -- when the backend received it
    source       TEXT NOT NULL,                  -- e.g. 'simulator', 'gtfs-rt'
    is_simulated BOOLEAN NOT NULL
);
CREATE INDEX vehicle_positions_vehicle_observed_idx ON vehicle_positions (vehicle_id, observed_at DESC);
CREATE INDEX vehicle_positions_route_observed_idx ON vehicle_positions (route_id, observed_at DESC);

CREATE TABLE crowd_reports (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    route_id     TEXT NOT NULL REFERENCES routes(id),
    stop_id      TEXT REFERENCES stops(id),
    vehicle_id   TEXT REFERENCES vehicles(id),
    trip_id      UUID REFERENCES trips(id),
    crowd_level  TEXT NOT NULL CHECK (crowd_level IN ('LOW', 'MEDIUM', 'HIGH')),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(), -- server receipt time (authoritative)
    -- Opaque, non-personal key (e.g. hashed device token) used for dedup/rate limiting.
    source_key   TEXT NOT NULL
);
CREATE INDEX crowd_reports_route_created_idx ON crowd_reports (route_id, created_at DESC);
CREATE INDEX crowd_reports_source_created_idx ON crowd_reports (source_key, created_at DESC);

CREATE TABLE route_crowd_status (
    target_type         TEXT NOT NULL CHECK (target_type IN ('ROUTE', 'VEHICLE', 'STOP', 'SEGMENT')),
    target_id           TEXT NOT NULL,
    status              TEXT NOT NULL CHECK (status IN ('LOW', 'MEDIUM', 'HIGH', 'UNKNOWN')),
    valid_report_count  INTEGER NOT NULL DEFAULT 0,
    last_report_at      TIMESTAMPTZ,
    calculated_at       TIMESTAMPTZ NOT NULL,
    -- Start of the current continuous reliable-HIGH period. Persisted so the
    -- 10-minute timer survives backend restarts (plan 8.3, AC-12).
    high_since          TIMESTAMPTZ,
    PRIMARY KEY (target_type, target_id)
);

CREATE TABLE alerts (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    target_type      TEXT NOT NULL CHECK (target_type IN ('ROUTE', 'VEHICLE', 'STOP', 'SEGMENT')),
    target_id        TEXT NOT NULL,
    status           TEXT NOT NULL DEFAULT 'ACTIVE'
                     CHECK (status IN ('ACTIVE', 'ACKNOWLEDGED', 'RESOLVED')),
    severity         TEXT NOT NULL DEFAULT 'HIGH',
    reason           TEXT NOT NULL,
    recommendation   TEXT NOT NULL,
    high_since       TIMESTAMPTZ NOT NULL,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    acknowledged_at  TIMESTAMPTZ,
    acknowledged_by  UUID,
    resolved_at      TIMESTAMPTZ,
    resolved_by      UUID
);
-- At most one open (not RESOLVED) alert per target: prevents duplicate alerts
-- for the same ongoing incident even under concurrent evaluation (plan 8.3, AC-11).
CREATE UNIQUE INDEX alerts_one_open_per_target_idx
    ON alerts (target_type, target_id)
    WHERE status IN ('ACTIVE', 'ACKNOWLEDGED');
CREATE INDEX alerts_status_created_idx ON alerts (status, created_at DESC);

CREATE TABLE users (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email          TEXT NOT NULL UNIQUE,
    password_hash  TEXT NOT NULL,
    role           TEXT NOT NULL CHECK (role IN ('OPERATOR', 'ADMIN')),
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE alerts
    ADD CONSTRAINT alerts_acknowledged_by_fk FOREIGN KEY (acknowledged_by) REFERENCES users(id),
    ADD CONSTRAINT alerts_resolved_by_fk FOREIGN KEY (resolved_by) REFERENCES users(id);

CREATE TABLE audit_logs (
    id          BIGSERIAL PRIMARY KEY,
    user_id     UUID REFERENCES users(id),
    action      TEXT NOT NULL,
    target_id   TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    metadata    JSONB NOT NULL DEFAULT '{}'::jsonb
);
CREATE INDEX audit_logs_created_idx ON audit_logs (created_at DESC);
