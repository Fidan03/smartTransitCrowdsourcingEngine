package alerts

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"smart-transit/internal/clock"
)

var (
	ErrNotFound          = errors.New("alert not found")
	ErrInvalidTransition = errors.New("invalid alert status transition")
)

const (
	StatusActive       = "ACTIVE"
	StatusAcknowledged = "ACKNOWLEDGED"
	StatusResolved     = "RESOLVED"
)

type Alert struct {
	ID             string     `json:"id"`
	TargetType     string     `json:"target_type"`
	TargetID       string     `json:"target_id"`
	RouteNumber    string     `json:"route_number"`
	RouteName      string     `json:"route_name"`
	Status         string     `json:"status"`
	Severity       string     `json:"severity"`
	Reason         string     `json:"reason"`
	Recommendation string     `json:"recommendation"`
	HighSince      time.Time  `json:"high_since"`
	CreatedAt      time.Time  `json:"created_at"`
	AcknowledgedAt *time.Time `json:"acknowledged_at"`
	AcknowledgedBy *string    `json:"acknowledged_by"`
	ResolvedAt     *time.Time `json:"resolved_at"`
	ResolvedBy     *string    `json:"resolved_by"`
	ResolutionNote *string    `json:"resolution_note"`
	RecoveredAt    *time.Time `json:"recovered_at"` // crowding reliably recovered; ready for review
}

// Lifecycle manages operator actions on alerts.
type Lifecycle struct {
	pool  *pgxpool.Pool
	clock clock.Clock
}

func NewLifecycle(pool *pgxpool.Pool, clk clock.Clock) *Lifecycle {
	return &Lifecycle{pool: pool, clock: clk}
}

const alertSelect = `
	SELECT a.id::text, a.target_type, a.target_id, COALESCE(r.route_number, ''), COALESCE(r.name, ''),
	       a.status, a.severity, a.reason, a.recommendation, a.high_since, a.created_at,
	       a.acknowledged_at, ua.email, a.resolved_at, ur.email, a.resolution_note, a.recovered_at
	FROM alerts a
	LEFT JOIN routes r ON a.target_type = 'ROUTE' AND r.id = a.target_id
	LEFT JOIN users ua ON ua.id = a.acknowledged_by
	LEFT JOIN users ur ON ur.id = a.resolved_by`

func scanAlert(r pgx.Row) (Alert, error) {
	var a Alert
	err := r.Scan(&a.ID, &a.TargetType, &a.TargetID, &a.RouteNumber, &a.RouteName,
		&a.Status, &a.Severity, &a.Reason, &a.Recommendation, &a.HighSince, &a.CreatedAt,
		&a.AcknowledgedAt, &a.AcknowledgedBy, &a.ResolvedAt, &a.ResolvedBy, &a.ResolutionNote, &a.RecoveredAt)
	return a, err
}

// List returns alerts, newest first, optionally filtered by statuses.
func (l *Lifecycle) List(ctx context.Context, statuses []string, limit int) ([]Alert, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	q := alertSelect + ` WHERE (cardinality($1::text[]) = 0 OR a.status = ANY($1)) ORDER BY a.created_at DESC LIMIT $2`
	if statuses == nil {
		statuses = []string{}
	}
	rows, err := l.pool.Query(ctx, q, statuses, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Alert, error) { return scanAlert(r) })
}

func (l *Lifecycle) Get(ctx context.Context, id string) (Alert, error) {
	if !isUUID(id) {
		return Alert{}, ErrNotFound
	}
	a, err := scanAlert(l.pool.QueryRow(ctx, alertSelect+` WHERE a.id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Alert{}, ErrNotFound
	}
	return a, err
}

// Acknowledge marks an ACTIVE alert as seen. It does not resolve the
// crowding problem (plan 8.3).
func (l *Lifecycle) Acknowledge(ctx context.Context, id, userID string) (Alert, error) {
	return l.transition(ctx, id, userID, "acknowledge", "")
}

// Resolve closes an ACTIVE or ACKNOWLEDGED alert. If crowding is still HIGH,
// the sustained-HIGH timer restarts from now, so a new alert can only follow
// after another full sustain period, never immediately.
func (l *Lifecycle) Resolve(ctx context.Context, id, userID, note string) (Alert, error) {
	return l.transition(ctx, id, userID, "resolve", note)
}

func (l *Lifecycle) transition(ctx context.Context, id, userID, action, note string) (Alert, error) {
	if !isUUID(id) {
		return Alert{}, ErrNotFound
	}
	err := pgx.BeginFunc(ctx, l.pool, func(tx pgx.Tx) error {
		now := l.clock.Now()
		var status, targetType, targetID string
		err := tx.QueryRow(ctx, `SELECT status, target_type, target_id FROM alerts WHERE id = $1 FOR UPDATE`, id).
			Scan(&status, &targetType, &targetID)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}

		switch {
		case action == "acknowledge" && status == StatusActive:
			_, err = tx.Exec(ctx, `UPDATE alerts SET status = 'ACKNOWLEDGED', acknowledged_at = $2, acknowledged_by = $3 WHERE id = $1`,
				id, now, userID)
		case action == "resolve" && (status == StatusActive || status == StatusAcknowledged):
			_, err = tx.Exec(ctx, `UPDATE alerts SET status = 'RESOLVED', resolved_at = $2, resolved_by = $3, resolution_note = NULLIF($4, '') WHERE id = $1`,
				id, now, userID, note)
			if err == nil {
				_, err = tx.Exec(ctx, `
					UPDATE route_crowd_status
					SET high_since = CASE WHEN status = 'HIGH' THEN $3::timestamptz ELSE NULL END
					WHERE target_type = $1 AND target_id = $2`, targetType, targetID, now)
			}
		default:
			return ErrInvalidTransition
		}
		if err != nil {
			return err
		}

		meta, _ := json.Marshal(map[string]string{"from_status": status, "note": note})
		_, err = tx.Exec(ctx, `INSERT INTO audit_logs (user_id, action, target_id, created_at, metadata) VALUES ($1, $2, $3, $4, $5)`,
			userID, "alert."+action, id, now, meta)
		return err
	})
	if err != nil {
		return Alert{}, err
	}
	return l.Get(ctx, id)
}

func isUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, c := range s {
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return false
			}
		default:
			if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
				return false
			}
		}
	}
	return true
}
