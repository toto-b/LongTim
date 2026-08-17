// Package config liest die gesamte Konfiguration aus der Umgebung (12 Factor III).
// Es gibt bewusst keine Konfigurationsdatei im Image: alles, was sich zwischen
// Entwicklung und Cluster unterscheidet, kommt als Environment-Variable rein.
package config

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config ist die aufgeloeste Laufzeitkonfiguration.
type Config struct {
	Port            int
	LogLevel        slog.Level
	LogFormat       string
	ScalesPath      string
	StaticDir       string
	LobbyTTL        time.Duration
	MaxPlayers      int
	MaxLobbies      int
	AllowedOrigins  []string
	ShutdownTimeout time.Duration
}

// Load liest die Konfiguration und faellt fuer jeden Wert auf einen sinnvollen
// Default zurueck. Ein Fehler bedeutet: ein gesetzter Wert war unbrauchbar —
// dann startet der Prozess lieber gar nicht, statt still etwas anderes zu tun.
func Load() (Config, error) {
	c := Config{
		Port:            8080,
		LogLevel:        slog.LevelInfo,
		LogFormat:       "json",
		ScalesPath:      "", // leer = eingebettete Skalen verwenden
		LobbyTTL:        30 * time.Minute,
		MaxPlayers:      12,
		MaxLobbies:      500,
		ShutdownTimeout: 10 * time.Second,
	}

	var err error
	if c.Port, err = envInt("PORT", c.Port); err != nil {
		return c, err
	}
	if c.MaxPlayers, err = envInt("MAX_PLAYERS", c.MaxPlayers); err != nil {
		return c, err
	}
	if c.MaxLobbies, err = envInt("MAX_LOBBIES", c.MaxLobbies); err != nil {
		return c, err
	}
	if c.LobbyTTL, err = envDuration("LOBBY_TTL", c.LobbyTTL); err != nil {
		return c, err
	}
	if c.ShutdownTimeout, err = envDuration("SHUTDOWN_TIMEOUT", c.ShutdownTimeout); err != nil {
		return c, err
	}
	if c.LogLevel, err = envLevel("LOG_LEVEL", c.LogLevel); err != nil {
		return c, err
	}

	c.ScalesPath = os.Getenv("SCALES_PATH")
	// STATIC_DIR ist ausschliesslich fuer den lokalen Entwicklungs-Loop gedacht:
	// dann liegen Frontend und API auf demselben Origin und der relative
	// /api-Pfad des Frontends stimmt ohne Proxy. Im Cluster bleibt die Variable
	// leer — dort liefert nginx die Dateien aus und der Ingress routet /api.
	c.StaticDir = os.Getenv("STATIC_DIR")
	if f := os.Getenv("LOG_FORMAT"); f != "" {
		if f != "json" && f != "text" {
			return c, fmt.Errorf("LOG_FORMAT: %q ist weder json noch text", f)
		}
		c.LogFormat = f
	}
	if o := os.Getenv("ALLOWED_ORIGINS"); o != "" {
		for _, part := range strings.Split(o, ",") {
			if part = strings.TrimSpace(part); part != "" {
				c.AllowedOrigins = append(c.AllowedOrigins, part)
			}
		}
	}

	if c.Port < 1 || c.Port > 65535 {
		return c, fmt.Errorf("PORT: %d liegt ausserhalb 1..65535", c.Port)
	}
	if c.MaxPlayers < 2 {
		return c, fmt.Errorf("MAX_PLAYERS: %d ist kleiner als 2, damit waere keine Runde spielbar", c.MaxPlayers)
	}
	if c.MaxLobbies < 1 {
		return c, fmt.Errorf("MAX_LOBBIES: %d ist kleiner als 1", c.MaxLobbies)
	}
	return c, nil
}

// Addr ist die Listen-Adresse des HTTP-Servers.
func (c Config) Addr() string { return ":" + strconv.Itoa(c.Port) }

// LogValue macht die Konfiguration beim Start protokollierbar.
func (c Config) LogValue() slog.Value {
	origins := "*"
	if len(c.AllowedOrigins) > 0 {
		origins = strings.Join(c.AllowedOrigins, ",")
	}
	scales := "embedded"
	if c.ScalesPath != "" {
		scales = c.ScalesPath
	}
	static := "off"
	if c.StaticDir != "" {
		static = c.StaticDir
	}
	return slog.GroupValue(
		slog.Int("port", c.Port),
		slog.String("staticDir", static),
		slog.String("logLevel", c.LogLevel.String()),
		slog.String("logFormat", c.LogFormat),
		slog.String("scales", scales),
		slog.Duration("lobbyTTL", c.LobbyTTL),
		slog.Int("maxPlayers", c.MaxPlayers),
		slog.Int("maxLobbies", c.MaxLobbies),
		slog.String("allowedOrigins", origins),
		slog.Duration("shutdownTimeout", c.ShutdownTimeout),
	)
}

func envInt(key string, def int) (int, error) {
	raw := os.Getenv(key)
	if raw == "" {
		return def, nil
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return def, fmt.Errorf("%s: %q ist keine ganze zahl", key, raw)
	}
	return v, nil
}

func envDuration(key string, def time.Duration) (time.Duration, error) {
	raw := os.Getenv(key)
	if raw == "" {
		return def, nil
	}
	v, err := time.ParseDuration(raw)
	if err != nil {
		return def, fmt.Errorf("%s: %q ist keine dauer (z.B. 30m, 10s)", key, raw)
	}
	if v <= 0 {
		return def, fmt.Errorf("%s: %q muss positiv sein", key, raw)
	}
	return v, nil
}

func envLevel(key string, def slog.Level) (slog.Level, error) {
	raw := os.Getenv(key)
	if raw == "" {
		return def, nil
	}
	var l slog.Level
	if err := l.UnmarshalText([]byte(raw)); err != nil {
		return def, fmt.Errorf("%s: %q ist kein log-level (debug, info, warn, error)", key, raw)
	}
	return l, nil
}
