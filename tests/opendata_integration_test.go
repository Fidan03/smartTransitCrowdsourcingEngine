package tests

import (
	"errors"
	"testing"
	"time"

	"smart-transit/internal/demand"
	"smart-transit/internal/geo"
	"smart-transit/internal/opendata"
	"smart-transit/internal/transit"
)

func gp(lat, lon float64) geo.Point { return geo.Point{Lat: lat, Lon: lon} }

// syntheticImport: one round-trip route "62" with stops on the correct sides,
// plus ridership for "62" and for "149E" (a route without geometry).
func syntheticImport(t *testing.T) opendata.Import {
	t.Helper()
	lines := []opendata.RouteLine{{Number: "62", Parts: [][]geo.Point{{
		gp(40.40, 49.80), gp(40.40, 49.81), gp(40.40, 49.82), gp(40.401, 49.82), gp(40.401, 49.81), gp(40.401, 49.80),
	}}}}
	var stops []opendata.Stop
	for i, lon := range []float64{49.802, 49.808, 49.814, 49.819} {
		stops = append(stops,
			opendata.Stop{Code: "E" + string(rune('A'+i)), Name: "East stop " + string(rune('A'+i)), Lat: 40.39990, Lon: lon},
			opendata.Stop{Code: "W" + string(rune('A'+i)), Name: "West stop " + string(rune('A'+i)), Lat: 40.40110, Lon: lon})
	}
	built, skipped := opendata.Build(lines, stops, opendata.DefaultBuildConfig())
	if len(built) != 1 || len(skipped) != 0 {
		t.Fatalf("build: %d built, skipped %+v", len(built), skipped)
	}
	var ride []opendata.RidershipRow
	for i := 0; i < 14; i++ {
		d := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC).AddDate(0, 0, i)
		ride = append(ride, opendata.RidershipRow{RouteNumber: "62", Date: d, Trips: 20000},
			opendata.RidershipRow{RouteNumber: "149E", Date: d, Trips: 1000})
	}
	return opendata.Import{
		Routes: built, Stops: stops, Ridership: ride, SimulatedRoutes: 10,
		Sources: []transit.DataSource{{ID: "stops", Title: "BakuBus stops", Publisher: "Bakı Metropoliteni",
			URL: "https://opendata.az/dataset/x", License: "Not specified by publisher", RetrievedAt: t0, Records: len(stops)}},
	}
}

func TestOpenDataImportReplacesDemoData(t *testing.T) {
	pool := freshDB(t) // has demo routes D1-D3
	imp := syntheticImport(t)

	if _, err := opendata.Load(ctx, pool, imp, false, t0); !errors.Is(err, opendata.ErrNotEmpty) {
		t.Fatalf("without replace: %v, want ErrNotEmpty", err)
	}
	sum, err := opendata.Load(ctx, pool, imp, true, t0)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Routes != 1 || sum.Stops != 8 || sum.RouteStops != 8 || sum.RidershipRows != 28 || sum.SimulatedBuses != 2 {
		t.Fatalf("summary: %+v", sum)
	}

	ts := transit.NewPGStore(pool)
	routes, _ := ts.ListRoutes(ctx)
	if len(routes) != 1 || routes[0].ID != "bb_62" || routes[0].DataSource != "opendata.az" || routes[0].IsSimulated {
		t.Fatalf("routes after import: %+v", routes)
	}
	if routes[0].Name != "East stop A – East stop D" {
		t.Errorf("route name from terminal stops: %q", routes[0].Name)
	}

	// Direction 1 uses the stored return path (the parallel street), with its own stops.
	paths := transit.NewPathCache(ts)
	p1, err := paths.Get(ctx, "bb_62", 1)
	if err != nil {
		t.Fatal(err)
	}
	if first := p1.Line.PointAt(0); first.Lat != 40.40 && first.Lat != 40.401 {
		t.Errorf("unexpected return start %v", first)
	}
	stops1, _ := ts.ListRouteStops(ctx, "bb_62", 1)
	if len(stops1) != 4 || stops1[0].Name != "West stop D" || stops1[3].Name != "West stop A" {
		t.Errorf("return stops: %+v", stops1)
	}

	// Simulated buses on the busiest route, one per direction.
	trips, _ := ts.ActiveTrips(ctx)
	if len(trips) != 2 || trips[0].RouteID != "bb_62" {
		t.Errorf("trips: %+v", trips)
	}

	srcs, _ := ts.ListDataSources(ctx)
	if len(srcs) != 1 || srcs[0].License != "Not specified by publisher" {
		t.Errorf("data sources: %+v", srcs)
	}

	// The demand model loads ridership for routes with and without geometry.
	svc := demand.NewService(pool)
	if err := svc.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	if s, ok := svc.Summary("62"); !ok || s.AvgDailyTrips != 20000 || s.Days != 14 {
		t.Errorf("summary 62: %+v %v", s, ok)
	}
	if _, ok := svc.Summary("149E"); !ok {
		t.Error("ridership without geometry should still load")
	}
	if e, ok := svc.Estimate("62", time.Date(2026, 9, 15, 14, 0, 0, 0, time.UTC)); !ok || !e.IsEstimate {
		t.Errorf("estimate: %+v %v", e, ok)
	}
}
