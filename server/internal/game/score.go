package game

import "math"

// Band ordnet eine maximale Abweichung einer Punktzahl zu. Die Werte stammen
// unveraendert aus SCORE_BANDS der urspruenglichen Offline-Fassung, damit sich
// das Spielgefuehl durch den Umzug auf den Server nicht aendert.
type Band struct {
	Max    float64 `json:"max"`
	Points int     `json:"points"`
	Fill   string  `json:"fill"`
	Stroke string  `json:"stroke"`
}

// ScoreBands ist von innen nach aussen sortiert; PointsFor verlaesst sich darauf.
var ScoreBands = []Band{
	{Max: 6, Points: 4, Fill: "rgba(46,204,113,0.22)", Stroke: "#2ecc71"},
	{Max: 14, Points: 3, Fill: "rgba(241,196,15,0.15)", Stroke: "#f1c40f"},
	{Max: 20, Points: 2, Fill: "rgba(231,76,60,0.10)", Stroke: "#e74c3c"},
	{Max: 28, Points: 1, Fill: "rgba(141,153,174,0.08)", Stroke: "#8d99ae"},
}

// Point ist eine Koordinate in Prozent der Spielflaeche (0..100).
type Point struct {
	X int `json:"x"`
	Y int `json:"y"`
}

// Distance liefert den euklidischen Abstand zweier Punkte.
func Distance(a, b Point) float64 {
	dx := float64(a.X - b.X)
	dy := float64(a.Y - b.Y)
	return math.Sqrt(dx*dx + dy*dy)
}

// PointsFor gibt die Punktzahl fuer eine Abweichung zurueck, 0 ausserhalb aller Baender.
func PointsFor(distance float64) int {
	for _, b := range ScoreBands {
		if distance <= b.Max {
			return b.Points
		}
	}
	return 0
}
