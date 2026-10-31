// Package httpapi exposes the REST API (plan section 11).
package httpapi

import (
	"context"
	"io/fs"
	"net/http"
	"time"

	"smart-transit/internal/alerts"
	"smart-transit/internal/auth"
	"smart-transit/internal/clock"
	"smart-transit/internal/crowd"
	"smart-transit/internal/demand"
	"smart-transit/internal/eta"
	"smart-transit/internal/gps"
	"smart-transit/internal/operator"
	"smart-transit/internal/transit"
)

// VehicleQuerier reads current vehicle state for a route.
type VehicleQuerier interface {
	ListRouteVehicles(ctx context.Context, routeID string, since time.Time) ([]gps.VehicleState, error)
}

// PositionIngester accepts a position through the shared ingestion path.
type PositionIngester interface {
	Ingest(ctx context.Context, p gps.Position) (time.Time, error)
}

type ArrivalEstimator interface {
	ArrivalsForStop(ctx context.Context, stopID string, routes []transit.StopRoute) ([]eta.Arrival, error)
}

type CrowdSubmitter interface {
	Submit(ctx context.Context, req crowd.SubmitRequest, sourceKey string) (crowd.Report, error)
}

type CrowdStatusReader interface {
	GetRouteStatus(ctx context.Context, routeID string) (crowd.RouteStatus, error)
}

type Authenticator interface {
	Login(ctx context.Context, email, password string) (token string, user auth.User, expires time.Time, err error)
	Authenticate(ctx context.Context, token string) (auth.User, error)
	Logout(ctx context.Context, token string) error
}

type AlertManager interface {
	List(ctx context.Context, statuses []string, limit int) ([]alerts.Alert, error)
	Get(ctx context.Context, id string) (alerts.Alert, error)
	Acknowledge(ctx context.Context, id, userID string) (alerts.Alert, error)
	Resolve(ctx context.Context, id, userID, note string) (alerts.Alert, error)
}

type OperatorQueries interface {
	Routes(ctx context.Context, freshSince, maxAgeSince time.Time) ([]operator.RouteRow, error)
	Summary(ctx context.Context, now, freshSince time.Time, routes []operator.RouteRow) (operator.Dashboard, error)
}

type Limiter interface {
	Allow(key string) (bool, time.Duration)
}

// DemandEstimator gives expected crowding from historical ridership. Its
// output is display-only: it never feeds crowd status or alerts.
type DemandEstimator interface {
	Estimate(routeNumber string, now time.Time) (demand.Estimate, bool)
	Summary(routeNumber string) (demand.Summary, bool)
}

type DataSourceLister interface {
	ListDataSources(ctx context.Context) ([]transit.DataSource, error)
}

type Deps struct {
	Transit   transit.Store
	Vehicles  VehicleQuerier
	Ingester  PositionIngester
	Arrivals  ArrivalEstimator
	Crowd     CrowdSubmitter
	CrowdRead CrowdStatusReader
	CrowdRule crowd.Rule
	Auth      Authenticator
	Alerts    AlertManager
	Operator  OperatorQueries
	Demand    DemandEstimator  // optional
	Sources   DataSourceLister // optional
	Clock     clock.Clock
	Simulated bool // true while GPS data comes from the simulator

	GPSStaleAfter time.Duration
	GPSMaxAge     time.Duration

	ReportKeySecret []byte  // HMAC key for anonymous submitter keys
	ReportLimiter   Limiter // per-IP limit on crowd reports
	LoginLimiter    Limiter // per-IP limit on login attempts
	DevLimiter      Limiter // per-IP limit on dev endpoints
	SecureCookies   bool    // set Secure on session cookies (production/HTTPS)

	// DevEndpoints registers /api/v1/dev/*. Must be false in production.
	DevEndpoints bool
	// Web is the static frontend served at "/". Optional.
	Web fs.FS
}

// NewRouter wires all API routes. Uses Go 1.22+ method/path patterns.
func NewRouter(d Deps) http.Handler {
	if d.Clock == nil {
		d.Clock = clock.Real{}
	}
	mux := http.NewServeMux()
	h := &handlers{deps: d}

	mux.HandleFunc("GET /healthz", h.health)

	// Passenger and route endpoints.
	mux.HandleFunc("GET /api/v1/routes", h.listRoutes)
	mux.HandleFunc("GET /api/v1/routes/{id}", h.getRoute)
	mux.HandleFunc("GET /api/v1/routes/{id}/stops", h.listRouteStops)
	mux.HandleFunc("GET /api/v1/routes/{id}/vehicles", h.listRouteVehicles)
	mux.HandleFunc("GET /api/v1/routes/{id}/crowd-status", h.routeCrowdStatus)
	mux.HandleFunc("GET /api/v1/stops/{id}/arrivals", h.stopArrivals)
	mux.HandleFunc("POST /api/v1/crowd-reports", h.limit(d.ReportLimiter, h.submitCrowdReport))
	mux.HandleFunc("GET /api/v1/data-sources", h.listDataSources)

	// Operator authentication.
	mux.HandleFunc("POST /api/v1/auth/login", h.limit(d.LoginLimiter, h.login))
	mux.HandleFunc("POST /api/v1/auth/logout", h.logout)
	mux.HandleFunc("GET /api/v1/auth/me", h.requireOperator(h.me))

	// Operator endpoints (authenticated, role-checked).
	mux.HandleFunc("GET /api/v1/operator/dashboard", h.requireOperator(h.operatorDashboard))
	mux.HandleFunc("GET /api/v1/operator/routes", h.requireOperator(h.operatorRoutes))
	mux.HandleFunc("GET /api/v1/operator/alerts", h.requireOperator(h.listAlerts))
	mux.HandleFunc("GET /api/v1/operator/alerts/{id}", h.requireOperator(h.getAlert))
	mux.HandleFunc("PATCH /api/v1/operator/alerts/{id}", h.requireOperator(h.updateAlert))

	if d.DevEndpoints {
		mux.HandleFunc("POST /api/v1/dev/simulator/positions", h.limit(d.DevLimiter, h.devInjectPosition))
	}

	// "GET /" only matches GET, so it does not turn API 405s into 404s.
	if d.Web != nil {
		mux.Handle("GET /", http.FileServerFS(d.Web))
	}

	return recoverPanics(logRequests(securityHeaders(dataModeHeader(d.Simulated, mux))))
}

type handlers struct {
	deps Deps
}
