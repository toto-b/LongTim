// Command server ist der Longwave-Game-Server: Lobby-Verwaltung, Rundenlogik
// und die Zusicherung, dass die Zielkoordinate nur beim Hinweisgeber landet.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/toto-b/longlongwave/server/internal/config"
	"github.com/toto-b/longlongwave/server/internal/lobby"
	"github.com/toto-b/longlongwave/server/internal/scales"
	"github.com/toto-b/longlongwave/server/internal/transport"
)

// version wird beim Bauen ueber -ldflags gesetzt (12 Factor V: Build und Release
// sind unterscheidbar).
var version = "dev"

// reapInterval bestimmt, wie oft nach verwaisten Lobbys gesucht wird.
const reapInterval = time.Minute

func main() {
	if err := run(); err != nil {
		// Vor dem Aufsetzen des Loggers oder bei Startfehlern: nach stderr und raus.
		fmt.Fprintln(os.Stderr, "fehler:", err)
		os.Exit(1)
	}
}

func run() error {
	// Admin-Kommandos laufen im selben Binary wie der Server (12 Factor XII).
	var (
		showVersion  = flag.Bool("version", false, "Version ausgeben und beenden")
		validateFlag = flag.Bool("validate-scales", false, "Skalen pruefen und beenden")
		dumpFlag     = flag.Bool("dump-scales", false, "Eingebettete Skalen nach stdout schreiben und beenden")
	)
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		return nil
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	if *dumpFlag {
		_, err := os.Stdout.Write(scales.EmbeddedRaw())
		return err
	}

	log := newLogger(cfg)
	slog.SetDefault(log)

	pairs, source, err := scales.Load(cfg.ScalesPath)
	if err != nil {
		return err
	}

	if *validateFlag {
		fmt.Printf("ok: %d skalenpaare aus %s\n", len(pairs), source)
		return nil
	}

	log.Info("longwave server startet",
		slog.String("version", version),
		slog.Int("scalePairs", len(pairs)),
		slog.Any("config", cfg))

	manager := lobby.NewManager(lobby.Options{
		Pairs:      pairs,
		MaxPlayers: cfg.MaxPlayers,
		MaxLobbies: cfg.MaxLobbies,
		Logger:     log,
	})

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go manager.RunReaper(ctx, cfg.LobbyTTL, reapInterval)

	// ready steuert /readyz. Beim Herunterfahren zuerst auf false, damit der
	// Ingress keine neuen Verbindungen mehr schickt, bevor der Server schliesst.
	var ready atomic.Bool
	ready.Store(true)

	// connCtx haengt an jeder Verbindung. Beim Herunterfahren wird er abgebrochen,
	// damit die WebSocket-Leseschleifen zurueckkehren: http.Server.Shutdown wartet
	// auf offene Verbindungen, kennt aber uebernommene (hijacked) Sockets nicht und
	// wuerde ohne das bis zum Timeout haengen.
	connCtx, closeConns := context.WithCancel(context.Background())
	defer closeConns()

	srv := &http.Server{
		Addr:              cfg.Addr(),
		Handler:           newMux(manager, cfg, &ready, log),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
		// Kein WriteTimeout: der wuerde langlebige WebSocket-Verbindungen kappen.
		BaseContext: func(net.Listener) context.Context { return connCtx },
	}

	errCh := make(chan error, 1)
	go func() {
		log.Info("http server hoert zu", slog.String("addr", cfg.Addr()))
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}

	log.Info("signal empfangen, fahre herunter", slog.Duration("timeout", cfg.ShutdownTimeout))
	ready.Store(false)
	closeConns()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Warn("kein sauberes herunterfahren", slog.Any("err", err))
		return srv.Close()
	}
	log.Info("beendet")
	return nil
}

// newMux baut die HTTP-Oberflaeche.
func newMux(m *lobby.Manager, cfg config.Config, ready *atomic.Bool, log *slog.Logger) http.Handler {
	h := transport.NewHandler(m, cfg.AllowedOrigins, log)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/lobby", h.CreateLobby)
	mux.HandleFunc("GET /api/lobby", h.LobbyInfo)
	mux.HandleFunc("GET /api/ws", h.ServeWS)

	// Liveness: der Prozess laeuft. Absichtlich ohne jede Abhaengigkeit — sonst
	// startet Kubernetes den Server wegen fremder Probleme neu.
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok\n"))
	})

	// Readiness: darf Verkehr bekommen. Beim Herunterfahren sofort false.
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, _ *http.Request) {
		if !ready.Load() {
			http.Error(w, "shutting down", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ready\n"))
	})

	mux.Handle("GET /metrics", promhttp.Handler())

	mux.HandleFunc("GET /api/version", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = fmt.Fprintf(w, "{\"version\":%q}\n", version)
	})

	// Nur fuer den lokalen Entwicklungs-Loop: liegt STATIC_DIR vor, liefert der
	// Server das Frontend gleich mit, damit beide auf demselben Origin laufen.
	// Im Cluster ist die Variable leer — dort macht das nginx.
	if cfg.StaticDir != "" {
		log.Warn("frontend wird vom game-server ausgeliefert (nur fuer entwicklung)",
			slog.String("dir", cfg.StaticDir))
		mux.Handle("GET /", http.FileServer(http.Dir(cfg.StaticDir)))
	}

	return logRequests(log, mux)
}

// logRequests protokolliert Anfragen strukturiert auf stdout (12 Factor XI).
// /healthz, /readyz und /metrics bleiben draussen, sonst besteht das Log zu
// 99 % aus Kubernetes- und Prometheus-Verkehr.
func logRequests(log *slog.Logger, next http.Handler) http.Handler {
	quiet := map[string]bool{"/healthz": true, "/readyz": true, "/metrics": true}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if quiet[r.URL.Path] {
			next.ServeHTTP(w, r)
			return
		}
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		log.Info("http",
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.Int("status", rec.status),
			slog.Duration("duration", time.Since(start)))
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status  int
	written bool
}

func (r *statusRecorder) WriteHeader(code int) {
	if !r.written {
		r.status = code
		r.written = true
	}
	r.ResponseWriter.WriteHeader(code)
}

// Unwrap gibt den urspruenglichen ResponseWriter frei. Ohne das verdeckt der
// Recorder dessen http.Hijacker, und der WebSocket-Upgrade scheitert mit 501.
func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

// Hijack reicht die Verbindungsuebernahme durch — fuer WebSocket-Bibliotheken,
// die direkt auf http.Hijacker pruefen statt auf http.ResponseController.
func (r *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := r.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("%T unterstuetzt kein hijacking", r.ResponseWriter)
	}
	return h.Hijack()
}

// Flush reicht das Leeren des Puffers durch (Server-Sent-Events, Streaming).
func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// newLogger baut den Logger nach Konfiguration. Ziel ist immer stdout: der
// Prozess schreibt einen Stream, das Sammeln uebernimmt die Plattform.
func newLogger(cfg config.Config) *slog.Logger {
	opts := &slog.HandlerOptions{Level: cfg.LogLevel}
	if cfg.LogFormat == "text" {
		return slog.New(slog.NewTextHandler(os.Stdout, opts))
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, opts))
}
