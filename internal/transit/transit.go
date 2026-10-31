// Package transit holds route, stop, and vehicle reference data.
package transit

import (
	"context"
	"errors"
	"time"
)

// ErrNotFound is returned when a requested entity does not exist.
var ErrNotFound = errors.New("not found")

// LatLon is a [latitude, longitude] pair, serialized as a JSON array.
type LatLon [2]float64

type Route struct {
	ID          string `json:"id"`
	RouteNumber string `json:"route_number"`
	Name        string `json:"name"`
	IsSimulated bool   `json:"is_simulated"` // true for fictional demo routes
	DataSource  string `json:"data_source"`  // "demo" or "opendata.az"
	// Geometry is the direction-0 path. GeometryReturn is the direction-1 path
	// when it differs (real routes); empty means "direction 0 reversed".
	Geometry       []LatLon `json:"geometry,omitempty"`
	GeometryReturn []LatLon `json:"geometry_return,omitempty"`
}

// DataSource records provenance and attribution for imported open data.
type DataSource struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	Publisher   string    `json:"publisher"`
	URL         string    `json:"url"`
	License     string    `json:"license"`
	RetrievedAt time.Time `json:"retrieved_at"`
	Records     int       `json:"records"`
	Notes       string    `json:"notes"`
}

type Stop struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
}

// RouteStop is a stop in the context of a route, with its position in order.
type RouteStop struct {
	Stop
	Direction int `json:"direction"`
	StopOrder int `json:"stop_order"`
}

// Trip is one run of a vehicle along a route in one direction.
type Trip struct {
	ID        string    `json:"id"`
	RouteID   string    `json:"route_id"`
	VehicleID string    `json:"vehicle_id"`
	Direction int       `json:"direction"`
	StartedAt time.Time `json:"started_at"`
	Status    string    `json:"status"`
}

// StopRoute is a route (and direction) that serves a stop.
type StopRoute struct {
	RouteID     string `json:"route_id"`
	RouteNumber string `json:"route_number"`
	RouteName   string `json:"route_name"`
	Direction   int    `json:"direction"`
	StopOrder   int    `json:"stop_order"`
}

// Store is the persistence boundary for transit reference data.
type Store interface {
	ListRoutes(ctx context.Context) ([]Route, error)
	GetRoute(ctx context.Context, id string) (Route, error)
	ListRouteStops(ctx context.Context, routeID string, direction int) ([]RouteStop, error)
	GetStop(ctx context.Context, id string) (Stop, error)
	ListStopRoutes(ctx context.Context, stopID string) ([]StopRoute, error)
}
