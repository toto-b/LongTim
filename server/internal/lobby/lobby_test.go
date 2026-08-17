package lobby

import (
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/toto-b/longlongwave/server/internal/game"
	"github.com/toto-b/longlongwave/server/internal/protocol"
)

// fakeClient sammelt die Nachrichten, die eine Verbindung erhalten haette.
type fakeClient struct {
	id string
	mu sync.Mutex
	in []protocol.ServerMessage
}

func (f *fakeClient) PlayerID() string { return f.id }

func (f *fakeClient) Send(m protocol.ServerMessage) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.in = append(f.in, m)
}

// last liefert die zuletzt empfangene Zustandsnachricht.
func (f *fakeClient) lastState(t *testing.T) game.Snapshot {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.in) - 1; i >= 0; i-- {
		if f.in[i].Type == protocol.EvtState && f.in[i].State != nil {
			return *f.in[i].State
		}
	}
	t.Fatalf("client %q hat keine zustandsnachricht erhalten", f.id)
	return game.Snapshot{}
}

// lastStateJSON liefert das rohe JSON des letzten Zustands — so, wie es ueber
// die Leitung gegangen waere.
func (f *fakeClient) lastStateJSON(t *testing.T) string {
	t.Helper()
	b, err := json.Marshal(f.lastState(t))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func (f *fakeClient) lastError(t *testing.T) string {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.in) - 1; i >= 0; i-- {
		if f.in[i].Type == protocol.EvtError {
			return f.in[i].Error
		}
	}
	return ""
}

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}

func testManager(t *testing.T, maxPlayers int) *Manager {
	t.Helper()
	pairs, err := game.ParseScales([]byte(`{"pairs":[
		["Kalt","Heiss"],["Leise","Laut"],["Rund","Eckig"],["Dunkel","Hell"],
		["Lokal","Global"],["Einfach","Kompliziert"],["Sanft","Hart"],["Nass","Trocken"]
	]}`))
	if err != nil {
		t.Fatal(err)
	}
	return NewManager(Options{
		Pairs:      pairs,
		MaxPlayers: maxPlayers,
		MaxLobbies: 10,
		Logger:     quietLogger(),
	})
}

// joinAll legt eine Lobby an und meldet die genannten Spieler an.
func joinAll(t *testing.T, m *Manager, names ...string) (*Lobby, []*fakeClient) {
	t.Helper()
	l, err := m.Create()
	if err != nil {
		t.Fatal(err)
	}
	clients := make([]*fakeClient, 0, len(names))
	for i, n := range names {
		c := &fakeClient{id: strings.Repeat(string(rune('a'+i)), 32)}
		if err := l.Join(c, n); err != nil {
			t.Fatal(err)
		}
		clients = append(clients, c)
	}
	return l, clients
}

func TestCreateProducesUsableCode(t *testing.T) {
	m := testManager(t, 8)
	l, err := m.Create()
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Code) != CodeLength {
		t.Fatalf("code %q hat laenge %d, erwartet %d", l.Code, len(l.Code), CodeLength)
	}
	for _, r := range l.Code {
		if !strings.ContainsRune(codeAlphabet, r) {
			t.Fatalf("code %q enthaelt das verwechselbare zeichen %q", l.Code, r)
		}
	}
	if got, err := m.Get(strings.ToLower(l.Code)); err != nil || got != l {
		t.Fatalf("kleingeschriebener code wurde nicht gefunden: %v", err)
	}
	if _, err := m.Get("ZZZZ"); err == nil {
		t.Fatal("unbekannter code wurde gefunden")
	}
}

func TestMaxLobbiesIsEnforced(t *testing.T) {
	m := testManager(t, 8)
	m.opts.MaxLobbies = 2
	for i := 0; i < 2; i++ {
		if _, err := m.Create(); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := m.Create(); err == nil {
		t.Fatal("dritte lobby wurde trotz MaxLobbies=2 angelegt")
	}
}

func TestJoinRespectsMaxPlayersButLetsKnownPlayersBack(t *testing.T) {
	m := testManager(t, 2)
	l, clients := joinAll(t, m, "Anna", "Ben")

	third := &fakeClient{id: strings.Repeat("z", 32)}
	if err := l.Join(third, "Cem"); err == nil {
		t.Fatal("dritter spieler kam in eine lobby fuer zwei")
	}

	// Reload eines bekannten Spielers muss auch bei voller Lobby durchgehen.
	l.Leave(clients[0])
	again := &fakeClient{id: clients[0].id}
	if err := l.Join(again, "Anna"); err != nil {
		t.Fatalf("bekannter spieler kam nach reload nicht zurueck: %v", err)
	}
}

// Das Gegenstueck zum Redaction-Test der Engine, eine Ebene hoeher: hier wird
// geprueft, dass der BROADCAST jedem Client wirklich seinen eigenen, redigierten
// Zustand schickt — nicht nur, dass die Engine ihn korrekt bauen koennte.
func TestBroadcastRedactsPerRecipient(t *testing.T) {
	m := testManager(t, 8)
	l, clients := joinAll(t, m, "Anna", "Ben", "Cem")

	if err := l.Handle(clients[0], protocol.ClientMessage{Type: protocol.CmdStartRound}); err != nil {
		t.Fatal(err)
	}

	// Genau ein Client darf das Ziel im Zustand haben: der Hinweisgeber.
	withTarget := 0
	var clueGiver *fakeClient
	for _, c := range clients {
		snap := c.lastState(t)
		if snap.Target != nil {
			withTarget++
			clueGiver = c
		}
	}
	if withTarget != 1 {
		t.Fatalf("%d von %d clients haben das ziel erhalten, erwartet genau 1", withTarget, len(clients))
	}
	if clueGiver.lastState(t).ClueGiverID != clueGiver.id {
		t.Fatal("das ziel ging an einen client, der nicht der hinweisgeber ist")
	}

	// Im rohen JSON der Ratenden darf kein target-Feld auftauchen.
	for _, c := range clients {
		if c == clueGiver {
			continue
		}
		if raw := c.lastStateJSON(t); strings.Contains(raw, `"target"`) {
			t.Fatalf("der ratende %q bekam ein target-feld:\n%s", c.id, raw)
		}
	}
}

func TestFullRoundOverProtocol(t *testing.T) {
	m := testManager(t, 8)
	l, clients := joinAll(t, m, "Anna", "Ben", "Cem")

	if err := l.Handle(clients[0], protocol.ClientMessage{Type: protocol.CmdStartRound}); err != nil {
		t.Fatal(err)
	}

	var cg *fakeClient
	var guessers []*fakeClient
	for _, c := range clients {
		if c.lastState(t).YouAreClueGiver {
			cg = c
		} else {
			guessers = append(guessers, c)
		}
	}
	if cg == nil {
		t.Fatal("kein hinweisgeber im zustand markiert")
	}
	target := *cg.lastState(t).Target

	// Ein Ratender darf keinen Hinweis setzen.
	if err := l.Handle(guessers[0], protocol.ClientMessage{Type: protocol.CmdSubmitHint, Hint: "nope"}); err == nil {
		t.Fatal("ein ratender durfte den hinweis setzen")
	}

	if err := l.Handle(cg, protocol.ClientMessage{Type: protocol.CmdSubmitHint, Hint: "Kaffee"}); err != nil {
		t.Fatal(err)
	}
	if got := guessers[0].lastState(t).Hint; got != "Kaffee" {
		t.Fatalf("hinweis beim ratenden = %q, erwartet %q", got, "Kaffee")
	}

	// Erster Tipp: Volltreffer.
	if err := l.Handle(guessers[0], protocol.ClientMessage{Type: protocol.CmdPlaceGuess, Point: &target}); err != nil {
		t.Fatal(err)
	}
	if l.Snapshot(cg.id).Phase != game.PhaseGuess {
		t.Fatal("zu frueh aufgedeckt")
	}
	// Zweiter Tipp schliesst die Runde.
	far := game.Point{X: (target.X + 60) % 100, Y: (target.Y + 60) % 100}
	if err := l.Handle(guessers[1], protocol.ClientMessage{Type: protocol.CmdPlaceGuess, Point: &far}); err != nil {
		t.Fatal(err)
	}

	final := guessers[0].lastState(t)
	if final.Phase != game.PhaseReveal {
		t.Fatalf("phase = %q, erwartet %q", final.Phase, game.PhaseReveal)
	}
	if final.Target == nil {
		t.Fatal("nach dem aufdecken fehlt dem ratenden das ziel")
	}
	if len(final.Results) != 2 {
		t.Fatalf("%d ergebnisse, erwartet 2", len(final.Results))
	}
	if final.Results[0].Points != 4 && final.Results[1].Points != 4 {
		t.Fatal("der volltreffer wurde nicht mit 4 punkten bewertet")
	}
}

func TestPlaceGuessWithoutPointIsRejected(t *testing.T) {
	m := testManager(t, 8)
	l, clients := joinAll(t, m, "Anna", "Ben")
	if err := l.Handle(clients[0], protocol.ClientMessage{Type: protocol.CmdStartRound}); err != nil {
		t.Fatal(err)
	}
	if err := l.Handle(clients[0], protocol.ClientMessage{Type: protocol.CmdPlaceGuess}); err == nil {
		t.Fatal("tipp ohne koordinate wurde akzeptiert")
	}
}

func TestUnknownCommandIsRejected(t *testing.T) {
	m := testManager(t, 8)
	l, clients := joinAll(t, m, "Anna", "Ben")
	err := l.Handle(clients[0], protocol.ClientMessage{Type: "rm -rf"})
	if err == nil {
		t.Fatal("unbekanntes kommando wurde akzeptiert")
	}
	if !strings.Contains(err.Error(), "unbekanntes kommando") {
		t.Fatalf("unerwarteter fehler: %v", err)
	}
}

func TestPingAnswersOnlyTheSender(t *testing.T) {
	m := testManager(t, 8)
	l, clients := joinAll(t, m, "Anna", "Ben")
	before := len(clients[1].in)

	if err := l.Handle(clients[0], protocol.ClientMessage{Type: protocol.CmdPing}); err != nil {
		t.Fatal(err)
	}
	if got := clients[0].in[len(clients[0].in)-1].Type; got != protocol.EvtPong {
		t.Fatalf("antwort auf ping = %q, erwartet %q", got, protocol.EvtPong)
	}
	if len(clients[1].in) != before {
		t.Fatal("ping loeste einen broadcast an alle aus")
	}
}

func TestSetNameIsBroadcast(t *testing.T) {
	m := testManager(t, 8)
	l, clients := joinAll(t, m, "Anna", "Ben")
	if err := l.Handle(clients[0], protocol.ClientMessage{Type: protocol.CmdSetName, Name: "Annika"}); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, p := range clients[1].lastState(t).Players {
		if p.Name == "Annika" {
			found = true
		}
	}
	if !found {
		t.Fatal("die umbenennung erreichte den anderen client nicht")
	}
}

func TestTwoTabsSamePlayerStayConnected(t *testing.T) {
	m := testManager(t, 8)
	l, clients := joinAll(t, m, "Anna", "Ben")

	// Zweiter Tab mit derselben Spieler-ID.
	tab2 := &fakeClient{id: clients[0].id}
	if err := l.Join(tab2, "Anna"); err != nil {
		t.Fatal(err)
	}
	// Ein Tab geht zu: der Spieler muss verbunden bleiben.
	l.Leave(tab2)
	for _, p := range l.Snapshot(clients[1].id).Players {
		if p.ID == clients[0].id && !p.Connected {
			t.Fatal("das schliessen eines zweiten tabs trennte den spieler")
		}
	}
}

func TestReapRemovesOnlyIdleLobbies(t *testing.T) {
	m := testManager(t, 8)
	busy, _ := joinAll(t, m, "Anna", "Ben")
	idle, err := m.Create()
	if err != nil {
		t.Fatal(err)
	}

	if n := m.Reap(time.Hour); n != 0 {
		t.Fatalf("%d lobbys abgeraeumt, erwartet 0 bei ttl=1h", n)
	}
	// Eine Lobby ohne Verbindung gilt sofort als untaetig.
	if n := m.Reap(0); n != 1 {
		t.Fatalf("%d lobbys abgeraeumt, erwartet 1 (die leere)", n)
	}
	if _, err := m.Get(idle.Code); err == nil {
		t.Fatal("die leere lobby existiert noch")
	}
	if _, err := m.Get(busy.Code); err != nil {
		t.Fatal("die besetzte lobby wurde abgeraeumt, obwohl clients verbunden sind")
	}
}

func TestErrorGoesOnlyToSender(t *testing.T) {
	m := testManager(t, 8)
	l, clients := joinAll(t, m, "Anna", "Ben")
	// Runde starten, dann ein zweites Mal — der zweite Versuch ist ein Regelverstoss.
	if err := l.Handle(clients[0], protocol.ClientMessage{Type: protocol.CmdStartRound}); err != nil {
		t.Fatal(err)
	}
	err := l.Handle(clients[0], protocol.ClientMessage{Type: protocol.CmdStartRound})
	if err == nil {
		t.Fatal("zweiter rundenstart wurde akzeptiert")
	}
	// Handle liefert den Fehler zurueck; das Verteilen an den Absender macht der
	// Transport. Der andere Client darf keine Fehlermeldung gesehen haben.
	if msg := clients[1].lastError(t); msg != "" {
		t.Fatalf("der andere client bekam die fehlermeldung %q", msg)
	}
}
