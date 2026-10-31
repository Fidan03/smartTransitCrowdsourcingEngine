// Package operator provides read models for the operator dashboard
// (plan 5.2): summary statistics and the route crowding table.
package operator

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type RouteRow struct {
	RouteID          string     `json:"route_id"`
	RouteNumber      string     `json:"route_number"`
	RouteName        string     `json:"route_name"`
	CrowdStatus      string     `json:"crowd_status"`
	ValidReportCount int        `json:"valid_report_count"`
	LowCount         int        `json:"low_count"`
	MediumCount      int        `json:"medium_count"`
	HighCount        int        `json:"high_count"`
	LastReportAt     *time.Time `json:"last_report_at"`
	CalculatedAt     *time.Time `json:"last_analytics_update"`
	HighSince        *time.Time `json:"high_since"`
	LastGPSAt        *time.Time `json:"last_gps_update"`
	FreshVehicles    int        `json:"vehicles_with_recent_gps"`
	ActiveTrips      int        `json:"active_trips"`
	OpenAlertID      *string    `json:"open_alert_id"`
	OpenAlertStatus  *string    `json:"open_alert_status"`
}

type Dashboard struct {
	GeneratedAt            time.Time `json:"generated_at"`
	ActiveRoutes           int       `json:"active_routes"`
	VehiclesWithRecentGPS  int       `json:"vehicles_with_recent_gps"`
	HighRoutes             []string  `json:"high_routes"`
	ActiveAlerts           int       `json:"active_alerts"`
	AcknowledgedAlerts     int       `json:"acknowledged_alerts"`
	DataMode               string    `json:"data_mode"`
	StaleAfterSeconds      float64   `json:"stale_after_seconds"`
	AnalyticsWindowMinutes float64   `json:"analytics_window_minutes"`
}

type Queries struct {
	pool *pgxpool.Pool
}

func NewQueries(pool *pgxpool.Pool) *Queries { return &Queries{pool: pool} }

// Routes returns the route crowding table. Vehicles count as having recent
// GPS if observed after freshSince; last GPS looks back to maxAgeSince.
func (q *Queries) Routes(ctx context.Context, freshSince, maxAgeSince time.Time) ([]RouteRow, error) {
	rows, err := q.pool.Query(ctx, `
		SELECT r.id, r.route_number, r.name,
		       COALESCE(s.status, 'UNKNOWN'), COALESCE(s.valid_report_count, 0),
		       COALESCE(s.low_count, 0), COALESCE(s.medium_count, 0), COALESCE(s.high_count, 0),
		       s.last_report_at, s.calculated_at, s.high_since,
		       (SELECT max(vp.observed_at) FROM vehicle_positions vp
		         WHERE vp.route_id = r.id AND vp.observed_at > $2),
		       (SELECT count(DISTINCT vp.vehicle_id) FROM vehicle_positions vp
		         WHERE vp.route_id = r.id AND vp.observed_at > $1),
		       (SELECT count(*) FROM trips t WHERE t.route_id = r.id AND t.status = 'IN_PROGRESS'),
		       a.id::text, a.status
		FROM routes r
		LEFT JOIN route_crowd_status s ON s.target_type = 'ROUTE' AND s.target_id = r.id
		LEFT JOIN alerts a ON a.target_type = 'ROUTE' AND a.target_id = r.id
		                  AND a.status IN ('ACTIVE', 'ACKNOWLEDGED')
		WHERE r.active
		ORDER BY NULLIF(substring(r.route_number from '^[0-9]+'), '')::int NULLS LAST, r.route_number`,
		freshSince, maxAgeSince)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (RouteRow, error) {
		var x RouteRow
		err := r.Scan(&x.RouteID, &x.RouteNumber, &x.RouteName,
			&x.CrowdStatus, &x.ValidReportCount, &x.LowCount, &x.MediumCount, &x.HighCount,
			&x.LastReportAt, &x.CalculatedAt, &x.HighSince,
			&x.LastGPSAt, &x.FreshVehicles, &x.ActiveTrips, &x.OpenAlertID, &x.OpenAlertStatus)
		return x, err
	})
}

// Summary builds dashboard statistics.
func (q *Queries) Summary(ctx context.Context, now, freshSince time.Time, routes []RouteRow) (Dashboard, error) {
	d := Dashboard{GeneratedAt: now, ActiveRoutes: len(routes), HighRoutes: []string{}}
	for _, r := range routes {
		if r.CrowdStatus == "HIGH" {
			d.HighRoutes = append(d.HighRoutes, r.RouteNumber)
		}
	}
	err := q.pool.QueryRow(ctx, `
		SELECT (SELECT count(DISTINCT vehicle_id) FROM vehicle_positions WHERE observed_at > $1),
		       (SELECT count(*) FROM alerts WHERE status = 'ACTIVE'),
		       (SELECT count(*) FROM alerts WHERE status = 'ACKNOWLEDGED')`, freshSince,
	).Scan(&d.VehiclesWithRecentGPS, &d.ActiveAlerts, &d.AcknowledgedAlerts)
	return d, err
}
