-- Phase 4-8 schema additions: ETA speed lookups, crowd evidence counts,
-- alert recovery marker, and operator sessions.

-- ETA: recent positions for a trip.
CREATE INDEX vehicle_positions_trip_observed_idx ON vehicle_positions (trip_id, observed_at DESC);

-- Crowd reports: duplicate check per source and route.
CREATE INDEX crowd_reports_route_source_created_idx ON crowd_reports (route_id, source_key, created_at DESC);

-- Evidence behind each calculated status, shown to operators (plan 7.4).
ALTER TABLE route_crowd_status
    ADD COLUMN low_count    INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN medium_count INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN high_count   INTEGER NOT NULL DEFAULT 0;

-- Set when reliable LOW/MEDIUM returns while an alert is still open, so the
-- operator sees the alert is ready for review. Never auto-resolves (plan 8.2).
ALTER TABLE alerts ADD COLUMN recovered_at TIMESTAMPTZ;
ALTER TABLE alerts ADD COLUMN resolution_note TEXT;

CREATE TABLE sessions (
    token_hash  TEXT PRIMARY KEY,           -- SHA-256 of the session token; raw token never stored
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at  TIMESTAMPTZ NOT NULL
);
CREATE INDEX sessions_expires_idx ON sessions (expires_at);
