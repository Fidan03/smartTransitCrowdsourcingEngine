// Command api runs the Smart Transit & Crowdsourcing Engine backend.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"smart-transit/internal/alerts"
	"smart-transit/internal/auth"
	"smart-transit/internal/clock"
	"smart-transit/internal/config"
	"smart-transit/internal/crowd"
	"smart-transit/internal/database"
	"smart-transit/internal/demand"
	"smart-transit/internal/eta"
	"smart-transit/internal/gps"
	"smart-transit/internal/gps/simulator"
	"smart-transit/internal/httpapi"
	"smart-transit/internal/operator"
	"smart-transit/internal/ratelimit"
	"smart-transit/internal/transit"
	"smart-transit/migrations"
	"smart-transit/seeds"
	"smart-transit/web"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, nil))
	slog.SetDefault(log)

	if err := run(log); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

// simStore combines the stores the simulator needs.
type simStore struct {
	*transit.PGStore
	positions *gps.PGStore
}

func (s simStore) LastTripPosition(ctx context.Context, tripID string) (float64, float64, bool, error) {
	return s.positions.LastTripPosition(ctx, tripID)
}

func run(log *slog.Logger) error {
	cfg, err := config.Load(".env")
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := database.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	if err := database.Migrate(ctx, pool, migrations.FS, log); err != nil {
		return err
	}
	if !cfg.IsProduction() {
		if err := database.SeedDemoIfEmpty(ctx, pool, seeds.Demo, log); err != nil {
			return err
		}
	}

	clk := clock.Real{}

	// Stores and services.
	transitStore := transit.NewPGStore(pool)
	gpsStore := gps.NewPGStore(pool)
	ingestor := gps.NewIngestor(gpsStore, clk, log)
	paths := transit.NewPathCache(transitStore)
	etaSvc := eta.NewService(paths, gpsStore, clk, eta.DefaultConfig(), cfg.GPSStaleAfter, cfg.GPSMaxAge)

	rule := crowd.DefaultRule
	policy := alerts.DefaultPolicy
	policy.Sustain = cfg.AlertSustain
	if cfg.AlertSustain != 10*time.Minute {
		log.Warn("ALERT_SUSTAIN overridden for demo/testing; production always uses 10m", "sustain", cfg.AlertSustain)
	}
	engine := alerts.NewEngine(pool, rule, policy, clk, log)
	crowdStore := crowd.NewPGStore(pool)
	crowdSvc := crowd.NewService(crowdStore, transitStore, engine, clk, log, cfg.ReportDupInterval)
	lifecycle := alerts.NewLifecycle(pool, clk)

	// Expected-crowding estimate from historical ridership (display only).
	demandSvc := demand.NewService(pool)
	if err := demandSvc.Reload(ctx); err != nil {
		return err
	}
	authSvc := auth.NewService(pool, clk, log, 12*time.Hour)

	if cfg.OperatorEmail != "" {
		created, err := authSvc.EnsureUser(ctx, cfg.OperatorEmail, cfg.OperatorPassword, auth.RoleOperator)
		if err != nil {
			return err
		}
		if created {
			log.Info("created operator account", "email", cfg.OperatorEmail)
		}
	}

	// Background workers; waited on before the DB pool closes.
	var wg sync.WaitGroup
	defer wg.Wait()
	workerCtx, cancelWorkers := context.WithCancel(ctx)
	defer cancelWorkers()

	wg.Add(1)
	go func() {
		defer wg.Done()
		engine.Run(workerCtx, cfg.CrowdEvalInterval)
	}()

	wg.Add(1)
	go func() { // pick up re-imported ridership without a restart
		defer wg.Done()
		t := time.NewTicker(time.Hour)
		defer t.Stop()
		for {
			select {
			case <-workerCtx.Done():
				return
			case <-t.C:
				if err := demandSvc.Reload(workerCtx); err != nil && workerCtx.Err() == nil {
					log.Error("reload ridership model", "err", err)
				}
			}
		}
	}()

	if cfg.SimulatorEnabled {
		sim := simulator.New(simStore{transitStore, gpsStore}, simulator.DefaultConfig(cfg.SimulatorInterval), clk, log)
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := ingestor.Run(workerCtx, sim); err != nil {
				log.Error("simulator stopped", "err", err)
			}
		}()
	}

	srv := &http.Server{
		Addr: cfg.HTTPAddr,
		Handler: httpapi.NewRouter(httpapi.Deps{
			Transit:         transitStore,
			Vehicles:        gpsStore,
			Ingester:        ingestor,
			Arrivals:        etaSvc,
			Crowd:           crowdSvc,
			CrowdRead:       crowdStore,
			CrowdRule:       rule,
			Auth:            authSvc,
			Alerts:          lifecycle,
			Operator:        operator.NewQueries(pool),
			Demand:          demandSvc,
			Sources:         transitStore,
			Clock:           clk,
			Simulated:       cfg.SimulatorEnabled,
			GPSStaleAfter:   cfg.GPSStaleAfter,
			GPSMaxAge:       cfg.GPSMaxAge,
			ReportKeySecret: cfg.ReportKeySecret,
			ReportLimiter:   ratelimit.New(20, 10, clk),
			LoginLimiter:    ratelimit.New(10, 5, clk),
			DevLimiter:      ratelimit.New(120, 30, clk),
			SecureCookies:   cfg.IsProduction(),
			DevEndpoints:    !cfg.IsProduction(),
			Web:             web.FS(),
		}),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Info("server listening", "addr", cfg.HTTPAddr, "env", cfg.Env, "simulated", cfg.SimulatorEnabled)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}

	log.Info("shutting down")
	cancelWorkers()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}
