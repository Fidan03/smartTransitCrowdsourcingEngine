package httpapi

import (
	"errors"
	"math"
	"net/http"
	"strconv"
	"time"

	"smart-transit/internal/crowd"
	"smart-transit/internal/transit"
)

type crowdReportRequest struct {
	RouteID    string `json:"route_id"`
	StopID     string `json:"stop_id"`
	VehicleID  string `json:"vehicle_id"`
	CrowdLevel string `json:"crowd_level"`
}

type crowdReportResponse struct {
	ID         string    `json:"id"`
	Accepted   bool      `json:"accepted"`
	ReceivedAt time.Time `json:"received_at"`
}

// submitCrowdReport implements POST /api/v1/crowd-reports (plan 7.1).
// Optional header X-Client-Id: a random per-browser ID used (hashed) for
// duplicate control. The server assigns the ID and receipt timestamp.
func (h *handlers) submitCrowdReport(w http.ResponseWriter, r *http.Request) {
	var req crowdReportRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	key := crowd.SourceKey(h.deps.ReportKeySecret, r.Header.Get("X-Client-Id"), clientIP(r))
	rep, err := h.deps.Crowd.Submit(r.Context(), crowd.SubmitRequest{
		RouteID: req.RouteID, StopID: req.StopID, VehicleID: req.VehicleID, Level: req.CrowdLevel,
	}, key)

	var dup *crowd.DuplicateError
	switch {
	case errors.Is(err, crowd.ErrInvalidLevel):
		writeError(w, http.StatusBadRequest, "invalid_crowd_level", err.Error())
	case errors.Is(err, crowd.ErrUnknownRoute):
		writeError(w, http.StatusUnprocessableEntity, "unknown_route", err.Error())
	case errors.Is(err, crowd.ErrUnknownStop):
		writeError(w, http.StatusUnprocessableEntity, "unknown_stop", err.Error())
	case errors.Is(err, crowd.ErrUnknownVehicle):
		writeError(w, http.StatusUnprocessableEntity, "unknown_vehicle", err.Error())
	case errors.As(err, &dup):
		w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(dup.RetryAfter.Seconds()))))
		writeError(w, http.StatusTooManyRequests, "duplicate_report",
			"you already reported on this route recently; please wait before reporting again")
	case err != nil:
		writeInternal(w, r, err)
	default:
		writeData(w, http.StatusCreated, crowdReportResponse{ID: rep.ID, Accepted: true, ReceivedAt: rep.CreatedAt})
	}
}

type crowdStatusResponse struct {
	crowd.RouteStatus
	WindowMinutes float64 `json:"window_minutes"`
	MinReports    int     `json:"min_reports"`
	Threshold     float64 `json:"threshold"`
}

// routeCrowdStatus returns the persisted status and its evidence (plan 7.4).
func (h *handlers) routeCrowdStatus(w http.ResponseWriter, r *http.Request) {
	routeID := r.PathValue("id")
	if _, err := h.deps.Transit.GetRoute(r.Context(), routeID); errors.Is(err, transit.ErrNotFound) {
		writeError(w, http.StatusNotFound, "route_not_found", "route not found")
		return
	} else if err != nil {
		writeInternal(w, r, err)
		return
	}
	st, err := h.deps.CrowdRead.GetRouteStatus(r.Context(), routeID)
	if err != nil {
		writeInternal(w, r, err)
		return
	}
	writeData(w, http.StatusOK, crowdStatusResponse{
		RouteStatus:   st,
		WindowMinutes: h.deps.CrowdRule.Window.Minutes(),
		MinReports:    h.deps.CrowdRule.MinReports,
		Threshold:     h.deps.CrowdRule.Threshold,
	})
}
