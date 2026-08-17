package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/toto-b/longlongwave/server/internal/config"
	"github.com/toto-b/longlongwave/server/internal/lobby"
	"github.com/toto-b/longlongwave/server/internal/scales"
)

// testMux baut denselben Handler-Stapel wie der echte Server, inklusive der
// Logging-Middleware. Die Tests im transport-Paket registrieren die Handler
// direkt und wuerden Fehler in dieser Verpackung nicht sehen.
func testMux(t *testing.T, ready *atomic.Bool) http.Handler {
	t.Helper()
	pairs, err := scales.Embedded()
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
	m := lobby.NewManager(lobby.Options{Pairs: pairs, MaxPlayers: 8, MaxLobbies: 10, Logger: log})
	return newMux(m, config.Config{}, ready, log)
}

func newTestServer(t *testing.T) (*httptest.Server, *atomic.Bool) {
	t.Helper()
	var ready atomic.Bool
	ready.Store(true)
	srv := httptest.NewServer(testMux(t, &ready))
	t.Cleanup(srv.Close)
	return srv, &ready
}

// Regressionstest: die Logging-Middleware ersetzt den ResponseWriter durch einen
// statusRecorder. Ohne dessen Hijack/Unwrap verdeckt er den http.Hijacker des
// Originals und jeder WebSocket-Upgrade endet mit 501 statt 101.
func TestWebSocketUpgradeSurvivesLoggingMiddleware(t *testing.T) {
	srv, _ := newTestServer(t)

	resp, err := srv.Client().Post(srv.URL+"/api/lobby", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /api/lobby lieferte %d, erwartet 201", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	code := strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(string(body)), `{"code":"`), `"}`)
	if len(code) != lobby.CodeLength {
		t.Fatalf("code %q konnte nicht gelesen werden aus %s", code, body)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	url := strings.Replace(srv.URL, "http://", "ws://", 1) + "/api/ws?lobby=" + code + "&name=Anna"
	ws, httpResp, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		status := 0
		if httpResp != nil {
			status = httpResp.StatusCode
		}
		t.Fatalf("websocket-upgrade durch die middleware fehlgeschlagen (status %d): %v", status, err)
	}
	defer ws.Close(websocket.StatusNormalClosure, "")

	if _, _, err := ws.Read(ctx); err != nil {
		t.Fatalf("kein erster zustand nach dem upgrade: %v", err)
	}
}

func TestHealthzIsAlwaysOK(t *testing.T) {
	srv, ready := newTestServer(t)

	for _, state := range []bool{true, false} {
		ready.Store(state)
		resp, err := srv.Client().Get(srv.URL + "/healthz")
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		// Liveness haengt nicht am Bereitschaftszustand: sonst wuerde Kubernetes
		// den Pod beim Herunterfahren zusaetzlich neu starten.
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("/healthz mit ready=%v lieferte %d, erwartet 200", state, resp.StatusCode)
		}
	}
}

func TestReadyzFollowsShutdownFlag(t *testing.T) {
	srv, ready := newTestServer(t)

	resp, err := srv.Client().Get(srv.URL + "/readyz")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/readyz lieferte %d, erwartet 200", resp.StatusCode)
	}

	ready.Store(false)
	resp, err = srv.Client().Get(srv.URL + "/readyz")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("/readyz beim herunterfahren lieferte %d, erwartet 503", resp.StatusCode)
	}
}

func TestMetricsExposesGameMetrics(t *testing.T) {
	srv, _ := newTestServer(t)

	resp, err := srv.Client().Get(srv.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)

	// Die fachlichen Metriken sind der Grund, warum Prometheus hier Sinn ergibt —
	// fehlen sie, bleibt nur Go-Runtime-Rauschen.
	for _, want := range []string{
		"longwave_lobbies_active",
		"longwave_rounds_total",
		"longwave_guess_distance",
		"longwave_hint_seconds",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("/metrics enthaelt %q nicht", want)
		}
	}
}

func TestStaticFilesAreOffByDefault(t *testing.T) {
	var ready atomic.Bool
	ready.Store(true)
	srv := httptest.NewServer(testMux(t, &ready))
	defer srv.Close()

	// Ohne STATIC_DIR liefert der Game-Server kein Frontend aus. Im Cluster macht
	// das nginx; der Server wuerde sonst zwei Aufgaben vermischen.
	resp, err := srv.Client().Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("GET / lieferte %d, erwartet 404 ohne STATIC_DIR", resp.StatusCode)
	}
}

func TestStaticFilesServedWhenConfigured(t *testing.T) {
	pairs, err := scales.Embedded()
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
	m := lobby.NewManager(lobby.Options{Pairs: pairs, MaxPlayers: 8, MaxLobbies: 10, Logger: log})

	var ready atomic.Bool
	ready.Store(true)
	srv := httptest.NewServer(newMux(m, config.Config{StaticDir: "../../../web"}, &ready, log))
	defer srv.Close()

	resp, err := srv.Client().Get(srv.URL + "/app.js")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /app.js lieferte %d, erwartet 200 mit STATIC_DIR", resp.StatusCode)
	}

	// Die API darf davon nicht verdeckt werden.
	api, err := srv.Client().Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	api.Body.Close()
	if api.StatusCode != http.StatusOK {
		t.Fatalf("/healthz wurde vom FileServer verdeckt (%d)", api.StatusCode)
	}
}

func TestEmbeddedScalesAreValid(t *testing.T) {
	pairs, err := scales.Embedded()
	if err != nil {
		t.Fatalf("die eingebetteten skalen sind unbrauchbar: %v", err)
	}
	if len(pairs) < 20 {
		t.Fatalf("nur %d skalenpaare eingebettet — zu wenig fuer abwechslungsreiche runden", len(pairs))
	}
}
