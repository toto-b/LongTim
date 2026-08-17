package game

import (
	"math/rand"
	"testing"
)

func TestRotationGivesEveryoneExactlyOnePerPass(t *testing.T) {
	ids := []string{"a", "b", "c", "d"}
	r := NewRotation(rand.New(rand.NewSource(42)))

	for pass := 0; pass < 25; pass++ {
		seen := map[string]int{}
		for range ids {
			seen[r.Next(ids)]++
		}
		for _, id := range ids {
			if seen[id] != 1 {
				t.Fatalf("durchgang %d: %q kam %dx dran, erwartet genau 1 (%v)", pass, id, seen[id], seen)
			}
		}
	}
}

func TestRotationNeverRepeatsBackToBack(t *testing.T) {
	ids := []string{"a", "b", "c"}
	r := NewRotation(rand.New(rand.NewSource(9)))

	prev := ""
	for i := 0; i < 300; i++ {
		cur := r.Next(ids)
		if cur == prev {
			t.Fatalf("schritt %d: %q kam zweimal hintereinander dran", i, cur)
		}
		prev = cur
	}
}

func TestRotationSinglePlayer(t *testing.T) {
	r := NewRotation(rand.New(rand.NewSource(1)))
	for i := 0; i < 5; i++ {
		if got := r.Next([]string{"solo"}); got != "solo" {
			t.Fatalf("erwartet solo, bekam %q", got)
		}
	}
}

func TestRotationEmpty(t *testing.T) {
	r := NewRotation(rand.New(rand.NewSource(1)))
	if got := r.Next(nil); got != "" {
		t.Fatalf("erwartet leer, bekam %q", got)
	}
}

func TestRotationUpcoming(t *testing.T) {
	ids := []string{"a", "b", "c"}
	r := NewRotation(rand.New(rand.NewSource(4)))
	cur := r.Next(ids)
	next := r.Upcoming()
	if next == "" {
		t.Fatal("Upcoming ist leer, obwohl noch Eintraege anstehen")
	}
	if next == cur {
		t.Fatal("Upcoming meldet den aktuellen Hinweisgeber")
	}
	if got := r.Next(ids); got != next {
		t.Fatalf("Upcoming sagte %q voraus, Next lieferte %q", next, got)
	}
}

func TestRotationRemoveDropsPlayer(t *testing.T) {
	ids := []string{"a", "b", "c", "d"}
	r := NewRotation(rand.New(rand.NewSource(13)))
	r.Next(ids)

	r.Remove("c")
	remaining := []string{"a", "b", "d"}
	for i := 0; i < 60; i++ {
		if got := r.Next(remaining); got == "c" {
			t.Fatal("entfernter spieler kam wieder dran")
		}
	}
}

func TestRotationOrderStreamStaysBounded(t *testing.T) {
	ids := []string{"a", "b", "c"}
	r := NewRotation(rand.New(rand.NewSource(2)))
	for i := 0; i < 5000; i++ {
		r.Next(ids)
	}
	if len(r.order) > orderTrimThreshold+len(ids)*3 {
		t.Fatalf("reihenfolge-strom waechst unbegrenzt: %d eintraege", len(r.order))
	}
}
