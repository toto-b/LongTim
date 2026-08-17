package game

import (
	"math/rand"
	"testing"
)

func testPairs() []Pair {
	return []Pair{
		{"Kalt", "Heiss"},
		{"Langweilig", "Spannend"},
		{"Leise", "Laut"},
		{"Rund", "Eckig"},
		{"Dunkel", "Hell"},
		{"Lokal", "Global"},
		{"Guenstig", "Teuer"},
		{"Einfach", "Kompliziert"},
	}
}

func TestPairKeyIgnoresOrientation(t *testing.T) {
	if PairKey(Pair{"Kalt", "Heiss"}) != PairKey(Pair{"Heiss", "Kalt"}) {
		t.Fatal("PairKey haengt von der Ausrichtung ab")
	}
}

func TestDrawNeverRepeatsWithinRecentMemory(t *testing.T) {
	d := NewDeck(testPairs(), rand.New(rand.NewSource(1)))
	var window []string

	for round := 0; round < 200; round++ {
		x, y := d.Draw()
		for _, k := range []string{PairKey(x), PairKey(y)} {
			for _, blocked := range window {
				if k == blocked {
					t.Fatalf("runde %d: paar %q kam innerhalb der letzten %d gesperrten erneut", round, k, RecentMemory)
				}
			}
		}
		d.Remember(x, y)
		window = d.Recent()
	}
}

func TestDrawAxesNeverShareAWord(t *testing.T) {
	pairs := append(testPairs(), Pair{"Heiss", "Mild"}) // teilt "Heiss" mit dem ersten Paar
	d := NewDeck(pairs, rand.New(rand.NewSource(7)))
	for i := 0; i < 500; i++ {
		x, y := d.Draw()
		if sharesWord(x, y) {
			t.Fatalf("achsen teilen ein wort: %v / %v", x, y)
		}
		d.Remember(x, y)
	}
}

func TestDrawRespectsExtraBlocked(t *testing.T) {
	d := NewDeck(testPairs(), rand.New(rand.NewSource(3)))
	x, y := d.Draw()
	for i := 0; i < 100; i++ {
		nx, ny := d.Draw(x, y)
		for _, k := range []string{PairKey(nx), PairKey(ny)} {
			if k == PairKey(x) || k == PairKey(y) {
				t.Fatalf("neuwuerfeln lieferte ein gesperrtes paar zurueck: %q", k)
			}
		}
	}
}

func TestOrientationVaries(t *testing.T) {
	d := NewDeck([]Pair{{"A", "B"}, {"C", "D"}}, rand.New(rand.NewSource(11)))
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		x, _ := d.Draw()
		seen[x[0]+x[1]] = true
	}
	if len(seen) < 2 {
		t.Fatal("ausrichtung wird nie gedreht")
	}
}

func TestDrawSurvivesTinyDeck(t *testing.T) {
	// Notbremse: bei zwei Paaren muss Draw trotz Sperrliste liefern statt zu haengen.
	d := NewDeck([]Pair{{"A", "B"}, {"C", "D"}}, rand.New(rand.NewSource(5)))
	for i := 0; i < 50; i++ {
		x, y := d.Draw()
		if PairKey(x) == PairKey(y) {
			t.Fatal("beide achsen sind dasselbe paar")
		}
		d.Remember(x, y)
	}
}

func TestValidateScales(t *testing.T) {
	cases := []struct {
		name    string
		pairs   []Pair
		wantErr bool
	}{
		{"gut", testPairs(), false},
		{"zu kurz", []Pair{{"A", "B"}}, true},
		{"leeres wort", []Pair{{"A", " "}, {"C", "D"}}, true},
		{"identische seiten", []Pair{{"A", "A"}, {"C", "D"}}, true},
		{"duplikat", []Pair{{"A", "B"}, {"B", "A"}}, true},
	}
	for _, c := range cases {
		err := ValidateScales(c.pairs)
		if (err != nil) != c.wantErr {
			t.Errorf("%s: ValidateScales lieferte %v, wantErr=%v", c.name, err, c.wantErr)
		}
	}
}

func TestParseScalesRoundTrip(t *testing.T) {
	pairs, err := ParseScales([]byte(`{"pairs":[["Kalt","Heiss"],["Leise","Laut"]]}`))
	if err != nil {
		t.Fatalf("ParseScales: %v", err)
	}
	if len(pairs) != 2 || pairs[0][0] != "Kalt" {
		t.Fatalf("unerwartete paare: %v", pairs)
	}
}
