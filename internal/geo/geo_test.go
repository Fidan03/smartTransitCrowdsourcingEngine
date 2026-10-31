package geo

import (
	"math"
	"testing"
)

func near(a, b, tol float64) bool { return math.Abs(a-b) <= tol }

func TestDistanceOneDegreeLatitude(t *testing.T) {
	d := DistanceM(Point{40, 49}, Point{41, 49})
	if !near(d, 111_195, 50) {
		t.Fatalf("1 degree latitude = %.0f m, want ~111195", d)
	}
}

func TestPolylinePointAtAndLength(t *testing.T) {
	pl, err := NewPolyline([]Point{{40.0, 49.0}, {40.01, 49.0}, {40.01, 49.01}})
	if err != nil {
		t.Fatal(err)
	}
	seg1 := DistanceM(Point{40.0, 49.0}, Point{40.01, 49.0})
	if !near(pl.LengthM(), seg1+DistanceM(Point{40.01, 49.0}, Point{40.01, 49.01}), 0.001) {
		t.Fatal("length mismatch")
	}
	if p := pl.PointAt(-5); p != (Point{40.0, 49.0}) {
		t.Errorf("PointAt(<0) = %v", p)
	}
	if p := pl.PointAt(1e9); p != (Point{40.01, 49.01}) {
		t.Errorf("PointAt(>len) = %v", p)
	}
	mid := pl.PointAt(seg1 / 2)
	if !near(mid.Lat, 40.005, 1e-6) || mid.Lon != 49.0 {
		t.Errorf("midpoint = %v", mid)
	}
}

func TestProjectRoundTrip(t *testing.T) {
	pl, _ := NewPolyline([]Point{{40.0, 49.0}, {40.01, 49.0}, {40.01, 49.01}})
	for _, d := range []float64{0, 300, 1111, 1500, pl.LengthM()} {
		along, off := pl.Project(pl.PointAt(d))
		if !near(along, d, 2) || off > 2 {
			t.Errorf("Project(PointAt(%.0f)) = along %.1f, offset %.1f", d, along, off)
		}
	}
}

func TestProjectOffPath(t *testing.T) {
	pl, _ := NewPolyline([]Point{{40.0, 49.0}, {40.01, 49.0}})
	// ~85 m east of the midpoint of a north-south segment.
	along, off := pl.Project(Point{40.005, 49.001})
	if !near(along, pl.LengthM()/2, 2) || !near(off, 85, 3) {
		t.Errorf("along %.1f offset %.1f", along, off)
	}
}

func TestReversed(t *testing.T) {
	pl, _ := NewPolyline([]Point{{40.0, 49.0}, {40.01, 49.0}, {40.01, 49.01}})
	r := pl.Reversed()
	if r.PointAt(0) != pl.PointAt(pl.LengthM()) || !near(r.LengthM(), pl.LengthM(), 1e-6) {
		t.Error("reversed polyline mismatch")
	}
}

func TestNewPolylineTooShort(t *testing.T) {
	if _, err := NewPolyline([]Point{{1, 1}}); err == nil {
		t.Fatal("expected error")
	}
}
