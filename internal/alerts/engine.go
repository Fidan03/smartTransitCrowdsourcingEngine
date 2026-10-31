package alerts

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"smart-transit/internal/clock"
	"smart-transit/internal/crowd"
)

// Engine recalculates crowd status and applies the alert policy. All state
// (timer, alerts) lives in PostgreSQL so it survives restarts (plan 8.3).
type Engine struct {
	pool   *pgxpool.Pool
	rule   crowd.Rule
	policy Policy
	clock  clock.Clock
	log    *slog.Logger
}

func NewEngine(pool *pgxpool.Pool, rule crowd.Rule, policy Policy, clk clock.Clock, log *slog.Logger) *Engine {
	return &Engine{pool: pool, rule: rule, policy: policy, clock: clk, log: log}
}

// EvalResult describes one evaluation (useful for tests and logs).
type EvalResult struct {
	Result       crowd.Result
	HighSince    *time.Time
	AlertCreated string // ID of a newly created alert, if any
}

// EvaluateRoute implements crowd.Evaluator.
func (e *Engine) EvaluateRoute(ctx context.Context, routeID string) error {
	_, err := e.Evaluate(ctx, routeID)
	return err
}

// Evaluate runs one evaluation for a route in a single transaction. A
// per-route advisory lock serializes concurrent evaluations (periodic loop
// vs. report submissions) and the partial unique index on alerts is a second
// guard against duplicate active alerts (AC-11).
func (e *Engine) Evaluate(ctx context.Context, routeID string) (EvalResult, error) {
	var out EvalResult
	err := pgx.BeginFunc(ctx, e.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "route-eval:"+routeID); err != nil {
			return err
		}
		now := e.clock.Now()

		var prev Prev
		var prevStatus string
		var calc time.Time
		err := tx.QueryRow(ctx, `
			SELECT status, high_since, calculated_at FROM route_crowd_status
			WHERE target_type = 'ROUTE' AND target_id = $1`, routeID,
		).Scan(&prevStatus, &prev.HighSince, &calc)
		switch {
		case err == nil:
			prev.Status = crowd.Status(prevStatus)
			prev.CalculatedAt = &calc
		case !errors.Is(err, pgx.ErrNoRows):
			return err
		}

		reports, err := crowd.RecentReports(ctx, tx, routeID, now.Add(-e.rule.Window), now)
		if err != nil {
			return err
		}
		res := e.rule.Calculate(reports, now)

		var openID string
		var recoveredAt *time.Time
		err = tx.QueryRow(ctx, `
			SELECT id::text, recovered_at FROM alerts
			WHERE target_type = 'ROUTE' AND target_id = $1 AND status IN ('ACTIVE', 'ACKNOWLEDGED')`,
			routeID).Scan(&openID, &recoveredAt)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		hasOpen := openID != ""

		d := e.policy.Decide(prev, res.Status, now, hasOpen)

		if _, err := tx.Exec(ctx, `
			INSERT INTO route_crowd_status
				(target_type, target_id, status, valid_report_count, low_count, medium_count, high_count,
				 last_report_at, calculated_at, high_since)
			VALUES ('ROUTE', $1, $2, $3, $4, $5, $6, $7, $8, $9)
			ON CONFLICT (target_type, target_id) DO UPDATE SET
				status = EXCLUDED.status,
				valid_report_count = EXCLUDED.valid_report_count,
				low_count = EXCLUDED.low_count,
				medium_count = EXCLUDED.medium_count,
				high_count = EXCLUDED.high_count,
				last_report_at = EXCLUDED.last_report_at,
				calculated_at = EXCLUDED.calculated_at,
				high_since = EXCLUDED.high_since`,
			routeID, string(res.Status), res.ValidCount, res.LowCount, res.MediumCount, res.HighCount,
			res.LastReportAt, now, d.HighSince); err != nil {
			return err
		}

		switch {
		case d.CreateAlert:
			err := tx.QueryRow(ctx, `
				INSERT INTO alerts (target_type, target_id, status, severity, reason, recommendation, high_since, created_at)
				VALUES ('ROUTE', $1, 'ACTIVE', 'HIGH', $2, $3, $4, $5)
				ON CONFLICT (target_type, target_id) WHERE status IN ('ACTIVE', 'ACKNOWLEDGED') DO NOTHING
				RETURNING id::text`,
				routeID, ReasonFor(e.policy.Sustain), Recommendation, d.HighSince, now).Scan(&out.AlertCreated)
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
		case d.MarkRecovered && recoveredAt == nil:
			if _, err := tx.Exec(ctx, `UPDATE alerts SET recovered_at = $2 WHERE id = $1`, openID, now); err != nil {
				return err
			}
		case d.ClearRecovered && recoveredAt != nil:
			if _, err := tx.Exec(ctx, `UPDATE alerts SET recovered_at = NULL WHERE id = $1`, openID); err != nil {
				return err
			}
		}

		out.Result, out.HighSince = res, d.HighSince
		return nil
	})
	if err == nil && out.AlertCreated != "" {
		e.log.Warn("ALERT created: sustained HIGH crowding", "route_id", routeID, "alert_id", out.AlertCreated)
	}
	return out, err
}

// EvaluateAll evaluates every active route; one failure does not stop others.
func (e *Engine) EvaluateAll(ctx context.Context) error {
	rows, err := e.pool.Query(ctx, `SELECT id FROM routes WHERE active ORDER BY id`)
	if err != nil {
		return err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return err
	}
	var firstErr error
	for _, id := range ids {
		if _, err := e.Evaluate(ctx, id); err != nil {
			e.log.Error("evaluate route failed", "route_id", id, "err", err)
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

// Run evaluates all routes immediately and then every interval, so time-based
// transitions (timer reaching 10 minutes, reports ageing out) happen even
// without new reports.
func (e *Engine) Run(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		if err := e.EvaluateAll(ctx); err != nil && ctx.Err() == nil {
			e.log.Error("periodic evaluation had errors", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
