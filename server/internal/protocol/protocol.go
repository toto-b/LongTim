// Package protocol definiert die WebSocket-Nachrichten zwischen Frontend und
// Game-Server. Es liegt bewusst zwischen lobby und transport, damit beide
// dieselben Typen benutzen koennen, ohne sich gegenseitig zu importieren.
package protocol

import "github.com/toto-b/longlongwave/server/internal/game"

// Kommandos vom Client an den Server.
const (
	CmdSetName    = "set_name"
	CmdStartRound = "start_round"
	CmdRedraw     = "redraw_scales"
	CmdSubmitHint = "submit_hint"
	CmdPlaceGuess = "place_guess"
	CmdReveal     = "reveal"
	CmdReset      = "reset"
	CmdPing       = "ping"
)

// Nachrichten vom Server an den Client.
const (
	EvtState = "state"
	EvtError = "error"
	EvtPong  = "pong"
)

// ClientMessage ist eine eingehende Nachricht. Alle Nutzdaten liegen flach
// im selben Objekt; das Protokoll ist klein genug, dass sich ein verschachteltes
// payload-Feld nicht lohnt.
type ClientMessage struct {
	Type  string      `json:"type"`
	Name  string      `json:"name,omitempty"`
	Hint  string      `json:"hint,omitempty"`
	Point *game.Point `json:"point,omitempty"`
}

// ServerMessage ist eine ausgehende Nachricht.
type ServerMessage struct {
	Type  string         `json:"type"`
	State *game.Snapshot `json:"state,omitempty"`
	Error string         `json:"error,omitempty"`
}

// State verpackt einen Snapshot als Zustandsnachricht.
func State(s game.Snapshot) ServerMessage {
	return ServerMessage{Type: EvtState, State: &s}
}

// Error verpackt eine Fehlermeldung fuer den Client.
func Error(msg string) ServerMessage {
	return ServerMessage{Type: EvtError, Error: msg}
}
