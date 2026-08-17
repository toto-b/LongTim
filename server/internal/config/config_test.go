package config

import (
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestDefaultsWithEmptyEnvironment(t *testing.T) {
	c, err := Load()
	if err != nil {
		t.Fatalf("Load ohne environment schlug fehl: %v", err)
	}
	if c.Port != 8080 {
		t.Errorf("Port = %d, erwartet 8080", c.Port)
	}
	if c.Addr() != ":8080" {
		t.Errorf("Addr = %q, erwartet \":8080\"", c.Addr())
	}
	if c.LobbyTTL != 30*time.Minute {
		t.Errorf("LobbyTTL = %v, erwartet 30m", c.LobbyTTL)
	}
	if c.ScalesPath != "" {
		t.Errorf("ScalesPath = %q, erwartet leer (eingebettete skalen)", c.ScalesPath)
	}
	if c.LogFormat != "json" {
		t.Errorf("LogFormat = %q, erwartet json", c.LogFormat)
	}
}

func TestEnvironmentOverridesEveryValue(t *testing.T) {
	t.Setenv("PORT", "9000")
	t.Setenv("LOG_LEVEL", "debug")
	t.Setenv("LOG_FORMAT", "text")
	t.Setenv("SCALES_PATH", "/config/scales.json")
	t.Setenv("LOBBY_TTL", "5m")
	t.Setenv("MAX_PLAYERS", "20")
	t.Setenv("MAX_LOBBIES", "3")
	t.Setenv("SHUTDOWN_TIMEOUT", "25s")
	t.Setenv("ALLOWED_ORIGINS", "longwave.local, example.test ,")

	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.Port != 9000 {
		t.Errorf("Port = %d, erwartet 9000", c.Port)
	}
	if c.LogLevel != slog.LevelDebug {
		t.Errorf("LogLevel = %v, erwartet debug", c.LogLevel)
	}
	if c.LogFormat != "text" {
		t.Errorf("LogFormat = %q, erwartet text", c.LogFormat)
	}
	if c.ScalesPath != "/config/scales.json" {
		t.Errorf("ScalesPath = %q", c.ScalesPath)
	}
	if c.LobbyTTL != 5*time.Minute {
		t.Errorf("LobbyTTL = %v, erwartet 5m", c.LobbyTTL)
	}
	if c.MaxPlayers != 20 || c.MaxLobbies != 3 {
		t.Errorf("MaxPlayers/MaxLobbies = %d/%d, erwartet 20/3", c.MaxPlayers, c.MaxLobbies)
	}
	if c.ShutdownTimeout != 25*time.Second {
		t.Errorf("ShutdownTimeout = %v, erwartet 25s", c.ShutdownTimeout)
	}
	// Leere Einträge und Leerzeichen muessen wegfallen.
	if len(c.AllowedOrigins) != 2 ||
		c.AllowedOrigins[0] != "longwave.local" || c.AllowedOrigins[1] != "example.test" {
		t.Errorf("AllowedOrigins = %#v", c.AllowedOrigins)
	}
}

// Ein unbrauchbarer Wert muss den Start verhindern. Still auf einen Default
// zurueckzufallen waere die schlechtere Variante: dann laeuft im Cluster etwas
// anderes als konfiguriert, ohne dass es auffaellt.
func TestBadValuesFailFast(t *testing.T) {
	cases := []struct{ key, value string }{
		{"PORT", "achtzig"},
		{"PORT", "0"},
		{"PORT", "70000"},
		{"MAX_PLAYERS", "1"},
		{"MAX_LOBBIES", "0"},
		{"LOBBY_TTL", "30"},
		{"LOBBY_TTL", "-5m"},
		{"SHUTDOWN_TIMEOUT", "gleich"},
		{"LOG_LEVEL", "verbose"},
		{"LOG_FORMAT", "xml"},
	}
	for _, c := range cases {
		t.Run(c.key+"="+c.value, func(t *testing.T) {
			t.Setenv(c.key, c.value)
			if _, err := Load(); err == nil {
				t.Fatalf("%s=%q wurde akzeptiert", c.key, c.value)
			}
		})
	}
}

func TestLogValueMentionsEmbeddedScales(t *testing.T) {
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	// Die Startzeile soll erkennbar machen, woher die Skalen kommen.
	if got := c.LogValue().String(); !strings.Contains(got, "embedded") {
		t.Errorf("LogValue erwaehnt die skalenquelle nicht: %s", got)
	}
}
