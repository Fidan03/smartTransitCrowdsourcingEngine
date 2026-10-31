package gps

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"smart-transit/internal/clock"
)

type memStore struct {
	got []Position
	rec []time.Time
}

func (m *memStore) InsertPosition(_ context.Context, p Position, receivedAt time.Time) error {
	m.got = append(m.got, p)
	m.rec = append(m.rec, receivedAt)
	return nil
}

var t0 = time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)

func newTestIngestor() (*Ingestor, *memStore, *clock.Fake) {
	st := &memStore{}
	clk := clock.NewFake(t0)
	return NewIngestor(st, clk, slog.New(slog.NewTextHandler(io.Discard, nil))), st, clk
}

func validPos() Position {
	return Position{VehicleID: "bus_101", Latitude: 40.4, Longitude: 49.8, ObservedAt: t0.Add(-5 * time.Second), Source: "test"}
}

func ptr[T any](v T) *T { return &v }

func TestIngestSetsServerReceiptTime(t *testing.T) {
	in, st, _ := newTestIngestor()
	rec, err := in.Ingest(context.Background(), validPos())
	if err != nil {
		t.Fatal(err)
	}
	if !rec.Equal(t0) || !st.rec[0].Equal(t0) {
		t.Errorf("received_at = %v, want server time %v", rec, t0)
	}
	if !st.got[0].ObservedAt.Equal(t0.Add(-5 * time.Second)) {
		t.Error("observed_at must be kept separate from received_at")
	}
}

func TestIngestValidation(t *testing.T) {
	cases := map[string]func(*Position){
		"missing vehicle":  func(p *Position) { p.VehicleID = " " },
		"missing source":   func(p *Position) { p.Source = "" },
		"bad latitude":     func(p *Position) { p.Latitude = 91 },
		"bad longitude":    func(p *Position) { p.Longitude = -181 },
		"negative speed":   func(p *Position) { p.SpeedKmh = ptr(-1.0) },
		"absurd speed":     func(p *Position) { p.SpeedKmh = ptr(500.0) },
		"zero observed_at": func(p *Position) { p.ObservedAt = time.Time{} },
		"future":           func(p *Position) { p.ObservedAt = t0.Add(time.Minute) },
		"too old":          func(p *Position) { p.ObservedAt = t0.Add(-25 * time.Hour) },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			in, st, _ := newTestIngestor()
			p := validPos()
			mutate(&p)
			_, err := in.Ingest(context.Background(), p)
			var ve *ValidationError
			if !errors.As(err, &ve) {
				t.Fatalf("err = %v, want ValidationError", err)
			}
			if len(st.got) != 0 {
				t.Error("invalid position was stored")
			}
		})
	}
}

func TestIngestAllowsSmallClockSkew(t *testing.T) {
	in, _, _ := newTestIngestor()
	p := validPos()
	p.ObservedAt = t0.Add(10 * time.Second)
	if _, err := in.Ingest(context.Background(), p); err != nil {
		t.Fatalf("small future skew should be accepted: %v", err)
	}
}

func TestClassify(t *testing.T) {
	stale := 60 * time.Second
	if got := Classify(nil, t0, stale); got != NoData {
		t.Errorf("nil = %s", got)
	}
	if got := Classify(ptr(t0.Add(-60*time.Second)), t0, stale); got != Fresh {
		t.Errorf("exactly at threshold = %s, want FRESH", got)
	}
	if got := Classify(ptr(t0.Add(-61*time.Second)), t0, stale); got != Stale {
		t.Errorf("past threshold = %s, want STALE", got)
	}
}

type chanProvider struct{ ps []Position }

func (c chanProvider) Positions(ctx context.Context) (<-chan Position, error) {
	ch := make(chan Position)
	go func() {
		defer close(ch)
		for _, p := range c.ps {
			select {
			case ch <- p:
			case <-ctx.Done():
				return
			}
		}
	}()
	return ch, nil
}

func TestRunSkipsBadPositions(t *testing.T) {
	in, st, _ := newTestIngestor()
	bad := validPos()
	bad.Latitude = 999
	if err := in.Run(context.Background(), chanProvider{ps: []Position{validPos(), bad, validPos()}}); err != nil {
		t.Fatal(err)
	}
	if len(st.got) != 2 {
		t.Errorf("stored %d positions, want 2", len(st.got))
	}
}
