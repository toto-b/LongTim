// Command loadgen spielt echte Runden gegen einen laufenden Longwave-Server.
//
// Zwei Zwecke:
//  1. Ende-zu-Ende-Pruefung des ganzen Pfads (REST + WebSocket + Spiellogik),
//     inklusive der Zusicherung, dass die Zielkoordinate nie bei einem Ratenden
//     ankommt. Taucht sie doch auf, bricht das Programm mit Exit-Code 1 ab.
//  2. Verkehr fuer das Grafana-Dashboard: ohne Spieler bleiben alle Kurven flach.
//
// Beispiel:
//
//	go run ./cmd/loadgen -url http://localhost:8080 -lobbies 3 -players 4 -rounds 5
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	"github.com/toto-b/longlongwave/server/internal/game"
	"github.com/toto-b/longlongwave/server/internal/protocol"
)

var hintWords = []string{
	"Kaffee", "Sonntagmorgen", "Fahrradkette", "Bahnhofsuhr", "Zimtschnecke",
	"Aktenordner", "Regenschirm", "Serverraum", "Kirschkerne", "Nachtbus",
}

// leaks zaehlt Faelle, in denen ein Ratender die Zielkoordinate gesehen hat.
var leaks atomic.Int64

func main() {
	var (
		baseURL = flag.String("url", "http://localhost:8080", "Basis-URL des Servers")
		lobbies = flag.Int("lobbies", 2, "Anzahl paralleler Lobbys")
		players = flag.Int("players", 3, "Spieler je Lobby")
		rounds  = flag.Int("rounds", 3, "Runden je Lobby (0 = endlos)")
		pace    = flag.Duration("pace", 700*time.Millisecond, "Pause zwischen den Spielzuegen")
		quiet   = flag.Bool("quiet", false, "Nur Fehler ausgeben")
	)
	flag.Parse()

	if *players < 2 {
		fmt.Fprintln(os.Stderr, "fehler: mindestens 2 spieler je lobby")
		os.Exit(2)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup
	failures := make(chan error, *lobbies)

	for i := 0; i < *lobbies; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			if err := runLobby(ctx, *baseURL, *players, *rounds, *pace, *quiet, n); err != nil {
				failures <- fmt.Errorf("lobby %d: %w", n, err)
			}
		}(i)
	}

	wg.Wait()
	close(failures)

	failed := false
	for err := range failures {
		fmt.Fprintln(os.Stderr, "fehler:", err)
		failed = true
	}
	if n := leaks.Load(); n > 0 {
		fmt.Fprintf(os.Stderr, "\nFEHLER: die zielkoordinate ist %dx bei einem ratenden angekommen\n", n)
		os.Exit(1)
	}
	if failed {
		os.Exit(1)
	}
	if !*quiet {
		fmt.Println("\nok — alle runden gespielt, das ziel blieb beim hinweisgeber")
	}
}

// runLobby legt eine Lobby an, verbindet die Spieler und spielt Runden.
func runLobby(ctx context.Context, baseURL string, playerCount, rounds int, pace time.Duration, quiet bool, n int) error {
	code, err := createLobby(ctx, baseURL)
	if err != nil {
		return err
	}
	if !quiet {
		fmt.Printf("lobby %s angelegt\n", code)
	}

	clients := make([]*client, 0, playerCount)
	for i := 0; i < playerCount; i++ {
		c, err := dial(ctx, baseURL, code, fmt.Sprintf("Bot-%d%c", n+1, 'A'+i))
		if err != nil {
			return err
		}
		defer c.close()
		clients = append(clients, c)
	}

	// Warten, bis jeder Client alle Mitspieler sieht — sonst schlaegt der
	// Rundenstart mit "mindestens 2 spieler" fehl.
	for _, c := range clients {
		if err := c.waitFor(func(s game.Snapshot) bool {
			return len(s.Players) >= playerCount
		}); err != nil {
			return fmt.Errorf("warten auf mitspieler: %w", err)
		}
	}

	rng := rand.New(rand.NewSource(time.Now().UnixNano() + int64(n)))

	for round := 0; rounds == 0 || round < rounds; round++ {
		if ctx.Err() != nil {
			return nil
		}
		if err := playRound(ctx, clients, rng, pace, quiet); err != nil {
			return fmt.Errorf("runde %d: %w", round+1, err)
		}
	}
	return nil
}

// playRound spielt genau eine Runde durch.
func playRound(ctx context.Context, clients []*client, rng *rand.Rand, pace time.Duration, quiet bool) error {
	clients[0].send(protocol.ClientMessage{Type: protocol.CmdStartRound})

	// Rollen abwarten und einsammeln.
	var clueGiver *client
	guessers := make([]*client, 0, len(clients))
	for _, c := range clients {
		if err := c.waitFor(func(s game.Snapshot) bool { return s.Phase == game.PhaseHint }); err != nil {
			return fmt.Errorf("warten auf HINT: %w", err)
		}
		if c.state.YouAreClueGiver {
			clueGiver = c
		} else {
			guessers = append(guessers, c)
		}
	}
	if clueGiver == nil {
		return errors.New("kein hinweisgeber zugewiesen")
	}
	if clueGiver.state.Target == nil {
		return errors.New("der hinweisgeber hat kein ziel erhalten")
	}
	target := *clueGiver.state.Target

	sleep(ctx, pace)

	hint := hintWords[rng.Intn(len(hintWords))]
	clueGiver.send(protocol.ClientMessage{Type: protocol.CmdSubmitHint, Hint: hint})

	for _, c := range guessers {
		if err := c.waitFor(func(s game.Snapshot) bool { return s.Phase == game.PhaseGuess }); err != nil {
			return fmt.Errorf("warten auf GUESS: %w", err)
		}
	}

	// Tipps um das Ziel streuen, damit das Distanz-Histogram alle Baender trifft.
	for i, c := range guessers {
		sleep(ctx, pace/2)
		spread := 4 + i*9
		p := game.Point{
			X: clamp(target.X + rng.Intn(2*spread+1) - spread),
			Y: clamp(target.Y + rng.Intn(2*spread+1) - spread),
		}
		c.send(protocol.ClientMessage{Type: protocol.CmdPlaceGuess, Point: &p})
	}

	for _, c := range clients {
		if err := c.waitFor(func(s game.Snapshot) bool { return s.Phase == game.PhaseReveal }); err != nil {
			return fmt.Errorf("warten auf REVEAL: %w", err)
		}
	}

	if !quiet {
		pts := make([]string, 0, len(clueGiver.state.Results))
		for _, r := range clueGiver.state.Results {
			pts = append(pts, fmt.Sprintf("%s=%d", r.Name, r.Points))
		}
		fmt.Printf("  %s runde %d: %q → %s\n",
			clueGiver.state.LobbyCode, clueGiver.state.Round, hint, strings.Join(pts, " "))
	}

	sleep(ctx, pace)
	return nil
}

func clamp(v int) int {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}

func sleep(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}

// ---------------------------------------------------------------------------
// Client
// ---------------------------------------------------------------------------

type client struct {
	name     string
	ws       *websocket.Conn
	ctx      context.Context
	state    game.Snapshot
	hasState bool
}

func createLobby(ctx context.Context, baseURL string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/api/lobby", nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("POST /api/lobby: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("POST /api/lobby lieferte %s", resp.Status)
	}
	var body struct{ Code string }
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", err
	}
	return body.Code, nil
}

func dial(ctx context.Context, baseURL, code, name string) (*client, error) {
	u, err := url.Parse(baseURL)
	if err != nil {
		return nil, err
	}
	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	default:
		u.Scheme = "ws"
	}
	u.Path = "/api/ws"
	u.RawQuery = url.Values{"lobby": {code}, "name": {name}}.Encode()

	ws, _, err := websocket.Dial(ctx, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("dial %s als %s: %w", code, name, err)
	}
	c := &client{name: name, ws: ws, ctx: ctx}
	// Erster Zustand direkt nach dem Join.
	if err := c.waitFor(func(game.Snapshot) bool { return true }); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *client) close() { c.ws.Close(websocket.StatusNormalClosure, "") }

func (c *client) send(m protocol.ClientMessage) {
	data, err := json.Marshal(m)
	if err != nil {
		return
	}
	_ = c.ws.Write(c.ctx, websocket.MessageText, data)
}

// waitFor liest Zustaende, bis die Bedingung erfuellt ist. Jeder gelesene
// Zustand wird dabei auf ein durchgesickertes Ziel geprueft.
//
// Zuerst wird der bereits vorliegende Zustand geprueft: der letzte Beitretende
// sieht alle Mitspieler schon in seinem ersten Frame und wuerde sonst auf ein
// Update warten, das es nicht mehr gibt.
func (c *client) waitFor(cond func(game.Snapshot) bool) error {
	if c.hasState && cond(c.state) {
		return nil
	}
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(c.ctx, 20*time.Second)
		_, data, err := c.ws.Read(ctx)
		cancel()
		if err != nil {
			return fmt.Errorf("%s: read: %w", c.name, err)
		}

		var env protocol.ServerMessage
		if err := json.Unmarshal(data, &env); err != nil {
			return fmt.Errorf("%s: unmarshal: %w", c.name, err)
		}
		if env.Type == protocol.EvtError {
			return fmt.Errorf("%s: server meldet %q", c.name, env.Error)
		}
		if env.Type != protocol.EvtState || env.State == nil {
			continue
		}

		c.state = *env.State
		c.hasState = true
		c.checkNoLeak(string(data))

		if cond(c.state) {
			return nil
		}
	}
	return fmt.Errorf("%s: bedingung nicht innerhalb des zeitfensters erfuellt (phase %q)", c.name, c.state.Phase)
}

// checkNoLeak prueft die Zusicherung des Servers am rohen Frame: wer nicht
// Hinweisgeber ist, darf vor dem Aufdecken kein target-Feld sehen.
//
// Geprueft wird gezielt der Schluessel auf oberster Ebene. Die history-Eintraege
// enthalten ebenfalls ein target — das sind aber abgeschlossene, aufgedeckte
// Runden und damit oeffentlich.
func (c *client) checkNoLeak(raw string) {
	if c.state.YouAreClueGiver || c.state.Phase == game.PhaseReveal {
		return
	}

	leaked := c.state.Target != nil
	if !leaked {
		var top map[string]json.RawMessage
		if err := json.Unmarshal([]byte(raw), &top); err == nil {
			var state map[string]json.RawMessage
			if err := json.Unmarshal(top["state"], &state); err == nil {
				_, leaked = state["target"]
			}
		}
	}
	// Die laufende Runde darf auch nicht ueber den Verlauf sichtbar werden.
	for _, h := range c.state.History {
		if h.Round >= c.state.Round && c.state.Round > 0 {
			leaked = true
		}
	}

	if leaked {
		leaks.Add(1)
		fmt.Fprintf(os.Stderr, "LEAK: %s (phase %s, runde %d) hat die zielposition erhalten:\n%s\n",
			c.name, c.state.Phase, c.state.Round, raw)
	}
}
