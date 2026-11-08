package spoolnet

import (
	"context"
	"io"
	"net/http"
	"testing"
	"testing/synctest"
	"time"

	"github.com/coder/websocket"
)

func TestResponseActivityIsPerClientUsesElapsedTimeAndExcludesLocalNodeState(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		client := &Client{ctx: ctx, direct: true, transport: &http.Transport{}}
		other := &Client{ctx: ctx}
		if _, err := client.Status(); err != nil || client.ResponseAgeMillis() != -1 {
			t.Fatal("cached node state counted as a server response")
		}
		listener := responseActivityListener{client: client, listener: nodeEventFunc(func(string, string) {})}
		listener.OnEvent("turn", "invalid JSON")
		if client.ResponseAgeMillis() != -1 {
			t.Fatal("an invalid frame established server activity")
		}
		listener.OnEvent("ping", `{"ok":true}`)
		time.Sleep(45 * time.Second)
		if age := client.ResponseAgeMillis(); age != 45000 || other.ResponseAgeMillis() != -1 {
			t.Fatal("activity age was not scoped to this connection", age)
		}
		client.NetworkChanged()
		if client.ResponseAgeMillis() != -1 {
			t.Fatal("network change retained evidence from the previous network")
		}
		cancel()
		listener.OnEvent("ping", `{"ok":true}`)
		if client.ResponseAgeMillis() != -1 {
			t.Fatal("closed client accepted late activity")
		}
	})
}

func TestAPIAndHealthResponsesEstablishActivityAndErrorsInvalidateIt(t *testing.T) {
	status := http.StatusOK
	client := nativeFixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, `{"ok":true}`)
	})
	if client.ResponseAgeMillis() != -1 {
		t.Fatal("new client already had response activity")
	}
	if _, err := client.Request("GET", "/api/me", "", "", "en"); err != nil || client.ResponseAgeMillis() < 0 {
		t.Fatal("successful API response was not recorded", err)
	}
	status = http.StatusServiceUnavailable
	if _, err := client.Request("GET", "/api/me", "", "", "en"); err != nil || client.ResponseAgeMillis() != -1 {
		t.Fatal("server failure retained healthy activity", err)
	}
	status = http.StatusUnauthorized
	if _, err := client.Request("GET", "/api/me", "", "", "en"); err != nil || client.ResponseAgeMillis() != -1 {
		t.Fatal("an authentication error established service health", err)
	}
	status = http.StatusOK
	if err := client.probeOnce(client.ctx); err != nil || client.ResponseAgeMillis() < 0 {
		t.Fatal("health response was not recorded", err)
	}
	client.transport.CloseIdleConnections()
	client.transport.TLSClientConfig.ServerName = "wrong.invalid.test"
	if _, err := client.Request("GET", "/api/me", "", "", "en"); err == nil || client.ResponseAgeMillis() != -1 {
		t.Fatal("TLS error retained healthy activity", err)
	}
}

func TestChatSSEFramesCountAsReceivedServerActivity(t *testing.T) {
	client := nativeFixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: ping\ndata: {\"ok\":true}\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	})
	events := make(eventRecorder, 4)
	stream, err := client.SubscribeChat("chat", "en", events)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	select {
	case event := <-events:
		if event[0] != "ping" || client.ResponseAgeMillis() < 0 {
			t.Fatal("SSE response did not establish activity", event)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("missing chat heartbeat")
	}
}

func TestWorkspaceWritesDoNotCountUntilAServerFrameArrives(t *testing.T) {
	entered, respond := make(chan struct{}), make(chan struct{})
	client := nativeFixture(t, func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		if _, _, err := conn.Read(r.Context()); err != nil {
			return
		}
		close(entered)
		select {
		case <-respond:
		case <-r.Context().Done():
			return
		}
		if err := conn.Write(r.Context(), websocket.MessageText, []byte(`{"type":"ping"}`)); err != nil {
			return
		}
		_, _, _ = conn.Read(r.Context())
	})
	events := make(eventRecorder, 4)
	stream, err := client.SubscribeWorkspace("csrf", "en", events)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	select {
	case <-entered:
		if client.ResponseAgeMillis() != -1 {
			t.Fatal("a buffered hello write established server health")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("workspace did not send hello")
	}
	close(respond)
	select {
	case event := <-events:
		if event[0] != "ping" || client.ResponseAgeMillis() < 0 {
			t.Fatal("workspace heartbeat did not establish activity", event)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("missing workspace heartbeat")
	}
}
