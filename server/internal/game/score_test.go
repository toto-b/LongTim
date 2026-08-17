package game

import (
	"math"
	"testing"
)

// Die Erwartungswerte stammen aus SCORE_BANDS der Offline-Fassung. Wenn dieser
// Test bricht, hat sich das Spielgefuehl gegenueber dem Original geaendert.
func TestPointsForMatchesOriginalBands(t *testing.T) {
	cases := []struct {
		distance float64
		want     int
	}{
		{0, 4},
		{5.9, 4},
		{6, 4},
		{6.01, 3},
		{14, 3},
		{14.1, 2},
		{20, 2},
		{20.5, 1},
		{28, 1},
		{28.1, 0},
		{141.4, 0},
	}
	for _, c := range cases {
		if got := PointsFor(c.distance); got != c.want {
			t.Errorf("PointsFor(%v) = %d, erwartet %d", c.distance, got, c.want)
		}
	}
}

func TestScoreBandsAreOrderedInsideOut(t *testing.T) {
	for i := 1; i < len(ScoreBands); i++ {
		if ScoreBands[i].Max <= ScoreBands[i-1].Max {
			t.Fatalf("band %d (max %v) ist nicht groesser als band %d (max %v); PointsFor verlaesst sich darauf",
				i, ScoreBands[i].Max, i-1, ScoreBands[i-1].Max)
		}
		if ScoreBands[i].Points >= ScoreBands[i-1].Points {
			t.Fatalf("band %d gibt nicht weniger punkte als band %d", i, i-1)
		}
	}
}

func TestDistance(t *testing.T) {
	if d := Distance(Point{0, 0}, Point{3, 4}); d != 5 {
		t.Errorf("Distance = %v, erwartet 5", d)
	}
	if d := Distance(Point{50, 50}, Point{50, 50}); d != 0 {
		t.Errorf("Distance zu sich selbst = %v, erwartet 0", d)
	}
	// Symmetrie
	a, b := Point{12, 87}, Point{63, 4}
	if math.Abs(Distance(a, b)-Distance(b, a)) > 1e-9 {
		t.Error("Distance ist nicht symmetrisch")
	}
}
