package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"smart-transit/internal/alerts"
	"smart-transit/internal/auth"
	"smart-transit/internal/clock"
	"smart-transit/internal/crowd"
	"smart-transit/internal/demand"
	"smart-transit/internal/transit"
)

type fakeCrowd struct {
	err     error
	gotReq  crowd.SubmitRequest
	gotKeys []string
}

func (f *fakeCrowd) Submit(_ context.Context, req crowd.SubmitRequest, key string) (crowd.Report, error) {
	f.gotReq = req
	f.gotKeys = append(f.gotKeys, key)
	if f.err != nil {
		return crowd.Report{}, f.err
	}
	return crowd.Report{ID: "rep-1", CreatedAt: testNow}, nil
}

type fakeAuth struct{ users map[string]auth.User }

func (f *fakeAuth) Login(_ context.Context, email, pw string) (string, auth.User, time.Time, error) {
	if email == "op@example.com" && pw == "correct-horse-battery" {
		return "tok-op", f.users["tok-op"], testNow.Add(time.Hour), nil
	}
	return "", auth.User{}, time.Time{}, auth.ErrInvalidCredentials
}
func (f *fakeAuth) Authenticate(_ context.Context, token string) (auth.User, error) {
	if u, ok := f.users[token]; ok {
		return u, nil
	}
	return auth.User{}, auth.ErrUnauthenticated
}
func (f *fakeAuth) Logout(context.Context, string) error { return nil }

type fakeAlerts struct {
	acked, resolved []string
	byUser          []string
}

func (f *fakeAlerts) List(context.Context, []string, int) ([]alerts.Alert, error) {
	return []alerts.Alert{{ID: "a1", Status: "ACTIVE"}}, nil
}
func (f *fakeAlerts) Get(_ context.Context, id string) (alerts.Alert, error) {
	if id == "a1" {
		return alerts.Alert{ID: "a1"}, nil
	}
	return alerts.Alert{}, alerts.ErrNotFound
}
func (f *fakeAlerts) Acknowledge(_ context.Context, id, user string) (alerts.Alert, error) {
	if id == "done" {
		return alerts.Alert{}, alerts.ErrInvalidTransition
	}
	f.acked, f.byUser = append(f.acked, id), append(f.byUser, user)
	return alerts.Alert{ID: id, Status: "ACKNOWLEDGED"}, nil
}
func (f *fakeAlerts) Resolve(_ context.Context, id, user, _ string) (alerts.Alert, error) {
	f.resolved, f.byUser = append(f.resolved, id), append(f.byUser, user)
	return alerts.Alert{ID: id, Status: "RESOLVED"}, nil
}

func fullRouter(fc *fakeCrowd, fa *fakeAlerts) http.Handler {
	return NewRouter(Deps{
		Transit:         &fakeTransit{routes: []transit.Route{{ID: "route_d1"}}},
		Crowd:           fc,
		Alerts:          fa,
		Auth:            &fakeAuth{users: map[string]auth.User{"tok-op": {ID: "u1", Email: "op@example.com", Role: auth.RoleOperator}, "tok-viewer": {ID: "u2", Role: "VIEWER"}}},
		Clock:           clock.NewFake(testNow),
		ReportKeySecret: []byte("secret"),
	})
}

func send(t *testing.T, h http.Handler, method, path, body, cookie string, hdr map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestSubmitCrowdReportAccepted(t *testing.T) {
	fc := &fakeCrowd{}
	rec := send(t, fullRouter(fc, &fakeAlerts{}), http.MethodPost, "/api/v1/crowd-reports",
		`{"route_id":"route_d1","stop_id":"stop_102","vehicle_id":"bus_101","crowd_level":"HIGH"}`, "",
		map[string]string{"X-Client-Id": "abcdef12-3456"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	var body struct {
		Data crowdReportResponse
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if !body.Data.Accepted || body.Data.ID != "rep-1" || !body.Data.ReceivedAt.Equal(testNow) {
		t.Errorf("unexpected response: %+v", body.Data)
	}
	if fc.gotReq.StopID != "stop_102" || fc.gotReq.Level != "HIGH" {
		t.Errorf("request not passed through: %+v", fc.gotReq)
	}
	if want := crowd.SourceKey([]byte("secret"), "abcdef12-3456", ""); fc.gotKeys[0] != want {
		t.Errorf("source key not derived from client id")
	}
}

func TestSubmitCrowdReportErrors(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		body   string
		ctype  string
		status int
		code   string
	}{
		{"invalid level", crowd.ErrInvalidLevel, `{"route_id":"route_d1","crowd_level":"X"}`, "", 400, "invalid_crowd_level"},
		{"unknown route", crowd.ErrUnknownRoute, `{"route_id":"nope","crowd_level":"HIGH"}`, "", 422, "unknown_route"},
		{"unknown stop", crowd.ErrUnknownStop, `{"route_id":"route_d1","stop_id":"x","crowd_level":"HIGH"}`, "", 422, "unknown_stop"},
		{"duplicate", &crowd.DuplicateError{RetryAfter: 61 * time.Second}, `{"route_id":"route_d1","crowd_level":"HIGH"}`, "", 429, "duplicate_report"},
		{"client timestamp rejected", nil, `{"route_id":"route_d1","crowd_level":"HIGH","created_at":"2020-01-01T00:00:00Z"}`, "", 400, "invalid_json"},
		{"form post rejected", nil, `route_id=route_d1`, "application/x-www-form-urlencoded", 415, "unsupported_media_type"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			hdr := map[string]string{}
			if c.ctype != "" {
				hdr["Content-Type"] = c.ctype
			}
			rec := send(t, fullRouter(&fakeCrowd{err: c.err}, &fakeAlerts{}), http.MethodPost, "/api/v1/crowd-reports", c.body, "", hdr)
			var body errorEnvelope
			_ = json.Unmarshal(rec.Body.Bytes(), &body)
			if rec.Code != c.status || body.Error.Code != c.code {
				t.Fatalf("got %d %q, want %d %q", rec.Code, body.Error.Code, c.status, c.code)
			}
			if c.status == 429 && rec.Header().Get("Retry-After") != "61" {
				t.Errorf("Retry-After = %q", rec.Header().Get("Retry-After"))
			}
		})
	}
}

type fakeCrowdRead struct{ st crowd.RouteStatus }

func (f fakeCrowdRead) GetRouteStatus(context.Context, string) (crowd.RouteStatus, error) {
	return f.st, nil
}

type fakeDemand struct{ gotRoute string }

func (f *fakeDemand) Estimate(routeNumber string, _ time.Time) (demand.Estimate, bool) {
	f.gotRoute = routeNumber
	if routeNumber == "D1" {
		return demand.Estimate{Level: demand.High, IsEstimate: true, TypicalDailyTrips: 21000}, true
	}
	return demand.Estimate{}, false
}
func (f *fakeDemand) Summary(string) (demand.Summary, bool) { return demand.Summary{}, false }

func TestCrowdStatusIncludesEstimateWithoutChangingLiveStatus(t *testing.T) {
	fd := &fakeDemand{}
	h := NewRouter(Deps{
		Transit:   &fakeTransit{routes: []transit.Route{{ID: "route_d1", RouteNumber: "D1"}, {ID: "route_x", RouteNumber: "X"}}},
		CrowdRead: fakeCrowdRead{st: crowd.RouteStatus{RouteID: "route_d1", Status: crowd.StatusUnknown, ValidCount: 1}},
		CrowdRule: crowd.DefaultRule,
		Demand:    fd,
		Clock:     clock.NewFake(testNow),
	})
	rec := send(t, h, http.MethodGet, "/api/v1/routes/route_d1/crowd-status", "", "", nil)
	var body struct {
		Data struct {
			Status   string `json:"status"`
			Expected *struct {
				Level      string `json:"level"`
				IsEstimate bool   `json:"is_estimate"`
			} `json:"expected"`
		}
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Data.Status != "UNKNOWN" {
		t.Errorf("live status must stay UNKNOWN, got %s", body.Data.Status)
	}
	if body.Data.Expected == nil || body.Data.Expected.Level != "HIGH" || !body.Data.Expected.IsEstimate {
		t.Errorf("expected estimate missing: %+v", body.Data.Expected)
	}
	if fd.gotRoute != "D1" {
		t.Errorf("estimate must be looked up by public route number, got %q", fd.gotRoute)
	}

	// No ridership data for the route: expected is null, never a made-up LOW.
	rec = send(t, h, http.MethodGet, "/api/v1/routes/route_x/crowd-status", "", "", nil)
	if !strings.Contains(rec.Body.String(), `"expected":null`) {
		t.Errorf("expected null estimate, got %s", rec.Body)
	}
}

type denyAll struct{}

func (denyAll) Allow(string) (bool, time.Duration) { return false, 5 * time.Second }

func TestReportRateLimit(t *testing.T) {
	h := NewRouter(Deps{Transit: &fakeTransit{}, Crowd: &fakeCrowd{}, Clock: clock.NewFake(testNow), ReportLimiter: denyAll{}})
	rec := send(t, h, http.MethodPost, "/api/v1/crowd-reports", `{"route_id":"r","crowd_level":"HIGH"}`, "", nil)
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") != "5" {
		t.Fatalf("got %d Retry-After=%q", rec.Code, rec.Header().Get("Retry-After"))
	}
}

func TestOperatorEndpointsRequireAuth(t *testing.T) {
	h := fullRouter(&fakeCrowd{}, &fakeAlerts{})
	for _, path := range []string{"/api/v1/operator/alerts", "/api/v1/operator/alerts/a1", "/api/v1/auth/me"} {
		if rec := send(t, h, http.MethodGet, path, "", "", nil); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s without session: %d, want 401", path, rec.Code)
		}
		if rec := send(t, h, http.MethodGet, path, "", "forged", nil); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s with bad session: %d, want 401", path, rec.Code)
		}
		if rec := send(t, h, http.MethodGet, path, "", "tok-viewer", nil); rec.Code != http.StatusForbidden {
			t.Errorf("%s with non-operator role: %d, want 403", path, rec.Code)
		}
	}
	if rec := send(t, h, http.MethodPatch, "/api/v1/operator/alerts/a1", `{"action":"resolve"}`, "", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("PATCH without session: %d", rec.Code)
	}
}

func TestLoginSetsSecureSessionCookie(t *testing.T) {
	h := fullRouter(&fakeCrowd{}, &fakeAlerts{})
	rec := send(t, h, http.MethodPost, "/api/v1/auth/login", `{"email":"op@example.com","password":"correct-horse-battery"}`, "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	c := rec.Result().Cookies()
	if len(c) != 1 || c[0].Name != sessionCookie || c[0].Value != "tok-op" || !c[0].HttpOnly || c[0].SameSite != http.SameSiteStrictMode {
		t.Fatalf("unexpected cookie: %+v", c)
	}
	if rec := send(t, h, http.MethodPost, "/api/v1/auth/login", `{"email":"op@example.com","password":"wrong"}`, "", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("wrong password: %d", rec.Code)
	}
}

func TestAlertActions(t *testing.T) {
	fa := &fakeAlerts{}
	h := fullRouter(&fakeCrowd{}, fa)

	rec := send(t, h, http.MethodPatch, "/api/v1/operator/alerts/a1", `{"action":"acknowledge"}`, "tok-op", nil)
	if rec.Code != http.StatusOK || len(fa.acked) != 1 || fa.byUser[0] != "u1" {
		t.Fatalf("acknowledge: %d %v %v", rec.Code, fa.acked, fa.byUser)
	}
	if rec := send(t, h, http.MethodPatch, "/api/v1/operator/alerts/a1", `{"action":"resolve","note":"spare bus sent"}`, "tok-op", nil); rec.Code != http.StatusOK || len(fa.resolved) != 1 {
		t.Fatalf("resolve: %d", rec.Code)
	}
	if rec := send(t, h, http.MethodPatch, "/api/v1/operator/alerts/done", `{"action":"acknowledge"}`, "tok-op", nil); rec.Code != http.StatusConflict {
		t.Errorf("invalid transition: %d, want 409", rec.Code)
	}
	if rec := send(t, h, http.MethodPatch, "/api/v1/operator/alerts/a1", `{"action":"dispatch_bus"}`, "tok-op", nil); rec.Code != http.StatusBadRequest {
		t.Errorf("unknown action: %d, want 400 (no automatic dispatch exists)", rec.Code)
	}
	if rec := send(t, h, http.MethodGet, "/api/v1/operator/alerts?status=BOGUS", "", "tok-op", nil); rec.Code != http.StatusBadRequest {
		t.Errorf("bad status filter: %d", rec.Code)
	}
	if rec := send(t, h, http.MethodGet, "/api/v1/operator/alerts/zzz", "", "tok-op", nil); rec.Code != http.StatusNotFound {
		t.Errorf("missing alert: %d", rec.Code)
	}
}
