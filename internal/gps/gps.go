// Package gps defines vehicle-position data, the provider boundary that lets a
// simulator or a real feed supply positions, and the shared ingestion service.
package gps

import (
	"context"
	"time"
)

// Position is one vehicle-position observation from any source.
type Position struct {
	VehicleID   string
	RouteID     string // optional; "" if unknown
	TripID      string // optional; "" if unknown
	Latitude    float64
	Longitude   float64
	SpeedKmh    *float64  // optional
	ObservedAt  time.Time // when the position was measured (source clock)
	Source      string    // e.g. "simulator", "dev-inject", "gtfs-rt"
	IsSimulated bool
}

// Provider is the boundary between position sources and the rest of the
// system (plan 10.1). The simulator and any future live feed implement it;
// crowding, ETA, and alert logic never depend on a specific vendor.
//
// The returned channel is closed when ctx is cancelled or the source ends.
type Provider interface {
	Positions(ctx context.Context) (<-chan Position, error)
}

// Freshness describes how trustworthy a vehicle's latest position is.
type Freshness string

const (
	Fresh  Freshness = "FRESH"   // recent enough to show as current
	Stale  Freshness = "STALE"   // too old to present as current or use for a precise ETA
	NoData Freshness = "NO_DATA" // no usable position at all
)

// Classify returns the freshness of an observation at time now.
func Classify(observedAt *time.Time, now time.Time, staleAfter time.Duration) Freshness {
	if observedAt == nil {
		return NoData
	}
	if now.Sub(*observedAt) > staleAfter {
		return Stale
	}
	return Fresh
}
