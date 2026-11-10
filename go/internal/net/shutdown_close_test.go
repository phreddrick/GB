package net

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// Closing a session while queued messages are ready must still send the
// shutdown's 1012 frame. In the original writer, the outbound-select branch
// could observe done and return without writing a close frame (client 1006).
// Use actual upgraded connections and the regular session writer.
func TestGB102ShutdownSendsCloseWithQueuedOutput(t *testing.T) {
	srv, _, url := newSeqServer(t, defaultWSTiming())

	const clients = 48
	readers := make([]*reader, 0, clients)
	for i := 0; i < clients; i++ {
		conn, _, err := websocket.DefaultDialer.Dial(url, nil)
		if err != nil {
			t.Fatalf("dial connection %d: %v", i, err)
		}
		t.Cleanup(func() { _ = conn.Close() })
		rd := readAll(conn)
		_ = rd.next(t, 5*time.Second) // Welcome proves registration completed.
		readers = append(readers, rd)
	}
	if got := srv.registry.Count(); got != clients {
		t.Fatalf("registered sessions = %d, want %d", got, clients)
	}

	// Leave outbound messages available when CloseAll closes each session.
	// The writer may select either outbound or done; both must close with 1012.
	for i := 0; i < 3; i++ {
		srv.registry.Shout("queued before shutdown\n")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown: %v", err)
	}

	deadline := time.After(5 * time.Second)
	for i, rd := range readers {
		select {
		case err := <-rd.errc:
			var closeErr *websocket.CloseError
			if !errors.As(err, &closeErr) || closeErr.Code != websocket.CloseServiceRestart {
				t.Errorf("connection %d ended with %v, want close 1012", i, err)
			}
		case <-deadline:
			t.Fatalf("connection %d did not receive a close frame", i)
		}
	}
	waitFor(t, 5*time.Second, "all sessions to unregister", func() bool { return srv.registry.Count() == 0 })
}
