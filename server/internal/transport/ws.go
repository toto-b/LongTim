// Package transport bindet die Lobbys an HTTP und WebSocket. Es enthaelt keine
// Spielregeln — nur Verbindungsverwaltung, Serialisierung und Fehlerabbildung.
package transport

import (
	"context"
	crand "crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/toto-b/longlongwave/server/internal/lobby"
	"github.com/toto-b/longlongwave/server/internal/metrics"
	"github.com/toto-b/longlongwave/server/internal/protocol"
)

const (
	// readLimit begrenzt eine einzelne Client-Nachricht.
	readLimit = 4 << 10
	// sendBuffer ist die Warteschlange je Verbindung. Laeuft sie voll, ist der
	// Client zu langsam und wird getrennt, statt den Broadcast aller zu bremsen.
	sendBuffer = 32
	// pingInterval haelt Verbindungen durch Proxies offen und erkennt tote Peers.
	pingInterval = 25 * time.Second
	// writeTimeout begrenzt einen einzelnen Schreibvorgang.
	writeTimeout = 10 * time.Second
)

// playerIDPattern akzeptiert nur vom Server erzeugte Token-Formate.
var playerIDPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

// conn ist eine WebSocket-Verbindung und zugleich ein lobby.Client.
type conn struct {
	playerID string
	ws       *websocket.Conn
	out      chan protocol.ServerMessage
	log      *slog.Logger

	closeOnce sync.Once
	done      chan struct{}
}

func newConn(playerID string, ws *websocket.Conn, log *slog.Logger) *conn {
	return &conn{
		playerID: playerID,
		ws:       ws,
		out:      make(chan protocol.ServerMessage, sendBuffer),
		log:      log,
		done:     make(chan struct{}),
	}
}

// PlayerID erfuellt lobby.Client.
func (c *conn) PlayerID() string { return c.playerID }

// Send erfuellt lobby.Client und blockiert nie: die Lobby haelt beim Aufruf
// ihren Mutex, ein blockierender Client wuerde den ganzen Raum einfrieren.
func (c *conn) Send(m protocol.ServerMessage) {
	select {
	case <-c.done:
	case c.out <- m:
	default:
		// Warteschlange voll: der Client kommt nicht hinterher.
		c.log.Warn("sendepuffer voll, verbindung wird getrennt")
		c.close()
	}
}

func (c *conn) close() {
	c.closeOnce.Do(func() { close(c.done) })
}

// writeLoop serialisiert alle ausgehenden Nachrichten dieser Verbindung.
func (c *conn) writeLoop(ctx context.Context) {
	ping := time.NewTicker(pingInterval)
	defer ping.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-c.done:
			return

		case m := <-c.out:
			payload, err := json.Marshal(m)
			if err != nil {
				c.log.Error("nachricht konnte nicht serialisiert werden", slog.Any("err", err))
				continue
			}
			wctx, cancel := context.WithTimeout(ctx, writeTimeout)
			err = c.ws.Write(wctx, websocket.MessageText, payload)
			cancel()
			if err != nil {
				c.log.Debug("schreiben fehlgeschlagen", slog.Any("err", err))
				c.close()
				return
			}
			metrics.WSMessages.WithLabelValues(metrics.DirectionOut, m.Type).Inc()

		case <-ping.C:
			pctx, cancel := context.WithTimeout(ctx, writeTimeout)
			err := c.ws.Ping(pctx)
			cancel()
			if err != nil {
				c.log.Debug("ping fehlgeschlagen", slog.Any("err", err))
				c.close()
				return
			}
		}
	}
}

// Handler bedient die WebSocket- und Lobby-Endpunkte.
type Handler struct {
	manager *lobby.Manager
	origins []string
	log     *slog.Logger
}

// NewHandler erzeugt den Handler. origins sind erlaubte Origin-Muster; ist die
// Liste leer, akzeptiert die Bibliothek nur gleichnamige Herkunft (same origin),
// was im Cluster hinter einem gemeinsamen Ingress der Normalfall ist.
func NewHandler(m *lobby.Manager, origins []string, log *slog.Logger) *Handler {
	return &Handler{manager: m, origins: origins, log: log}
}

// CreateLobby legt eine Lobby an und liefert ihren Code.
func (h *Handler) CreateLobby(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "nur POST")
		return
	}
	l, err := h.manager.Create()
	if err != nil {
		h.log.Warn("lobby konnte nicht angelegt werden", slog.Any("err", err))
		status := http.StatusInternalServerError
		if errors.Is(err, lobby.ErrNoCapacity) {
			status = http.StatusServiceUnavailable
		}
		writeJSONError(w, status, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"code": l.Code})
}

// LobbyInfo meldet, ob ein Code existiert. Damit kann das Frontend einen
// Tippfehler abfangen, bevor es die WebSocket-Verbindung aufbaut.
func (h *Handler) LobbyInfo(w http.ResponseWriter, r *http.Request) {
	code := lobby.NormalizeCode(r.URL.Query().Get("lobby"))
	l, err := h.manager.Get(code)
	if err != nil {
		writeJSONError(w, http.StatusNotFound, "lobby nicht gefunden")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"code":    l.Code,
		"clients": l.Clients(),
	})
}

// ServeWS nimmt eine Spielverbindung an.
func (h *Handler) ServeWS(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	l, err := h.manager.Get(q.Get("lobby"))
	if err != nil {
		writeJSONError(w, http.StatusNotFound, "lobby nicht gefunden")
		return
	}

	// Die Spieler-ID ist zugleich das Reconnect-Token. Der Client bekommt sie
	// beim ersten Verbinden im Zustand mitgeteilt (Feld "you") und schickt sie
	// nach einem Reload wieder mit. Nur serverseitig erzeugte Formate zaehlen —
	// wer sich als jemand anderes ausgeben will, muesste dessen Token raten.
	playerID := q.Get("pid")
	if !playerIDPattern.MatchString(playerID) {
		if playerID, err = newPlayerID(); err != nil {
			h.log.Error("spieler-id konnte nicht erzeugt werden", slog.Any("err", err))
			writeJSONError(w, http.StatusInternalServerError, "interner fehler")
			return
		}
	}

	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns: h.origins,
	})
	if err != nil {
		h.log.Debug("websocket-upgrade abgelehnt", slog.Any("err", err))
		return
	}
	ws.SetReadLimit(readLimit)

	log := h.log.With(slog.String("lobby", l.Code), slog.String("player", playerID))
	c := newConn(playerID, ws, log)

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	go c.writeLoop(ctx)

	metrics.PlayersConnected.Inc()
	defer metrics.PlayersConnected.Dec()

	if err := l.Join(c, q.Get("name")); err != nil {
		c.Send(protocol.Error(err.Error()))
		// Kurz Zeit lassen, damit die Meldung noch rausgeht.
		time.Sleep(100 * time.Millisecond)
		ws.Close(websocket.StatusPolicyViolation, "lobby voll")
		return
	}
	defer l.Leave(c)
	defer ws.Close(websocket.StatusNormalClosure, "")

	h.readLoop(ctx, l, c)
}

// readLoop verarbeitet eingehende Kommandos bis zum Verbindungsende.
func (h *Handler) readLoop(ctx context.Context, l *lobby.Lobby, c *conn) {
	for {
		select {
		case <-c.done:
			return
		default:
		}

		_, data, err := c.ws.Read(ctx)
		if err != nil {
			status := websocket.CloseStatus(err)
			if status == websocket.StatusNormalClosure || status == websocket.StatusGoingAway || ctx.Err() != nil {
				c.log.Debug("verbindung beendet")
			} else {
				c.log.Debug("lesen fehlgeschlagen", slog.Any("err", err))
			}
			c.close()
			return
		}

		var msg protocol.ClientMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			metrics.WSErrors.WithLabelValues("malformed").Inc()
			c.Send(protocol.Error("nachricht konnte nicht gelesen werden"))
			continue
		}
		metrics.WSMessages.WithLabelValues(metrics.DirectionIn, msg.Type).Inc()

		if err := l.Handle(c, msg); err != nil {
			metrics.WSErrors.WithLabelValues(msg.Type).Inc()
			// Regelverstoesse gehen nur an den Absender, nicht an den ganzen Raum.
			c.Send(protocol.Error(err.Error()))
		}
	}
}

// newPlayerID erzeugt ein 128-Bit-Token als Spieler-Identitaet.
func newPlayerID() (string, error) {
	var buf [16]byte
	if _, err := crand.Read(buf[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf[:]), nil
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
