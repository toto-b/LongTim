package lobby

import (
	"context"
	crand "crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"strings"
	"sync"
	"time"

	"github.com/toto-b/longlongwave/server/internal/game"
	"github.com/toto-b/longlongwave/server/internal/metrics"
)

// codeAlphabet laesst leicht verwechselbare Zeichen weg (0/O, 1/I/L), damit
// sich ein Code fehlerfrei durchs Zimmer rufen laesst.
const codeAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"

// CodeLength ist die Laenge eines Lobby-Codes.
const CodeLength = 4

// ErrNoCapacity meldet, dass MAX_LOBBIES erreicht ist.
var ErrNoCapacity = errors.New("keine freie lobby-kapazitaet")

// ErrNotFound meldet einen unbekannten Lobby-Code.
var ErrNotFound = errors.New("lobby nicht gefunden")

// Options konfiguriert den Manager.
type Options struct {
	Pairs      []game.Pair
	MaxPlayers int
	MaxLobbies int
	Logger     *slog.Logger
}

// Manager haelt alle offenen Lobbys.
//
// Der Zustand liegt im Prozessspeicher. Das ist der bewusst dokumentierte
// Trade-off gegenueber 12-Factor VI/VIII: mehrere Replicas brauchen deshalb
// Session-Affinitaet am Ingress. Der saubere Weg waere ein geteilter Speicher
// (Redis) oder ein Lobby-Shard-Router.
type Manager struct {
	mu      sync.RWMutex
	lobbies map[string]*Lobby
	opts    Options
	log     *slog.Logger
}

// NewManager erzeugt einen Manager.
func NewManager(opts Options) *Manager {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	return &Manager{
		lobbies: make(map[string]*Lobby),
		opts:    opts,
		log:     opts.Logger,
	}
}

// Create legt eine neue Lobby mit frisch gewuerfeltem Code an.
func (m *Manager) Create() (*Lobby, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(m.lobbies) >= m.opts.MaxLobbies {
		return nil, fmt.Errorf("%w (%d lobbys offen)", ErrNoCapacity, len(m.lobbies))
	}

	code, err := m.freeCodeLocked()
	if err != nil {
		return nil, err
	}

	seed, err := cryptoSeed()
	if err != nil {
		return nil, err
	}
	l := newLobby(code, m.opts.Pairs, rand.New(rand.NewSource(seed)), m.opts.MaxPlayers, m.log)
	m.lobbies[code] = l

	metrics.LobbiesCreated.Inc()
	metrics.LobbiesActive.Set(float64(len(m.lobbies)))
	m.log.Info("lobby angelegt", slog.String("lobby", code), slog.Int("open", len(m.lobbies)))
	return l, nil
}

// freeCodeLocked wuerfelt einen noch unbenutzten Code aus.
func (m *Manager) freeCodeLocked() (string, error) {
	for attempt := 0; attempt < 100; attempt++ {
		code, err := randomCode()
		if err != nil {
			return "", err
		}
		if _, taken := m.lobbies[code]; !taken {
			return code, nil
		}
	}
	return "", errors.New("kein freier lobby-code gefunden")
}

// Get sucht eine Lobby. Der Code wird gross geschrieben und getrimmt, damit
// Tippfehler beim Eingeben nicht sofort scheitern.
func (m *Manager) Get(code string) (*Lobby, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	l, ok := m.lobbies[NormalizeCode(code)]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrNotFound, code)
	}
	return l, nil
}

// NormalizeCode bringt einen Code in die kanonische Form.
func NormalizeCode(code string) string {
	return strings.ToUpper(strings.TrimSpace(code))
}

// Count meldet die Anzahl offener Lobbys.
func (m *Manager) Count() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.lobbies)
}

// Reap raeumt Lobbys ab, die laenger als ttl ohne Verbindung sind, und meldet
// die Anzahl entfernter Lobbys.
func (m *Manager) Reap(ttl time.Duration) int {
	m.mu.Lock()
	defer m.mu.Unlock()

	removed := 0
	for code, l := range m.lobbies {
		if idle := l.IdleSince(); idle > ttl {
			delete(m.lobbies, code)
			removed++
			metrics.LobbiesReaped.Inc()
			m.log.Info("leere lobby abgeraeumt",
				slog.String("lobby", code), slog.Duration("idle", idle))
		}
	}
	if removed > 0 {
		metrics.LobbiesActive.Set(float64(len(m.lobbies)))
	}
	return removed
}

// RunReaper laeuft bis zum Abbruch des Kontexts und raeumt regelmaessig auf.
func (m *Manager) RunReaper(ctx context.Context, ttl, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.Reap(ttl)
		}
	}
}

// randomCode zieht einen Code aus kryptografisch sicherem Zufall. Der Code ist
// die einzige Zugangshuerde zu einer Lobby, deshalb kein math/rand.
func randomCode() (string, error) {
	buf := make([]byte, CodeLength)
	if _, err := crand.Read(buf); err != nil {
		return "", fmt.Errorf("zufall fuer lobby-code: %w", err)
	}
	out := make([]byte, CodeLength)
	for i, b := range buf {
		out[i] = codeAlphabet[int(b)%len(codeAlphabet)]
	}
	return string(out), nil
}

// cryptoSeed liefert einen Startwert fuer den Spiel-Zufall einer Lobby. Der
// Spielverlauf selbst laeuft danach ueber math/rand — das ist schnell genug und
// muss nicht unvorhersagbar im kryptografischen Sinn sein, nur unkorreliert
// zwischen Lobbys.
func cryptoSeed() (int64, error) {
	var buf [8]byte
	if _, err := crand.Read(buf[:]); err != nil {
		return 0, fmt.Errorf("zufall fuer spiel-seed: %w", err)
	}
	return int64(binary.LittleEndian.Uint64(buf[:])), nil
}
