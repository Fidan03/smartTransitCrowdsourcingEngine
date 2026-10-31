package ratelimit

import (
	"testing"
	"time"

	"smart-transit/internal/clock"
)

func TestBurstThenRefill(t *testing.T) {
	clk := clock.NewFake(time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC))
	l := New(6, 3, clk) // 1 token / 10 s, burst 3

	for i := range 3 {
		if ok, _ := l.Allow("a"); !ok {
			t.Fatalf("request %d refused within burst", i)
		}
	}
	ok, wait := l.Allow("a")
	if ok || wait != 10*time.Second {
		t.Fatalf("4th request: ok=%v wait=%v, want refused 10s", ok, wait)
	}
	if ok, _ := l.Allow("b"); !ok {
		t.Fatal("other key should have its own bucket")
	}
	clk.Advance(10 * time.Second)
	if ok, _ := l.Allow("a"); !ok {
		t.Fatal("token should refill after 10 s")
	}
}
