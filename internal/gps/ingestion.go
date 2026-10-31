package gps

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"time"

	"smart-transit/internal/clock"
)

// ErrUnknownReference means the vehicle, route, or trip ID does not exist.
var ErrUnknownReference = errors.New("unknown vehicle, route, or trip")

// ValidationError is returned for malformed positions.
type ValidationError struct{ Msg string }

func (e *ValidationError) Error() string { return "invalid position: " + e.Msg }

func invalid(format string, args ...any) error {
	return &ValidationError{Msg: fmt.Sprintf(format, args...)}
}

// Store persists positions.
type Store interface {
	InsertPosition(ctx context.Context, p Position, receivedAt time.Time) error
}

// Ingestor validates positions from any Provider and stores them with a
// server-side receipt timestamp. It is the single entry point for GPS data.
type Ingestor struct {
	store         Store
	clock         clock.Clock
	log           *slog.Logger
	maxFutureSkew time.Duration // reject observations this far ahead of server time
	maxAge        time.Duration // reject observations older than this
}

func NewIngestor(store Store, clk clock.Clock, log *slog.Logger) *Ingestor {
	return &Ingestor{
		store:         store,
		clock:         clk,
		log:           log,
		maxFutureSkew: 30 * time.Second,
		maxAge:        24 * time.Hour,
	}
}

// Ingest validates and stores one position. It returns the receipt time.
func (in *Ingestor) Ingest(ctx context.Context, p Position) (time.Time, error) {
	receivedAt := in.clock.Now()
	if err := in.validate(p, receivedAt); err != nil {
		return time.Time{}, err
	}
	p.ObservedAt = p.ObservedAt.UTC()
	if err := in.store.InsertPosition(ctx, p, receivedAt); err != nil {
		return time.Time{}, err
	}
	return receivedAt, nil
}

func (in *Ingestor) validate(p Position, now time.Time) error {
	switch {
	case strings.TrimSpace(p.VehicleID) == "":
		return invalid("vehicle_id is required")
	case strings.TrimSpace(p.Source) == "":
		return invalid("source is required")
	case math.IsNaN(p.Latitude) || p.Latitude < -90 || p.Latitude > 90:
		return invalid("latitude out of range")
	case math.IsNaN(p.Longitude) || p.Longitude < -180 || p.Longitude > 180:
		return invalid("longitude out of range")
	case p.SpeedKmh != nil && (math.IsNaN(*p.SpeedKmh) || *p.SpeedKmh < 0 || *p.SpeedKmh > 200):
		return invalid("speed_kmh must be between 0 and 200")
	case p.ObservedAt.IsZero():
		return invalid("observed_at is required")
	case p.ObservedAt.Sub(now) > in.maxFutureSkew:
		return invalid("observed_at is in the future")
	case now.Sub(p.ObservedAt) > in.maxAge:
		return invalid("observed_at is too old")
	}
	return nil
}

// Run consumes a provider until its channel closes or ctx is cancelled.
// Bad individual positions are logged and skipped, not fatal.
func (in *Ingestor) Run(ctx context.Context, provider Provider) error {
	ch, err := provider.Positions(ctx)
	if err != nil {
		return fmt.Errorf("start position provider: %w", err)
	}
	for p := range ch {
		if _, err := in.Ingest(ctx, p); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			in.log.Warn("position rejected", "vehicle_id", p.VehicleID, "source", p.Source, "err", err)
		}
	}
	return nil
}
