package transport

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	neturl "net/url"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/toto-b/longlongwave/server/internal/game"
	"github.com/toto-b/longlongwave/server/internal/lobby"
	"github.com/toto-b/longlongwave/server/internal/protocol"
)

// testServer startet den echten HTTP- und WebSocket-Pfad auf einem Zufallsport.
func testServer(t *testing.T) *httptest.Server {
	t.Helper()

	pairs, err := game.ParseScales([]byte(`{"pairs":[
		["Kalt","Heiss"],["Leise","Laut"],["Rund","Eckig"],["Dunkel","Hell"],
		["Lokal","Global"],["Einfach","Kompliziert"],["Sanft","Hart"],["Nass","Trocken"]
	]}`))
	if err != nil {
		t.Fatal(err)
	}

	log := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
	m := lobby.NewManager(lobby.Options{Pairs: pairs, MaxPlayers: 8, MaxLobbies: 10, Logger: log})
	h := NewHandler(m, nil, log)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/lobby", h.CreateLobby)
	mux.HandleFunc("GET /api/lobby", h.LobbyInfo)
	mux.HandleFunc("GET /api/ws", h.ServeWS)

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// createLobby ruft den REST-Endpunkt auf und liefert den Code.
func createLobby(t *testing.T, srv *httptest.Server) string {
	t.Helper()
	resp, err := srv.Client().Post(srv.URL+"/api/lobby", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /api/lobby lieferte %d, erwartet 201", resp.StatusCode)
	}
	var body struct{ Code string }
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Code) != lobby.CodeLength {
		t.Fatalf("code %q hat die falsche laenge", body.Code)
	}
	return body.Code
}

// player ist ein echter WebSocket-Client fuer den Test.
type player struct {
	t   *testing.T
	ws  *websocket.Conn
	ctx context.Context
	id  string
	// raw haelt die letzte Zustandsnachricht als unveraendertes JSON — genau die
	// Bytes, die auch im Browser ankommen wuerden.
	raw   string
	state game.Snapshot
}

func dial(t *testing.T, srv *httptest.Server, code, name string) *player {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)

	url := strings.Replace(srv.URL, "http://", "ws://", 1) +
		"/api/ws?lobby=" + code + "&name=" + name
	ws, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { ws.Close(websocket.StatusNormalClosure, "") })

	p := &player{t: t, ws: ws, ctx: ctx}
	p.awaitState() // der erste Zustand kommt direkt nach dem Join
	p.id = p.state.You
	return p
}

// awaitState liest, bis eine Zustandsnachricht eintrifft.
func (p *player) awaitState() {
	p.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		_, data, err := p.ws.Read(p.ctx)
		if err != nil {
			p.t.Fatalf("read: %v", err)
		}
		var env protocol.ServerMessage
		if err := json.Unmarshal(data, &env); err != nil {
			p.t.Fatalf("unmarshal: %v", err)
		}
		if env.Type != protocol.EvtState || env.State == nil {
			continue
		}
		// Nur den state-Teil merken, so wie das Frontend ihn auswertet.
		var wrapper struct {
			State json.RawMessage `json:"state"`
		}
		if err := json.Unmarshal(data, &wrapper); err != nil {
			p.t.Fatal(err)
		}
		p.raw = string(wrapper.State)
		p.state = *env.State
		return
	}
	p.t.Fatal("keine zustandsnachricht innerhalb des zeitfensters")
}

func (p *player) send(m protocol.ClientMessage) {
	p.t.Helper()
	data, err := json.Marshal(m)
	if err != nil {
		p.t.Fatal(err)
	}
	if err := p.ws.Write(p.ctx, websocket.MessageText, data); err != nil {
		p.t.Fatalf("write: %v", err)
	}
}

func TestWSPlayerIDIsServerGeneratedAndStable(t *testing.T) {
	srv := testServer(t)
	code := createLobby(t, srv)

	a := dial(t, srv, code, "Anna")
	if len(a.id) != 32 {
		t.Fatalf("spieler-id %q ist kein 128-bit-token", a.id)
	}

	// Reconnect mit demselben Token muss dieselbe Identitaet liefern.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	url := strings.Replace(srv.URL, "http://", "ws://", 1) +
		"/api/ws?lobby=" + code + "&name=Anna&pid=" + a.id
	ws, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close(websocket.StatusNormalClosure, "")

	_, data, err := ws.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var env protocol.ServerMessage
	if err := json.Unmarshal(data, &env); err != nil {
		t.Fatal(err)
	}
	if env.State.You != a.id {
		t.Fatalf("reconnect gab die id %q, erwartet %q", env.State.You, a.id)
	}
	if len(env.State.Players) != 1 {
		t.Fatalf("%d spieler nach reconnect, erwartet 1", len(env.State.Players))
	}
}

func TestWSRejectsForgedPlayerID(t *testing.T) {
	srv := testServer(t)
	code := createLobby(t, srv)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// Kein gueltiges Token-Format: der Server muss eine eigene ID vergeben,
	// statt die mitgeschickte zu uebernehmen.
	q := neturl.Values{}
	q.Set("lobby", code)
	q.Set("name", "Boese")
	q.Set("pid", "aaaa' OR 1=1")
	url := strings.Replace(srv.URL, "http://", "ws://", 1) + "/api/ws?" + q.Encode()
	ws, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close(websocket.StatusNormalClosure, "")

	_, data, err := ws.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var env protocol.ServerMessage
	if err := json.Unmarshal(data, &env); err != nil {
		t.Fatal(err)
	}
	if !playerIDPattern.MatchString(env.State.You) {
		t.Fatalf("server uebernahm die untergeschobene id %q", env.State.You)
	}
}

func TestWSUnknownLobbyIs404(t *testing.T) {
	srv := testServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	url := strings.Replace(srv.URL, "http://", "ws://", 1) + "/api/ws?lobby=ZZZZ"
	if _, _, err := websocket.Dial(ctx, url, nil); err == nil {
		t.Fatal("verbindung zu einer unbekannten lobby wurde akzeptiert")
	}

	resp, err := srv.Client().Get(srv.URL + "/api/lobby?lobby=ZZZZ")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("GET /api/lobby?lobby=ZZZZ lieferte %d, erwartet 404", resp.StatusCode)
	}
}

// Der Ende-zu-Ende-Beweis fuer die zentrale Zusicherung: die Zielkoordinate
// erscheint in keinem einzigen Frame, das ein ratender Client empfaengt.
func TestWSTargetNeverAppearsInGuesserFrames(t *testing.T) {
	srv := testServer(t)
	code := createLobby(t, srv)

	a := dial(t, srv, code, "Anna")
	b := dial(t, srv, code, "Ben")
	a.awaitState() // Bens Beitritt loest bei Anna einen neuen Zustand aus

	a.send(protocol.ClientMessage{Type: protocol.CmdStartRound})
	a.awaitState()
	b.awaitState()

	clueGiver, guesser := a, b
	if b.state.YouAreClueGiver {
		clueGiver, guesser = b, a
	}
	if !clueGiver.state.YouAreClueGiver || guesser.state.YouAreClueGiver {
		t.Fatal("die rollen sind nicht eindeutig verteilt")
	}
	if clueGiver.state.Target == nil {
		t.Fatal("der hinweisgeber bekam das ziel nicht")
	}
	target := *clueGiver.state.Target

	if guesser.state.Target != nil {
		t.Fatal("der ratende bekam das ziel im HINT-zustand")
	}
	if strings.Contains(guesser.raw, `"target"`) {
		t.Fatalf("das HINT-frame des ratenden enthaelt ein target-feld:\n%s", guesser.raw)
	}

	clueGiver.send(protocol.ClientMessage{Type: protocol.CmdSubmitHint, Hint: "Kaffee"})
	guesser.awaitState()
	if guesser.state.Phase != game.PhaseGuess {
		t.Fatalf("phase beim ratenden = %q, erwartet %q", guesser.state.Phase, game.PhaseGuess)
	}
	if strings.Contains(guesser.raw, `"target"`) {
		t.Fatalf("das GUESS-frame des ratenden enthaelt ein target-feld:\n%s", guesser.raw)
	}
	if guesser.state.Hint != "Kaffee" {
		t.Fatalf("hinweis = %q, erwartet %q", guesser.state.Hint, "Kaffee")
	}

	// Volltreffer setzen — das deckt auf, weil er der einzige Ratende ist.
	guesser.send(protocol.ClientMessage{Type: protocol.CmdPlaceGuess, Point: &target})
	guesser.awaitState()

	if guesser.state.Phase != game.PhaseReveal {
		t.Fatalf("phase = %q, erwartet %q", guesser.state.Phase, game.PhaseReveal)
	}
	if guesser.state.Target == nil {
		t.Fatal("nach dem aufdecken fehlt dem ratenden das ziel")
	}
	if len(guesser.state.Results) != 1 || guesser.state.Results[0].Points != 4 {
		t.Fatalf("unerwartetes ergebnis: %+v", guesser.state.Results)
	}
}

func TestWSMalformedMessageGetsAnErrorNotADisconnect(t *testing.T) {
	srv := testServer(t)
	code := createLobby(t, srv)
	a := dial(t, srv, code, "Anna")

	if err := a.ws.Write(a.ctx, websocket.MessageText, []byte("kein json")); err != nil {
		t.Fatal(err)
	}
	_, data, err := a.ws.Read(a.ctx)
	if err != nil {
		t.Fatalf("die verbindung wurde wegen einer kaputten nachricht getrennt: %v", err)
	}
	var env protocol.ServerMessage
	if err := json.Unmarshal(data, &env); err != nil {
		t.Fatal(err)
	}
	if env.Type != protocol.EvtError {
		t.Fatalf("antwort = %q, erwartet %q", env.Type, protocol.EvtError)
	}

	// Die Verbindung muss danach weiter benutzbar sein.
	a.send(protocol.ClientMessage{Type: protocol.CmdPing})
	_, data, err = a.ws.Read(a.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &env); err != nil {
		t.Fatal(err)
	}
	if env.Type != protocol.EvtPong {
		t.Fatalf("antwort auf ping = %q, erwartet %q", env.Type, protocol.EvtPong)
	}
}

func TestWSRuleViolationGoesOnlyToSender(t *testing.T) {
	srv := testServer(t)
	code := createLobby(t, srv)
	a := dial(t, srv, code, "Anna")

	// Runde mit nur einem Spieler starten: Regelverstoss.
	a.send(protocol.ClientMessage{Type: protocol.CmdStartRound})
	_, data, err := a.ws.Read(a.ctx)
	if err != nil {
		t.Fatal(err)
	}
	var env protocol.ServerMessage
	if err := json.Unmarshal(data, &env); err != nil {
		t.Fatal(err)
	}
	if env.Type != protocol.EvtError {
		t.Fatalf("antwort = %q (%+v), erwartet %q", env.Type, env.State, protocol.EvtError)
	}
	if !strings.Contains(env.Error, "spieler") {
		t.Fatalf("unerwartete meldung: %q", env.Error)
	}
}

func TestLobbyInfoReportsClientCount(t *testing.T) {
	srv := testServer(t)
	code := createLobby(t, srv)
	dial(t, srv, code, "Anna")

	resp, err := srv.Client().Get(srv.URL + "/api/lobby?lobby=" + strings.ToLower(code))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, erwartet 200", resp.StatusCode)
	}
	var body struct {
		Code    string `json:"code"`
		Clients int    `json:"clients"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Code != code {
		t.Fatalf("code = %q, erwartet %q", body.Code, code)
	}
	if body.Clients != 1 {
		t.Fatalf("clients = %d, erwartet 1", body.Clients)
	}
}

func TestCreateLobbyRejectsGet(t *testing.T) {
	srv := testServer(t)
	// Die Route ist auf POST registriert; GET darf die Lobby nicht anlegen.
	resp, err := srv.Client().Get(srv.URL + "/api/lobby")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	// Ohne lobby-Parameter landet GET bei LobbyInfo und findet nichts.
	if resp.StatusCode == http.StatusCreated {
		t.Fatal("GET /api/lobby hat eine lobby angelegt")
	}
}
