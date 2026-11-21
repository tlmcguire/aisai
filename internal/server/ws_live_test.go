package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// TestWebSocketStaysOpenAndDeliversEvents is the regression test for the
// event stream being torn down the moment the handshake completed.
//
// The hub used to read with the context of the HTTP request. net/http
// cancels that context as soon as the handler returns, even for a hijacked
// connection, so every event stream died immediately after the 101 and the
// renderer never received a progress event, a finding event or a chat
// token.
//
// The test opens a real connection to a real server, waits, and then checks
// that an event broadcast afterwards actually arrives.
func TestWebSocketStaysOpenAndDeliversEvents(t *testing.T) {
	s, handler := newTestServer(t)
	srv := httptest.NewServer(handler)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, resp, err := websocket.Dial(ctx, wsURL, nil)
	if resp != nil && resp.Body != nil {
		defer resp.Body.Close()
	}
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	// Give the server a moment to finish the handshake and start its read
	// loop. The old code tore the connection down in this window.
	time.Sleep(300 * time.Millisecond)

	// The connection must still be usable.
	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"type":"subscribe"}`)); err != nil {
		t.Fatalf("the connection was closed right after the handshake: %v", err)
	}

	// And an event broadcast afterwards must arrive.
	s.EmitProgress("audit-1", "scanning", 50, "halfway")

	readCtx, readCancel := context.WithTimeout(ctx, 5*time.Second)
	defer readCancel()
	typ, data, err := conn.Read(readCtx)
	if err != nil {
		t.Fatalf("the event was not delivered: %v", err)
	}
	if typ != websocket.MessageText {
		t.Fatalf("the event came as type %v, want a text frame", typ)
	}
	if !strings.Contains(string(data), "audit-1") {
		t.Errorf("the event does not name the audit: %q", data)
	}
}

// TestWebSocketRejectsAForeignOriginOverARealConnection pins the origin
// policy against a real server rather than a recorder.
func TestWebSocketRejectsAForeignOriginOverARealConnection(t *testing.T) {
	_, handler := newTestServer(t)
	srv := httptest.NewServer(handler)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, resp, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{"Origin": []string{"http://attacker.example"}},
	})
	if resp != nil && resp.Body != nil {
		defer resp.Body.Close()
	}
	if err == nil {
		t.Fatal("a connection with a foreign origin must be refused")
	}
}
