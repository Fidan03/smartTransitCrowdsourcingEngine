package crowd

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PGStore struct {
	pool *pgxpool.Pool
}

func NewPGStore(pool *pgxpool.Pool) *PGStore { return &PGStore{pool: pool} }

// InsertIfNotDuplicate takes a transaction-scoped advisory lock on
// (route, source) so concurrent submissions from the same source cannot both
// pass the duplicate check.
func (s *PGStore) InsertIfNotDuplicate(ctx context.Context, r Report, dupSince time.Time) (Report, *time.Time, error) {
	var prevAt *time.Time
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "crowd:"+r.RouteID+":"+r.SourceKey); err != nil {
			return err
		}
		var last time.Time
		err := tx.QueryRow(ctx, `
			SELECT created_at FROM crowd_reports
			WHERE route_id = $1 AND source_key = $2 AND created_at > $3
			ORDER BY created_at DESC LIMIT 1`, r.RouteID, r.SourceKey, dupSince).Scan(&last)
		if err == nil {
			prevAt = &last
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		return tx.QueryRow(ctx, `
			INSERT INTO crowd_reports (route_id, stop_id, vehicle_id, trip_id, crowd_level, created_at, source_key)
			VALUES ($1, $2, $3, $4::uuid, $5, $6, $7)
			RETURNING id::text`,
			r.RouteID, r.StopID, r.VehicleID, r.TripID, string(r.Level), r.CreatedAt, r.SourceKey,
		).Scan(&r.ID)
	})
	if err != nil {
		return Report{}, nil, err
	}
	return r, prevAt, nil
}

// RecentReports returns reports for a route created in (since, until].
func RecentReports(ctx context.Context, q interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}, routeID string, since, until time.Time) ([]Report, error) {
	rows, err := q.Query(ctx, `
		SELECT id::text, route_id, stop_id, vehicle_id, trip_id::text, crowd_level, created_at, source_key
		FROM crowd_reports
		WHERE route_id = $1 AND created_at > $2 AND created_at <= $3`, routeID, since, until)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Report, error) {
		var rep Report
		var level string
		err := r.Scan(&rep.ID, &rep.RouteID, &rep.StopID, &rep.VehicleID, &rep.TripID, &level, &rep.CreatedAt, &rep.SourceKey)
		rep.Level = Level(level)
		return rep, err
	})
}

// RouteStatus is the persisted crowding state of a route.
type RouteStatus struct {
	RouteID      string     `json:"route_id"`
	Status       Status     `json:"status"`
	ValidCount   int        `json:"valid_report_count"`
	LowCount     int        `json:"low_count"`
	MediumCount  int        `json:"medium_count"`
	HighCount    int        `json:"high_count"`
	LastReportAt *time.Time `json:"last_report_at"`
	CalculatedAt *time.Time `json:"calculated_at"`
	HighSince    *time.Time `json:"high_since"`
}

// GetRouteStatus returns the persisted status, or UNKNOWN if never calculated.
func (s *PGStore) GetRouteStatus(ctx context.Context, routeID string) (RouteStatus, error) {
	rs := RouteStatus{RouteID: routeID}
	var status string
	var calc time.Time
	err := s.pool.QueryRow(ctx, `
		SELECT status, valid_report_count, low_count, medium_count, high_count,
		       last_report_at, calculated_at, high_since
		FROM route_crowd_status WHERE target_type = 'ROUTE' AND target_id = $1`, routeID,
	).Scan(&status, &rs.ValidCount, &rs.LowCount, &rs.MediumCount, &rs.HighCount,
		&rs.LastReportAt, &calc, &rs.HighSince)
	if errors.Is(err, pgx.ErrNoRows) {
		rs.Status = StatusUnknown
		return rs, nil
	}
	if err != nil {
		return RouteStatus{}, err
	}
	rs.Status = Status(status)
	rs.CalculatedAt = &calc
	return rs, nil
}
