package opendata

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"smart-transit/internal/geo"
	"smart-transit/internal/transit"
)

// Import is everything to load into the database.
type Import struct {
	Routes    []BuiltRoute
	Stops     []Stop
	Ridership []RidershipRow
	Sources   []transit.DataSource
	// SimulatedRoutes is how many of the busiest imported routes get
	// simulated buses (two each, one per direction).
	SimulatedRoutes int
}

type Summary struct {
	Routes, Stops, RouteStops, RidershipRows, SimulatedBuses int
	SimulatedOn                                              []string
}

// ErrNotEmpty is returned when routes exist and replace was not requested.
var ErrNotEmpty = errors.New("database already has routes; use -replace to overwrite them")

var nonIDChars = regexp.MustCompile(`[^a-z0-9]+`)

// RouteID builds a stable ID from a public route number, e.g. "7A" -> "bb_7a".
func RouteID(number string) string {
	return "bb_" + strings.Trim(nonIDChars.ReplaceAllString(strings.ToLower(number), "_"), "_")
}

func toLatLon(pts []geo.Point) []transit.LatLon {
	out := make([]transit.LatLon, len(pts))
	for i, p := range pts {
		out[i] = transit.LatLon{round6(p.Lat), round6(p.Lon)}
	}
	return out
}

func round6(v float64) float64 { return math.Round(v*1e6) / 1e6 } // ~0.1 m precision

// Load replaces transit reference data with the import in one transaction.
// Operational data tied to the old routes (positions, trips, reports,
// statuses, route alerts) is removed with it. Users and audit logs are kept.
func Load(ctx context.Context, pool *pgxpool.Pool, imp Import, replace bool, now time.Time) (Summary, error) {
	var sum Summary
	stopByCode := map[string]Stop{}
	for _, s := range imp.Stops {
		stopByCode[s.Code] = s
	}

	err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM routes`).Scan(&n); err != nil {
			return err
		}
		if n > 0 && !replace {
			return ErrNotEmpty
		}
		for _, q := range []string{
			`DELETE FROM alerts WHERE target_type = 'ROUTE'`,
			`DELETE FROM route_crowd_status`,
			`DELETE FROM crowd_reports`,
			`DELETE FROM vehicle_positions`,
			`DELETE FROM trips`,
			`DELETE FROM route_stops`,
			`DELETE FROM routes`,
			`DELETE FROM stops`,
			`DELETE FROM vehicles`,
			`DELETE FROM route_daily_ridership`,
			`DELETE FROM data_sources`,
		} {
			if _, err := tx.Exec(ctx, q); err != nil {
				return fmt.Errorf("%s: %w", q, err)
			}
		}

		// Stops actually used by imported routes.
		used := map[string]bool{}
		for _, r := range imp.Routes {
			for _, s := range append(append([]RouteStopRef{}, r.Stops0...), r.Stops1...) {
				used[s.Code] = true
			}
		}
		codes := make([]string, 0, len(used))
		for c := range used {
			codes = append(codes, c)
		}
		sort.Strings(codes)
		b := &pgx.Batch{}
		for _, c := range codes {
			s := stopByCode[c]
			b.Queue(`INSERT INTO stops (id, name, latitude, longitude) VALUES ($1, $2, $3, $4)`,
				"bbs_"+s.Code, s.Name, s.Lat, s.Lon)
		}
		if err := tx.SendBatch(ctx, b).Close(); err != nil {
			return fmt.Errorf("insert stops: %w", err)
		}
		sum.Stops = len(codes)

		// Routes and their ordered stops.
		b = &pgx.Batch{}
		for _, r := range imp.Routes {
			g0, _ := json.Marshal(toLatLon(r.Outbound))
			g1, _ := json.Marshal(toLatLon(r.Return))
			first := stopByCode[r.Stops0[0].Code].Name
			last := stopByCode[r.Stops0[len(r.Stops0)-1].Code].Name
			b.Queue(`INSERT INTO routes (id, route_number, name, geometry, geometry_return, is_simulated, data_source)
				VALUES ($1, $2, $3, $4, $5, FALSE, 'opendata.az')`,
				RouteID(r.Number), r.Number, first+" – "+last, g0, g1)
			for dir, refs := range [][]RouteStopRef{r.Stops0, r.Stops1} {
				for i, s := range refs {
					b.Queue(`INSERT INTO route_stops (route_id, stop_id, direction, stop_order) VALUES ($1, $2, $3, $4)`,
						RouteID(r.Number), "bbs_"+s.Code, dir, i)
					sum.RouteStops++
				}
			}
		}
		if err := tx.SendBatch(ctx, b).Close(); err != nil {
			return fmt.Errorf("insert routes: %w", err)
		}
		sum.Routes = len(imp.Routes)

		// Ridership (all routes, including ones without geometry).
		rows := make([][]any, 0, len(imp.Ridership))
		seen := map[string]bool{}
		for _, r := range imp.Ridership {
			key := r.RouteNumber + "|" + r.Date.Format("2006-01-02")
			if seen[key] {
				continue
			}
			seen[key] = true
			rows = append(rows, []any{r.RouteNumber, r.Date, r.Trips})
		}
		if _, err := tx.CopyFrom(ctx, pgx.Identifier{"route_daily_ridership"},
			[]string{"route_number", "service_date", "trips"}, pgx.CopyFromRows(rows)); err != nil {
			return fmt.Errorf("insert ridership: %w", err)
		}
		sum.RidershipRows = len(rows)

		for _, s := range imp.Sources {
			if _, err := tx.Exec(ctx, `
				INSERT INTO data_sources (id, title, publisher, url, license, retrieved_at, records, notes)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
				s.ID, s.Title, s.Publisher, s.URL, s.License, s.RetrievedAt, s.Records, s.Notes); err != nil {
				return fmt.Errorf("insert data source: %w", err)
			}
		}

		// Simulated buses on the busiest imported routes.
		total := map[string]int{}
		for _, r := range imp.Ridership {
			total[r.RouteNumber] += r.Trips
		}
		ranked := make([]BuiltRoute, len(imp.Routes))
		copy(ranked, imp.Routes)
		sort.SliceStable(ranked, func(i, j int) bool { return total[ranked[i].Number] > total[ranked[j].Number] })
		for i := 0; i < len(ranked) && i < imp.SimulatedRoutes; i++ {
			r := ranked[i]
			for dir := 0; dir <= 1; dir++ {
				vid := fmt.Sprintf("sim_%s_%d", strings.TrimPrefix(RouteID(r.Number), "bb_"), dir+1)
				if _, err := tx.Exec(ctx, `INSERT INTO vehicles (id, label) VALUES ($1, $2)`,
					vid, fmt.Sprintf("Simulated bus %s-%d", r.Number, dir+1)); err != nil {
					return err
				}
				if _, err := tx.Exec(ctx, `INSERT INTO trips (route_id, vehicle_id, direction, started_at, status)
					VALUES ($1, $2, $3, $4, 'IN_PROGRESS')`, RouteID(r.Number), vid, dir, now); err != nil {
					return err
				}
				sum.SimulatedBuses++
			}
			sum.SimulatedOn = append(sum.SimulatedOn, r.Number)
		}
		return nil
	})
	return sum, err
}
