// Package tests holds integration tests that run against a real PostgreSQL
// database (plan 16). They need TEST_DATABASE_URL (env or ../.env) pointing
// at a database whose name ends in "_test"; that database is wiped per test.
// Without it, the tests are skipped.
package tests

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"smart-transit/internal/alerts"
	"smart-transit/internal/auth"
	"smart-transit/internal/clock"
	"smart-transit/internal/config"
	"smart-transit/internal/crowd"
	"smart-transit/internal/database"
	"smart-transit/internal/eta"
	"smart-transit/internal/gps"
	"smart-transit/internal/transit"
	"smart-transit/migrations"
	"smart-transit/seeds"
)

var (
	t0     = time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	quiet  = slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx    = context.Background()
	route  = "route_d1"
	minute = time.Minute
)

func testDBURL(t *testing.T) string {
	t.Helper()
	_ = config.LoadDotEnv("../.env")
	u := os.Getenv("TEST_DATABASE_URL")
	if u == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping integration test")
	}
	parsed, err := url.Parse(u)
	if err != nil || !strings.HasSuffix(strings.TrimPrefix(parsed.Path, "/"), "_test") {
		t.Fatalf("refusing to run: TEST_DATABASE_URL database name must end in _test")
	}
	return u
}

// freshDB wipes the test database, applies migrations, and loads demo data.
func freshDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool, err := database.Connect(ctx, testDBURL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public;`); err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(ctx, pool, migrations.FS, quiet); err != nil {
		t.Fatal(err)
	}
	if err := database.SeedDemoIfEmpty(ctx, pool, seeds.Demo, quiet); err != nil {
		t.Fatal(err)
	}
	return pool
}

type env struct {
	pool   *pgxpool.Pool
	clk    *clock.Fake
	engine *alerts.Engine
	crowd  *crowd.Service
	store  *crowd.PGStore
	life   *alerts.Lifecycle
}

func newEnv(t *testing.T) *env {
	pool := freshDB(t)
	clk := clock.NewFake(t0)
	e := &env{pool: pool, clk: clk, store: crowd.NewPGStore(pool), life: alerts.NewLifecycle(pool, clk)}
	e.engine = alerts.NewEngine(pool, crowd.DefaultRule, alerts.DefaultPolicy, clk, quiet)
	e.crowd = crowd.NewService(e.store, transit.NewPGStore(pool), e.engine, clk, quiet, 2*time.Minute)
	return e
}

func (e *env) report(t *testing.T, source, level string) {
	t.Helper()
	if _, err := e.crowd.Submit(ctx, crowd.SubmitRequest{RouteID: route, Level: level}, source); err != nil {
		t.Fatalf("report %s %s at %v: %v", source, level, e.clk.Now(), err)
	}
}

// reportAll has three distinct passengers report the same level.
func (e *env) reportAll(t *testing.T, level string) {
	for i := 1; i <= 3; i++ {
		e.report(t, fmt.Sprintf("passenger-%d", i), level)
	}
}

func (e *env) countAlerts(t *testing.T, where string) int {
	t.Helper()
	var n int
	if err := e.pool.QueryRow(ctx, `SELECT count(*) FROM alerts WHERE `+where).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// runHigh moves the clock in 15 s evaluation steps up to offset `to` from t0,
// re-reporting HIGH every 4 minutes to keep the evidence inside the window.
// It returns the offset at which the first new alert appeared, or -1. (New
// alerts are detected by count because a report submission also triggers an
// evaluation, which may be the one that creates the alert.)
func (e *env) runHigh(t *testing.T, eng *alerts.Engine, to time.Duration) (alertAt time.Duration) {
	t.Helper()
	alertAt = -1
	before := e.countAlerts(t, "true")
	for e.clk.Now().Sub(t0) < to {
		e.clk.Advance(15 * time.Second)
		off := e.clk.Now().Sub(t0)
		if off%(4*minute) == 0 {
			e.reportAll(t, "HIGH")
		}
		if _, err := eng.Evaluate(ctx, route); err != nil {
			t.Fatal(err)
		}
		if alertAt < 0 && e.countAlerts(t, "true") > before {
			alertAt = off
		}
	}
	return alertAt
}

func TestMigrationsAreIdempotent(t *testing.T) {
	pool := freshDB(t)
	if err := database.Migrate(ctx, pool, migrations.FS, quiet); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
}

func TestTimestampsAreUTC(t *testing.T) {
	pool := freshDB(t)
	var ts time.Time
	if err := pool.QueryRow(ctx, `SELECT now()`).Scan(&ts); err != nil {
		t.Fatal(err)
	}
	if ts.Location() != time.UTC {
		t.Fatalf("timestamptz scanned in %v, want UTC", ts.Location())
	}
}

// AC-06/07/08/09: reports are stored, validated, and drive the status.
func TestReportsDriveCrowdStatus(t *testing.T) {
	e := newEnv(t)

	e.report(t, "p1", "HIGH")
	e.report(t, "p2", "HIGH")
	st, _ := e.store.GetRouteStatus(ctx, route)
	if st.Status != crowd.StatusUnknown || st.ValidCount != 2 {
		t.Fatalf("2 reports: %+v, want UNKNOWN/2", st)
	}

	e.report(t, "p3", "MEDIUM")
	st, _ = e.store.GetRouteStatus(ctx, route)
	if st.Status != crowd.StatusHigh || st.HighCount != 2 || st.MediumCount != 1 || st.HighSince == nil {
		t.Fatalf("2 HIGH + 1 MEDIUM: %+v, want HIGH with timer", st)
	}

	// Window: 11 minutes later all reports have aged out => UNKNOWN.
	e.clk.Advance(11 * minute)
	if _, err := e.engine.Evaluate(ctx, route); err != nil {
		t.Fatal(err)
	}
	st, _ = e.store.GetRouteStatus(ctx, route)
	if st.Status != crowd.StatusUnknown || st.ValidCount != 0 || st.HighSince != nil {
		t.Fatalf("after window: %+v, want UNKNOWN, timer cleared", st)
	}

	// Validation against real reference data.
	_, err := e.crowd.Submit(ctx, crowd.SubmitRequest{RouteID: "route_zz", Level: "HIGH"}, "p9")
	if !errors.Is(err, crowd.ErrUnknownRoute) {
		t.Errorf("unknown route: %v", err)
	}
	_, err = e.crowd.Submit(ctx, crowd.SubmitRequest{RouteID: route, StopID: "stop_khatai", Level: "HIGH"}, "p9")
	if !errors.Is(err, crowd.ErrUnknownStop) {
		t.Errorf("stop not on route: %v", err)
	}
	_, err = e.crowd.Submit(ctx, crowd.SubmitRequest{RouteID: route, StopID: "stop_sahil", VehicleID: "bus_101", Level: "LOW"}, "p9")
	if err != nil {
		t.Errorf("valid stop+vehicle report rejected: %v", err)
	}
	var tripSet bool
	_ = e.pool.QueryRow(ctx, `SELECT trip_id IS NOT NULL FROM crowd_reports WHERE source_key = 'p9'`).Scan(&tripSet)
	if !tripSet {
		t.Error("trip should be attached server-side for a vehicle on the route")
	}
}

func TestDuplicateReportsRejectedConcurrently(t *testing.T) {
	e := newEnv(t)
	var wg sync.WaitGroup
	var mu sync.Mutex
	accepted := 0
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := e.crowd.Submit(ctx, crowd.SubmitRequest{RouteID: route, Level: "HIGH"}, "same-device"); err == nil {
				mu.Lock()
				accepted++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if accepted != 1 {
		t.Fatalf("accepted %d concurrent reports from one device, want 1", accepted)
	}
}

// AC-10, AC-11: one alert at exactly 10 minutes of reliable HIGH, never duplicated.
func TestSustainedHighCreatesExactlyOneAlert(t *testing.T) {
	e := newEnv(t)
	e.reportAll(t, "HIGH") // t0: HIGH, timer starts

	if at := e.runHigh(t, e.engine, 10*minute-15*time.Second); at != -1 {
		t.Fatalf("alert created early at %v", at)
	}
	if n := e.countAlerts(t, "true"); n != 0 {
		t.Fatalf("%d alerts before 10 minutes", n)
	}

	// Many concurrent evaluations exactly at the threshold.
	e.clk.Advance(15 * time.Second)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := e.engine.Evaluate(ctx, route); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()

	e.runHigh(t, e.engine, 20*minute) // ten more minutes of repeated evaluations
	if n := e.countAlerts(t, "true"); n != 1 {
		t.Fatalf("got %d alerts, want exactly 1", n)
	}

	list, err := e.life.List(ctx, []string{alerts.StatusActive}, 10)
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %v %v", list, err)
	}
	a := list[0]
	if !a.CreatedAt.Equal(t0.Add(10*minute)) || !a.HighSince.Equal(t0) || a.RouteNumber != "D1" {
		t.Errorf("alert timing/route wrong: created %v high_since %v route %q", a.CreatedAt, a.HighSince, a.RouteNumber)
	}
	// AC-15: a recommendation, not a dispatch.
	if a.Recommendation != alerts.Recommendation || a.Reason != alerts.Reason {
		t.Errorf("unexpected text: %q / %q", a.Reason, a.Recommendation)
	}
}

// AC-12: the sustained-HIGH timer is persisted and survives a restart.
func TestTimerSurvivesBackendRestart(t *testing.T) {
	e := newEnv(t)
	e.reportAll(t, "HIGH")
	e.runHigh(t, e.engine, 6*minute)

	// "Restart": brand-new engine instance, 60 s of downtime.
	e.clk.Advance(45 * time.Second)
	restarted := alerts.NewEngine(e.pool, crowd.DefaultRule, alerts.DefaultPolicy, e.clk, quiet)
	at := e.runHigh(t, restarted, 12*minute)
	if at != 10*minute {
		t.Fatalf("after restart, alert at %v; want 10m (timer from before restart)", at)
	}
}

func TestLongOutageRestartsTimer(t *testing.T) {
	e := newEnv(t)
	e.reportAll(t, "HIGH")
	e.runHigh(t, e.engine, 5*minute)

	// 5 minutes with no evaluations (> MaxGap): continuity unproven.
	e.clk.Advance(5 * minute)
	e.reportAll(t, "HIGH")
	res, err := e.engine.Evaluate(ctx, route)
	if err != nil {
		t.Fatal(err)
	}
	if res.AlertCreated != "" || res.HighSince == nil || !res.HighSince.Equal(t0.Add(10*minute)) {
		t.Fatalf("timer should restart after outage: %+v", res)
	}
}

// AC-13 + recovery policy + audit log.
func TestAlertLifecycleAndRecovery(t *testing.T) {
	e := newEnv(t)
	auth := auth.NewService(e.pool, e.clk, quiet, time.Hour)
	if _, err := auth.EnsureUser(ctx, "op@example.com", "a-long-password-123", "OPERATOR"); err != nil {
		t.Fatal(err)
	}
	var userID string
	_ = e.pool.QueryRow(ctx, `SELECT id::text FROM users`).Scan(&userID)

	e.reportAll(t, "HIGH")
	e.runHigh(t, e.engine, 10*minute)
	list, _ := e.life.List(ctx, nil, 10)
	if len(list) != 1 {
		t.Fatalf("want 1 alert, got %d", len(list))
	}
	id := list[0].ID

	// UNKNOWN (reports age out) must not claim recovery.
	e.clk.Advance(11 * minute)
	if _, err := e.engine.Evaluate(ctx, route); err != nil {
		t.Fatal(err)
	}
	a, _ := e.life.Get(ctx, id)
	if a.RecoveredAt != nil || a.Status != alerts.StatusActive {
		t.Fatalf("UNKNOWN must not mark recovery: %+v", a)
	}

	// Reliable LOW marks the alert ready for review but does not resolve it.
	e.reportAll(t, "LOW")
	a, _ = e.life.Get(ctx, id)
	if a.RecoveredAt == nil || a.Status != alerts.StatusActive {
		t.Fatalf("reliable LOW should mark recovered, keep ACTIVE: %+v", a)
	}

	// Acknowledge (not a resolution), then resolve.
	if a, err := e.life.Acknowledge(ctx, id, userID); err != nil || a.Status != alerts.StatusAcknowledged || a.AcknowledgedBy == nil {
		t.Fatalf("acknowledge: %+v %v", a, err)
	}
	if _, err := e.life.Acknowledge(ctx, id, userID); !errors.Is(err, alerts.ErrInvalidTransition) {
		t.Errorf("double acknowledge: %v", err)
	}
	e.clk.Advance(time.Minute)
	a, err := e.life.Resolve(ctx, id, userID, "Spare bus 950 deployed")
	if err != nil || a.Status != alerts.StatusResolved || a.ResolvedAt == nil || a.ResolutionNote == nil {
		t.Fatalf("resolve: %+v %v", a, err)
	}
	if _, err := e.life.Resolve(ctx, id, userID, ""); !errors.Is(err, alerts.ErrInvalidTransition) {
		t.Errorf("resolve twice: %v", err)
	}
	if _, err := e.life.Get(ctx, "not-a-uuid"); !errors.Is(err, alerts.ErrNotFound) {
		t.Errorf("bad id: %v", err)
	}

	var audits int
	_ = e.pool.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE action LIKE 'alert.%' AND target_id = $1`, id).Scan(&audits)
	if audits != 2 {
		t.Errorf("audit entries = %d, want 2", audits)
	}
}

func TestResolveWhileStillHighRestartsTimer(t *testing.T) {
	e := newEnv(t)
	e.reportAll(t, "HIGH")
	e.runHigh(t, e.engine, 10*minute)
	list, _ := e.life.List(ctx, nil, 10)

	var userID string
	authSvc := auth.NewService(e.pool, e.clk, quiet, time.Hour)
	_, _ = authSvc.EnsureUser(ctx, "op@example.com", "a-long-password-123", "OPERATOR")
	_ = e.pool.QueryRow(ctx, `SELECT id::text FROM users`).Scan(&userID)

	resolvedAt := e.clk.Now()
	if _, err := e.life.Resolve(ctx, list[0].ID, userID, ""); err != nil {
		t.Fatal(err)
	}
	// Still HIGH: a new alert only after another full 10 minutes.
	at := e.runHigh(t, e.engine, 30*minute)
	if want := resolvedAt.Sub(t0) + 10*minute; at != want {
		t.Fatalf("second alert at %v, want %v", at, want)
	}
	if n := e.countAlerts(t, "true"); n != 2 {
		t.Fatalf("alerts = %d, want 2 (one resolved, one new)", n)
	}
}

// AC-04/05: ETA available with fresh GPS, withheld when stale.
func TestArrivalsUseFreshGPSOnly(t *testing.T) {
	pool := freshDB(t)
	clk := clock.NewFake(t0)
	ts := transit.NewPGStore(pool)
	gs := gps.NewPGStore(pool)
	ing := gps.NewIngestor(gs, clk, quiet)
	paths := transit.NewPathCache(ts)
	svc := eta.NewService(paths, gs, clk, eta.DefaultConfig(), time.Minute, 15*time.Minute)

	trip, ok, err := ts.ActiveTripForVehicle(ctx, "bus_101")
	if err != nil || !ok || trip.Direction != 0 {
		t.Fatalf("seed trip: %+v %v %v", trip, ok, err)
	}
	path, err := paths.Get(ctx, route, 0)
	if err != nil {
		t.Fatal(err)
	}

	// Drive bus_101 at 10 m/s for 2 minutes from the first stop.
	speed := 36.0
	for s := 0; s <= 120; s += 10 {
		pt := path.Line.PointAt(float64(s) * 10)
		if _, err := ing.Ingest(ctx, gps.Position{
			VehicleID: "bus_101", RouteID: route, TripID: trip.ID, Latitude: pt.Lat, Longitude: pt.Lon,
			SpeedKmh: &speed, ObservedAt: clk.Now(), Source: "test", IsSimulated: true,
		}); err != nil {
			t.Fatal(err)
		}
		if s < 120 {
			clk.Advance(10 * time.Second)
		}
	}

	routes, _ := ts.ListStopRoutes(ctx, "stop_ganjlik")
	arr, err := svc.ArrivalsForStop(ctx, "stop_ganjlik", routes)
	if err != nil {
		t.Fatal(err)
	}
	var got *eta.Arrival
	for i := range arr {
		if arr[i].VehicleID == "bus_101" {
			got = &arr[i]
		}
	}
	if got == nil || got.Status != eta.Estimated || got.Confidence != eta.High || got.ETASeconds == nil {
		t.Fatalf("fresh GPS: %+v", got)
	}
	// Remaining distance / 10 m/s plus 20 s per intermediate stop.
	idx := path.StopIndex("stop_ganjlik")
	remaining := path.Stops[idx].DistM - 1200
	want := remaining/10 + float64(*got.StopsAway-1)*20
	if diff := float64(*got.ETASeconds) - want; diff < -3 || diff > 3 {
		t.Errorf("ETA %ds, want ~%.0fs", *got.ETASeconds, want)
	}

	// Two minutes with no updates: STALE => no precise ETA.
	clk.Advance(2 * time.Minute)
	arr, _ = svc.ArrivalsForStop(ctx, "stop_ganjlik", routes)
	for _, a := range arr {
		if a.VehicleID == "bus_101" && (a.Status != eta.Unavailable || a.Reason != "gps_stale" || a.ETASeconds != nil) {
			t.Fatalf("stale GPS must not yield an ETA: %+v", a)
		}
	}
	vs, _ := gs.ListRouteVehicles(ctx, route, clk.Now().Add(-15*time.Minute))
	for _, v := range vs {
		if v.VehicleID == "bus_101" && gps.Classify(v.ObservedAt, clk.Now(), time.Minute) != gps.Stale {
			t.Error("bus_101 should be STALE")
		}
	}
}

func TestOperatorLoginAndSessions(t *testing.T) {
	pool := freshDB(t)
	clk := clock.NewFake(t0)
	svc := auth.NewService(pool, clk, quiet, time.Hour)

	if _, err := svc.EnsureUser(ctx, "op@example.com", "short", "OPERATOR"); err == nil {
		t.Error("short password accepted")
	}
	created, err := svc.EnsureUser(ctx, "Op@Example.com", "a-long-password-123", "OPERATOR")
	if err != nil || !created {
		t.Fatalf("create: %v %v", created, err)
	}
	if again, _ := svc.EnsureUser(ctx, "op@example.com", "a-long-password-123", "OPERATOR"); again {
		t.Error("EnsureUser should not duplicate")
	}

	if _, _, _, err := svc.Login(ctx, "op@example.com", "wrong-password"); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Errorf("wrong password: %v", err)
	}
	if _, _, _, err := svc.Login(ctx, "nobody@example.com", "a-long-password-123"); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Errorf("unknown user: %v", err)
	}
	token, user, _, err := svc.Login(ctx, " OP@example.com ", "a-long-password-123")
	if err != nil || user.Role != "OPERATOR" {
		t.Fatalf("login: %v", err)
	}

	var stored string
	_ = pool.QueryRow(ctx, `SELECT token_hash FROM sessions`).Scan(&stored)
	if stored == token || stored == "" {
		t.Error("raw session token must not be stored")
	}
	if u, err := svc.Authenticate(ctx, token); err != nil || u.Email != "op@example.com" {
		t.Fatalf("authenticate: %v %v", u, err)
	}

	clk.Advance(61 * time.Minute)
	if _, err := svc.Authenticate(ctx, token); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Error("expired session accepted")
	}

	token, _, _, _ = svc.Login(ctx, "op@example.com", "a-long-password-123")
	_ = svc.Logout(ctx, token)
	if _, err := svc.Authenticate(ctx, token); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Error("logged-out session accepted")
	}
}
