-- Real open data from opendata.az (AYNA route paths, BakuBus stops and ridership).

-- Real routes often return along different streets (one-way systems), so the
-- direction-1 path is stored explicitly. NULL means "direction 0 reversed".
ALTER TABLE routes ADD COLUMN geometry_return JSONB;

-- Where the route reference data came from: 'demo' (fictional seed) or 'opendata.az'.
ALTER TABLE routes ADD COLUMN data_source TEXT NOT NULL DEFAULT 'demo';

-- Daily passenger trips per route from BakuBus validators (real, historical).
-- Keyed by public route number because ridership can exist for routes whose
-- geometry was not imported. Used ONLY for the "expected crowding" estimate and
-- operator context, never by the alert engine.
CREATE TABLE route_daily_ridership (
    route_number  TEXT NOT NULL,
    service_date  DATE NOT NULL,
    trips         INTEGER NOT NULL CHECK (trips >= 0),
    PRIMARY KEY (route_number, service_date)
);

-- Provenance and attribution for imported datasets (CC BY requires credit).
CREATE TABLE data_sources (
    id            TEXT PRIMARY KEY,
    title         TEXT NOT NULL,
    publisher     TEXT NOT NULL,
    url           TEXT NOT NULL,
    license       TEXT NOT NULL,
    retrieved_at  TIMESTAMPTZ NOT NULL,
    records       INTEGER NOT NULL DEFAULT 0,
    notes         TEXT NOT NULL DEFAULT ''
);
