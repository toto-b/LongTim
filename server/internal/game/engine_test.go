package game

import (
	"encoding/json"
	"math/rand"
	"strings"
	"testing"
)

func newTestGame(t *testing.T, names ...string) (*Game, []string) {
	t.Helper()
	g := New(testPairs(), rand.New(rand.NewSource(1234)))
	ids := make([]string, 0, len(names))
	for i, n := range names {
		id := string(rune('A' + i))
		g.AddPlayer(id, n)
		ids = append(ids, id)
	}
	return g, ids
}

func TestStartRoundNeedsTwoPlayers(t *testing.T) {
	g, _ := newTestGame(t, "Solo")
	if err := g.StartRound(); err == nil {
		t.Fatal("runde mit einem spieler wurde akzeptiert")
	}
	g.AddPlayer("B", "Zwei")
	if err := g.StartRound(); err != nil {
		t.Fatalf("runde mit zwei spielern abgelehnt: %v", err)
	}
	if g.Phase() != PhaseHint {
		t.Fatalf("phase = %q, erwartet %q", g.Phase(), PhaseHint)
	}
}

func TestTargetStaysInsideMargin(t *testing.T) {
	g, _ := newTestGame(t, "A", "B")
	for i := 0; i < 200; i++ {
		g.phase = PhaseLobby
		if err := g.StartRound(); err != nil {
			t.Fatal(err)
		}
		if g.target.X < 10 || g.target.X > 89 || g.target.Y < 10 || g.target.Y > 89 {
			t.Fatalf("ziel %v liegt ausserhalb des 10%%-randes", g.target)
		}
	}
}

func TestFullRoundFlow(t *testing.T) {
	g, ids := newTestGame(t, "Anna", "Ben", "Cem")
	if err := g.StartRound(); err != nil {
		t.Fatal(err)
	}
	cg := g.ClueGiverID()

	// Raten geht erst nach dem Hinweis.
	guesser := otherThan(ids, cg)[0]
	if _, err := g.PlaceGuess(guesser, Point{50, 50}); err != ErrWrongPhase {
		t.Fatalf("tipp in der hinweisphase lieferte %v, erwartet ErrWrongPhase", err)
	}

	// Nur der Hinweisgeber darf den Hinweis setzen.
	if _, err := g.SubmitHint(guesser, "geht nicht"); err != ErrNotClueGiver {
		t.Fatalf("fremder hinweis lieferte %v, erwartet ErrNotClueGiver", err)
	}
	if _, err := g.SubmitHint(cg, "  ein Hinweis  "); err != nil {
		t.Fatal(err)
	}
	if g.hint != "ein Hinweis" {
		t.Fatalf("hinweis wurde nicht getrimmt: %q", g.hint)
	}
	if g.Phase() != PhaseGuess {
		t.Fatalf("phase = %q, erwartet %q", g.Phase(), PhaseGuess)
	}

	// Der Hinweisgeber raet nicht mit.
	if _, err := g.PlaceGuess(cg, Point{10, 10}); err != ErrIsClueGiver {
		t.Fatalf("hinweisgeber durfte raten: %v", err)
	}

	others := otherThan(ids, cg)
	res, err := g.PlaceGuess(others[0], Point{50, 50})
	if err != nil {
		t.Fatal(err)
	}
	if res != nil {
		t.Fatal("zu frueh aufgedeckt, es fehlte noch ein tipp")
	}
	res, err = g.PlaceGuess(others[1], Point{55, 45})
	if err != nil {
		t.Fatal(err)
	}
	if res == nil {
		t.Fatal("nicht automatisch aufgedeckt, obwohl alle tipps drin waren")
	}
	if g.Phase() != PhaseReveal {
		t.Fatalf("phase = %q, erwartet %q", g.Phase(), PhaseReveal)
	}
	if len(res.Results) != 2 {
		t.Fatalf("%d ergebnisse, erwartet 2", len(res.Results))
	}
	if len(g.history) != 1 {
		t.Fatalf("%d verlaufseintraege, erwartet 1", len(g.history))
	}
}

func TestScoringMatchesDistance(t *testing.T) {
	g, ids := newTestGame(t, "A", "B")
	if err := g.StartRound(); err != nil {
		t.Fatal(err)
	}
	cg := g.ClueGiverID()
	guesser := otherThan(ids, cg)[0]
	if _, err := g.SubmitHint(cg, "h"); err != nil {
		t.Fatal(err)
	}

	// Punktgenauer Treffer: 4 Punkte fuer den Ratenden, Schnitt 4 fuer den Hinweisgeber.
	res, err := g.PlaceGuess(guesser, g.target)
	if err != nil {
		t.Fatal(err)
	}
	if res == nil {
		t.Fatal("nicht aufgedeckt")
	}
	if res.Results[0].Points != 4 {
		t.Fatalf("volltreffer gab %d punkte, erwartet 4", res.Results[0].Points)
	}
	if res.ClueGiverPoints != 4 {
		t.Fatalf("hinweisgeber bekam %d punkte, erwartet 4", res.ClueGiverPoints)
	}
	if p, _ := g.Player(guesser); p.Score != 4 {
		t.Fatalf("punktestand des ratenden = %d, erwartet 4", p.Score)
	}
	if p, _ := g.Player(cg); p.Score != 4 {
		t.Fatalf("punktestand des hinweisgebers = %d, erwartet 4", p.Score)
	}
}

func TestClueGiverGetsAverageOfGroup(t *testing.T) {
	g, ids := newTestGame(t, "A", "B", "C")
	if err := g.StartRound(); err != nil {
		t.Fatal(err)
	}
	cg := g.ClueGiverID()
	others := otherThan(ids, cg)
	if _, err := g.SubmitHint(cg, "h"); err != nil {
		t.Fatal(err)
	}

	// Ein Volltreffer (4) und ein weit danebenliegender Tipp (0) → Schnitt 2.
	far := Point{X: (g.target.X + 60) % 100, Y: (g.target.Y + 60) % 100}
	if _, err := g.PlaceGuess(others[0], g.target); err != nil {
		t.Fatal(err)
	}
	res, err := g.PlaceGuess(others[1], far)
	if err != nil {
		t.Fatal(err)
	}
	if res == nil {
		t.Fatal("nicht aufgedeckt")
	}
	sum := 0
	for _, r := range res.Results {
		sum += r.Points
	}
	want := (sum + len(res.Results)/2) / len(res.Results) // kaufmaennisch gerundet
	if res.ClueGiverPoints != want {
		t.Fatalf("hinweisgeber bekam %d, erwartet den schnitt %d (einzelpunkte %d/%d)",
			res.ClueGiverPoints, want, sum, len(res.Results))
	}
}

func TestPlaceGuessRejectsOutOfBounds(t *testing.T) {
	g, ids := newTestGame(t, "A", "B")
	if err := g.StartRound(); err != nil {
		t.Fatal(err)
	}
	cg := g.ClueGiverID()
	if _, err := g.SubmitHint(cg, "h"); err != nil {
		t.Fatal(err)
	}
	guesser := otherThan(ids, cg)[0]
	for _, bad := range []Point{{-1, 50}, {50, -1}, {101, 50}, {50, 101}} {
		if _, err := g.PlaceGuess(guesser, bad); err != ErrOutOfBounds {
			t.Errorf("tipp %v lieferte %v, erwartet ErrOutOfBounds", bad, err)
		}
	}
}

func TestRevealNeedsAtLeastOneGuess(t *testing.T) {
	g, _ := newTestGame(t, "A", "B")
	if err := g.StartRound(); err != nil {
		t.Fatal(err)
	}
	cg := g.ClueGiverID()
	if _, err := g.SubmitHint(cg, "h"); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Reveal(cg); err == nil {
		t.Fatal("aufdecken ohne einen einzigen tipp wurde akzeptiert")
	}
}

func TestDisconnectUnblocksStuckRound(t *testing.T) {
	g, ids := newTestGame(t, "A", "B", "C")
	if err := g.StartRound(); err != nil {
		t.Fatal(err)
	}
	cg := g.ClueGiverID()
	others := otherThan(ids, cg)
	if _, err := g.SubmitHint(cg, "h"); err != nil {
		t.Fatal(err)
	}
	if _, err := g.PlaceGuess(others[0], Point{50, 50}); err != nil {
		t.Fatal(err)
	}
	if g.Phase() != PhaseGuess {
		t.Fatal("zu frueh aufgedeckt")
	}
	// Der zweite Ratende schliesst den Browser: die Runde darf nicht haengen bleiben.
	g.Disconnect(others[1])
	if g.Phase() != PhaseReveal {
		t.Fatalf("phase = %q, erwartet %q nach trennung des letzten offenen ratenden", g.Phase(), PhaseReveal)
	}
}

func TestRemovingClueGiverAbortsRound(t *testing.T) {
	g, _ := newTestGame(t, "A", "B", "C")
	if err := g.StartRound(); err != nil {
		t.Fatal(err)
	}
	g.RemovePlayer(g.ClueGiverID())
	if g.Phase() != PhaseLobby {
		t.Fatalf("phase = %q, erwartet %q nachdem der hinweisgeber die lobby verliess", g.Phase(), PhaseLobby)
	}
}

func TestReconnectKeepsScore(t *testing.T) {
	g, _ := newTestGame(t, "Anna", "Ben")
	p, _ := g.Player("A")
	p.Score = 7
	g.Disconnect("A")
	again := g.AddPlayer("A", "Anna")
	if !again.Connected {
		t.Fatal("reconnect setzte connected nicht zurueck")
	}
	if again.Score != 7 {
		t.Fatalf("punktestand nach reconnect = %d, erwartet 7", again.Score)
	}
	if g.PlayerCount() != 2 {
		t.Fatalf("%d spieler, erwartet 2 — reconnect legte einen doppelten an", g.PlayerCount())
	}
}

func TestHintIsTruncated(t *testing.T) {
	g, _ := newTestGame(t, "A", "B")
	if err := g.StartRound(); err != nil {
		t.Fatal(err)
	}
	long := strings.Repeat("ü", MaxHintLen+50)
	if _, err := g.SubmitHint(g.ClueGiverID(), long); err != nil {
		t.Fatal(err)
	}
	if n := len([]rune(g.hint)); n != MaxHintLen {
		t.Fatalf("hinweis hat %d zeichen, erwartet %d", n, MaxHintLen)
	}
}

func TestResetClearsScoresAndHistory(t *testing.T) {
	g, ids := newTestGame(t, "A", "B")
	if err := g.StartRound(); err != nil {
		t.Fatal(err)
	}
	cg := g.ClueGiverID()
	if _, err := g.SubmitHint(cg, "h"); err != nil {
		t.Fatal(err)
	}
	if _, err := g.PlaceGuess(otherThan(ids, cg)[0], g.target); err != nil {
		t.Fatal(err)
	}

	g.Reset()
	if g.Round() != 0 || g.Phase() != PhaseLobby || len(g.history) != 0 {
		t.Fatalf("Reset unvollstaendig: runde=%d phase=%q verlauf=%d", g.Round(), g.Phase(), len(g.history))
	}
	for _, p := range g.Players() {
		if p.Score != 0 {
			t.Fatalf("%s hat nach Reset noch %d punkte", p.Name, p.Score)
		}
	}
}

func TestHistoryIsCapped(t *testing.T) {
	g, ids := newTestGame(t, "A", "B")
	for i := 0; i < HistoryLimit+10; i++ {
		if err := g.StartRound(); err != nil {
			t.Fatal(err)
		}
		cg := g.ClueGiverID()
		if _, err := g.SubmitHint(cg, "h"); err != nil {
			t.Fatal(err)
		}
		if _, err := g.PlaceGuess(otherThan(ids, cg)[0], Point{50, 50}); err != nil {
			t.Fatal(err)
		}
	}
	if len(g.history) != HistoryLimit {
		t.Fatalf("verlauf hat %d eintraege, erwartet den deckel %d", len(g.history), HistoryLimit)
	}
}

func TestRedrawScalesOnlyForClueGiverAndKeepsTarget(t *testing.T) {
	g, ids := newTestGame(t, "A", "B")
	if err := g.StartRound(); err != nil {
		t.Fatal(err)
	}
	cg := g.ClueGiverID()
	before := g.target

	if err := g.RedrawScales(otherThan(ids, cg)[0]); err != ErrNotClueGiver {
		t.Fatalf("fremdes neuwuerfeln lieferte %v, erwartet ErrNotClueGiver", err)
	}
	if err := g.RedrawScales(cg); err != nil {
		t.Fatal(err)
	}
	if g.target != before {
		t.Fatal("neuwuerfeln der achsen hat auch das ziel verschoben")
	}

	// Nach dem Hinweis ist Neuwuerfeln zu.
	if _, err := g.SubmitHint(cg, "h"); err != nil {
		t.Fatal(err)
	}
	if err := g.RedrawScales(cg); err != ErrWrongPhase {
		t.Fatalf("neuwuerfeln in der ratephase lieferte %v, erwartet ErrWrongPhase", err)
	}
}

func otherThan(ids []string, exclude string) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if id != exclude {
			out = append(out, id)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Redaction — die zentrale Zusicherung des Servers.
// ---------------------------------------------------------------------------

// mustJSON serialisiert einen Snapshot so, wie er auch ueber die Leitung geht.
func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

// hasTopLevelKey prueft gezielt einen Schluessel auf oberster Ebene des
// Snapshots. Ein blosses strings.Contains waere hier irrefuehrend: die
// history-Eintraege fuehren ebenfalls ein target-Feld, das aber zu bereits
// aufgedeckten Runden gehoert und deshalb oeffentlich ist.
func hasTopLevelKey(t *testing.T, snap Snapshot, key string) bool {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(mustJSON(t, snap)), &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	_, ok := m[key]
	return ok
}

func TestTargetNeverLeaksToGuessers(t *testing.T) {
	g, ids := newTestGame(t, "Anna", "Ben", "Cem")
	if err := g.StartRound(); err != nil {
		t.Fatal(err)
	}
	cg := g.ClueGiverID()
	guessers := otherThan(ids, cg)

	assertNoTarget := func(phase string, viewer string) {
		t.Helper()
		snap := g.SnapshotFor(viewer, "TEST")
		if snap.Target != nil {
			t.Fatalf("%s: ziel wurde an den ratenden %q ausgeliefert", phase, viewer)
		}
		if hasTopLevelKey(t, snap, "target") {
			t.Fatalf("%s: das JSON fuer %q enthaelt ein target-feld:\n%s",
				phase, viewer, mustJSON(t, snap))
		}
	}

	// Hinweisphase
	for _, v := range guessers {
		assertNoTarget("HINT", v)
	}
	if snap := g.SnapshotFor(cg, "TEST"); snap.Target == nil {
		t.Fatal("HINT: der hinweisgeber bekommt das ziel nicht")
	} else if *snap.Target != g.target {
		t.Fatal("HINT: der hinweisgeber bekommt ein falsches ziel")
	}

	// Ratephase
	if _, err := g.SubmitHint(cg, "hinweis"); err != nil {
		t.Fatal(err)
	}
	for _, v := range guessers {
		assertNoTarget("GUESS", v)
	}

	// Nicht angemeldete Zuschauer duerfen es ebenfalls nicht sehen.
	assertNoTarget("GUESS", "")
	assertNoTarget("GUESS", "unbekannte-id")

	// Nach dem Aufdecken sehen es alle.
	if _, err := g.PlaceGuess(guessers[0], Point{50, 50}); err != nil {
		t.Fatal(err)
	}
	if _, err := g.PlaceGuess(guessers[1], Point{40, 60}); err != nil {
		t.Fatal(err)
	}
	for _, v := range append(guessers, cg) {
		snap := g.SnapshotFor(v, "TEST")
		if snap.Target == nil {
			t.Fatalf("REVEAL: %q bekommt das ziel nicht", v)
		}
	}
}

func TestForeignGuessesStayHiddenUntilReveal(t *testing.T) {
	g, ids := newTestGame(t, "Anna", "Ben", "Cem")
	if err := g.StartRound(); err != nil {
		t.Fatal(err)
	}
	cg := g.ClueGiverID()
	guessers := otherThan(ids, cg)
	if _, err := g.SubmitHint(cg, "hinweis"); err != nil {
		t.Fatal(err)
	}

	secret := Point{X: 17, Y: 83}
	if _, err := g.PlaceGuess(guessers[0], secret); err != nil {
		t.Fatal(err)
	}

	// Der zweite Ratende darf die Koordinate des ersten nirgends im JSON finden.
	raw := mustJSON(t, g.SnapshotFor(guessers[1], "TEST"))
	if strings.Contains(raw, `"yourGuess"`) {
		t.Fatalf("wer noch nicht geraten hat, bekommt ein yourGuess-feld:\n%s", raw)
	}
	if strings.Contains(raw, `"results"`) {
		t.Fatalf("ergebnisse tauchen vor dem aufdecken auf:\n%s", raw)
	}
	if strings.Contains(raw, `"x":17`) {
		t.Fatalf("fremder tipp %v ist im snapshot sichtbar:\n%s", secret, raw)
	}

	// Der Hinweisgeber sieht nur, DASS getippt wurde, nicht wohin.
	rawCG := mustJSON(t, g.SnapshotFor(cg, "TEST"))
	if strings.Contains(rawCG, `"x":17`) {
		t.Fatalf("der hinweisgeber sieht den tipp des ratenden:\n%s", rawCG)
	}
	snapCG := g.SnapshotFor(cg, "TEST")
	if snapCG.Pending != 1 {
		t.Fatalf("Pending = %d, erwartet 1 offenen tipp", snapCG.Pending)
	}
	var flagged int
	for _, p := range snapCG.Players {
		if p.HasGuessed {
			flagged++
		}
	}
	if flagged != 1 {
		t.Fatalf("%d spieler als 'hat getippt' markiert, erwartet 1", flagged)
	}

	// Der Ratende selbst sieht seinen eigenen Marker.
	own := g.SnapshotFor(guessers[0], "TEST")
	if own.YourGuess == nil || *own.YourGuess != secret {
		t.Fatalf("eigener tipp fehlt oder ist falsch: %+v", own.YourGuess)
	}
}

func TestLobbyPhaseLeaksNothing(t *testing.T) {
	g, _ := newTestGame(t, "Anna", "Ben")
	snap := g.SnapshotFor("A", "TEST")
	for _, forbidden := range []string{"target", "axisX", "axisY", "results"} {
		if hasTopLevelKey(t, snap, forbidden) {
			t.Fatalf("in der lobby-phase taucht %q im snapshot auf:\n%s",
				forbidden, mustJSON(t, snap))
		}
	}
}

// Der Verlauf enthaelt die Ziele abgeschlossener Runden — das ist gewollt, die
// sind aufgedeckt. Die LAUFENDE Runde darf dort aber nicht auftauchen, und das
// Feld auf oberster Ebene muss weiter fehlen. Genau diese Unterscheidung ist beim
// Ende-zu-Ende-Lauf zuerst als falscher Alarm aufgefallen.
func TestHistoryDoesNotLeakTheRunningRound(t *testing.T) {
	g, ids := newTestGame(t, "Anna", "Ben", "Cem")

	// Runde 1 komplett spielen, damit ein Verlaufseintrag existiert.
	if err := g.StartRound(); err != nil {
		t.Fatal(err)
	}
	cg := g.ClueGiverID()
	if _, err := g.SubmitHint(cg, "erste runde"); err != nil {
		t.Fatal(err)
	}
	for _, id := range otherThan(ids, cg) {
		if _, err := g.PlaceGuess(id, Point{50, 50}); err != nil {
			t.Fatal(err)
		}
	}

	// Runde 2 starten.
	if err := g.StartRound(); err != nil {
		t.Fatal(err)
	}
	cg2 := g.ClueGiverID()

	for _, viewer := range otherThan(ids, cg2) {
		snap := g.SnapshotFor(viewer, "TEST")
		if hasTopLevelKey(t, snap, "target") {
			t.Fatalf("ratender %q bekam ein target auf oberster ebene", viewer)
		}
		if len(snap.History) != 1 {
			t.Fatalf("%d verlaufseintraege, erwartet 1", len(snap.History))
		}
		for _, h := range snap.History {
			if h.Round >= snap.Round {
				t.Fatalf("der verlauf enthaelt runde %d, die laufende runde ist %d — "+
					"das ziel der aktuellen runde ist ueber den verlauf sichtbar", h.Round, snap.Round)
			}
			if h.Target == (Point{}) {
				t.Fatal("verlaufseintrag ohne ziel: aufgedeckte runden sollen es zeigen")
			}
		}
	}
}
