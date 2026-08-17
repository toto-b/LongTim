// Package scales liefert die Skalenpaare des Spiels.
//
// Die Paare sind ins Binary eingebettet, damit der Server ohne jede externe
// Ressource startet. Ueber SCALES_PATH laesst sich stattdessen eine Datei
// mounten (im Cluster: eine ConfigMap) — dieselbe Anwendung, andere Daten,
// ohne Neubau des Images (12 Factor III/IV).
package scales

import (
	_ "embed"

	"github.com/toto-b/longlongwave/server/internal/game"
)

//go:embed scales.json
var embedded []byte

// Embedded gibt die eingebauten Skalenpaare zurueck.
func Embedded() ([]game.Pair, error) {
	return game.ParseScales(embedded)
}

// EmbeddedRaw gibt die eingebettete Datei unveraendert zurueck. Wird vom
// Admin-Kommando --dump-scales genutzt, um daraus eine ConfigMap zu erzeugen.
func EmbeddedRaw() []byte { return embedded }

// Load liest die Skalen von path; ist path leer, kommen die eingebetteten.
func Load(path string) ([]game.Pair, string, error) {
	if path == "" {
		p, err := Embedded()
		return p, "embedded", err
	}
	p, err := game.LoadScales(path)
	return p, path, err
}
