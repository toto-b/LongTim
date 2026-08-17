// Package lobby haelt den Spielzustand je Raum und verteilt ihn an die
// verbundenen Clients. Jede Lobby serialisiert alle Zugriffe ueber einen Mutex;
// die Spiel-Engine selbst muss dadurch nicht nebenlaeufigkeitssicher sein.
package lobby

import (
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"strconv"
	"sync"
	"time"

	"github.com/toto-b/longlongwave/server/internal/game"
	"github.com/toto-b/longlongwave/server/internal/metrics"
	"github.com/toto-b/longlongwave/server/internal/protocol"
)

// Client ist die Sicht der Lobby auf eine Verbindung. Das Transportpaket
// implementiert das Interface; die Lobby kennt kein WebSocket.
type Client interface {
	// PlayerID identifiziert den Spieler ueber Reconnects hinweg.
	PlayerID() string
	// Send stellt eine Nachricht zu. Muss nicht blockieren: die Lobby ruft Send
	// unter ihrem eigenen Mutex auf.
	Send(protocol.ServerMessage)
}

var (
	// ErrLobbyFull meldet, dass MAX_PLAYERS erreicht ist.
	ErrLobbyFull = errors.New("lobby ist voll")
	// ErrUnknownCommand meldet einen Nachrichtentyp, den der Server nicht kennt.
	ErrUnknownCommand = errors.New("unbekanntes kommando")
)

// Lobby ist ein Spielraum.
type Lobby struct {
	Code string

	mu         sync.Mutex
	game       *game.Game
	clients    map[Client]struct{}
	lastActive time.Time
	maxPlayers int
	log        *slog.Logger
}

func newLobby(code string, pairs []game.Pair, rng *rand.Rand, maxPlayers int, log *slog.Logger) *Lobby {
	return &Lobby{
		Code:       code,
		game:       game.New(pairs, rng),
		clients:    make(map[Client]struct{}),
		lastActive: time.Now(),
		maxPlayers: maxPlayers,
		log:        log.With(slog.String("lobby", code)),
	}
}

// Join meldet einen Client an und schickt allen den neuen Zustand.
func (l *Lobby) Join(c Client, name string) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	// Ein bekannter Spieler darf immer zurueck, auch wenn die Lobby voll ist —
	// sonst sperrt ein Reload den eigenen Platz aus.
	if _, known := l.game.Player(c.PlayerID()); !known && l.game.PlayerCount() >= l.maxPlayers {
		return fmt.Errorf("%w (%d spieler)", ErrLobbyFull, l.maxPlayers)
	}

	l.clients[c] = struct{}{}
	p := l.game.AddPlayer(c.PlayerID(), name)
	l.touchLocked()
	l.log.Info("spieler beigetreten",
		slog.String("player", p.ID), slog.String("name", p.Name),
		slog.Int("players", l.game.PlayerCount()))
	l.broadcastLocked()
	return nil
}

// Leave meldet einen Client ab. Der Spieler bleibt mit seinen Punkten in der
// Lobby, damit ein Reload den Spielstand nicht kostet — er gilt nur als getrennt.
func (l *Lobby) Leave(c Client) {
	l.mu.Lock()
	defer l.mu.Unlock()

	delete(l.clients, c)
	// Nur trennen, wenn keine andere Verbindung dieselbe Spieler-ID haelt
	// (zwei Tabs mit derselben Session).
	if !l.hasClientForLocked(c.PlayerID()) {
		l.game.Disconnect(c.PlayerID())
	}
	l.touchLocked()
	l.log.Info("spieler getrennt", slog.String("player", c.PlayerID()),
		slog.Int("connected", l.game.ConnectedCount()))
	l.broadcastLocked()
}

func (l *Lobby) hasClientForLocked(playerID string) bool {
	for c := range l.clients {
		if c.PlayerID() == playerID {
			return true
		}
	}
	return false
}

// Handle fuehrt ein Client-Kommando aus und verteilt den neuen Zustand.
// Ein zurueckgegebener Fehler ist eine Spielregel-Verletzung und geht als
// error-Nachricht nur an den Absender.
func (l *Lobby) Handle(c Client, msg protocol.ClientMessage) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	actor := c.PlayerID()
	var err error

	switch msg.Type {
	case protocol.CmdPing:
		c.Send(protocol.ServerMessage{Type: protocol.EvtPong})
		return nil

	case protocol.CmdSetName:
		err = l.game.Rename(actor, msg.Name)

	case protocol.CmdStartRound:
		err = l.game.StartRound()

	case protocol.CmdRedraw:
		err = l.game.RedrawScales(actor)

	case protocol.CmdSubmitHint:
		var d time.Duration
		if d, err = l.game.SubmitHint(actor, msg.Hint); err == nil {
			metrics.HintSeconds.Observe(d.Seconds())
		}

	case protocol.CmdPlaceGuess:
		if msg.Point == nil {
			err = errors.New("tipp ohne koordinate")
			break
		}
		var res *game.RoundResult
		if res, err = l.game.PlaceGuess(actor, *msg.Point); err == nil {
			l.recordRoundLocked(res)
		}

	case protocol.CmdReveal:
		var res *game.RoundResult
		if res, err = l.game.Reveal(actor); err == nil {
			l.recordRoundLocked(res)
		}

	case protocol.CmdReset:
		l.game.Reset()

	default:
		err = fmt.Errorf("%w: %q", ErrUnknownCommand, msg.Type)
	}

	if err != nil {
		return err
	}

	l.touchLocked()
	l.broadcastLocked()
	return nil
}

// recordRoundLocked erfasst die Metriken einer ausgewerteten Runde.
// res darf nil sein — dann wurde noch nicht aufgedeckt.
func (l *Lobby) recordRoundLocked(res *game.RoundResult) {
	if res == nil {
		return
	}
	metrics.RoundsTotal.Inc()
	for _, r := range res.Results {
		metrics.GuessDistance.Observe(r.Distance)
		metrics.GuessPoints.WithLabelValues(strconv.Itoa(r.Points)).Inc()
		metrics.PointsAwarded.WithLabelValues(metrics.RoleGuesser).Add(float64(r.Points))
	}
	metrics.PointsAwarded.WithLabelValues(metrics.RoleClueGiver).Add(float64(res.ClueGiverPoints))
	l.log.Info("runde ausgewertet",
		slog.Int("round", res.Round),
		slog.Int("guesses", len(res.Results)),
		slog.Int("clueGiverPoints", res.ClueGiverPoints),
		slog.Duration("duration", res.RoundDuration))
}

// broadcastLocked schickt jedem Client seinen eigenen, redigierten Zustand.
// Genau hier trennt sich, wer die Zielkoordinate sehen darf und wer nicht.
func (l *Lobby) broadcastLocked() {
	for c := range l.clients {
		c.Send(protocol.State(l.game.SnapshotFor(c.PlayerID(), l.Code)))
	}
}

// SendStateTo schickt einem einzelnen Client seinen aktuellen Zustand.
func (l *Lobby) SendStateTo(c Client) {
	l.mu.Lock()
	defer l.mu.Unlock()
	c.Send(protocol.State(l.game.SnapshotFor(c.PlayerID(), l.Code)))
}

func (l *Lobby) touchLocked() { l.lastActive = time.Now() }

// IdleSince meldet, wie lange die Lobby ohne Verbindung und ohne Aktivitaet ist.
// Eine Lobby mit mindestens einem verbundenen Client gilt nie als untaetig.
func (l *Lobby) IdleSince() time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.clients) > 0 {
		return 0
	}
	return time.Since(l.lastActive)
}

// Clients meldet die Anzahl offener Verbindungen.
func (l *Lobby) Clients() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.clients)
}

// Snapshot liefert den Zustand aus Sicht eines Spielers (fuer Tests).
func (l *Lobby) Snapshot(playerID string) game.Snapshot {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.game.SnapshotFor(playerID, l.Code)
}
