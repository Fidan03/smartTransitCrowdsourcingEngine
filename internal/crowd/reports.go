package crowd

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"smart-transit/internal/clock"
	"smart-transit/internal/transit"
)

var (
	ErrInvalidLevel   = errors.New("crowd_level must be LOW, MEDIUM, or HIGH")
	ErrUnknownRoute   = errors.New("unknown route_id")
	ErrUnknownStop    = errors.New("stop_id is not on this route")
	ErrUnknownVehicle = errors.New("unknown vehicle_id")
)

// DuplicateError means the same submitter reported on this route too recently.
type DuplicateError struct{ RetryAfter time.Duration }

func (e *DuplicateError) Error() string {
	return fmt.Sprintf("duplicate report; try again in %s", e.RetryAfter.Round(time.Second))
}

// Lookups validates report references against transit data.
type Lookups interface {
	GetRoute(ctx context.Context, id string) (transit.Route, error)
	StopOnRoute(ctx context.Context, routeID, stopID string) (bool, error)
	VehicleExists(ctx context.Context, id string) (bool, error)
	ActiveTripForVehicle(ctx context.Context, vehicleID string) (transit.Trip, bool, error)
}

// Store persists reports.
type Store interface {
	// InsertIfNotDuplicate stores r unless the same source reported on the
	// same route after dupSince. It returns the previous report time when it
	// refuses. Implementations must make check-and-insert atomic.
	InsertIfNotDuplicate(ctx context.Context, r Report, dupSince time.Time) (stored Report, prevAt *time.Time, err error)
}

// Evaluator recalculates a route's status (implemented by the alert engine).
type Evaluator interface {
	EvaluateRoute(ctx context.Context, routeID string) error
}

type Service struct {
	store       Store
	lookups     Lookups
	evaluator   Evaluator
	clock       clock.Clock
	log         *slog.Logger
	dupInterval time.Duration
}

func NewService(store Store, lookups Lookups, evaluator Evaluator, clk clock.Clock, log *slog.Logger, dupInterval time.Duration) *Service {
	return &Service{store: store, lookups: lookups, evaluator: evaluator, clock: clk, log: log, dupInterval: dupInterval}
}

type SubmitRequest struct {
	RouteID   string
	StopID    string // optional
	VehicleID string // optional
	Level     string
}

// Submit validates and stores a report, then recalculates the route status
// (plan 7.1). sourceKey identifies the submitter for duplicate control.
func (s *Service) Submit(ctx context.Context, req SubmitRequest, sourceKey string) (Report, error) {
	level, ok := ParseLevel(req.Level)
	if !ok {
		return Report{}, ErrInvalidLevel
	}
	if _, err := s.lookups.GetRoute(ctx, req.RouteID); errors.Is(err, transit.ErrNotFound) {
		return Report{}, ErrUnknownRoute
	} else if err != nil {
		return Report{}, err
	}

	r := Report{RouteID: req.RouteID, Level: level, SourceKey: sourceKey, CreatedAt: s.clock.Now()}
	if req.StopID != "" {
		ok, err := s.lookups.StopOnRoute(ctx, req.RouteID, req.StopID)
		if err != nil {
			return Report{}, err
		}
		if !ok {
			return Report{}, ErrUnknownStop
		}
		r.StopID = &req.StopID
	}
	if req.VehicleID != "" {
		ok, err := s.lookups.VehicleExists(ctx, req.VehicleID)
		if err != nil {
			return Report{}, err
		}
		if !ok {
			return Report{}, ErrUnknownVehicle
		}
		r.VehicleID = &req.VehicleID
		// Attach the trip server-side when the vehicle is on this route.
		if trip, ok, err := s.lookups.ActiveTripForVehicle(ctx, req.VehicleID); err != nil {
			return Report{}, err
		} else if ok && trip.RouteID == req.RouteID {
			r.TripID = &trip.ID
		}
	}

	stored, prevAt, err := s.store.InsertIfNotDuplicate(ctx, r, r.CreatedAt.Add(-s.dupInterval))
	if err != nil {
		return Report{}, err
	}
	if prevAt != nil {
		return Report{}, &DuplicateError{RetryAfter: prevAt.Add(s.dupInterval).Sub(r.CreatedAt)}
	}

	if s.evaluator != nil {
		if err := s.evaluator.EvaluateRoute(ctx, r.RouteID); err != nil {
			// The report is stored; the periodic evaluator will catch up.
			s.log.Error("evaluate after report failed", "route_id", r.RouteID, "err", err)
		}
	}
	return stored, nil
}

var clientIDPattern = regexp.MustCompile(`^[A-Za-z0-9-]{8,64}$`)

// SourceKey derives an opaque submitter key. A well-formed client ID (random
// per browser) is preferred; otherwise the client IP is used. Both are
// HMAC-hashed so raw identifiers are never stored (plan 13).
func SourceKey(secret []byte, clientID, ip string) string {
	input := "ip:" + ip
	if clientIDPattern.MatchString(clientID) {
		input = "client:" + strings.ToLower(clientID)
	}
	m := hmac.New(sha256.New, secret)
	m.Write([]byte(input))
	return hex.EncodeToString(m.Sum(nil))[:32]
}
