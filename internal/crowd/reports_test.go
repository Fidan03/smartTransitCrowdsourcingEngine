package crowd

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"smart-transit/internal/clock"
	"smart-transit/internal/transit"
)

type fakeLookups struct{}

func (fakeLookups) GetRoute(_ context.Context, id string) (transit.Route, error) {
	if id == "route_d1" {
		return transit.Route{ID: id}, nil
	}
	return transit.Route{}, transit.ErrNotFound
}
func (fakeLookups) StopOnRoute(_ context.Context, _, stop string) (bool, error) {
	return stop == "stop_on", nil
}
func (fakeLookups) VehicleExists(_ context.Context, id string) (bool, error) {
	return id == "bus_101" || id == "bus_201", nil
}
func (fakeLookups) ActiveTripForVehicle(_ context.Context, id string) (transit.Trip, bool, error) {
	switch id {
	case "bus_101":
		return transit.Trip{ID: "trip-1", RouteID: "route_d1"}, true, nil
	case "bus_201":
		return transit.Trip{ID: "trip-2", RouteID: "route_d2"}, true, nil
	}
	return transit.Trip{}, false, nil
}

type memReports struct{ reps []Report }

func (m *memReports) InsertIfNotDuplicate(_ context.Context, r Report, dupSince time.Time) (Report, *time.Time, error) {
	for i := len(m.reps) - 1; i >= 0; i-- {
		p := m.reps[i]
		if p.RouteID == r.RouteID && p.SourceKey == r.SourceKey && p.CreatedAt.After(dupSince) {
			at := p.CreatedAt
			return Report{}, &at, nil
		}
	}
	r.ID = "id"
	m.reps = append(m.reps, r)
	return r, nil, nil
}

type countingEvaluator struct{ calls []string }

func (c *countingEvaluator) EvaluateRoute(_ context.Context, id string) error {
	c.calls = append(c.calls, id)
	return nil
}

func newService() (*Service, *memReports, *countingEvaluator, *clock.Fake) {
	st, ev, clk := &memReports{}, &countingEvaluator{}, clock.NewFake(now)
	return NewService(st, fakeLookups{}, ev, clk, slog.New(slog.NewTextHandler(io.Discard, nil)), 2*time.Minute), st, ev, clk
}

func TestSubmitStoresWithServerTimeAndEvaluates(t *testing.T) {
	s, st, ev, _ := newService()
	r, err := s.Submit(context.Background(), SubmitRequest{RouteID: "route_d1", StopID: "stop_on", VehicleID: "bus_101", Level: "HIGH"}, "k1")
	if err != nil {
		t.Fatal(err)
	}
	if !r.CreatedAt.Equal(now) || r.Level != High || r.TripID == nil || *r.TripID != "trip-1" {
		t.Errorf("unexpected report: %+v", r)
	}
	if len(st.reps) != 1 || len(ev.calls) != 1 || ev.calls[0] != "route_d1" {
		t.Errorf("stored=%d evaluations=%v", len(st.reps), ev.calls)
	}
}

func TestSubmitValidation(t *testing.T) {
	cases := []struct {
		req  SubmitRequest
		want error
	}{
		{SubmitRequest{RouteID: "route_d1", Level: "EXTREME"}, ErrInvalidLevel},
		{SubmitRequest{RouteID: "route_d1", Level: "high"}, ErrInvalidLevel},
		{SubmitRequest{RouteID: "nope", Level: "HIGH"}, ErrUnknownRoute},
		{SubmitRequest{RouteID: "route_d1", StopID: "stop_off", Level: "HIGH"}, ErrUnknownStop},
		{SubmitRequest{RouteID: "route_d1", VehicleID: "bus_999", Level: "HIGH"}, ErrUnknownVehicle},
	}
	for _, c := range cases {
		s, st, _, _ := newService()
		if _, err := s.Submit(context.Background(), c.req, "k"); !errors.Is(err, c.want) {
			t.Errorf("%+v: err = %v, want %v", c.req, err, c.want)
		}
		if len(st.reps) != 0 {
			t.Errorf("%+v: invalid report stored", c.req)
		}
	}
}

func TestSubmitTripOnlyAttachedWhenVehicleOnRoute(t *testing.T) {
	s, _, _, _ := newService()
	r, err := s.Submit(context.Background(), SubmitRequest{RouteID: "route_d1", VehicleID: "bus_201", Level: "LOW"}, "k")
	if err != nil {
		t.Fatal(err)
	}
	if r.TripID != nil {
		t.Errorf("trip from another route attached: %v", *r.TripID)
	}
}

func TestSubmitDuplicateControl(t *testing.T) {
	s, st, _, clk := newService()
	ctx := context.Background()
	req := SubmitRequest{RouteID: "route_d1", Level: "HIGH"}
	if _, err := s.Submit(ctx, req, "k"); err != nil {
		t.Fatal(err)
	}

	clk.Advance(30 * time.Second)
	_, err := s.Submit(ctx, req, "k")
	var dup *DuplicateError
	if !errors.As(err, &dup) || dup.RetryAfter != 90*time.Second {
		t.Fatalf("err = %v, want duplicate with 90s retry", err)
	}
	if _, err := s.Submit(ctx, req, "other"); err != nil {
		t.Fatalf("different source should be accepted: %v", err)
	}

	clk.Advance(91 * time.Second)
	if _, err := s.Submit(ctx, req, "k"); err != nil {
		t.Fatalf("after interval should be accepted: %v", err)
	}
	if len(st.reps) != 3 {
		t.Errorf("stored %d, want 3", len(st.reps))
	}
}

func TestSourceKey(t *testing.T) {
	secret := []byte("s")
	a := SourceKey(secret, "abcdef12-3456", "1.2.3.4")
	if a != SourceKey(secret, "ABCDEF12-3456", "9.9.9.9") {
		t.Error("client ID key should ignore IP and case")
	}
	if SourceKey(secret, "", "1.2.3.4") == SourceKey(secret, "", "5.6.7.8") {
		t.Error("different IPs should differ")
	}
	if SourceKey(secret, "bad id!", "1.2.3.4") != SourceKey(secret, "", "1.2.3.4") {
		t.Error("malformed client ID should fall back to IP")
	}
	if SourceKey([]byte("other"), "abcdef12-3456", "") == a {
		t.Error("key must depend on secret")
	}
	if len(a) != 32 {
		t.Errorf("len = %d", len(a))
	}
}
