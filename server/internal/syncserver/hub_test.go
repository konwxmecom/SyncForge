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
