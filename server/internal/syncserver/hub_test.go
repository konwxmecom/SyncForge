package syncserver

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/konwxmecom/SyncForge/server/internal/oplog"
)

type failingOperationStore struct{}

func (failingOperationStore) Load(string) ([]oplog.Record, error) {
	return nil, nil
}

func (failingOperationStore) Append(string, string, []byte) error {
	return errors.New("simulated disk failure")
}

func TestStorageFailureIsReportedInsteadOfAcknowledged(t *testing.T) {
	hub := NewHubWithServices(failingOperationStore{}, nil)
	server := httptest.NewServer(http.HandlerFunc(hub.HandleWebSocket))
	defer server.Close()
	url := "ws" + server.URL[len("http"):] + "/sync"
	connection, response, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("dial websocket: %v (response: %v)", err, response)
	}
	defer connection.Close()
	if err := connection.WriteJSON(map[string]any{
		"protocolVersion": 1,
		"type":            "join",
		"documentId":      "storage-doc",
		"replicaId":       "writer",
	}); err != nil {
		t.Fatal(err)
	}
	var joined map[string]any
	if err := connection.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := connection.ReadJSON(&joined); err != nil {
		t.Fatal(err)
	}
	if joined["type"] != "joined" {
		t.Fatalf("unexpected join response: %#v", joined)
	}
	if err := connection.WriteJSON(map[string]any{
		"protocolVersion": 1,
		"type":            "operation",
		"documentId":      "storage-doc",
		"replicaId":       "writer",
		"operationId":     "insert:writer:writer:1",
		"operation": map[string]any{
			"kind":  "insert",
			"id":    map[string]any{"replicaId": "writer", "counter": 1},
			"after": nil,
			"value": "x",
		},
	}); err != nil {
		t.Fatal(err)
	}
	var failure map[string]any
	if err := connection.ReadJSON(&failure); err != nil {
		t.Fatal(err)
	}
	if failure["type"] != "error" || failure["code"] != "storage_error" {
		t.Fatalf("storage failure was not reported: %#v", failure)
	}
}

func TestCheckSameHostOrigin(t *testing.T) {
	tests := []struct {
		name   string
		origin string
		host   string
		allow  bool
	}{
		{name: "same host with a different development port", origin: "http://localhost:5173", host: "localhost:8080", allow: true},
		{name: "same host without a port", origin: "https://example.test", host: "example.test:8080", allow: true},
		{name: "different host", origin: "http://attacker.test", host: "localhost:8080", allow: false},
		{name: "invalid scheme", origin: "javascript://localhost", host: "localhost:8080", allow: false},
		{name: "malformed origin", origin: "://localhost", host: "localhost:8080", allow: false},
		{name: "missing origin", origin: "", host: "localhost:8080", allow: true},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			request := httptest.NewRequest("GET", "http://"+testCase.host+"/sync", nil)
			request.Host = testCase.host
			if testCase.origin != "" {
				request.Header.Set("Origin", testCase.origin)
			}
			if got := checkSameHostOrigin(request); got != testCase.allow {
				t.Fatalf("checkSameHostOrigin() = %v, want %v", got, testCase.allow)
			}
		})
	}
}

func TestHandleWebSocketRejectsConnectionsAtGlobalLimit(t *testing.T) {
	limits := DefaultLimits()
	limits.MaxConnections = 1
	hub := NewHubWithLimits(nil, nil, limits)
	if !hub.acquireConnection() {
		t.Fatal("first connection reservation was rejected")
	}
	defer hub.releaseConnection()

	request := httptest.NewRequest(http.MethodGet, "/sync", nil)
	recorder := httptest.NewRecorder()
	hub.HandleWebSocket(recorder, request)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusServiceUnavailable)
	}
}

func TestAddAndReplayRejectsConnectionsAtDocumentLimit(t *testing.T) {
	limits := DefaultLimits()
	limits.MaxConnectionsPerDocument = 1
	hub := NewHubWithLimits(nil, nil, limits)
	first := &peer{
		room:     "limited-room",
		outbound: make(chan outboundMessage, outboundBuffer),
		stopped:  make(chan struct{}),
	}
	second := &peer{
		room:     "limited-room",
		outbound: make(chan outboundMessage, outboundBuffer),
		stopped:  make(chan struct{}),
	}
	if err := hub.addAndReplay(first, []byte(`{"type":"joined"}`)); err != nil {
		t.Fatal(err)
	}
	if err := hub.addAndReplay(second, []byte(`{"type":"joined"}`)); !errors.Is(err, errRoomConnectionLimit) {
		t.Fatalf("second addAndReplay() error = %v, want room connection limit", err)
	}
}

func TestPeerMessageRateLimitResetsAfterWindow(t *testing.T) {
	client := &peer{}
	start := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	if !client.allowInbound(start, 2) || !client.allowInbound(start.Add(time.Second), 2) {
		t.Fatal("messages within the configured limit were rejected")
	}
	if client.allowInbound(start.Add(2*time.Second), 2) {
		t.Fatal("message above the configured limit was accepted")
	}
	if !client.allowInbound(start.Add(inboundMessageWindow), 2) {
		t.Fatal("message was rejected after the rate-limit window reset")
	}
}
