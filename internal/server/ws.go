package server

import (
	"net/http"

	"github.com/coder/websocket"

	"github.com/tlmcguire/aisai/internal/model"
)

// handleWebSocket upgrades the connection and registers it with the event
// hub. Progress events, finding events and streaming chat all flow through
// this single channel.
func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		// No OriginPatterns are configured, and that is deliberate.
		//
		// The desktop app connects from the Electron main process with a
		// hand-written handshake that carries no Origin header, and the
		// library accepts a request without one. A browser, on the other
		// hand, always sends an Origin, so a page on another site that
		// tries to subscribe to the event stream of the loopback fallback
		// is refused: the origin has to match the host it connected to.
		//
		// The socket's own permissions (0600) are the primary control; this
		// is the second one, for the TCP fallback.
	})
	if err != nil {
		// Accept already wrote the error response.
		return
	}
	s.hub.Subscribe(conn)
}

// EmitProgress is a convenience wrapper for broadcasting an audit progress
// event.
func (s *Server) EmitProgress(auditID, phase string, percent float64, message string) {
	s.hub.Broadcast(model.Event{
		Type:    model.EventAuditProgress,
		AuditID: auditID,
		Phase:   phase,
		Percent: percent,
		Message: message,
	})
}
