// Command importer loads real Baku bus data from opendata.az into the
// database, replacing the fictional demo routes:
//
//   - AYNA "Avtobusların hərəkət trayektoriyası" (route paths, KMZ, CC0)
//   - BakuBus stop coordinates (CSV; licence not specified by the publisher)
//   - BakuBus daily passenger trips 2026 (CSV, CC BY)
//
// Bus positions remain SIMULATED: opendata.az publishes no live GPS feed.
//
// Usage:
//
//	go run ./cmd/importer -replace            # download from opendata.az
//	go run ./cmd/importer -replace -kmz f.kmz -stops s.csv -ridership r.csv
package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	"smart-transit/internal/config"
	"smart-transit/internal/database"
	"smart-transit/internal/opendata"
	"smart-transit/internal/transit"
	"smart-transit/migrations"
)

func main() {
	var (
		replace   = flag.Bool("replace", false, "replace existing routes (deletes demo routes, trips, positions, reports, route alerts)")
		kmzPath   = flag.String("kmz", "", "local route paths KMZ (default: download)")
		stopsPath = flag.String("stops", "", "local stops CSV (default: download)")
		ridePath  = flag.String("ridership", "", "local ridership CSV (default: download)")
		allRoutes = flag.Bool("all-routes", false, "import every route path, not only BakuBus routes with ridership data")
		simulated = flag.Int("simulate", 10, "number of busiest routes that get simulated buses")
	)
	flag.Parse()
	log := slog.New(slog.NewTextHandler(os.Stdout, nil))
	if err := run(log, *replace, *kmzPath, *stopsPath, *ridePath, *allRoutes, *simulated); err != nil {
		log.Error("import failed", "err", err)
		os.Exit(1)
	}
}

type input struct {
	key, dataset, localPath string
	maxBytes                int64
}

func run(log *slog.Logger, replace bool, kmzPath, stopsPath, ridePath string, allRoutes bool, simulate int) error {
	cfg, err := config.Load(".env")
	if err != nil {
		return err
	}
	if cfg.IsProduction() {
		return errors.New("refusing to run the importer with APP_ENV=production")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	client := opendata.NewHTTPClient()
	now := time.Now().UTC()
	files := map[string][]byte{}
	var sources []transit.DataSource

	for _, in := range []input{
		{"routes", opendata.DatasetRoutes, kmzPath, 50 << 20},
		{"stops", opendata.DatasetStops, stopsPath, 20 << 20},
		{"ridership", opendata.DatasetRidership, ridePath, 50 << 20},
	} {
		res, err := opendata.Resolve(ctx, client, in.dataset)
		if err != nil {
			if in.localPath == "" {
				return fmt.Errorf("%s: %w", in.key, err)
			}
			log.Warn("could not resolve metadata; using local file only", "dataset", in.key, "err", err)
			res = opendata.Resource{Title: in.dataset, PageURL: "https://opendata.az/dataset/" + in.dataset, License: "see dataset page"}
		}
		var data []byte
		if in.localPath != "" {
			data, err = os.ReadFile(in.localPath)
		} else {
			log.Info("downloading", "dataset", in.key, "url", res.FileURL)
			data, err = opendata.Download(ctx, client, res.FileURL, in.maxBytes)
		}
		if err != nil {
			return fmt.Errorf("%s: %w", in.key, err)
		}
		files[in.key] = data
		license := res.License
		if license == "" || license == "License not specified" {
			license = "Not specified by publisher"
		}
		sources = append(sources, transit.DataSource{
			ID: in.key, Title: res.Title, Publisher: res.Publisher, URL: res.PageURL,
			License: license, RetrievedAt: now, Notes: "opendata.az; last modified " + res.Modified,
		})
	}

	lines, err := opendata.ParseKMZ(files["routes"])
	if err != nil {
		return err
	}
	stops, skippedStops, err := opendata.ParseStops(bytes.NewReader(files["stops"]))
	if err != nil {
		return err
	}
	ridership, err := opendata.ParseRidership(bytes.NewReader(files["ridership"]))
	if err != nil {
		return err
	}
	log.Info("parsed", "route_lines", len(lines), "stops", len(stops), "stops_skipped", skippedStops, "ridership_rows", len(ridership))

	if !allRoutes {
		withRiders := map[string]bool{}
		for _, r := range ridership {
			withRiders[r.RouteNumber] = true
		}
		filtered := lines[:0]
		for _, l := range lines {
			if withRiders[l.Number] {
				filtered = append(filtered, l)
			}
		}
		lines = filtered
		log.Info("keeping BakuBus routes that have ridership data", "routes", len(lines))
	}

	built, skipped := opendata.Build(lines, stops, opendata.DefaultBuildConfig())
	for _, s := range skipped {
		log.Warn("route skipped", "route", s.Number, "reason", s.Reason)
	}
	if len(built) == 0 {
		return errors.New("no routes could be built")
	}

	sources[0].Records = len(built)
	sources[1].Records = len(stops)
	sources[2].Records = len(ridership)
	sources[1].Notes += "; stop-to-route assignment inferred spatially (stops within 25 m, right-hand side); proposed ('Təklif') stops excluded"

	pool, err := database.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := database.Migrate(ctx, pool, migrations.FS, log); err != nil {
		return err
	}

	sum, err := opendata.Load(ctx, pool, opendata.Import{
		Routes: built, Stops: stops, Ridership: ridership, Sources: sources, SimulatedRoutes: simulate,
	}, replace, now)
	if err != nil {
		return err
	}
	log.Info("import complete",
		"routes", sum.Routes, "stops", sum.Stops, "route_stops", sum.RouteStops,
		"ridership_rows", sum.RidershipRows, "simulated_buses", sum.SimulatedBuses, "simulated_on", sum.SimulatedOn)
	fmt.Println("\nRestart the server to load the new routes. Bus positions remain SIMULATED.")
	return nil
}
