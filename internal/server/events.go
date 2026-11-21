package server

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/tlmcguire/aisai/internal/model"
)

// Hub fans server events out to connected clients.
//
// Clients subscribe to a specific audit so a UI watching one package is not
// woken by the progress of another. The hub is safe for concurrent use.
type Hub struct {
	mu      sync.RWMutex
	clients map[*client]struct{}

	// sendTimeout bounds how long a slow client may block a broadcast.
	sendTimeout time.Duration
}

// client is one connected WebSocket peer.
type client struct {
	conn *websocket.Conn
	mu   sync.Mutex
	// subs holds the audit IDs this client is subscribed to. An empty set
	// means "everything" is delivered.
	subs map[string]bool
	// cancel ends the context of the read loop. It is called when the
	// connection goes away.
	cancel context.CancelFunc
}

// NewHub creates an empty hub.
func NewHub() *Hub {
	return &Hub{
		clients:     make(map[*client]struct{}),
		sendTimeout: 10 * time.Second,
	}
}

// Subscribe registers a connection with the hub.
//
// The context of the HTTP handshake is deliberately **not** used. net/http
// cancels the request context as soon as the handler returns, even for a
// hijacked connection, so a hub that reads with the request context tears
// the event stream down the moment the handshake completes. The hub creates
// its own context and cancels it when the connection goes away.
func (h *Hub) Subscribe(conn *websocket.Conn) {
	ctx, cancel := context.WithCancel(context.Background())
	c := &client{conn: conn, subs: map[string]bool{}, cancel: cancel}
	h.mu.Lock()
	h.clients[c] = struct{}{}
	h.mu.Unlock()

	go h.readLoop(ctx, c)
}

// readLoop consumes client messages (subscribe/unsubscribe/ping) and detects
// disconnects.
func (h *Hub) readLoop(ctx context.Context, c *client) {
	defer func() {
		h.mu.Lock()
		delete(h.clients, c)
		h.mu.Unlock()
		c.cancel()
		_ = c.conn.Close(websocket.StatusNormalClosure, "")
	}()

	c.conn.SetReadLimit(64 * 1024)
	for {
		typ, data, err := c.conn.Read(ctx)
		if err != nil {
			return
		}
		switch typ {
		case websocket.MessageText:
			h.handleClientMessage(c, data)
		default:
			// Binary and other frame types are ignored.
		}
	}
}

type clientMessage struct {
	Type    string `json:"type"`
	AuditID string `json:"auditId"`
}

func (h *Hub) handleClientMessage(c *client, data []byte) {
	var msg clientMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		return
	}
	switch msg.Type {
	case "subscribe":
		c.mu.Lock()
		c.subs[msg.AuditID] = true
		c.mu.Unlock()
	case "unsubscribe":
		c.mu.Lock()
		delete(c.subs, msg.AuditID)
		c.mu.Unlock()
	case "ping":
		h.sendTo(c, model.Event{Type: "pong"})
	}
}

// Broadcast delivers an event to every subscribed client.
//
// Delivery is best-effort: a client whose send times out is dropped rather
// than allowed to stall the broadcast.
func (h *Hub) Broadcast(ev model.Event) {
	h.mu.RLock()
	clients := make([]*client, 0, len(h.clients))
	for c := range h.clients {
		clients = append(clients, c)
	}
	h.mu.RUnlock()

	for _, c := range clients {
		if !c.wants(ev.AuditID) {
			continue
		}
		h.sendTo(c, ev)
	}
}

// sendTo writes one event to a client.
func (h *Hub) sendTo(c *client, ev model.Event) {
	payload, err := json.Marshal(ev)
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), h.sendTimeout)
	defer cancel()

	c.mu.Lock()
	defer c.mu.Unlock()
	_ = c.conn.Write(ctx, websocket.MessageText, payload)
}

// wants reports whether this client should receive an event for auditID.
func (c *client) wants(auditID string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.subs) == 0 {
		return true
	}
	return c.subs[auditID]
}

// ClientCount returns the number of connected clients.
func (h *Hub) ClientCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}

// Close closes every client connection.
func (h *Hub) Close() {
	h.mu.Lock()
	clients := make([]*client, 0, len(h.clients))
	for c := range h.clients {
		clients = append(clients, c)
	}
	h.clients = map[*client]struct{}{}
	h.mu.Unlock()

	for _, c := range clients {
		_ = c.conn.Close(websocket.StatusGoingAway, "server shutting down")
	}
}
