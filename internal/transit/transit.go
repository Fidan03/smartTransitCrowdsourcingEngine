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
	ID          string   `json:"id"`
	RouteNumber string   `json:"route_number"`
	Name        string   `json:"name"`
	IsSimulated bool     `json:"is_simulated"`
	Geometry    []LatLon `json:"geometry,omitempty"`
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
