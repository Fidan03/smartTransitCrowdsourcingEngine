package gps

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PGStore implements position persistence and queries on PostgreSQL.
type PGStore struct {
	pool *pgxpool.Pool
}

func NewPGStore(pool *pgxpool.Pool) *PGStore { return &PGStore{pool: pool} }

func nullIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func (s *PGStore) InsertPosition(ctx context.Context, p Position, receivedAt time.Time) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO vehicle_positions
			(vehicle_id, route_id, trip_id, latitude, longitude, speed_kmh,
			 observed_at, received_at, source, is_simulated)
		VALUES ($1, $2, $3::uuid, $4, $5, $6, $7, $8, $9, $10)`,
		p.VehicleID, nullIfEmpty(p.RouteID), nullIfEmpty(p.TripID), p.Latitude, p.Longitude,
		p.SpeedKmh, p.ObservedAt, receivedAt, p.Source, p.IsSimulated)

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23503": // foreign_key_violation
			return ErrUnknownReference
		case "22P02": // invalid_text_representation (malformed trip UUID)
			return &ValidationError{Msg: "trip_id is not a valid ID"}
		}
	}
	return err
}

// LastTripPosition returns the most recent position recorded for a trip, so
// the simulator can resume where it left off after a restart.
func (s *PGStore) LastTripPosition(ctx context.Context, tripID string) (lat, lon float64, ok bool, err error) {
	err = s.pool.QueryRow(ctx, `
		SELECT latitude, longitude FROM vehicle_positions
		WHERE trip_id = $1
		ORDER BY observed_at DESC
		LIMIT 1`, tripID).Scan(&lat, &lon)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, 0, false, nil
	}
	return lat, lon, err == nil, err
}

// TimedPoint is a position with its observation time.
type TimedPoint struct {
	Lat, Lon   float64
	ObservedAt time.Time
}

// TripPositionSpan returns the earliest and latest positions of a trip
// observed after `since`, used to measure recent average speed for ETA.
func (s *PGStore) TripPositionSpan(ctx context.Context, tripID string, since time.Time) (first, last TimedPoint, ok bool, err error) {
	err = s.pool.QueryRow(ctx, `
		SELECT f.latitude, f.longitude, f.observed_at, l.latitude, l.longitude, l.observed_at
		FROM (SELECT latitude, longitude, observed_at FROM vehicle_positions
		      WHERE trip_id = $1 AND observed_at > $2 ORDER BY observed_at ASC LIMIT 1) f,
		     (SELECT latitude, longitude, observed_at FROM vehicle_positions
		      WHERE trip_id = $1 AND observed_at > $2 ORDER BY observed_at DESC LIMIT 1) l`,
		tripID, since).Scan(&first.Lat, &first.Lon, &first.ObservedAt, &last.Lat, &last.Lon, &last.ObservedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return first, last, false, nil
	}
	return first, last, err == nil, err
}

// VehicleState is a vehicle's latest known state on a route. Position fields
// are nil when the vehicle has an active trip but no recent observation.
type VehicleState struct {
	VehicleID   string
	Label       string
	TripID      *string
	Direction   *int
	Latitude    *float64
	Longitude   *float64
	SpeedKmh    *float64
	ObservedAt  *time.Time
	ReceivedAt  *time.Time
	Source      *string
	IsSimulated *bool
}

// ListRouteVehicles returns vehicles with an active trip on the route and/or a
// position on the route observed after `since`. Older positions are ignored.
func (s *PGStore) ListRouteVehicles(ctx context.Context, routeID string, since time.Time) ([]VehicleState, error) {
	rows, err := s.pool.Query(ctx, `
		WITH latest AS (
			SELECT DISTINCT ON (vehicle_id)
			       vehicle_id, latitude, longitude, speed_kmh,
			       observed_at, received_at, source, is_simulated
			FROM vehicle_positions
			WHERE route_id = $1 AND observed_at > $2
			ORDER BY vehicle_id, observed_at DESC
		)
		SELECT v.id, v.label, t.id::text, t.direction,
		       l.latitude, l.longitude, l.speed_kmh,
		       l.observed_at, l.received_at, l.source, l.is_simulated
		FROM vehicles v
		LEFT JOIN trips t
		       ON t.vehicle_id = v.id AND t.route_id = $1 AND t.status = 'IN_PROGRESS'
		LEFT JOIN latest l ON l.vehicle_id = v.id
		WHERE v.active AND (t.id IS NOT NULL OR l.vehicle_id IS NOT NULL)
		ORDER BY v.id`, routeID, since)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (VehicleState, error) {
		var v VehicleState
		err := r.Scan(&v.VehicleID, &v.Label, &v.TripID, &v.Direction,
			&v.Latitude, &v.Longitude, &v.SpeedKmh,
			&v.ObservedAt, &v.ReceivedAt, &v.Source, &v.IsSimulated)
		return v, err
	})
}
