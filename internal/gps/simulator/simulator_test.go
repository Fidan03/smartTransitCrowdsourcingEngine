package simulator

import (
	"context"
	"io"
	"log/slog"
	"math"
	"testing"
	"time"

	"smart-transit/internal/clock"
	"smart-transit/internal/geo"
	"smart-transit/internal/transit"
)

// A straight north-south demo route of ~2.2 km with three stops.
var (
	stopA = transit.Stop{ID: "a", Latitude: 40.00, Longitude: 49.0}
	stopB = transit.Stop{ID: "b", Latitude: 40.01, Longitude: 49.0}
	stopC = transit.Stop{ID: "c", Latitude: 40.02, Longitude: 49.0}
	t0    = time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
)

type fakeStore struct {
	trips   []transit.Trip
	rotated []transit.Trip
	lastPos map[string]geo.Point
}

func (f *fakeStore) GetRoute(context.Context, string) (transit.Route, error) {
	return transit.Route{ID: "r1", Geometry: []transit.LatLon{
		{stopA.Latitude, stopA.Longitude}, {stopB.Latitude, stopB.Longitude}, {stopC.Latitude, stopC.Longitude},
	}}, nil
}

func (f *fakeStore) ListRouteStops(_ context.Context, _ string, dir int) ([]transit.RouteStop, error) {
	stops := []transit.Stop{stopA, stopB, stopC}
	if dir == 1 {
		stops = []transit.Stop{stopC, stopB, stopA}
	}
	out := make([]transit.RouteStop, len(stops))
	for i, s := range stops {
		out[i] = transit.RouteStop{Stop: s, Direction: dir, StopOrder: i}
	}
	return out, nil
}

func (f *fakeStore) ActiveTrips(context.Context) ([]transit.Trip, error) { return f.trips, nil }

func (f *fakeStore) RotateTrip(_ context.Context, old string, dir int, at time.Time) (transit.Trip, error) {
	next := transit.Trip{ID: old + "-next", RouteID: "r1", VehicleID: "bus_1", Direction: dir, StartedAt: at}
	f.rotated = append(f.rotated, next)
	return next, nil
}

func (f *fakeStore) LastTripPosition(_ context.Context, id string) (float64, float64, bool, error) {
	if p, ok := f.lastPos[id]; ok {
		return p.Lat, p.Lon, true, nil
	}
	return 0, 0, false, nil
}

// testConfig: fixed 36 km/h (10 m/s), fixed 20 s dwell, no random events.
func testConfig() Config {
	return Config{
		Interval:    time.Second,
		MinSpeedKmh: 36, MaxSpeedKmh: 36,
		DwellMin: 20 * time.Second, DwellMax: 20 * time.Second,
		DelayFactor: 0.3,
		Seed:        1,
	}
}

func newSim(t *testing.T, st *fakeStore, cfg Config) (*Simulator, *clock.Fake) {
	t.Helper()
	clk := clock.NewFake(t0)
	s := New(st, cfg, clk, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := s.init(context.Background()); err != nil {
		t.Fatal(err)
	}
	return s, clk
}

func oneTrip() *fakeStore {
	return &fakeStore{trips: []transit.Trip{{ID: "t1", RouteID: "r1", VehicleID: "bus_1", Direction: 0}}}
}

func TestBusDwellsThenMovesAlongRoute(t *testing.T) {
	s, clk := newSim(t, oneTrip(), testConfig())
	ctx := context.Background()

	ps := s.step(ctx, clk.Now())
	if len(ps) != 1 || !ps[0].IsSimulated || ps[0].Source != Source || ps[0].TripID != "t1" {
		t.Fatalf("unexpected first emission: %+v", ps)
	}
	if *ps[0].SpeedKmh != 0 || ps[0].Latitude != stopA.Latitude {
		t.Fatalf("bus should start dwelling at first stop, got %+v", ps[0])
	}

	// Still dwelling at 19 s.
	clk.Advance(19 * time.Second)
	if p := s.step(ctx, clk.Now())[0]; p.Latitude != stopA.Latitude {
		t.Fatalf("moved during dwell: %v", p.Latitude)
	}

	// Dwell ends at 20 s; from then on it moves at 10 m/s Â±10% jitter,
	// so after 11 one-second ticks it should be ~99-121 m along.
	for range 11 {
		clk.Advance(time.Second)
		s.step(ctx, clk.Now())
	}
	if d := s.buses[0].distM; d < 99 || d > 121 {
		t.Fatalf("after 11 s at ~10 m/s, distance = %.1f m", d)
	}
}

func TestBusStopsAtIntermediateStop(t *testing.T) {
	s, clk := newSim(t, oneTrip(), testConfig())
	ctx := context.Background()
	stopBDist := s.buses[0].path.Stops[1].DistM

	// Run long enough to pass stop B if it did not stop (~1112 m at 10 m/s).
	for range 200 {
		s.step(ctx, clk.Now())
		clk.Advance(time.Second)
		if s.buses[0].distM == stopBDist {
			break
		}
	}
	if s.buses[0].distM != stopBDist || s.buses[0].nextStop != 2 {
		t.Fatalf("bus did not stop at B: dist=%.1f want %.1f nextStop=%d", s.buses[0].distM, stopBDist, s.buses[0].nextStop)
	}
}

func TestTripRotatesToReverseDirectionAtEnd(t *testing.T) {
	st := oneTrip()
	s, clk := newSim(t, st, testConfig())
	ctx := context.Background()

	for range 600 { // ample time for 2.2 km plus three dwells
		s.step(ctx, clk.Now())
		clk.Advance(time.Second)
		if len(st.rotated) > 0 {
			break
		}
	}
	if len(st.rotated) != 1 {
		t.Fatalf("expected one rotation, got %d", len(st.rotated))
	}
	b := s.buses[0]
	if b.trip.Direction != 1 || b.trip.ID != "t1-next" || b.distM != 0 {
		t.Fatalf("after rotation: %+v dist=%.1f", b.trip, b.distM)
	}
	if p := b.path.Line.PointAt(0); math.Abs(p.Lat-stopC.Latitude) > 1e-9 {
		t.Fatalf("reverse trip should start at C, starts at %v", p)
	}
}

func TestSilentBusEmitsNothing(t *testing.T) {
	cfg := testConfig()
	cfg.SilenceProbability = 1
	cfg.SilenceMin, cfg.SilenceMax = time.Minute, time.Minute
	s, clk := newSim(t, oneTrip(), cfg)
	if ps := s.step(context.Background(), clk.Now()); len(ps) != 0 {
		t.Fatalf("silent bus emitted %d positions", len(ps))
	}
}

func TestResumesFromLastPosition(t *testing.T) {
	st := oneTrip()
	st.lastPos = map[string]geo.Point{"t1": {Lat: 40.015, Lon: 49.0}} // between B and C
	s, _ := newSim(t, st, testConfig())
	b := s.buses[0]
	want := b.path.Line.LengthM() * 0.75
	if math.Abs(b.distM-want) > 5 {
		t.Fatalf("resumed at %.1f m, want ~%.1f", b.distM, want)
	}
	if b.nextStop != 2 || !b.dwellUntil.IsZero() {
		t.Fatalf("resumed bus should head to C without layover: nextStop=%d dwell=%v", b.nextStop, b.dwellUntil)
	}
}
