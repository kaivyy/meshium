package discovery

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestWSHandlerRejectsInvalidServerID(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/ws/connect/abc", nil)
	w := httptest.NewRecorder()

	h := &Handler{}
	h.handleConnect(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, w.Code)
	}
	if !strings.Contains(w.Body.String(), "invalid server ID") {
		t.Fatalf("expected invalid server ID error, got %q", w.Body.String())
	}
}

type blockingConnectionRunner struct {
	continueCh chan struct{}
	doneCh     chan struct{}
	steps      int
	runErr     error
}

func (r *blockingConnectionRunner) RunConnectionTest(ctx context.Context, serverID int, onStep StepCallback) error {
	defer close(r.doneCh)

	messages := []WSMessage{
		{Step: "ssh", Status: "success"},
		{Step: "hostname", Status: "success"},
		{Step: "kernel", Status: "success"},
		{Step: "cpu_cores", Status: "success"},
		{Step: "done", Status: "complete"},
	}

	for i, msg := range messages {
		r.steps++
		onStep(msg)
		if i == 0 {
			<-r.continueCh
		}
		if ctx.Err() != nil {
			r.runErr = ctx.Err()
			return ctx.Err()
		}
	}

	r.runErr = ctx.Err()
	return ctx.Err()
}

func TestWSHandlerStopsOnWriteFailure(t *testing.T) {
	runner := &blockingConnectionRunner{
		continueCh: make(chan struct{}),
		doneCh:     make(chan struct{}),
	}

	h := NewHandler(runner)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	srv := httptest.NewServer(mux)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws/connect/1"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}

	var msg WSMessage
	if err := conn.ReadJSON(&msg); err != nil {
		t.Fatalf("read first websocket message: %v", err)
	}
	if msg.Step != "ssh" || msg.Status != "success" {
		t.Fatalf("unexpected first message: %+v", msg)
	}

	if err := conn.Close(); err != nil {
		t.Fatalf("close websocket client: %v", err)
	}

	time.Sleep(100 * time.Millisecond)
	close(runner.continueCh)

	select {
	case <-runner.doneCh:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for discovery to stop")
	}

	if runner.steps >= 5 {
		t.Fatalf("expected discovery to stop before completing all step messages, got %d attempted step messages", runner.steps)
	}
	if runner.runErr == nil || !strings.Contains(strings.ToLower(runner.runErr.Error()), "context canceled") {
		t.Fatalf("expected context cancellation after websocket write failure, got %v", runner.runErr)
	}
}
