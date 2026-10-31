package eta

import (
	"context"
	"math"
	"sort"
	"time"

	"smart-transit/internal/clock"
	"smart-transit/internal/geo"
	"smart-transit/internal/gps"
	"smart-transit/internal/transit"
)

type PathGetter interface {
	Get(ctx context.Context, routeID string, direction int) (transit.Path, error)
}

type VehicleSource interface {
	ListRouteVehicles(ctx context.Context, routeID string, since time.Time) ([]gps.VehicleState, error)
	TripPositionSpan(ctx context.Context, tripID string, since time.Time) (first, last gps.TimedPoint, ok bool, err error)
}

type Service struct {
	paths      PathGetter
	vehicles   VehicleSource
	clock      clock.Clock
	cfg        Config
	staleAfter time.Duration
	maxAge     time.Duration
}

func NewService(paths PathGetter, vehicles VehicleSource, clk clock.Clock, cfg Config, staleAfter, maxAge time.Duration) *Service {
	return &Service{paths: paths, vehicles: vehicles, clock: clk, cfg: cfg, staleAfter: staleAfter, maxAge: maxAge}
}

// Arrival is one vehicle's predicted arrival at a stop.
type Arrival struct {
	RouteID       string        `json:"route_id"`
	RouteNumber   string        `json:"route_number"`
	Direction     int           `json:"direction"`
	VehicleID     string        `json:"vehicle_id"`
	VehicleLabel  string        `json:"vehicle_label"`
	Status        Status        `json:"status"`
	ETASeconds    *int          `json:"eta_seconds"`
	ETAMinutes    *int          `json:"eta_minutes"`
	ExpectedAt    *time.Time    `json:"expected_at"`
	Confidence    Confidence    `json:"confidence"`
	Reason        string        `json:"reason"`
	DistanceM     *float64      `json:"distance_m"`
	StopsAway     *int          `json:"stops_away"`
	GPSAgeSeconds *float64      `json:"gps_age_seconds"`
	Freshness     gps.Freshness `json:"freshness"`
	IsSimulated   bool          `json:"is_simulated"`
}

// ArrivalsForStop predicts arrivals at stopID for every route/direction in
// routes. Buses that already passed the stop are omitted. Estimated arrivals
// come first, soonest first; unavailable ones follow.
func (s *Service) ArrivalsForStop(ctx context.Context, stopID string, routes []transit.StopRoute) ([]Arrival, error) {
	now := s.clock.Now()
	out := []Arrival{}
	vehiclesByRoute := map[string][]gps.VehicleState{}

	for _, sr := range routes {
		path, err := s.paths.Get(ctx, sr.RouteID, sr.Direction)
		if err != nil {
			return nil, err
		}
		idx := path.StopIndex(stopID)
		if idx < 0 {
			continue
		}
		vs, ok := vehiclesByRoute[sr.RouteID]
		if !ok {
			vs, err = s.vehicles.ListRouteVehicles(ctx, sr.RouteID, now.Add(-s.maxAge))
			if err != nil {
				return nil, err
			}
			vehiclesByRoute[sr.RouteID] = vs
		}

		for _, v := range vs {
			if v.Direction == nil || *v.Direction != sr.Direction {
				continue // direction unknown or opposite: no ETA for this stop
			}
			a, err := s.arrival(ctx, now, sr, path, idx, v)
			if err != nil {
				return nil, err
			}
			if a != nil {
				out = append(out, *a)
			}
		}
	}

	sort.SliceStable(out, func(i, j int) bool {
		ai, aj := out[i].ETASeconds, out[j].ETASeconds
		switch {
		case ai != nil && aj != nil:
			return *ai < *aj
		case ai != nil:
			return true
		default:
			return false
		}
	})
	return out, nil
}

func (s *Service) arrival(ctx context.Context, now time.Time, sr transit.StopRoute, path transit.Path, idx int, v gps.VehicleState) (*Arrival, error) {
	fresh := gps.Classify(v.ObservedAt, now, s.staleAfter)
	in := Input{Path: path, TargetIdx: idx, Freshness: fresh}

	if v.Latitude != nil && v.Longitude != nil && v.ObservedAt != nil {
		in.AlongM, in.OffsetM = path.Line.Project(geo.Point{Lat: *v.Latitude, Lon: *v.Longitude})
		in.GPSAge = max(0, now.Sub(*v.ObservedAt))
		if fresh == gps.Fresh && v.TripID != nil {
			sp, err := s.measuredSpeed(ctx, path, *v.TripID, now)
			if err != nil {
				return nil, err
			}
			in.MeasuredSpeedKmh = sp
		}
	}

	r := Estimate(in, s.cfg)
	if r.Status == Passed {
		return nil, nil
	}
	a := &Arrival{
		RouteID: sr.RouteID, RouteNumber: sr.RouteNumber, Direction: sr.Direction,
		VehicleID: v.VehicleID, VehicleLabel: v.Label,
		Status: r.Status, Confidence: r.Confidence, Reason: r.Reason, Freshness: fresh,
		IsSimulated: v.IsSimulated != nil && *v.IsSimulated,
	}
	if v.ObservedAt != nil {
		age := math.Round(in.GPSAge.Seconds()*10) / 10
		a.GPSAgeSeconds = &age
	}
	if r.Status == Estimated {
		secs := int(r.ETA.Seconds())
		mins := int(math.Ceil(r.ETA.Minutes()))
		at := now.Add(r.ETA)
		dist, stops := r.DistanceM, r.StopsAway
		a.ETASeconds, a.ETAMinutes, a.ExpectedAt = &secs, &mins, &at
		a.DistanceM, a.StopsAway = &dist, &stops
	}
	return a, nil
}

// measuredSpeed returns the trip's average along-route speed over the recent
// window, or nil if there is not enough history.
func (s *Service) measuredSpeed(ctx context.Context, path transit.Path, tripID string, now time.Time) (*float64, error) {
	first, last, ok, err := s.vehicles.TripPositionSpan(ctx, tripID, now.Add(-s.cfg.SpeedWindow))
	if err != nil || !ok {
		return nil, err
	}
	span := last.ObservedAt.Sub(first.ObservedAt)
	if span < s.cfg.MinSpeedSample {
		return nil, nil
	}
	d1, _ := path.Line.Project(geo.Point{Lat: first.Lat, Lon: first.Lon})
	d2, _ := path.Line.Project(geo.Point{Lat: last.Lat, Lon: last.Lon})
	kmh := (d2 - d1) / span.Seconds() * 3.6
	return &kmh, nil
}
