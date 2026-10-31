// Package simulator is a development-only GPS source (plan 5.4). It moves
// buses along seeded route geometry, dwells at stops, injects random delays
// and reporting gaps (stale data), and emits positions through the same
// gps.Provider interface a real feed would use.
package simulator

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"math/rand/v2"
	"time"

	"smart-transit/internal/clock"
	"smart-transit/internal/geo"
	"smart-transit/internal/gps"
	"smart-transit/internal/transit"
)

const Source = "simulator"

// Store is everything the simulator needs from persistence.
type Store interface {
	GetRoute(ctx context.Context, id string) (transit.Route, error)
	ListRouteStops(ctx context.Context, routeID string, direction int) ([]transit.RouteStop, error)
	ActiveTrips(ctx context.Context) ([]transit.Trip, error)
	RotateTrip(ctx context.Context, oldTripID string, nextDirection int, at time.Time) (transit.Trip, error)
	LastTripPosition(ctx context.Context, tripID string) (lat, lon float64, ok bool, err error)
}

type Config struct {
	Interval           time.Duration // how often positions are emitted
	MinSpeedKmh        float64       // per-bus cruise speed is drawn from [Min, Max]
	MaxSpeedKmh        float64
	DwellMin           time.Duration // time spent at each stop
	DwellMax           time.Duration
	DelayProbability   float64 // per tick, chance a moving bus enters a slowdown
	DelayMin           time.Duration
	DelayMax           time.Duration
	DelayFactor        float64 // speed multiplier while delayed
	SilenceProbability float64 // per tick, chance a bus stops reporting (stale demo)
	SilenceMin         time.Duration
	SilenceMax         time.Duration
	Seed               uint64
}

func DefaultConfig(interval time.Duration) Config {
	return Config{
		Interval:           interval,
		MinSpeedKmh:        18,
		MaxSpeedKmh:        32,
		DwellMin:           10 * time.Second,
		DwellMax:           25 * time.Second,
		DelayProbability:   0.01,
		DelayMin:           30 * time.Second,
		DelayMax:           90 * time.Second,
		DelayFactor:        0.3,
		SilenceProbability: 0.002,
		SilenceMin:         70 * time.Second,
		SilenceMax:         150 * time.Second,
		Seed:               uint64(time.Now().UnixNano()),
	}
}

type bus struct {
	trip        transit.Trip
	path        transit.Path
	distM       float64
	nextStop    int
	cruiseKmh   float64
	dwellUntil  time.Time
	delayUntil  time.Time
	silentUntil time.Time
}

type Simulator struct {
	store Store
	cfg   Config
	clk   clock.Clock
	log   *slog.Logger
	rng   *rand.Rand
	paths *transit.PathCache
	buses []*bus
	last  time.Time
}

func New(store Store, cfg Config, clk clock.Clock, log *slog.Logger) *Simulator {
	return &Simulator{
		store: store,
		cfg:   cfg,
		clk:   clk,
		log:   log,
		rng:   rand.New(rand.NewPCG(cfg.Seed, cfg.Seed^0x9e3779b97f4a7c15)),
		paths: transit.NewPathCache(store),
	}
}

// Positions implements gps.Provider.
func (s *Simulator) Positions(ctx context.Context) (<-chan gps.Position, error) {
	if s.cfg.Interval <= 0 {
		return nil, fmt.Errorf("simulator interval must be positive")
	}
	if err := s.init(ctx); err != nil {
		return nil, err
	}
	ch := make(chan gps.Position, 64)
	go func() {
		defer close(ch)
		ticker := time.NewTicker(s.cfg.Interval)
		defer ticker.Stop()
		for {
			for _, p := range s.step(ctx, s.clk.Now()) {
				select {
				case ch <- p:
				case <-ctx.Done():
					return
				}
			}
			select {
			case <-ticker.C:
			case <-ctx.Done():
				return
			}
		}
	}()
	return ch, nil
}

func (s *Simulator) init(ctx context.Context) error {
	trips, err := s.store.ActiveTrips(ctx)
	if err != nil {
		return fmt.Errorf("load active trips: %w", err)
	}
	now := s.clk.Now()
	for _, t := range trips {
		p, err := s.paths.Get(ctx, t.RouteID, t.Direction)
		if err != nil {
			s.log.Warn("simulator: skipping trip", "trip_id", t.ID, "route_id", t.RouteID, "err", err)
			continue
		}
		start := 0.0
		if lat, lon, ok, err := s.store.LastTripPosition(ctx, t.ID); err != nil {
			return fmt.Errorf("resume trip %s: %w", t.ID, err)
		} else if ok {
			start, _ = p.Line.Project(geo.Point{Lat: lat, Lon: lon})
		}
		s.buses = append(s.buses, s.newBus(t, p, start, now))
	}
	s.log.Info("simulator ready (SIMULATED DATA)", "buses", len(s.buses), "interval", s.cfg.Interval)
	return nil
}

func (s *Simulator) newBus(t transit.Trip, p transit.Path, startM float64, now time.Time) *bus {
	b := &bus{trip: t, path: p, distM: startM, cruiseKmh: s.between(s.cfg.MinSpeedKmh, s.cfg.MaxSpeedKmh)}
	for b.nextStop < len(p.Stops) && p.Stops[b.nextStop].DistM <= startM+1 {
		b.nextStop++
	}
	if startM <= 1 && len(p.Stops) > 0 {
		b.dwellUntil = now.Add(s.duration(s.cfg.DwellMin, s.cfg.DwellMax)) // layover at first stop
	}
	return b
}

// step advances every bus to time now and returns the positions to emit.
func (s *Simulator) step(ctx context.Context, now time.Time) []gps.Position {
	dt := 0.0
	if !s.last.IsZero() {
		dt = math.Max(0, now.Sub(s.last).Seconds())
	}
	s.last = now

	var out []gps.Position
	for _, b := range s.buses {
		if s.finished(b, now) {
			if err := s.rotate(ctx, b, now); err != nil {
				s.log.Warn("simulator: rotate trip failed; will retry", "vehicle_id", b.trip.VehicleID, "err", err)
			}
		}

		speed := 0.0
		if !now.Before(b.dwellUntil) && b.distM < b.path.Line.LengthM() {
			if !now.Before(b.delayUntil) && s.rng.Float64() < s.cfg.DelayProbability {
				b.delayUntil = now.Add(s.duration(s.cfg.DelayMin, s.cfg.DelayMax))
			}
			speed = b.cruiseKmh * s.between(0.9, 1.1)
			if now.Before(b.delayUntil) {
				speed *= s.cfg.DelayFactor
			}
			b.distM = math.Min(b.distM+speed/3.6*dt, b.path.Line.LengthM())
			if b.nextStop < len(b.path.Stops) && b.distM >= b.path.Stops[b.nextStop].DistM {
				b.distM = b.path.Stops[b.nextStop].DistM
				b.nextStop++
				b.dwellUntil = now.Add(s.duration(s.cfg.DwellMin, s.cfg.DwellMax))
				speed = 0
			}
		}

		if !now.Before(b.silentUntil) && s.rng.Float64() < s.cfg.SilenceProbability {
			b.silentUntil = now.Add(s.duration(s.cfg.SilenceMin, s.cfg.SilenceMax))
			s.log.Info("simulator: vehicle going silent (stale-data demo)", "vehicle_id", b.trip.VehicleID, "until", b.silentUntil)
		}
		if now.Before(b.silentUntil) {
			continue
		}

		pt := b.path.Line.PointAt(b.distM)
		sp := math.Round(speed*10) / 10
		out = append(out, gps.Position{
			VehicleID:   b.trip.VehicleID,
			RouteID:     b.trip.RouteID,
			TripID:      b.trip.ID,
			Latitude:    pt.Lat,
			Longitude:   pt.Lon,
			SpeedKmh:    &sp,
			ObservedAt:  now,
			Source:      Source,
			IsSimulated: true,
		})
	}
	return out
}

func (s *Simulator) finished(b *bus, now time.Time) bool {
	return b.nextStop >= len(b.path.Stops) &&
		b.distM >= b.path.Line.LengthM()-1 &&
		!now.Before(b.dwellUntil)
}

// rotate ends the current trip and turns the bus around on the same route.
func (s *Simulator) rotate(ctx context.Context, b *bus, now time.Time) error {
	nextDir := 1 - b.trip.Direction
	p, err := s.paths.Get(ctx, b.trip.RouteID, nextDir)
	if err != nil {
		return err
	}
	next, err := s.store.RotateTrip(ctx, b.trip.ID, nextDir, now)
	if err != nil {
		return err
	}
	*b = *s.newBus(next, p, 0, now)
	return nil
}

func (s *Simulator) between(lo, hi float64) float64 {
	if hi <= lo {
		return lo
	}
	return lo + s.rng.Float64()*(hi-lo)
}

func (s *Simulator) duration(lo, hi time.Duration) time.Duration {
	return time.Duration(s.between(float64(lo), float64(hi)))
}
