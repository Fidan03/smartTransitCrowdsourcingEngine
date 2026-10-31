package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"smart-transit/internal/alerts"
)

func (h *handlers) operatorDashboard(w http.ResponseWriter, r *http.Request) {
	now := h.deps.Clock.Now()
	fresh := now.Add(-h.deps.GPSStaleAfter)
	routes, err := h.deps.Operator.Routes(r.Context(), fresh, now.Add(-h.deps.GPSMaxAge))
	if err != nil {
		writeInternal(w, r, err)
		return
	}
	d, err := h.deps.Operator.Summary(r.Context(), now, fresh, routes)
	if err != nil {
		writeInternal(w, r, err)
		return
	}
	d.DataMode = "LIVE"
	if h.deps.Simulated {
		d.DataMode = "SIMULATED"
	}
	d.StaleAfterSeconds = h.deps.GPSStaleAfter.Seconds()
	d.AnalyticsWindowMinutes = h.deps.CrowdRule.Window.Minutes()
	writeData(w, http.StatusOK, d)
}

func (h *handlers) operatorRoutes(w http.ResponseWriter, r *http.Request) {
	now := h.deps.Clock.Now()
	routes, err := h.deps.Operator.Routes(r.Context(), now.Add(-h.deps.GPSStaleAfter), now.Add(-h.deps.GPSMaxAge))
	if err != nil {
		writeInternal(w, r, err)
		return
	}
	writeData(w, http.StatusOK, routes)
}

var validAlertStatus = map[string]bool{
	alerts.StatusActive: true, alerts.StatusAcknowledged: true, alerts.StatusResolved: true,
}

// listAlerts supports ?status=ACTIVE,ACKNOWLEDGED (comma-separated).
func (h *handlers) listAlerts(w http.ResponseWriter, r *http.Request) {
	var statuses []string
	if s := r.URL.Query().Get("status"); s != "" {
		for _, part := range strings.Split(s, ",") {
			p := strings.ToUpper(strings.TrimSpace(part))
			if !validAlertStatus[p] {
				writeError(w, http.StatusBadRequest, "invalid_status", "status must be ACTIVE, ACKNOWLEDGED, or RESOLVED")
				return
			}
			statuses = append(statuses, p)
		}
	}
	list, err := h.deps.Alerts.List(r.Context(), statuses, 100)
	if err != nil {
		writeInternal(w, r, err)
		return
	}
	if list == nil {
		list = []alerts.Alert{}
	}
	writeData(w, http.StatusOK, list)
}

func (h *handlers) getAlert(w http.ResponseWriter, r *http.Request) {
	a, err := h.deps.Alerts.Get(r.Context(), r.PathValue("id"))
	if errors.Is(err, alerts.ErrNotFound) {
		writeError(w, http.StatusNotFound, "alert_not_found", "alert not found")
		return
	}
	if err != nil {
		writeInternal(w, r, err)
		return
	}
	writeData(w, http.StatusOK, a)
}

type updateAlertRequest struct {
	Action string `json:"action"` // "acknowledge" or "resolve"
	Note   string `json:"note"`
}

// updateAlert implements PATCH /api/v1/operator/alerts/{id}.
func (h *handlers) updateAlert(w http.ResponseWriter, r *http.Request) {
	var req updateAlertRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if len(req.Note) > 500 {
		writeError(w, http.StatusBadRequest, "note_too_long", "note must be at most 500 characters")
		return
	}
	user := userFrom(r.Context())
	id := r.PathValue("id")

	var (
		a   alerts.Alert
		err error
	)
	switch req.Action {
	case "acknowledge":
		a, err = h.deps.Alerts.Acknowledge(r.Context(), id, user.ID)
	case "resolve":
		a, err = h.deps.Alerts.Resolve(r.Context(), id, user.ID, strings.TrimSpace(req.Note))
	default:
		writeError(w, http.StatusBadRequest, "invalid_action", `action must be "acknowledge" or "resolve"`)
		return
	}
	switch {
	case errors.Is(err, alerts.ErrNotFound):
		writeError(w, http.StatusNotFound, "alert_not_found", "alert not found")
	case errors.Is(err, alerts.ErrInvalidTransition):
		writeError(w, http.StatusConflict, "invalid_transition", "this action is not allowed in the alert's current status")
	case err != nil:
		writeInternal(w, r, err)
	default:
		writeData(w, http.StatusOK, a)
	}
}
