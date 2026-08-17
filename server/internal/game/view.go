package game

// Dieses Paket-Teil ist der eigentliche Grund, warum Longwave einen Server
// braucht: Der Spielzustand wird PRO EMPFAENGER serialisiert. Die Zielkoordinate
// verlaesst den Prozess nur in Richtung des Hinweisgebers — und ab dem Aufdecken
// in Richtung aller. Fremde Tipps bleiben bis zum Aufdecken ebenfalls verborgen,
// damit niemand abschreibt.
//
// In der urspruenglichen Offline-Fassung war beides reine Ehrensache ("bitte
// jetzt wegschauen"), weil der komplette Zustand im Browser jedes Mitspielers lag.

// PlayerView ist ein Spieler aus Sicht eines bestimmten Empfaengers.
type PlayerView struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Score       int    `json:"score"`
	Connected   bool   `json:"connected"`
	IsClueGiver bool   `json:"isClueGiver"`
	IsNext      bool   `json:"isNext"`
	HasGuessed  bool   `json:"hasGuessed"`
	IsYou       bool   `json:"isYou"`
}

// Snapshot ist der vollstaendige, bereits redigierte Zustand fuer einen Empfaenger.
type Snapshot struct {
	Phase      Phase  `json:"phase"`
	Round      int    `json:"round"`
	LobbyCode  string `json:"lobbyCode"`
	MinPlayers int    `json:"minPlayers"`

	AxisX *Pair `json:"axisX,omitempty"`
	AxisY *Pair `json:"axisY,omitempty"`

	Hint        string `json:"hint"`
	ClueGiverID string `json:"clueGiverId"`

	You             string `json:"you"`
	YouAreClueGiver bool   `json:"youAreClueGiver"`

	// Target fehlt im JSON komplett, solange der Empfaenger es nicht sehen darf.
	Target *Point `json:"target,omitempty"`
	// YourGuess ist der eigene Marker; jeder sieht immer nur den eigenen.
	YourGuess *Point `json:"yourGuess,omitempty"`

	Players []PlayerView `json:"players"`
	// Pending ist die Anzahl noch fehlender Tipps.
	Pending int `json:"pending"`

	// Results ist erst ab PhaseReveal gefuellt.
	Results         []GuessResult `json:"results,omitempty"`
	ClueGiverPoints int           `json:"clueGiverPoints,omitempty"`

	History    []HistoryEntry `json:"history"`
	ScoreBands []Band         `json:"scoreBands"`
}

// maySeeTarget entscheidet, ob ein Empfaenger die Zielkoordinate erhalten darf.
func (g *Game) maySeeTarget(viewerID string) bool {
	if g.phase == PhaseReveal {
		return true
	}
	if g.phase == PhaseLobby {
		return false
	}
	return viewerID != "" && viewerID == g.clueGiverID
}

// SnapshotFor baut den redigierten Zustand fuer genau einen Empfaenger.
func (g *Game) SnapshotFor(viewerID, lobbyCode string) Snapshot {
	next := g.rotation.Upcoming()

	players := make([]PlayerView, 0, len(g.players))
	for _, p := range g.Players() {
		_, guessed := g.guesses[p.ID]
		players = append(players, PlayerView{
			ID:          p.ID,
			Name:        p.Name,
			Score:       p.Score,
			Connected:   p.Connected,
			IsClueGiver: p.ID == g.clueGiverID && g.phase != PhaseLobby,
			IsNext:      p.ID == next && g.phase != PhaseLobby,
			HasGuessed:  guessed,
			IsYou:       p.ID == viewerID,
		})
	}

	s := Snapshot{
		Phase:           g.phase,
		Round:           g.round,
		LobbyCode:       lobbyCode,
		MinPlayers:      MinPlayers,
		Hint:            g.hint,
		ClueGiverID:     g.clueGiverID,
		You:             viewerID,
		YouAreClueGiver: viewerID != "" && viewerID == g.clueGiverID,
		Players:         players,
		Pending:         g.pendingGuessers(),
		History:         g.history,
		ScoreBands:      ScoreBands,
	}

	if g.phase != PhaseLobby {
		x, y := g.axisX, g.axisY
		s.AxisX, s.AxisY = &x, &y
	}

	if g.maySeeTarget(viewerID) {
		t := g.target
		s.Target = &t
	}

	if own, ok := g.guesses[viewerID]; ok {
		p := own
		s.YourGuess = &p
	}

	if g.phase == PhaseReveal && len(g.history) > 0 {
		last := g.history[len(g.history)-1]
		s.Results = last.Results
		s.ClueGiverPoints = last.ClueGiverPoints
	}

	return s
}
