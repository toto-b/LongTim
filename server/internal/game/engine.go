package game

import (
	"errors"
	"fmt"
	"math"
	"math/rand"
	"sort"
	"strings"
	"time"
)

// Phase ist der serverseitig autoritative Rundenzustand.
type Phase string

const (
	// PhaseLobby: noch keine Runde gestartet.
	PhaseLobby Phase = "LOBBY"
	// PhaseHint: Achsen und Ziel stehen, nur der Hinweisgeber sieht das Ziel.
	PhaseHint Phase = "HINT"
	// PhaseGuess: Hinweis ist raus, alle anderen setzen ihre Marker.
	PhaseGuess Phase = "GUESS"
	// PhaseReveal: Ziel und alle Tipps sind offen, Punkte sind verbucht.
	PhaseReveal Phase = "REVEAL"
)

// MinPlayers ist die kleinste sinnvolle Runde: ein Hinweisgeber und ein Ratender.
const MinPlayers = 2

// HistoryLimit begrenzt den mitgefuehrten Verlauf pro Lobby.
const HistoryLimit = 30

// MaxHintLen begrenzt die Hinweislaenge serverseitig.
const MaxHintLen = 120

// Fehler, die das Transportpaket dem Client als Meldung weiterreicht.
var (
	ErrWrongPhase     = errors.New("in dieser phase nicht moeglich")
	ErrNotClueGiver   = errors.New("nur der hinweisgeber darf das")
	ErrIsClueGiver    = errors.New("der hinweisgeber raet nicht mit")
	ErrUnknownPlayer  = errors.New("unbekannter spieler")
	ErrNotEnoughUsers = fmt.Errorf("mindestens %d spieler noetig", MinPlayers)
	ErrOutOfBounds    = errors.New("tipp liegt ausserhalb des feldes")
)

// Player ist ein Teilnehmer einer Lobby.
type Player struct {
	ID        string
	Name      string
	Score     int
	Connected bool
	seq       int // Beitrittsreihenfolge, haelt Listen stabil
}

// GuessResult ist ein ausgewerteter Tipp.
type GuessResult struct {
	PlayerID string  `json:"playerId"`
	Name     string  `json:"name"`
	Point    Point   `json:"point"`
	Distance float64 `json:"distance"`
	Points   int     `json:"points"`
}

// HistoryEntry protokolliert eine abgeschlossene Runde.
type HistoryEntry struct {
	Round           int           `json:"round"`
	AxisX           Pair          `json:"axisX"`
	AxisY           Pair          `json:"axisY"`
	Hint            string        `json:"hint"`
	ClueGiver       string        `json:"clueGiver"`
	Target          Point         `json:"target"`
	Results         []GuessResult `json:"results"`
	ClueGiverPoints int           `json:"clueGiverPoints"`
}

// RoundResult wird beim Aufdecken zurueckgegeben, damit der Aufrufer Metriken
// erfassen kann, ohne im Spielzustand zu wuehlen.
type RoundResult struct {
	Round           int
	Target          Point
	Results         []GuessResult
	ClueGiverID     string
	ClueGiverPoints int
	RoundDuration   time.Duration
}

// Game ist der Spielzustand einer Lobby. Nicht nebenlaeufigkeitssicher; die
// Serialisierung uebernimmt die Lobby.
type Game struct {
	phase       Phase
	round       int
	axisX       Pair
	axisY       Pair
	target      Point // klein geschrieben: kann nicht versehentlich mitserialisiert werden
	hint        string
	clueGiverID string

	players map[string]*Player
	guesses map[string]Point
	history []HistoryEntry

	deck     *Deck
	rotation *Rotation
	rng      *rand.Rand
	nextSeq  int

	roundStartedAt time.Time
}

// New erzeugt ein Spiel mit den uebergebenen Skalen.
func New(pairs []Pair, rng *rand.Rand) *Game {
	return &Game{
		phase:    PhaseLobby,
		players:  make(map[string]*Player),
		guesses:  make(map[string]Point),
		deck:     NewDeck(pairs, rng),
		rotation: NewRotation(rng),
		rng:      rng,
	}
}

// Phase liefert den aktuellen Rundenzustand.
func (g *Game) Phase() Phase { return g.phase }

// Round liefert die laufende Rundennummer.
func (g *Game) Round() int { return g.round }

// ClueGiverID liefert den aktuellen Hinweisgeber.
func (g *Game) ClueGiverID() string { return g.clueGiverID }

// PlayerCount meldet die Anzahl bekannter Spieler.
func (g *Game) PlayerCount() int { return len(g.players) }

// ConnectedCount meldet die Anzahl aktuell verbundener Spieler.
func (g *Game) ConnectedCount() int {
	n := 0
	for _, p := range g.players {
		if p.Connected {
			n++
		}
	}
	return n
}

// Player sucht einen Spieler.
func (g *Game) Player(id string) (*Player, bool) {
	p, ok := g.players[id]
	return p, ok
}

// Players liefert alle Spieler in Beitrittsreihenfolge.
func (g *Game) Players() []*Player {
	out := make([]*Player, 0, len(g.players))
	for _, p := range g.players {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].seq < out[j].seq })
	return out
}

// AddPlayer nimmt einen Spieler auf. Ein bereits bekannter Spieler wird nur
// wieder auf verbunden gesetzt (Reconnect nach Reload).
func (g *Game) AddPlayer(id, name string) *Player {
	if p, ok := g.players[id]; ok {
		p.Connected = true
		if n := sanitizeName(name); n != "" {
			p.Name = n
		}
		return p
	}
	p := &Player{ID: id, Name: sanitizeName(name), Connected: true, seq: g.nextSeq}
	if p.Name == "" {
		p.Name = fmt.Sprintf("Spieler %d", g.nextSeq+1)
	}
	g.nextSeq++
	g.players[id] = p
	return p
}

// Rename aendert den Anzeigenamen.
func (g *Game) Rename(id, name string) error {
	p, ok := g.players[id]
	if !ok {
		return ErrUnknownPlayer
	}
	if n := sanitizeName(name); n != "" {
		p.Name = n
	}
	return nil
}

// Disconnect markiert einen Spieler als getrennt. Er bleibt mit seinen Punkten
// in der Lobby, damit ein Reload nicht den Spielstand kostet.
func (g *Game) Disconnect(id string) {
	if p, ok := g.players[id]; ok {
		p.Connected = false
	}
	g.maybeAutoReveal()
}

// RemovePlayer entfernt einen Spieler samt Rotation und offenem Tipp.
func (g *Game) RemovePlayer(id string) {
	delete(g.players, id)
	delete(g.guesses, id)
	g.rotation.Remove(id)
	if g.clueGiverID == id && g.phase != PhaseReveal {
		// Ohne Hinweisgeber ist die Runde nicht mehr spielbar.
		g.phase = PhaseLobby
		g.clueGiverID = ""
		g.guesses = make(map[string]Point)
	}
	g.maybeAutoReveal()
}

// connectedIDs liefert die verbundenen Spieler in Beitrittsreihenfolge.
func (g *Game) connectedIDs() []string {
	ids := make([]string, 0, len(g.players))
	for _, p := range g.Players() {
		if p.Connected {
			ids = append(ids, p.ID)
		}
	}
	return ids
}

// StartRound beginnt eine neue Runde: Rollen verteilen, Achsen ziehen, Ziel wuerfeln.
func (g *Game) StartRound() error {
	if g.phase != PhaseLobby && g.phase != PhaseReveal {
		return ErrWrongPhase
	}
	ids := g.connectedIDs()
	if len(ids) < MinPlayers {
		return ErrNotEnoughUsers
	}

	// Achsen der abgeschlossenen Runde sperren, bevor neu gezogen wird.
	if g.round > 0 {
		g.deck.Remember(g.axisX, g.axisY)
	}

	g.round++
	g.clueGiverID = g.rotation.Next(ids)
	g.axisX, g.axisY = g.deck.Draw()
	// Rand von 10 % je Seite, damit das Ziel nicht in der Ecke klebt.
	g.target = Point{X: g.rng.Intn(80) + 10, Y: g.rng.Intn(80) + 10}
	g.hint = ""
	g.guesses = make(map[string]Point)
	g.phase = PhaseHint
	g.roundStartedAt = time.Now()
	return nil
}

// RedrawScales wuerfelt die Achsen neu, solange der Hinweis noch nicht raus ist.
// Das Ziel bleibt stehen — neu sind nur die Woerter.
func (g *Game) RedrawScales(actorID string) error {
	if g.phase != PhaseHint {
		return ErrWrongPhase
	}
	if actorID != g.clueGiverID {
		return ErrNotClueGiver
	}
	g.axisX, g.axisY = g.deck.Draw(g.axisX, g.axisY)
	return nil
}

// SubmitHint schliesst die Hinweisphase ab und gibt das Feld zum Raten frei.
// Die zurueckgegebene Dauer ist die Zeit vom Rundenstart bis zum Hinweis und
// wird vom Aufrufer als Metrik erfasst.
func (g *Game) SubmitHint(actorID, hint string) (time.Duration, error) {
	if g.phase != PhaseHint {
		return 0, ErrWrongPhase
	}
	if actorID != g.clueGiverID {
		return 0, ErrNotClueGiver
	}
	g.hint = truncate(strings.TrimSpace(hint), MaxHintLen)
	g.phase = PhaseGuess
	return time.Since(g.roundStartedAt), nil
}

// PlaceGuess setzt oder verschiebt den Marker eines Ratenden. Deckt automatisch
// auf, sobald alle Ratenden ihren Tipp abgegeben haben.
func (g *Game) PlaceGuess(actorID string, p Point) (*RoundResult, error) {
	if g.phase != PhaseGuess {
		return nil, ErrWrongPhase
	}
	if actorID == g.clueGiverID {
		return nil, ErrIsClueGiver
	}
	player, ok := g.players[actorID]
	if !ok || !player.Connected {
		return nil, ErrUnknownPlayer
	}
	if p.X < 0 || p.X > 100 || p.Y < 0 || p.Y > 100 {
		return nil, ErrOutOfBounds
	}
	g.guesses[actorID] = p

	if g.allGuessesIn() {
		return g.reveal(), nil
	}
	return nil, nil
}

// pendingGuessers meldet, wie viele Tipps noch fehlen.
func (g *Game) pendingGuessers() int {
	n := 0
	for _, id := range g.connectedIDs() {
		if id == g.clueGiverID {
			continue
		}
		if _, done := g.guesses[id]; !done {
			n++
		}
	}
	return n
}

func (g *Game) allGuessesIn() bool {
	return len(g.guesses) > 0 && g.pendingGuessers() == 0
}

// maybeAutoReveal deckt auf, wenn durch eine Trennung alle verbliebenen Tipps
// vollstaendig geworden sind. Sonst haengt die Runde an einem weggeklickten Browser.
func (g *Game) maybeAutoReveal() {
	if g.phase == PhaseGuess && g.allGuessesIn() {
		g.reveal()
	}
}

// Reveal deckt vorzeitig auf. Nur der Hinweisgeber darf das, und nur wenn
// mindestens ein Tipp vorliegt.
func (g *Game) Reveal(actorID string) (*RoundResult, error) {
	if g.phase != PhaseGuess {
		return nil, ErrWrongPhase
	}
	if actorID != g.clueGiverID {
		return nil, ErrNotClueGiver
	}
	if len(g.guesses) == 0 {
		return nil, errors.New("noch kein einziger tipp gesetzt")
	}
	return g.reveal(), nil
}

// reveal wertet aus, verbucht Punkte und schreibt den Verlauf fort.
func (g *Game) reveal() *RoundResult {
	results := make([]GuessResult, 0, len(g.guesses))
	total := 0
	for _, p := range g.Players() {
		point, ok := g.guesses[p.ID]
		if !ok {
			continue
		}
		d := Distance(g.target, point)
		pts := PointsFor(d)
		p.Score += pts
		total += pts
		results = append(results, GuessResult{
			PlayerID: p.ID,
			Name:     p.Name,
			Point:    point,
			Distance: d,
			Points:   pts,
		})
	}

	// Der Hinweisgeber bekommt den Schnitt seiner Gruppe: ein Hinweis ist gut,
	// wenn er moeglichst viele Leute nah ans Ziel bringt.
	clueGiverPoints := 0
	if len(results) > 0 {
		clueGiverPoints = int(math.Round(float64(total) / float64(len(results))))
	}
	clueGiverName := ""
	if cg, ok := g.players[g.clueGiverID]; ok {
		cg.Score += clueGiverPoints
		clueGiverName = cg.Name
	}

	g.phase = PhaseReveal
	entry := HistoryEntry{
		Round:           g.round,
		AxisX:           g.axisX,
		AxisY:           g.axisY,
		Hint:            g.hint,
		ClueGiver:       clueGiverName,
		Target:          g.target,
		Results:         results,
		ClueGiverPoints: clueGiverPoints,
	}
	g.history = append(g.history, entry)
	if len(g.history) > HistoryLimit {
		g.history = g.history[len(g.history)-HistoryLimit:]
	}

	return &RoundResult{
		Round:           g.round,
		Target:          g.target,
		Results:         results,
		ClueGiverID:     g.clueGiverID,
		ClueGiverPoints: clueGiverPoints,
		RoundDuration:   time.Since(g.roundStartedAt),
	}
}

// Reset setzt Punkte, Verlauf und Rotation zurueck. Die Spieler bleiben in der Lobby.
func (g *Game) Reset() {
	for _, p := range g.players {
		p.Score = 0
	}
	g.phase = PhaseLobby
	g.round = 0
	g.hint = ""
	g.clueGiverID = ""
	g.axisX = Pair{}
	g.axisY = Pair{}
	g.guesses = make(map[string]Point)
	g.history = nil
	g.rotation.Reset()
}

func sanitizeName(s string) string {
	s = strings.TrimSpace(s)
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
	return truncate(s, 24)
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
