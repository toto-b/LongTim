// Package metrics buendelt die Prometheus-Collectors des Game-Servers.
//
// Bewusst sind das nicht nur technische Zaehler, sondern fachliche: die
// Verteilung der Rate-Abweichungen ist gleichzeitig eine Betriebs- und eine
// Spielbalance-Metrik. Wenn fast alle Tipps im 4-Punkte-Band landen, sind die
// Skalenpaare zu leicht; wenn kaum einer ein Band trifft, zu schwer.
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// LobbiesActive zaehlt die aktuell offenen Lobbys.
	LobbiesActive = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "longwave_lobbies_active",
		Help: "Anzahl aktuell offener Lobbys.",
	})

	// PlayersConnected zaehlt die aktuell verbundenen Spieler ueber alle Lobbys.
	PlayersConnected = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "longwave_players_connected",
		Help: "Anzahl aktuell verbundener Spieler ueber alle Lobbys.",
	})

	// LobbiesCreated zaehlt alle jemals angelegten Lobbys.
	LobbiesCreated = promauto.NewCounter(prometheus.CounterOpts{
		Name: "longwave_lobbies_created_total",
		Help: "Insgesamt angelegte Lobbys.",
	})

	// LobbiesReaped zaehlt die vom Reaper wegen Leerlauf abgeraeumten Lobbys.
	LobbiesReaped = promauto.NewCounter(prometheus.CounterOpts{
		Name: "longwave_lobbies_reaped_total",
		Help: "Wegen Leerlauf abgeraeumte Lobbys.",
	})

	// RoundsTotal zaehlt die vollstaendig gespielten Runden.
	RoundsTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "longwave_rounds_total",
		Help: "Vollstaendig gespielte und ausgewertete Runden.",
	})

	// GuessDistance misst die Abweichung jedes einzelnen Tipps vom Ziel.
	// Die Buckets liegen exakt auf den Punktebaendern des Spiels (4/3/2/1/0 Punkte),
	// damit sich aus dem Histogram direkt die Punkteverteilung ablesen laesst.
	GuessDistance = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "longwave_guess_distance",
		Help:    "Euklidische Abweichung eines Tipps vom Ziel, in Prozent der Feldbreite.",
		Buckets: []float64{6, 14, 20, 28, 50, 100},
	})

	// PointsAwarded zaehlt die insgesamt vergebenen Punkte, aufgeschluesselt nach Rolle.
	PointsAwarded = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "longwave_points_awarded_total",
		Help: "Insgesamt vergebene Punkte.",
	}, []string{"role"})

	// GuessPoints zaehlt, wie oft welche Punktzahl vergeben wurde.
	//
	// Rechnerisch steckt das schon in GuessDistance: die Bucket-Grenzen liegen
	// genau auf den Punktebaendern. Die Baender daraus zurueckzurechnen hiesse
	// aber, im Dashboard auf le="6" zu selektieren — und wie das Label
	// formatiert ist ("6" oder "6.0"), haengt an der Prometheus-Version. Ein
	// eigener Zaehler mit selbst vergebenem Label ist davon unabhaengig.
	GuessPoints = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "longwave_guess_points_total",
		Help: "Anzahl der Tipps je vergebener Punktzahl.",
	}, []string{"points"})

	// HintSeconds misst, wie lange der Hinweisgeber fuer seinen Hinweis braucht.
	HintSeconds = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "longwave_hint_seconds",
		Help:    "Dauer vom Rundenstart bis zum abgeschickten Hinweis.",
		Buckets: []float64{5, 10, 20, 30, 60, 120, 300},
	})

	// WSMessages zaehlt den Protokollverkehr nach Richtung und Typ.
	WSMessages = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "longwave_ws_messages_total",
		Help: "WebSocket-Nachrichten nach Richtung und Typ.",
	}, []string{"direction", "type"})

	// WSErrors zaehlt abgelehnte Client-Kommandos, aufgeschluesselt nach Grund.
	WSErrors = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "longwave_ws_errors_total",
		Help: "Abgelehnte Client-Kommandos nach Grund.",
	}, []string{"reason"})
)

// Rollen fuer PointsAwarded.
const (
	RoleGuesser   = "guesser"
	RoleClueGiver = "clue_giver"
)

// Richtungen fuer WSMessages.
const (
	DirectionIn  = "in"
	DirectionOut = "out"
)
