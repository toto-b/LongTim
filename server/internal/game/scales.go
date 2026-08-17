package game

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"sort"
	"strings"
)

// RecentMemory ist die Anzahl zuletzt gespielter Skalen, die gesperrt bleiben
// (zwei pro Runde). Uebernommen aus der Offline-Fassung.
const RecentMemory = 5

// Pair ist ein Gegensatzpaar. Index 0 liegt links bzw. oben, Index 1 rechts bzw. unten.
type Pair [2]string

// ScaleFile ist das Dateiformat von scales.json.
type ScaleFile struct {
	Pairs []Pair `json:"pairs"`
}

// LoadScales liest Skalenpaare aus einer JSON-Datei.
func LoadScales(path string) ([]Pair, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("skalen lesen: %w", err)
	}
	return ParseScales(raw)
}

// ParseScales dekodiert und validiert Skalenpaare aus JSON.
func ParseScales(raw []byte) ([]Pair, error) {
	var f ScaleFile
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, fmt.Errorf("skalen dekodieren: %w", err)
	}
	if err := ValidateScales(f.Pairs); err != nil {
		return nil, err
	}
	return f.Pairs, nil
}

// ValidateScales prueft, dass die Skalenliste spielbar ist. Wird auch vom
// Admin-Flag --validate-scales genutzt.
func ValidateScales(pairs []Pair) error {
	if len(pairs) < 2 {
		return errors.New("mindestens zwei skalenpaare noetig")
	}
	seen := make(map[string]struct{}, len(pairs))
	for i, p := range pairs {
		for j, w := range p {
			if strings.TrimSpace(w) == "" {
				return fmt.Errorf("skalenpaar %d: wort %d ist leer", i, j)
			}
		}
		if p[0] == p[1] {
			return fmt.Errorf("skalenpaar %d: beide seiten identisch (%q)", i, p[0])
		}
		k := PairKey(p)
		if _, dup := seen[k]; dup {
			return fmt.Errorf("skalenpaar %d ist ein duplikat: %q / %q", i, p[0], p[1])
		}
		seen[k] = struct{}{}
	}
	return nil
}

// PairKey identifiziert ein Paar unabhaengig von seiner Ausrichtung.
func PairKey(p Pair) string {
	a, b := p[0], p[1]
	if a > b {
		a, b = b, a
	}
	return a + "|" + b
}

// sharesWord meldet, ob zwei Paare ein Wort gemeinsam haben. Verhindert Achsen
// wie "Kalt/Heiss" gegen "Heiss/Mild", die sich gegenseitig entwerten.
func sharesWord(a, b Pair) bool {
	for _, wa := range a {
		for _, wb := range b {
			if wa == wb {
				return true
			}
		}
	}
	return false
}

// Deck zieht Achsenpaare und merkt sich die zuletzt gespielten.
type Deck struct {
	pairs  []Pair
	recent []string
	rng    *rand.Rand
}

// NewDeck erzeugt ein Deck. Die uebergebenen Paare werden nicht kopiert.
func NewDeck(pairs []Pair, rng *rand.Rand) *Deck {
	return &Deck{pairs: pairs, rng: rng}
}

// orient wuerfelt die Ausrichtung eines Paars aus, damit "links" nicht immer
// dasselbe Wort ist.
func (d *Deck) orient(p Pair) Pair {
	if d.rng.Intn(2) == 0 {
		return Pair{p[1], p[0]}
	}
	return p
}

// Draw zieht zwei Paare fuer die X- und die Y-Achse. Zusaetzlich zu den zuletzt
// gespielten Paaren koennen weitere gesperrt werden (etwa beim Neuwuerfeln).
func (d *Deck) Draw(alsoBlocked ...Pair) (Pair, Pair) {
	blocked := make(map[string]struct{}, len(d.recent)+len(alsoBlocked))
	for _, k := range d.recent {
		blocked[k] = struct{}{}
	}
	for _, p := range alsoBlocked {
		blocked[PairKey(p)] = struct{}{}
	}

	pool := make([]Pair, 0, len(d.pairs))
	for _, p := range d.pairs {
		if _, bad := blocked[PairKey(p)]; !bad {
			pool = append(pool, p)
		}
	}
	// Notbremse bei sehr kurzer Skalenliste: lieber wiederholen als haengen.
	if len(pool) < 2 {
		pool = d.pairs
	}

	first := pool[d.rng.Intn(len(pool))]

	rest := make([]Pair, 0, len(pool))
	for _, p := range pool {
		if PairKey(p) != PairKey(first) && !sharesWord(p, first) {
			rest = append(rest, p)
		}
	}
	if len(rest) == 0 {
		for _, p := range pool {
			if PairKey(p) != PairKey(first) {
				rest = append(rest, p)
			}
		}
	}
	if len(rest) == 0 {
		rest = pool
	}
	second := rest[d.rng.Intn(len(rest))]

	return d.orient(first), d.orient(second)
}

// Remember schiebt gespielte Paare in die Sperrliste und kuerzt sie auf RecentMemory.
func (d *Deck) Remember(pairs ...Pair) {
	for _, p := range pairs {
		d.recent = append(d.recent, PairKey(p))
	}
	if len(d.recent) > RecentMemory {
		d.recent = d.recent[len(d.recent)-RecentMemory:]
	}
}

// Recent gibt die aktuelle Sperrliste zurueck (sortierte Kopie, fuer Tests).
func (d *Deck) Recent() []string {
	out := append([]string(nil), d.recent...)
	sort.Strings(out)
	return out
}

// Size meldet die Anzahl verfuegbarer Paare.
func (d *Deck) Size() int { return len(d.pairs) }
