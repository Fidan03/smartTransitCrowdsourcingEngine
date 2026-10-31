package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"smart-transit/internal/clock"
	"smart-transit/internal/transit"
)

var testNow = time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)

type fakeTransit struct {
	routes []transit.Route
	stops  map[string][]transit.RouteStop
}

func (f *fakeTransit) ListRoutes(context.Context) ([]transit.Route, error) { return f.routes, nil }

func (f *fakeTransit) GetRoute(_ context.Context, id string) (transit.Route, error) {
	for _, r := range f.routes {
		if r.ID == id {
			return r, nil
		}
	}
	return transit.Route{}, transit.ErrNotFound
}

func (f *fakeTransit) ListRouteStops(ctx context.Context, id string, _ int) ([]transit.RouteStop, error) {
	if _, err := f.GetRoute(ctx, id); err != nil {
		return nil, err
	}
	return f.stops[id], nil
}

func (f *fakeTransit) GetStop(_ context.Context, id string) (transit.Stop, error) {
	for _, list := range f.stops {
		for _, s := range list {
			if s.ID == id {
				return s.Stop, nil
			}
		}
	}
	return transit.Stop{}, transit.ErrNotFound
}

func (f *fakeTransit) ListStopRoutes(_ context.Context, stopID string) ([]transit.StopRoute, error) {
	var out []transit.StopRoute
	for rid, list := range f.stops {
		for _, s := range list {
			if s.ID == stopID {
				out = append(out, transit.StopRoute{RouteID: rid, Direction: s.Direction, StopOrder: s.StopOrder})
			}
		}
	}
	return out, nil
}

func newTestRouter() http.Handler {
	ft := &fakeTransit{
		routes: []transit.Route{{ID: "route_d1", RouteNumber: "D1", Name: "Demo", IsSimulated: true}},
		stops: map[string][]transit.RouteStop{
			"route_d1": {
				{Stop: transit.Stop{ID: "stop_a", Name: "A"}, StopOrder: 0},
				{Stop: transit.Stop{ID: "stop_b", Name: "B"}, StopOrder: 1},
			},
		},
	}
	return NewRouter(Deps{Transit: ft, Simulated: true, Clock: clock.NewFake(testNow)})
}

func do(t *testing.T, h http.Handler, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	return rec
}

func TestListRoutes(t *testing.T) {
	rec := do(t, newTestRouter(), http.MethodGet, "/api/v1/routes")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("X-Data-Mode"); got != "SIMULATED" {
		t.Errorf("X-Data-Mode = %q, want SIMULATED", got)
	}
	var body struct{ Data []transit.Route }
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Data) != 1 || body.Data[0].ID != "route_d1" {
		t.Errorf("unexpected routes: %+v", body.Data)
	}
}

func TestGetRouteNotFound(t *testing.T) {
	rec := do(t, newTestRouter(), http.MethodGet, "/api/v1/routes/nope")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	var body errorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Error.Code != "route_not_found" {
		t.Errorf("error code = %q", body.Error.Code)
	}
}

func TestListRouteStopsOrdered(t *testing.T) {
	rec := do(t, newTestRouter(), http.MethodGet, "/api/v1/routes/route_d1/stops")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body struct{ Data []transit.RouteStop }
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Data) != 2 || body.Data[0].ID != "stop_a" || body.Data[1].ID != "stop_b" {
		t.Errorf("unexpected stops: %+v", body.Data)
	}
}

func TestListRouteStopsInvalidDirection(t *testing.T) {
	rec := do(t, newTestRouter(), http.MethodGet, "/api/v1/routes/route_d1/stops?direction=5")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestMethodNotAllowed(t *testing.T) {
	rec := do(t, newTestRouter(), http.MethodPost, "/api/v1/routes")
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
}
