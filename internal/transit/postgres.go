package transit

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PGStore implements Store on PostgreSQL.
type PGStore struct {
	pool *pgxpool.Pool
}

func NewPGStore(pool *pgxpool.Pool) *PGStore { return &PGStore{pool: pool} }

func (s *PGStore) ListRoutes(ctx context.Context) ([]Route, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, route_number, name, is_simulated
		FROM routes
		WHERE active
		ORDER BY route_number`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Route, error) {
		var rt Route
		err := r.Scan(&rt.ID, &rt.RouteNumber, &rt.Name, &rt.IsSimulated)
		return rt, err
	})
}

func (s *PGStore) GetRoute(ctx context.Context, id string) (Route, error) {
	var rt Route
	err := s.pool.QueryRow(ctx, `
		SELECT id, route_number, name, is_simulated, geometry
		FROM routes
		WHERE id = $1 AND active`, id,
	).Scan(&rt.ID, &rt.RouteNumber, &rt.Name, &rt.IsSimulated, &rt.Geometry)
	if errors.Is(err, pgx.ErrNoRows) {
		return Route{}, ErrNotFound
	}
	return rt, err
}

func (s *PGStore) GetStop(ctx context.Context, id string) (Stop, error) {
	var st Stop
	err := s.pool.QueryRow(ctx, `SELECT id, name, latitude, longitude FROM stops WHERE id = $1`, id).
		Scan(&st.ID, &st.Name, &st.Latitude, &st.Longitude)
	if errors.Is(err, pgx.ErrNoRows) {
		return Stop{}, ErrNotFound
	}
	return st, err
}

func (s *PGStore) ListStopRoutes(ctx context.Context, stopID string) ([]StopRoute, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT r.id, r.route_number, r.name, rs.direction, rs.stop_order
		FROM route_stops rs
		JOIN routes r ON r.id = rs.route_id
		WHERE rs.stop_id = $1 AND r.active
		ORDER BY r.route_number, rs.direction`, stopID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (StopRoute, error) {
		var sr StopRoute
		err := r.Scan(&sr.RouteID, &sr.RouteNumber, &sr.RouteName, &sr.Direction, &sr.StopOrder)
		return sr, err
	})
}

// StopOnRoute reports whether the stop is served by the route (any direction).
func (s *PGStore) StopOnRoute(ctx context.Context, routeID, stopID string) (bool, error) {
	var ok bool
	err := s.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM route_stops WHERE route_id = $1 AND stop_id = $2)`,
		routeID, stopID).Scan(&ok)
	return ok, err
}

func (s *PGStore) VehicleExists(ctx context.Context, id string) (bool, error) {
	var ok bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM vehicles WHERE id = $1 AND active)`, id).Scan(&ok)
	return ok, err
}

// ActiveTripForVehicle returns the vehicle's IN_PROGRESS trip, if any.
func (s *PGStore) ActiveTripForVehicle(ctx context.Context, vehicleID string) (Trip, bool, error) {
	t, err := scanTrip(s.pool.QueryRow(ctx, `
		SELECT id::text, route_id, vehicle_id, direction, started_at, status
		FROM trips WHERE vehicle_id = $1 AND status = 'IN_PROGRESS'`, vehicleID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Trip{}, false, nil
	}
	return t, err == nil, err
}

// ActiveRouteIDs lists IDs of all active routes.
func (s *PGStore) ActiveRouteIDs(ctx context.Context) ([]string, error) {
	rows, err := s.pool.Query(ctx, `SELECT id FROM routes WHERE active ORDER BY id`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

// ActiveTrips returns every IN_PROGRESS trip.
func (s *PGStore) ActiveTrips(ctx context.Context) ([]Trip, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, route_id, vehicle_id, direction, started_at, status
		FROM trips
		WHERE status = 'IN_PROGRESS'
		ORDER BY vehicle_id`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Trip, error) { return scanTrip(r) })
}

// RotateTrip atomically completes an IN_PROGRESS trip and starts the next one
// for the same vehicle and route in the given direction. Doing both in one
// transaction means a crash can never leave a vehicle with zero or two
// active trips.
func (s *PGStore) RotateTrip(ctx context.Context, oldTripID string, nextDirection int, at time.Time) (Trip, error) {
	var next Trip
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var routeID, vehicleID string
		err := tx.QueryRow(ctx, `
			UPDATE trips SET status = 'COMPLETED', ended_at = $2
			WHERE id = $1 AND status = 'IN_PROGRESS'
			RETURNING route_id, vehicle_id`, oldTripID, at).Scan(&routeID, &vehicleID)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		next, err = scanTrip(tx.QueryRow(ctx, `
			INSERT INTO trips (route_id, vehicle_id, direction, started_at, status)
			VALUES ($1, $2, $3, $4, 'IN_PROGRESS')
			RETURNING id::text, route_id, vehicle_id, direction, started_at, status`,
			routeID, vehicleID, nextDirection, at))
		return err
	})
	return next, err
}

func scanTrip(r pgx.Row) (Trip, error) {
	var t Trip
	err := r.Scan(&t.ID, &t.RouteID, &t.VehicleID, &t.Direction, &t.StartedAt, &t.Status)
	return t, err
}

func (s *PGStore) ListRouteStops(ctx context.Context, routeID string, direction int) ([]RouteStop, error) {
	if _, err := s.GetRoute(ctx, routeID); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT s.id, s.name, s.latitude, s.longitude, rs.direction, rs.stop_order
		FROM route_stops rs
		JOIN stops s ON s.id = rs.stop_id
		WHERE rs.route_id = $1 AND rs.direction = $2
		ORDER BY rs.stop_order`, routeID, direction)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (RouteStop, error) {
		var st RouteStop
		err := r.Scan(&st.ID, &st.Name, &st.Latitude, &st.Longitude, &st.Direction, &st.StopOrder)
		return st, err
	})
}
