package game

import "math/rand"

// orderTrimThreshold begrenzt das Wachstum des Reihenfolge-Stroms.
const orderTrimThreshold = 40

// Rotation bestimmt, wer als naechstes den Hinweis gibt. Sie fuehrt einen Strom
// aus Spieler-IDs, an den bei Bedarf ein frisch gemischter Durchgang angehaengt
// wird. Dadurch kommt jeder Spieler pro Durchgang genau einmal dran, ohne dass
// die Reihenfolge vorhersehbar wird.
//
// Portiert aus shuffle/refillOrder/ensureOrder/assignRoles der Offline-Fassung;
// dort waren die Einheiten Teams, hier sind es einzelne Spieler.
type Rotation struct {
	order []string
	index int
	rng   *rand.Rand
}

// NewRotation erzeugt eine leere Rotation.
func NewRotation(rng *rand.Rand) *Rotation {
	return &Rotation{rng: rng}
}

func (r *Rotation) shuffled(ids []string) []string {
	out := append([]string(nil), ids...)
	r.rng.Shuffle(len(out), func(i, j int) { out[i], out[j] = out[j], out[i] })
	return out
}

// refill haengt einen gemischten Durchgang an, in dem jede ID genau einmal vorkommt.
func (r *Rotation) refill(ids []string) {
	if len(ids) == 0 {
		return
	}
	chunk := r.shuffled(ids)
	// Verhindert, dass jemand am Durchgangswechsel zweimal hintereinander drankommt.
	if len(chunk) > 1 && len(r.order) > 0 && chunk[0] == r.order[len(r.order)-1] {
		j := 1 + r.rng.Intn(len(chunk)-1)
		chunk[0], chunk[j] = chunk[j], chunk[0]
	}
	r.order = append(r.order, chunk...)
}

// ensure sorgt fuer mindestens zwei anstehende Eintraege, damit neben dem
// aktuellen Hinweisgeber auch der naechste angezeigt werden kann.
func (r *Rotation) ensure(ids []string) {
	for guard := 0; len(r.order)-r.index < 2 && guard < 10; guard++ {
		r.refill(ids)
	}
	if r.index > orderTrimThreshold {
		r.order = append([]string(nil), r.order[r.index:]...)
		r.index = 0
	}
}

// Next rueckt die Rotation vor und liefert den naechsten Hinweisgeber.
// Ein leeres Ergebnis bedeutet: keine Spieler vorhanden.
func (r *Rotation) Next(ids []string) string {
	if len(ids) == 0 {
		return ""
	}
	if len(ids) == 1 {
		return ids[0]
	}
	r.ensure(ids)
	if r.index >= len(r.order) {
		return ids[0]
	}
	current := r.order[r.index]
	r.index++
	return current
}

// Upcoming meldet, wer nach dem aktuellen Hinweisgeber an der Reihe ist.
// Leer, wenn noch nichts ansteht.
func (r *Rotation) Upcoming() string {
	if r.index >= len(r.order) {
		return ""
	}
	return r.order[r.index]
}

// Remove nimmt eine ID aus dem Strom, ohne die laufende Reihenfolge neu zu wuerfeln.
func (r *Rotation) Remove(id string) {
	before := make([]string, 0, r.index)
	for _, x := range r.order[:min(r.index, len(r.order))] {
		if x != id {
			before = append(before, x)
		}
	}
	after := make([]string, 0, len(r.order))
	for _, x := range r.order[min(r.index, len(r.order)):] {
		if x != id {
			after = append(after, x)
		}
	}
	r.index = len(before)
	r.order = append(before, after...)
}

// Reset leert den Strom.
func (r *Rotation) Reset() {
	r.order = nil
	r.index = 0
}
