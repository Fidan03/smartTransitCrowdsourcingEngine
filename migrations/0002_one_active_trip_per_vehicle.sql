-- A vehicle can be on at most one IN_PROGRESS trip at a time.
DROP INDEX IF EXISTS trips_vehicle_active_idx;
CREATE UNIQUE INDEX trips_one_active_per_vehicle_idx
    ON trips (vehicle_id) WHERE status = 'IN_PROGRESS';
